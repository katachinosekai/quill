package main

import (
	"testing"

	"fyne.io/fyne/v2"
)

func TestMessageBoxFollowsWindowAfterResizeSettles(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.animate = true
		h.d.setShare("inputShare", 0.6)
		h.d.root.Resize(fyne.NewSize(900, 700))
		h.d.root.Resize(fyne.NewSize(2400, 900))
	})
	h.eventually("message box widens to its share of the new window", func() bool {
		got := h.d.bottomBox.Objects[0].Size().Width
		want := h.d.inputWidth()
		return want > 700 && got >= want-1 && got <= want+1
	})
}
