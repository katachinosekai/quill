package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// absY: the top of the first object under o matching pick, in o's space.
func absY(o fyne.CanvasObject, pick func(fyne.CanvasObject) bool) (float32, bool) {
	var found bool
	var y float32
	var rec func(o fyne.CanvasObject, off float32)
	rec = func(o fyne.CanvasObject, off float32) {
		if found {
			return
		}
		top := off + o.Position().Y
		if pick(o) {
			found, y = true, top
			return
		}
		switch c := o.(type) {
		case *fyne.Container:
			for _, ch := range c.Objects {
				rec(ch, top)
			}
		case fyne.Widget:
			for _, ch := range test.WidgetRenderer(c).Objects() {
				rec(ch, top)
			}
		}
	}
	rec(o, 0)
	return y, found
}

const apprBoxPlan = "Plan:\n1. Edit x.\n2. Build.\n\nQuestions:\n" +
	"1. Use blue or red?\n" +
	"2. What should the tag say?\n\n" +
	"**Can I run steps 1–2? Yes (recommended) or no?**"

func accentBoxes(o fyne.CanvasObject) (accent, muted int) {
	walk(o, func(x fyne.CanvasObject) {
		if r, ok := x.(*canvas.Rectangle); ok && r.StrokeWidth == 1.5 && r.CornerRadius == 10 {
			if r.StrokeColor == colAccent {
				accent++
			} else {
				muted++
			}
		}
	})
	return
}

func TestApprovalBoxOrderAndBorder(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, apprBoxPlan)
	_ = s
	shot := func(name string, o fyne.CanvasObject) {
		dir := os.Getenv("QUILL_SHOT_DIR")
		if dir == "" {
			return
		}
		h.w.SetContent(container.NewVScroll(o))
		h.w.Resize(fyne.NewSize(900, 900))
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, h.w.Canvas().Capture())
		f.Close()
	}
	h.ui(func() {
		v := h.d.chat
		reply := planReply(s.conv, plan)
		o := v.renderPlanReply(reply)
		if o == nil {
			t.Fatal("nothing drawn")
		}
		o.Resize(fyne.NewSize(800, 900))
		container.NewWithoutLayout(o).Refresh()
		warn, ok1 := absY(o, func(x fyne.CanvasObject) bool {
			l, ok := x.(*widget.Label)
			return ok && strings.Contains(l.Text, "Answering")
		})
		radioY, ok3 := absY(o, func(x fyne.CanvasObject) bool {
			r, ok := x.(*widget.RadioGroup)
			return ok && len(r.Options) > 0 && strings.HasPrefix(r.Options[0], "Yes")
		})
		if !ok1 || !ok3 {
			t.Fatalf("warning found %v, radio found %v", ok1, ok3)
		}
		// The question text: a rich text holding it, between the two.
		qY, okq := absY(o, func(x fyne.CanvasObject) bool {
			rt, ok := x.(*widget.RichText)
			return ok && strings.Contains(rt.String(), "Can I run steps")
		})
		if !okq {
			t.Fatal("approval question not shown")
		}
		qCount := 0
		deepWalk(o, func(x fyne.CanvasObject) {
			if rt, ok := x.(*widget.RichText); ok && strings.Contains(rt.String(), "Can I run steps") {
				qCount++
			}
		})
		if qCount != 1 {
			t.Fatalf("approval question shown %d times", qCount)
		}
		if !(warn < qY && qY < radioY) {
			t.Fatalf("order: warning %v, question %v, radio %v", warn, qY, radioY)
		}
		// The other questions come before the box; the warning is not below them.
		if q2, ok := absY(o, func(x fyne.CanvasObject) bool {
			rt, ok := x.(*widget.RichText)
			return ok && strings.Contains(rt.String(), "What should the tag say")
		}); !ok || q2 > warn {
			t.Fatalf("other questions keep their place above the box: %v %v", q2, warn)
		}
		if a, m := accentBoxes(o); a != 1 || m != 0 {
			t.Fatalf("active: accent boxes %d, muted %d", a, m)
		}
		shot("apprbox-active", o)

		// Answered: same box, no accent, no warning.
		plan.PermState = chat.PermDenied
		o = v.renderPlanReply(reply)
		if a, m := accentBoxes(o); a != 0 || m != 1 {
			t.Fatalf("answered: accent boxes %d, muted %d", a, m)
		}
		if _, found := absY(o, func(x fyne.CanvasObject) bool {
			l, ok := x.(*widget.Label)
			return ok && strings.Contains(l.Text, "Answering")
		}); found {
			t.Fatal("answered card has no warning")
		}
		shot("apprbox-answered", o)
	})
}
