package main

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
)

// dimmedAt: a shown dimming piece covers the absolute point p.
func dimmedAt(d *desk, p fyne.Position) *dimPiece {
	for _, pc := range d.box().dim.pieces {
		if !pc.Visible() {
			continue
		}
		pos, sz := absRect(pc)
		if p.X > pos.X && p.X < pos.X+sz.Width && p.Y > pos.Y && p.Y < pos.Y+sz.Height {
			return pc
		}
	}
	return nil
}

func centerOf(o fyne.CanvasObject) fyne.Position {
	p, s := absRect(o)
	return fyne.NewPos(p.X+s.Width/2, p.Y+s.Height/2)
}

func shownPieces(d *desk) int {
	n := 0
	for _, pc := range d.box().dim.pieces {
		if pc.Visible() {
			n++
		}
	}
	return n
}

// t34c79f: over the chat the rest of the main area is dimmed ~30 %, the box
// and the main message box are not; docked or closed, nothing is.
func TestBoxCuesDimOverlayOnly(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		if shownPieces(d) != 0 {
			t.Fatal("no dimming while closed")
		}
		d.openAssistBox(boxQuill)
		d.layoutDim()
		if shownPieces(d) == 0 || !d.dimShown() {
			t.Fatal("open over the chat: dimmed")
		}
		pc := dimmedAt(d, centerOf(d.toolbarBox))
		if pc == nil {
			t.Fatal("the exposed toolbar is dimmed")
		}
		if c := pc.rect.FillColor.(color.NRGBA); c.R != 0 || c.A < 70 || c.A > 85 {
			t.Fatalf("black at ~30 %%: %v", c)
		}
		if dimmedAt(d, centerOf(d.box().card)) != nil || dimmedAt(d, centerOf(d.input)) != nil {
			t.Fatal("the box and the main message box are not dimmed")
		}
		d.setAssistDock(boxQuill, true)
		if !d.assistBoxDocked() || shownPieces(d) != 0 {
			t.Fatal("docked: no dimming")
		}
		d.setAssistDock(boxQuill, false)
		if shownPieces(d) == 0 {
			t.Fatal("undocked again: dimmed")
		}
		d.closeAssistBox()
		if shownPieces(d) != 0 || d.dimShown() {
			t.Fatal("closed: no dimming")
		}

		// Split: all panes dimmed but the focused pane's message box.
		d.setSplit(2)
		d.openAssistBox(boxQuill)
		d.layoutDim()
		f, o := d.focused, d.panes[0]
		if o == f {
			o = d.panes[1]
		}
		if dimmedAt(d, centerOf(f.input)) != nil || dimmedAt(d, centerOf(o.input)) == nil {
			t.Fatal("split: the focused pane's message box only is left un-dimmed")
		}
		d.closeAssistBox()
	})
}

// The frame and send button use the active assistant's colour, with a
// chip for kmem and the highlighted operator button for Quill.
func TestBoxCuesChipAndAccent(t *testing.T) {
	h := newHarness(t)
	testBoxView(h, boxKmem, "kmem assistant")
	h.ui(func() {
		d := h.d
		c := d.input.cue
		if c.chip.Visible() || c.frame.StrokeWidth != 0 {
			t.Fatal("no cues on the tab's message box")
		}
		d.openAssistBox(boxQuill)
		if c.chip.Visible() || c.frame.StrokeColor != colAccent || c.frame.StrokeWidth != cueFrame {
			t.Fatal("Quill uses the accent without a duplicate name chip")
		}
		if th, ok := c.send.Theme.(accentTheme); !ok || th.c != colAccent {
			t.Fatal("Quill: the send button in its colour")
		}
		d.input.SetText("typing")
		if c.chip.Visible() {
			t.Fatal("typing must not bring back the duplicate Quill name")
		}
		d.openAssistBox(boxKmem)
		if !c.chip.Visible() || c.chipText.Text != "kmem assistant ▸" || c.frame.StrokeColor != colKmemAccent || c.send.Theme.(accentTheme).c != colKmemAccent {
			t.Fatal("kmem: its own colour and name")
		}
		d.setAssistDock(boxKmem, true)
		if c.chip.Visible() || c.frame.StrokeWidth != 0 {
			t.Fatal("docked: no cues")
		}
		d.setAssistDock(boxKmem, false)
		d.closeAssistBox()
		if c.chip.Visible() || c.frame.StrokeWidth != 0 {
			t.Fatal("closed: no cues")
		}
		if _, ok := c.send.Theme.(deskTheme); !ok {
			t.Fatal("closed: the usual send button")
		}

		d.setSplit(2)
		d.openAssistBox(boxQuill)
		for _, p := range d.panes {
			if on := p == d.focused; p.input.cue.chip.Visible() || (p.input.cue.frame.StrokeWidth != 0) != on {
				t.Fatalf("split: cues on the focused pane's message box only (pane focused=%v)", on)
			}
		}
		d.closeAssistBox()
		for _, p := range d.panes {
			if p.input.cue.chip.Visible() {
				t.Fatal("split, closed: no chip")
			}
		}
	})
}

