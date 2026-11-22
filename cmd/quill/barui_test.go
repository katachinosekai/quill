package main

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// A centered line that starts (or ends) with inline code is centered as one
// line; the code used to sit at the far left (user screenshot 2026-11-22).
func TestCenteredLineStartingWithCode(t *testing.T) {
	test.NewTempApp(t)
	for _, md := range []string{
		"`TestStreamingIsSmooth` fails. Checking whether my change causes it:",
		"Checking `TestStreamingIsSmooth` in the middle of a line now.",
		"The line ends with the test name `TestStreamingIsSmooth`",
		"**Bold** at the start of a centered line, then plain text.",
		"[A link](https://example.com) at the start of a centered line.",
	} {
		segs := parseMD(md)
		alignSegments(segs, fyne.TextAlignCenter)
		rt := widget.NewRichText(segs...)
		w := test.NewWindow(container.NewThemeOverride(rt, msgTheme{}))
		w.Resize(fyne.NewSize(1200, 120))
		lo, hi := float32(1e9), float32(0)
		deepWalk(rt, func(o fyne.CanvasObject) {
			if tx, ok := o.(*canvas.Text); ok && strings.TrimSpace(tx.Text) != "" {
				x := fyne.CurrentApp().Driver().AbsolutePositionForObject(o).X
				tw := fyne.MeasureText(tx.Text, tx.TextSize, tx.TextStyle).Width
				switch tx.Alignment {
				case fyne.TextAlignTrailing:
					x += o.Size().Width - tw
				case fyne.TextAlignCenter:
					x += (o.Size().Width - tw) / 2
				}
				lo, hi = min(lo, x), max(hi, x+tw)
			}
		})
		w.Close()
		left, right := lo, 1200-hi
		if d := left - right; d > 40 || d < -40 {
			t.Errorf("%q: text spans %.0f..%.0f in 1200: not centered (left %.0f, right %.0f)", md, lo, hi, left, right)
		}
	}
}

func TestQueueFollowsCentering(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(s)
		s.queue = []string{"short one", "another short", longItem}
		h.d.center = true
		h.d.drawQueue(h.d.chat, s)
		if n := len(columns(h.d.chat.queueBox)); n != 3 {
			t.Fatalf("centering on: %d queued rows in a column, want 3", n)
		}
		var labels []*widget.Label
		deepWalk(h.d.chat.queueBox, func(o fyne.CanvasObject) {
			if l, ok := o.(*widget.Label); ok {
				labels = append(labels, l)
			}
		})
		for _, l := range labels {
			if l.Alignment != fyne.TextAlignCenter || l.Wrapping != fyne.TextWrapWord {
				t.Fatalf("centering on: queued text %q not centered and wrapped", l.Text)
			}
		}
		h.d.center = false
		h.d.drawQueue(h.d.chat, s)
		if n := len(columns(h.d.chat.queueBox)); n != 0 {
			t.Fatalf("centering off: %d columns, want 0", n)
		}
	})
}

func menuLabels(items []*fyne.MenuItem) []string {
	var out []string
	for _, it := range items {
		if it.IsSeparator {
			continue
		}
		out = append(out, it.Label)
	}
	return out
}

func TestChipsMenu(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.chipsBtn == nil || h.d.chipsBtn.Text != "Chips" {
			t.Fatal("one Chips button under the message box")
		}
		var stale []string
		deepWalk(h.d.bottomBox, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && (b.Text == "Open chips" || b.Text == "Collapse chips") {
				stale = append(stale, b.Text)
			}
		})
		if len(stale) > 0 {
			t.Fatalf("old chip buttons still in the bar: %v", stale)
		}
		got := strings.Join(menuLabels(h.d.chipItems()), " | ")
		if got != "Show chips | Open chips | Collapse chips | New chips start collapsed" {
			t.Fatalf("Chips menu: %s", got)
		}
		was := h.d.hideChips
		h.d.chipItems()[0].Action()
		if h.d.hideChips == was {
			t.Fatal("Show chips toggles")
		}
		if h.d.chipItems()[0].Checked == h.d.hideChips {
			t.Fatal("Show chips is checked while chips are shown")
		}
	})
}

func TestBarButtonsAreMenus(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.current = s
		h.d.updateChrome()
		if h.d.modelBtn.Text != s.model || h.d.modeBtn.Text != s.mode {
			t.Fatalf("buttons show %q/%q, session has %q/%q", h.d.modelBtn.Text, h.d.modeBtn.Text, s.model, s.mode)
		}
		check := func(name string, items []*fyne.MenuItem, opts []string, cur string) {
			if strings.Join(menuLabels(items), "|") != strings.Join(opts, "|") {
				t.Fatalf("%s menu %v, want %v", name, menuLabels(items), opts)
			}
			for _, it := range items {
				if it.Checked != (it.Label == cur) {
					t.Fatalf("%s menu: %q checked=%v", name, it.Label, it.Checked)
				}
			}
		}
		check("model", h.d.modelItems(s), h.d.modelOptions(s), s.model)
		check("mode", h.d.modeItems(s), modeOptions(s), s.mode)
		if l := menuLabels(h.d.voiceItems()); len(l) != 2 || !strings.HasPrefix(l[0], "Language: ") {
			t.Fatalf("voice menu %v", l)
		}
		if l := menuLabels(h.d.taskItems(s)); len(l) != 1 || l[0] != "Show background tasks..." {
			t.Fatalf("tasks menu %v", l)
		}
	})
}

