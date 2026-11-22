package main

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
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

var markerRow = regexp.MustCompile(`^\s*(?:\d+\.|[a-h][.)]|•)`)

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
				if markerRow.MatchString(r.text) {
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
