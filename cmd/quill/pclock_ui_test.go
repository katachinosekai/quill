package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// Password fields start hidden (dots) and only show the text through their
// eye button.
func TestPasswordFieldsHidden(t *testing.T) {
	h := newHarness(t)
	entries := func() []*widget.Entry {
		var out []*widget.Entry
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if e, ok := x.(*widget.Entry); ok {
					out = append(out, e)
				}
			})
		}
		return out
	}
	clear := func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
	}
	h.ui(func() {
		h.d.connectPCDialog(remotePCConf{ID: "a1b2c3d4", Name: "Office PC", Hosts: []string{"192.0.2.10"}, Port: 1, Key: "k", FP: strings.Repeat("ab", 32)})
		es := entries()
		if len(es) != 1 || !es[0].Password {
			t.Fatalf("connect: one hidden password field, got %d", len(es))
		}
		es[0].SetText("secret")
		if es[0].Password != true {
			t.Fatal("typing keeps it hidden")
		}
		clear()
		h.d.setDevicePasswordDialog("", nil)
		es = entries()
		if len(es) != 2 || !es[0].Password || !es[1].Password {
			t.Fatalf("device password and again: both hidden, got %d", len(es))
		}
		clear()
	})
}
