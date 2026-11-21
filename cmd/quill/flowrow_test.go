package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestChoiceButtonsWrapInANarrowWindow(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.root.Resize(fyne.NewSize(900, 800))
		msg := &chat.Item{Seq: 991, Kind: chat.Assistant, Text: "Plan.\n\n**How should it reach the friends' clients: each friend adds an unsigned preload entry by hand, I sign the jar with a key on your Steam profile, or a PR adds it to the authors list?**"}
		s.conv.Items = append(s.conv.Items, msg)
		h.d.chat.s = nil
		h.d.chat.show(s)
		h.d.chat.relayout()
	})
	h.ui(func() {
		v := h.d.chat
		view := v.scroll.Size().Width
		if got := v.box.MinSize().Width; got > view+1 {
			t.Fatalf("the chat's content (%v) is wider than the chat (%v): messages get clipped", got, view)
		}
		var btns []*widget.Button
		walkObjects([]fyne.CanvasObject{v.box}, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && b.Visible() && b.Text != "" && len(b.Text) > 20 {
				btns = append(btns, b)
			}
		})
		if len(btns) != 3 {
			t.Fatalf("all three choices are buttons: %d", len(btns))
		}
		d := fyne.CurrentApp().Driver()
		for i, a := range btns {
			pa := d.AbsolutePositionForObject(a)
			for _, b := range btns[i+1:] {
				pb := d.AbsolutePositionForObject(b)
				if pa.X < pb.X+b.Size().Width && pb.X < pa.X+a.Size().Width && pa.Y < pb.Y+b.Size().Height && pb.Y < pa.Y+a.Size().Height {
					t.Fatalf("buttons %q and %q overlap", a.Text, b.Text)
				}
			}
		}
		last := btns[len(btns)-1]
		if bottom, boxBottom := d.AbsolutePositionForObject(last).Y+last.Size().Height, d.AbsolutePositionForObject(v.box).Y+v.box.Size().Height; bottom > boxBottom+1 {
			t.Fatalf("the wrapped buttons stick out below the chat content (%v > %v)", bottom, boxBottom)
		}
		for _, b := range btns {
			p := fyne.CurrentApp().Driver().AbsolutePositionForObject(b)
			sp := fyne.CurrentApp().Driver().AbsolutePositionForObject(v.scroll)
			if p.X+b.Size().Width > sp.X+view+1 {
				t.Fatalf("button %q ends outside the chat (%v > %v)", b.Text, p.X+b.Size().Width, sp.X+view)
			}
		}
	})
}
