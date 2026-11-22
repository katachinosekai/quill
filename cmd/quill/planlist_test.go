package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/chat"
)

// drawnRow is one drawn line: its left and right edge and its text.
type drawnRow struct {
	y, left, right float32
	text           string
}

// drawnRows renders o in a window w wide and returns its text lines.
func drawnRows(t *testing.T, o fyne.CanvasObject, w float32) []drawnRow {
	t.Helper()
	win := test.NewWindow(o)
	defer win.Close()
	win.Resize(fyne.NewSize(w, 900))
	rows := map[int]*drawnRow{}
	deepWalk(o, func(x fyne.CanvasObject) {
		tx, ok := x.(*canvas.Text)
		if !ok || strings.TrimSpace(tx.Text) == "" {
			return
		}
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(x)
		tw := fyne.MeasureText(tx.Text, tx.TextSize, tx.TextStyle).Width
		l := pos.X
		switch tx.Alignment {
		case fyne.TextAlignTrailing:
			l += x.Size().Width - tw
		case fyne.TextAlignCenter:
			l += (x.Size().Width - tw) / 2
		}
		key := int(pos.Y + x.Size().Height/2)
		r := rows[key]
		if r == nil {
			r = &drawnRow{y: float32(key), left: l, right: l + tw}
			rows[key] = r
		}
		if l < r.left {
			r.left, r.text = l, tx.Text+r.text
		} else {
			r.text += tx.Text
		}
		r.right = max(r.right, l+tw)
	})
	var out []drawnRow
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].y < out[j].y })
	return out
}

var markedLine = regexp.MustCompile(`^\s*(?:\d+\.|[a-h][.)]|•)`)

// Rule f5c0dd9 (user 2026-11-22): numbered, lettered or bulleted plan lists
// keep their formatting (markers lined up, lines starting at one left
// edge); the list as a whole is centered as one column only when centering
// is on, otherwise it stays left-aligned.
func TestPlanListsFollowCenteringRule(t *testing.T) {
	h := newHarness(t)
	lists := map[string]string{
		"numbered":      "1. Read the code first\n2. Change the layout so it follows the rule\n3. Run all the tests",
		"lettered":      "a. Read the code first\nb. Change the layout so it follows the rule\nc. Run all the tests",
		"bulleted":      "- Read the code first\n- Change the layout so it follows the rule\n- Run all the tests",
		"lettered-bold": "- **a.** Read the code first\n- **b.** Change the layout so it follows the rule\n- **c.** Run all",
	}
	const width = 1400
	for _, center := range []bool{true, false} {
		for name, md := range lists {
			var rows []drawnRow
			h.ui(func() {
				h.d.center = center
				rows = drawnRows(t, msgText(h.d.chat.markdown(md)), width)
			})
			var marked []drawnRow
			for _, r := range rows {
				if markedLine.MatchString(r.text) {
					marked = append(marked, r)
				}
			}
			if len(marked) != 3 {
				t.Fatalf("center=%v %s: %d marked rows of %d: %+v", center, name, len(marked), len(rows), rows)
			}
			lo, hi := marked[0].left, marked[0].right
			for _, r := range marked[1:] {
				if d := r.left - marked[0].left; d > 1 || d < -1 {
					t.Errorf("center=%v %s: markers not lined up: %q at %.0f, %q at %.0f", center, name, marked[0].text, marked[0].left, r.text, r.left)
				}
				lo, hi = min(lo, r.left), max(hi, r.right)
			}
			if center {
				if d := (lo - 0) - (width - hi); d > 60 || d < -60 {
					t.Errorf("center=%v %s: column %.0f..%.0f is not centered in %d", center, name, lo, hi, width)
				}
			} else if lo > 40 {
				t.Errorf("center=%v %s: list starts at %.0f, want left-aligned", center, name, lo)
			}
		}
	}
}

