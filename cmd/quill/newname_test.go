package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestNewTabAsksForName(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.newSessionDialog()
		s := h.d.current
		e, ok := h.w.Canvas().Focused().(*nameEntry)
		if !ok || e.Text != "" || e.PlaceHolder != h.d.org.Titles[s.id] {
			t.Fatalf("a blank, focused name field with the default as placeholder: %T", h.w.Canvas().Focused())
		}
		test.Type(e, "Launcher fixes")
		e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		if h.d.org.Titles[s.id] != "Launcher fixes" || len(h.w.Canvas().Overlays().List()) != 0 || h.w.Canvas().Focused() != h.d.input {
			t.Fatalf("Enter names it and moves to the message box: %q", h.d.org.Titles[s.id])
		}

		h.d.newSessionDialog()
		s2 := h.d.current
		def := h.d.org.Titles[s2.id]
		e2 := h.w.Canvas().Focused().(*nameEntry)
		test.Type(e2, "half typed")
		e2.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
		if h.d.org.Titles[s2.id] != def || def == "" || len(h.w.Canvas().Overlays().List()) != 0 {
			t.Fatalf("Esc keeps the default name: %q", h.d.org.Titles[s2.id])
		}
	})
}
