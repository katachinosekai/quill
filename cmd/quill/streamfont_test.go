package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"

	"github.com/katachinosekai/quill/internal/chat"
)

// While a reply streams, every piece of its text is drawn at the message
// size from its first frame; new paragraphs used to draw at the default
// size until the next refresh, so text snapped between sizes (user
// 2026-11-22).
func TestStreamingTextKeepsMessageSize(t *testing.T) {
	h := newHarness(t)
	want := msgTheme{}.Size(theme.SizeNameText)
	var it *chat.Item
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		s := h.d.createSession(h.proj, "default", "default")
		h.d.current = s
		h.d.chat.show(s)
		it = s.conv.AddNotice("")
		it.Kind = chat.Assistant
		it.Streaming = true
	})
	full := "The first paragraph has **bold** words and `code` in it.\n\n- one item\n- another item\n\nThe second paragraph keeps growing while the reply arrives.\n\nAnd a short last one."
	for n := 8; n <= len(full); n += 9 {
		text := full[:n]
		h.ui(func() {
			it.Text = text
			h.d.chat.update([]*chat.Item{it})
			iv := h.d.chat.views[it.Seq]
			if iv == nil {
				t.Fatal("no view for the streaming reply")
			}
			deepWalk(iv.holder, func(o fyne.CanvasObject) {
				if tx, ok := o.(*canvas.Text); ok && strings.TrimSpace(tx.Text) != "" && tx.TextSize != want {
					t.Fatalf("at %d chars %q is drawn at %v, want the message size %v", n, tx.Text, tx.TextSize, want)
				}
			})
		})
	}
}

// The split-view button has the stop button's size and, in single view,
// sits straight above it.
func TestSplitButtonMatchesStop(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.current = s
		h.d.chat.show(s)
		h.d.updateChrome()
		h.w.Resize(fyne.NewSize(1280, 860))
	})
	h.ui(func() {
		if h.d.split() {
			t.Skip("starts in split view")
		}
		v, s := h.d.viewBtn, h.d.stopBtn
		if v.Size() != s.Size() {
			t.Fatalf("split-view button %v, stop button %v", v.Size(), s.Size())
		}
		drv := fyne.CurrentApp().Driver()
		vr := drv.AbsolutePositionForObject(v).X + v.Size().Width
		sr := drv.AbsolutePositionForObject(s).X + s.Size().Width
		if d := vr - sr; d > 0.5 || d < -0.5 {
			t.Fatalf("right edges: split-view %v, stop %v", vr, sr)
		}
	})
}
