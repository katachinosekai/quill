package main

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestDragTabsIntoSplitPanes(t *testing.T) {
	h := newHarness(t)
	var a, b *deskSession
	h.ui(func() {
		a = h.d.createSession(h.proj, "haiku", "default")
		b = h.d.createSession(h.proj, "haiku", "default")
		h.d.open(b.id)
		h.d.open(a.id)
		h.d.syncTabs()
	})
	h.ui(func() {
		chip := h.d.tabChips[b.id]
		if chip == nil {
			t.Fatal("b has a tab")
		}
		host := fyne.CurrentApp().Driver().AbsolutePositionForObject(h.d.chatHost)
		size := h.d.chatHost.Size()
		right := host.Add(fyne.NewPos(size.Width*3/4, size.Height/2))
		chip.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: right, Position: fyne.NewPos(5, 5)}})
		if !h.d.chatDropHL.Visible() {
			t.Fatal("dragging a tab over the chat highlights where it will land")
		}
		chip.DragEnd()
		if !h.d.split() || h.d.panes[1].view.s != b || h.d.panes[0].view.s != a || h.d.chatDropHL.Visible() {
			t.Fatalf("dropping a tab on the right half splits with it on the right: split=%v", h.d.split())
		}
	})
	h.ui(func() {
		p1 := h.d.panes[1]
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(p1.root).Add(fyne.NewPos(p1.root.Size().Width/2, p1.root.Size().Height/2))
		chip := h.d.tabChips[a.id]
		chip.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: pos, Position: fyne.NewPos(5, 5)}})
		if !p1.dropHL.Visible() || h.d.panes[0].dropHL.Visible() {
			t.Fatal("only the pane under the pointer is highlighted")
		}
		chip.DragEnd()
		if h.d.panes[1].view.s != a || h.d.panes[0].view.s != b || p1.dropHL.Visible() {
			t.Fatal("dropping a tab on a pane that shows another session swaps them")
		}
	})
}