// Turning times on or off redraws the chat at once (it used to bring back
// the cached drawing), and the time sits closer to its message than to the
// next one.
func TestTimesToggleRedrawsAndSitsClose(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		it := s.conv.AddUser("hello")
		it.Time = time.Now()
		h.d.chat.show(s)
		h.d.app.Preferences().SetBool(prefShowTimes, true)
		h.d.redrawChats()
		on := h.d.chat.box
		h.d.app.Preferences().SetBool(prefShowTimes, false)
		h.d.redrawChats()
		if h.d.chat.box == on {
			t.Fatal("turning times off kept the old drawing")
		}
		if len(h.d.chat.cache) != 0 {
			t.Fatal("cached chats keep the old setting")
		}
	})
	msg := widget.NewLabel("message")
	tm := canvas.NewText("3:46 AM", nil)
	c := container.New(timeLayout{above: timeGapAbove, below: timeGapBelow}, msg, tm)
	c.Resize(c.MinSize())
	above := tm.Position().Y - (msg.Position().Y + msg.Size().Height)
	below := c.Size().Height - (tm.Position().Y + tm.Size().Height)
	if above >= below/2 {
		t.Fatalf("time gap above %v, below %v: want it closer to its message", above, below)
	}
}

// The plan card from the user's screenshot (2026-11-22).
func TestPlanQuestionsScreenshot(t *testing.T) {
	plan := "Plan\n\n1. Do things.\n\n" +
		"1. Which buttons below the message box should become menus: all of them, with single-action buttons getting a menu of their related settings (recommended), or only buttons with several related actions?\n" +
		"2. How close should a timestamp sit to its message: about a third of the gap (recommended) or halfway between?\n" +
		"3. Should I rebuild `dist/quill -tags wayland` at the end so you can use \"Restart into it\"? Yes (recommended) or no?\n" +
		"4. **Approve this plan?**"
	qs := planQuestions(plan)
	if len(qs) != 3 {
		t.Fatalf("%d questions, want 3: %+v", len(qs), qs)
	}
	if len(qs[0].Options) != 2 || qs[0].Recommended != 0 || !strings.HasPrefix(qs[0].Options[0], "All of them, with") && !strings.HasPrefix(qs[0].Options[0], "all of them, with") {
		t.Fatalf("Q1 options %q rec %d: want 2, the first recommended", qs[0].Options, qs[0].Recommended)
	}
	if !strings.Contains(qs[2].Text, "dist/quill -tags wayland") {
		t.Fatalf("Q3 lost its code: %q", qs[2].Text)
	}
	if qs[2].Kind != qYesNo || qs[2].Recommended != 0 {
		t.Fatalf("Q3 kind %v rec %d: want yes/no, Yes recommended", qs[2].Kind, qs[2].Recommended)
	}
	if l := recommendedLabels(qs[2]); l[0] != "Yes (recommended)" || l[1] != "No" {
		t.Fatalf("Q3 labels %q", l)
	}
	if l := recommendedLabels(qs[1]); !strings.HasSuffix(l[0], "(recommended)") || strings.HasSuffix(l[1], "(recommended)") {
		t.Fatalf("Q2 labels %q", l)
	}
	h := newHarness(t)
	h.ui(func() {
		ans := h.d.chat.planAnswers(&chat.Item{}, qs)
		if a := ans[qs[2].ID]; a.Yes == nil || !*a.Yes {
			t.Fatal("Q3: the recommended Yes is picked by default")
		}
		if a := ans[qs[0].ID]; a.Choice != 0 {
			t.Fatalf("Q1: the recommended option is picked by default, got %d", a.Choice)
		}
		h.d.center = true
		form := h.d.chat.planQuestionForm(qs, ans)
		if n := len(columns(form)); n != 1 {
			t.Fatalf("centering on: the questions are %d columns, want 1", n)
		}
		var picked []string
		deepWalk(form, func(o fyne.CanvasObject) {
			if r, ok := o.(*widget.RadioGroup); ok && r.Selected != "" {
				picked = append(picked, r.Selected)
			}
		})
		if len(picked) != 3 || !strings.HasSuffix(picked[2], "(recommended)") {
			t.Fatalf("radios picked %q: want each recommended option", picked)
		}
	})
}

func TestTypedApprovalWithMoreText(t *testing.T) {
	yes := []string{
		"Approve the plan with the recommended options, also look to screen shot here \"file:///home/user/Pictures/x.png\" will this be fixed by the new changes in the new build later?\n\nAnother recommendation for the plan card, Show recommended next to each radio button.",
		"Approved. Also add a note to the README.",
	}
	no := []string{
		"Approve the plan but leave out step 3",
		"Approve the plan? I'm not sure yet",
		"Approved, wait for the build first",
	}
	for _, s := range yes {
		if !isApproval(s) {
			t.Errorf("isApproval(%q) = false", s)
		}
	}
	for _, s := range no {
		if isApproval(s) {
			t.Errorf("isApproval(%q) = true", s)
		}
	}
}
