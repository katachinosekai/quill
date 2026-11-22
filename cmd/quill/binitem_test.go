package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestBinItemWindow(t *testing.T) {
	it := binItem{ID: "f467b16", Head: "fact, 8 days in bin, by launcher: a long reason: with a colon inside it", Preview: "(decision) text..."}
	if it.binMeta() != "fact, 8 days in bin, by launcher" || it.binReason() != "a long reason: with a colon inside it" {
		t.Fatalf("meta %q reason %q", it.binMeta(), it.binReason())
	}
	if validBinID("f467b16; rm -rf ~") || !validBinID("t9a8265") {
		t.Fatal("only plain ids reach the terminal command")
	}
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if strings.Join(args, " ") == "show f467b16" {
			return strings.Repeat("A long fact line that needs a scroll. ", 120), nil
		}
		return "", nil
	}
	var started []string
	oldTerm := terminalCmd
	terminalCmd = func(kmemPath string, id ...string) *exec.Cmd {
		started = append(started, strings.Join(id, " "))
		return exec.Command("true")
	}
	defer func() { terminalCmd = oldTerm }()
	var p *kmemPanel
	h.ui(func() {
		p = h.d.newKmemPanel()
		p.showBinItem(it)
	})
	h.eventually("text loaded", func() bool {
		return strings.Contains(labelsIn([]fyne.CanvasObject{h.w.Canvas().Overlays().Top()}), "needs a scroll")
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { h.w.Resize(fyne.NewSize(1100, 760)) })
		h.ui(func() { shot(t, h.w, dir, "bin-item.png") })
	}
	h.ui(func() {
		var del *widget.Button
		deepWalk(h.w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && strings.HasPrefix(b.Text, "Delete for good") {
				del = b
			}
		})
		if del == nil {
			t.Fatal("the window has Delete for good")
		}
		test.Tap(del)
		confirmTopDialog(t, h)
	})
	h.eventually("purge of this item in a terminal", func() bool { return len(started) == 1 && started[0] == "f467b16" })
}
