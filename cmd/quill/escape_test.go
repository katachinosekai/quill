package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func TestEscapeClosesOverlaysBeforeInterrupting(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		c := h.d.win.Canvas()
		picked := ""
		h.d.pickLanguage("en", func(code string) { picked = code })
		pop := c.Overlays().Top()
		if pop == nil {
			t.Fatal("voice picker did not open")
		}
		s.conv.Busy = true
		prev := s.proc
		if !h.d.escape(s) {
			t.Fatal("Escape with the voice picker open must be handled")
		}
		if c.Overlays().Top() != nil || picked != "" {
			t.Fatalf("Escape must close the voice picker without picking: top=%T picked=%q", c.Overlays().Top(), picked)
		}
		if s.proc != prev {
			t.Fatal("Escape must not touch the session while a picker is open")
		}

		h.d.pickLanguage("en", func(string) {})
		if f, ok := c.Focused().(*escEntry); !ok {
			t.Fatal("picker search box should have focus")
		} else {
			f.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
		}
		if c.Overlays().Top() != nil {
			t.Fatal("Escape in the picker's search box must close it")
		}

		var got *bool
		dlg := dialog.NewConfirm("Q", "sure?", func(ok bool) { got = &ok }, h.d.win)
		dlg.Show()
		if !h.d.escape(s) || got == nil || *got || c.Overlays().Top() != nil {
			t.Fatal("Escape must cancel a dialog (callback false) instead of interrupting")
		}

		busy := dialog.NewCustomWithoutButtons("Working", widget.NewLabel("..."), h.d.win)
		busy.Show()
		if !h.d.escape(s) || c.Overlays().Top() == nil {
			t.Fatal("a dialog without a dismiss button stays open, and Escape is still swallowed")
		}
		busy.Hide()

		widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", fyne.NewMenuItem("x", func() {})), c, fyne.NewPos(1, 1))
		if !h.d.escape(s) || c.Overlays().Top() != nil {
			t.Fatal("Escape must dismiss a popup menu")
		}

		s.conv.Busy = false
		if h.d.escape(s) {
			t.Fatal("no overlay and an idle session: Escape does nothing")
		}
	})
}
