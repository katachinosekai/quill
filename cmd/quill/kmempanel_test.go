package main

import (
	"os/exec"

	"fyne.io/fyne/v2"
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestKmemPanelParsers(t *testing.T) {
	ts := parseTopics("browser/launch  2 facts, 2 KB\nchunkstream/bottleneck  13 facts, 11 KB\njunk\n")
	if len(ts) != 2 || ts[1].Name != "chunkstream/bottleneck" || ts[1].Facts != 13 || ts[1].KB != 11 {
		t.Fatalf("topics: %+v", ts)
	}
	bin := parseBin("[f467b16] fact, 0.2 days in bin, by launcher: wrong topic\n    (decision) launcher/x text...\n[t0000a1] task, 9 days in bin, by quill: dup\n")
	if len(bin) != 2 || bin[0].ID != "f467b16" || !strings.Contains(bin[0].Preview, "launcher/x") || bin[1].ID != "t0000a1" {
		t.Fatalf("bin: %+v", bin)
	}
	if ids := factIDs("[fdb3fc0] decision: x\n[f736ed0] decision: y [fdb3fc0]"); strings.Join(ids, ",") != "fdb3fc0,f736ed0" {
		t.Fatalf("fact ids: %v", ids)
	}
}

func TestKmemPanel(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	var mu sync.Mutex
	var calls []string
	binText := "[f467b16] fact, 8 days in bin, by launcher: wrong topic\n    (decision) launcher/x text\n"
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		a := strings.Join(args, " ")
		calls = append(calls, a)
		switch {
		case a == "version --json":
			return `{"version": "1.4.0", "api": 1}`, nil
		case a == "brief --json":
			return `{"brief": "rules: read, unchanged\nbin: 1 item(s) waiting for the user\n", "api": 1}`, nil
		case a == "topics":
			return "handoff/old-work  3 facts, 1 KB\nquill/decisions  40 facts, 13 KB\n", nil
		case a == "handoff list --json":
			return `{"handoffs": [{"name": "old-work", "items": 3, "saved": "2026-11-21T06:00:00-08:00"}], "api": 1}`, nil
		case a == "handoff get old-work --json":
			return `{"items": [{"id": "f0000a1"}, {"id": "f0000a2"}], "api": 1}`, nil
		case a == "task list --json":
			return `{"tasks": [{"id": "t1", "prio": 4, "status": "open", "title": "Do it", "topic": "load-opt", "owner": null}], "api": 1}`, nil
		case a == "trash list":
			return binText, nil
		}
		return "", nil
	}
	var started []string
	oldTerm := terminalCmd
	terminalCmd = func(kmemPath string) *exec.Cmd {
		started = append(started, kmemPath)
		return exec.Command("true")
	}
	defer func() { terminalCmd = oldTerm }()
	var p *kmemPanel
	h.ui(func() {
		h.d.showKmemPanel()
		p = h.d.kmemPanelNow
	})
	h.eventually("panel loaded", func() bool { return len(p.bin.Objects) > 2 && len(p.hand.Objects) > 1 })
	h.ui(func() {
		over := labelsIn(p.over.Objects)
		if !strings.Contains(over, "quill/decisions (13 KB)") || !strings.Contains(over, "rules: read") {
			t.Fatalf("overview shows the brief and oversized sections: %s", over)
		}
		if !strings.Contains(labelsIn(p.hand.Objects), "old-work") || !strings.Contains(labelsIn(p.tasks.Objects), "Do it") {
			t.Fatal("handoffs and tasks are listed")
		}
		if !strings.Contains(labelsIn(p.bin.Objects), "f467b16") {
			t.Fatal("the bin lists its items")
		}
		restore := buttonIn(p.bin.Objects, "Restore")
		test.Tap(restore)
	})
	h.eventually("restored", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(calls, "|"), "trash restore f467b16")
	})
	h.ui(func() {
		p.emptyBin()
		confirmTopDialog(t, h)
	})
	h.eventually("terminal opened", func() bool { return len(started) == 1 })
	h.ui(func() {
		p.deleteHandoff("old-work", 3)
		confirmTopDialog(t, h)
	})
	h.eventually("handoff binned", func() bool {
		mu.Lock()
		defer mu.Unlock()
		j := strings.Join(calls, "|")
		return strings.Contains(j, "trash f0000a1 ") && strings.Contains(j, "trash f0000a2 ")
	})
	mu.Lock()
	for _, c := range calls {
		if strings.HasPrefix(c, "trash empty") {
			t.Fatal("Quill never runs the purge itself; kmem asks the user in a terminal")
		}
	}
	mu.Unlock()
}

func walkObjects(objs []fyne.CanvasObject, f func(fyne.CanvasObject)) {
	for _, o := range objs {
		f(o)
		switch x := o.(type) {
		case *fyne.Container:
			walkObjects(x.Objects, f)
		case fyne.Widget:
			walkObjects(test.WidgetRenderer(x).Objects(), f)
		}
	}
}

func labelsIn(objs []fyne.CanvasObject) string {
	var out []string
	walkObjects(objs, func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Label:
			out = append(out, x.Text)
		case *widget.Button:
			out = append(out, x.Text)
		}
	})
	return strings.Join(out, "\n")
}

func buttonIn(objs []fyne.CanvasObject, text string) *widget.Button {
	var b *widget.Button
	walkObjects(objs, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Button); ok && x.Text == text && b == nil {
			b = x
		}
	})
	return b
}

func confirmTopDialog(t *testing.T, h *harness) {
	t.Helper()
	ovs := h.w.Canvas().Overlays().List()
	if len(ovs) == 0 {
		t.Fatal("no dialog open")
	}
	b := buttonIn([]fyne.CanvasObject{ovs[len(ovs)-1]}, "Yes")
	if b == nil {
		t.Fatalf("the top dialog has no Yes button: %s", labelsIn([]fyne.CanvasObject{ovs[len(ovs)-1]}))
	}
	test.Tap(b)
}
