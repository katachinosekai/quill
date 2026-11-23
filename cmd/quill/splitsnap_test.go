package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
)

// barRects: where the bar row above the message box and the stats line sit.
func barRects(d *desk) map[string]fyne.Position {
	drv := fyne.CurrentApp().Driver()
	at := func(o fyne.CanvasObject) fyne.Position { return drv.AbsolutePositionForObject(o) }
	return map[string]fyne.Position{
		"settings": at(d.settingsBtn),
		"kmem":     at(d.kmemBtnBox),
		"view":     at(d.viewBox),
		"stats":    at(d.statsRow),
	}
}

// After leaving split view the bar row (Settings, kmem, view buttons) lines up
// with the message box exactly as in a fresh single view.
func TestSplitExitBarSnapsBack(t *testing.T) {
	size := fyne.NewSize(1720, 900)
	shots := os.Getenv("QUILL_SHOTS_DIR")
	if shots != "" {
		os.MkdirAll(shots, 0o755)
	}
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.w.Resize(size)
		relayoutTree(h.d.mainPane)
	})
	var fresh map[string]fyne.Position
	h.ui(func() {
		fresh = barRects(h.d)
		if fresh["settings"].X < 100 {
			t.Fatalf("fresh single view should be centred, settings at %v", fresh["settings"])
		}
		if shots != "" {
			shot(t, h.w, shots, "fresh-single.png")
		}
	})
	for round := 1; round <= 2; round++ {
		h.ui(func() { h.d.setSplit(2); h.w.Resize(fyne.NewSize(1100, 700)) })
		h.ui(func() { h.w.Resize(size); h.d.setShare("split", 0.4) }) // a divider drag / resize settle while split lays out the hidden toolbar
		h.ui(func() {
			if shots != "" && round == 1 {
				shot(t, h.w, shots, "split.png")
			}
			h.d.setSplit(1)
		})
		h.ui(func() {
			got := barRects(h.d)
			for k, want := range fresh {
				if d := got[k].Subtract(want); d.X > 0.5 || d.X < -0.5 || d.Y > 0.5 || d.Y < -0.5 {
					t.Errorf("round %d: %s at %v, fresh single view has it at %v", round, k, got[k], want)
				}
			}
			if shots != "" && round == 1 {
				shot(t, h.w, filepath.Clean(shots), "after-split-exit.png")
			}
		})
	}
}