// Lettered items are a real list (as in Quill Android 0.9.24): a wrapped
// item continues under its text, not under the letter; rows are spaced
// like a numbered list's; blank lines between items keep one list.
func TestLetteredListHangingIndentAndSpacing(t *testing.T) {
	h := newHarness(t)
	long := "b. Change the layout so it follows the rule, and keep going with a much longer item that has to wrap onto a second line"
	for _, center := range []bool{true, false} {
		var rows, num []drawnRow
		h.ui(func() {
			h.d.center = center
			rows = drawnRows(t, msgText(h.d.chat.markdown("a. Read the code first\n"+long+"\n\nc) Run all the tests")), 700)
			num = drawnRows(t, msgText(h.d.chat.markdown("1. Read the code first\n2. Change the layout\n3. Run all the tests")), 700)
		})
		var marks []drawnRow
		cont := -1
		for i, r := range rows {
			if markedLine.MatchString(r.text) {
				marks = append(marks, r)
			} else if len(marks) == 2 && cont < 0 {
				cont = i
			}
		}
		if len(marks) != 3 || cont < 0 {
			t.Fatalf("center=%v: want 3 items and a wrapped line, got %+v", center, rows)
		}
		if !strings.HasPrefix(marks[2].text, "c)") {
			t.Fatalf("center=%v: the letter stays as written: %q", center, marks[2].text)
		}
		// The text of item b starts right of its letter; the wrapped line
		// starts there too.
		var textLeft float32 = -1
		for _, r := range rows {
			if r.text == rows[cont].text {
				textLeft = r.left
			}
		}
		if textLeft <= marks[1].left+5 {
			t.Fatalf("center=%v: wrapped line starts at %.0f, the letter at %.0f: want a hanging indent", center, textLeft, marks[1].left)
		}
		// Row pitch: item a to item b like 1 to 2 in a numbered list.
		var numMarks []drawnRow
		for _, r := range num {
			if markedLine.MatchString(r.text) {
				numMarks = append(numMarks, r)
			}
		}
		pitch, numPitch := marks[1].y-marks[0].y, numMarks[1].y-numMarks[0].y
		if d := pitch - numPitch; d > 3 || d < -3 {
			t.Errorf("center=%v: lettered rows %.0f apart, numbered %.0f", center, pitch, numPitch)
		}
	}
}

// A blank line between lettered items doesn't split them; while streaming,
// an item that arrives after a blank line joins the list.
func TestLetteredItemsAcrossBlankLines(t *testing.T) {
	done, live := splitParas("Intro.\n\na. one\n\nb. two\n\nAfter.\n\n")
	if len(done) != 2 || done[1] != "a. one\n\nb. two" || !strings.HasPrefix(live, "After.") {
		t.Fatalf("done %q live %q", done, live)
	}
	if !hasLetteredRun("a. one\n\nb) two") || hasLetteredRun("a. one\n\nplain") {
		t.Fatal("hasLetteredRun")
	}
	h := newHarness(t)
	var it *chat.Item
	h.ui(func() {
		h.d.center = true
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		s := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(s)
		it = s.conv.AddNotice("")
		it.Kind = chat.Assistant
		it.Streaming = true
	})
	for _, text := range []string{"Intro.\n\na. one\n\n", "Intro.\n\na. one\n\nb. tw", "Intro.\n\na. one\n\nb. two\n\nAfter."} {
		h.ui(func() {
			it.Text = text
			h.d.chat.update([]*chat.Item{it})
		})
	}
	var all []string
	h.ui(func() {
		deepWalk(h.d.chat.views[it.Seq].holder, func(o fyne.CanvasObject) {
			if tx, ok := o.(*canvas.Text); ok && strings.TrimSpace(tx.Text) != "" {
				all = append(all, tx.Text)
			}
		})
	})
	joined := strings.Join(all, "|")
	if strings.Count(joined, "one") != 1 || strings.Count(joined, "two") != 1 || !strings.Contains(joined, "a.") || !strings.Contains(joined, "b.") {
		t.Fatalf("streamed lettered items drawn once each, as a list: %s", joined)
	}
}
