package main

import (
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func kmemBoxHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	oldOpen, oldClose := kmemWindowOpenHook, kmemWindowCloseHook
	kmemWindowOpenHook, kmemWindowCloseHook = nil, nil
	t.Cleanup(func() { kmemWindowOpenHook, kmemWindowCloseHook = oldOpen, oldClose })
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "version --json":
			return `{"version": "1.8.1", "api": 1}`, nil
		case "brief --json":
			return `{"brief": "rules: read, unchanged\n", "api": 1}`, nil
		case "task list --json":
			return `{"tasks": [], "api": 1}`, nil
		case "handoff list --json":
			return `{"handoffs": [], "api": 1}`, nil
		}
		return "", nil
	}
	return h
}

func TestKmemBoxContentHasRailAndChat(t *testing.T) {
	h := kmemBoxHarness(t)
	h.ui(func() {
		obj := h.d.kmemBoxContent()
		w := h.d.kmemWin
		if w == nil || w.win != nil || obj != fyne.CanvasObject(w.split) {
			t.Fatal("the content is the split, built without a window")
		}
		names := ""
		for _, it := range w.panel.tabs.Items {
			names += it.Text + ","
		}
		for _, want := range []string{"Overview", "Topics", "Tasks", "Memory"} {
			if !strings.Contains(names, want) {
				t.Fatalf("tab %q missing: %s", want, names)
			}
		}
		if len(w.split.Trailing.(*fyne.Container).Objects) != 1 {
			t.Fatal("the chat side exists")
		}
		if h.d.kmemBoxContent() != obj {
			t.Fatal("asking again returns the same content")
		}
		os.MkdirAll("/tmp/quill-op-d-shots", 0o755)
		tw := test.NewWindow(obj)
		tw.Resize(fyne.NewSize(1000, 600))
		shot(t, tw, "/tmp/quill-op-d-shots", "kmem-box-content.png")
		tw.Close()
	})
}

func TestKmemPopOutKeepsState(t *testing.T) {
	h := kmemBoxHarness(t)
	var content fyne.CanvasObject
	h.ui(func() {
		content = h.d.kmemBoxContent()
		h.d.kmemWin.panel.tabs.SelectIndex(kmemTabTasks)
		h.d.kmemWin.setChat(wrapLabel("(chat)"))
		h.d.popOutKmem()
		w := h.d.kmemWin
		if w.win == nil || w.win.Content() != content {
			t.Fatal("Pop out shows the same content in its own window")
		}
		if w.panel.tabs.SelectedIndex() != kmemTabTasks {
			t.Fatal("the selected tab is kept")
		}
		if !strings.Contains(labelsIn([]fyne.CanvasObject{w.right}), "(chat)") {
			t.Fatal("the chat is kept")
		}
		h.d.popOutKmem()
		if h.d.kmemWin != w {
			t.Fatal("pop out again raises")
		}
		w.close()
	})
}

func TestShowKmemFromButtonAndCommand(t *testing.T) {
	h := kmemBoxHarness(t)
	h.ui(func() {
		test.Tap(h.d.kmemBtn)
		w := h.d.kmemWin
		if w == nil || w.win == nil {
			t.Fatal("the kmem button calls showKmem, which pops the window out")
		}
		w.close()
		h.d.showKmem()
		if h.d.kmemWin == nil || h.d.kmemWin.win == nil {
			t.Fatal("showKmem opens again")
		}
		h.d.kmemWin.close()
	})
}