// A click on the dimmed area closes the box like Escape: the main box goes
// back to the tab with its draft; the assistant's draft is kept.
func TestBoxCuesClickDimCloses(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		d.input.SetText("tab draft")
		d.openAssistBox(boxQuill)
		d.input.SetText("op draft")
		d.layoutDim()
		at := centerOf(d.toolbarBox)
		pc := dimmedAt(d, at)
		if pc == nil || d.tabChipAt(at) != nil {
			t.Fatal("the toolbar is dimmed")
		}
		pc.Tapped(&fyne.PointEvent{AbsolutePosition: at})
		if d.assistBoxRole() != "" || d.input.Text != "tab draft" || d.input.cue.chip.Visible() || shownPieces(d) != 0 {
			t.Fatalf("the click closes the box, the tab's draft is back: %q", d.input.Text)
		}
		d.openAssistBox(boxQuill)
		if d.input.Text != "op draft" {
			t.Fatalf("the operator's draft is kept: %q", d.input.Text)
		}
		d.closeAssistBox()
	})
}

func TestBoxCuesDimFollowsWindowResize(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		d.openAssistBox(boxQuill)
		for _, size := range []fyne.Size{fyne.NewSize(1400, 950), fyne.NewSize(900, 660), fyne.NewSize(1280, 780)} {
			d.win.Resize(size)
			d.layoutRoot()
			d.layoutDim()
			origin, bounds := absRect(d.mainRoot)
			right := fyne.NewPos(origin.X+bounds.Width-1, origin.Y+bounds.Height/2)
			if d.box().dim.root.Size() != bounds || dimmedAt(d, right) == nil {
				t.Fatalf("the mask should cover the resized area %v", bounds)
			}
			if dimmedAt(d, centerOf(d.box().card)) != nil || dimmedAt(d, centerOf(d.input)) != nil {
				t.Fatalf("the panel and composer should remain bright at %v", bounds)
			}
		}
	})
}

func TestDimRects(t *testing.T) {
	size := fyne.NewSize(100, 100)
	r := func(x, y, w, h float32) rectF { return rectF{fyne.NewPos(x, y), fyne.NewSize(w, h)} }
	if got := dimRects(size, nil); len(got) != 1 || got[0] != r(0, 0, 100, 100) {
		t.Fatalf("no holes: one rect: %v", got)
	}
	holes := []rectF{r(10, 10, 80, 50), r(20, 70, 60, 20)}
	got := dimRects(size, holes)
	var area float32
	for _, g := range got {
		area += g.size.Width * g.size.Height
		for _, hl := range holes {
			if g.pos.X < hl.pos.X+hl.size.Width && hl.pos.X < g.pos.X+g.size.Width && g.pos.Y < hl.pos.Y+hl.size.Height && hl.pos.Y < g.pos.Y+g.size.Height {
				t.Fatalf("%v overlaps hole %v", g, hl)
			}
		}
	}
	if area != 100*100-80*50-60*20 {
		t.Fatalf("covers the rest exactly: %v in %d rects", area, len(got))
	}
	if len(got) > 8 {
		t.Fatalf("merged: %d rects", len(got))
	}
}

// The dimming fades with the box animation's length; Reduce motion: at once.
// (The test driver runs an animation to its end at once: lastFade says it ran.)
func TestBoxCuesDimFadeReduceMotion(t *testing.T) {
	h := newHarness(t)
	old := systemReduceMotion
	systemReduceMotion = func() bool { return true }
	t.Cleanup(func() { systemReduceMotion = old })
	h.ui(func() {
		d := h.d
		d.animate = true
		defer func() { d.animate = false }()
		m := d.box().dim
		d.openAssistBox(boxQuill)
		if m.lastFade != 0 || m.level != 1 {
			t.Fatal("Reduce motion: dimmed at once")
		}
		d.closeAssistBox()
		if m.lastFade != 0 || m.level != 0 || shownPieces(d) != 0 {
			t.Fatal("Reduce motion: gone at once")
		}
		setUserPref(d.app.Preferences(), prefReduceMotion, false)
		d.openAssistBox(boxQuill)
		if m.lastFade != d.assistBoxDuration() || m.lastFade == 0 || m.level != 1 || shownPieces(d) == 0 {
			t.Fatal("with motion: the dimming fades in")
		}
		m.lastFade = 0
		d.closeAssistBox()
		if m.lastFade == 0 || m.level != 0 || shownPieces(d) != 0 {
			t.Fatal("with motion: it fades out")
		}
	})
}
