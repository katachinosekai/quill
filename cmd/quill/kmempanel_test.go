package main

import (
	"os"
	"os/exec"
	"path/filepath"

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
	longWhy := "wrong topic: filed under 'decision' because of a bad add syntax, re-added under launcher/decisions/features as a new fact with the same text, so this copy is not needed any more and only takes space in the history"
	binText := "[f467b16] fact, 8 days in bin, by launcher: " + longWhy + "\n    (decision) launcher/x text...\n"
	fullText := "[f467b16] decision: launcher/x text that kmem trash list cuts short but kmem show prints whole, all the way to the end.\n  IN THE RECYCLE BIN"
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
			return "handoff/old-work  3 facts, 1 KB\nquill/decisions  40 facts, 13 KB\nquill/status  1 fact, 1 KB\nload-opt/notes  2 facts, 1 KB\n", nil
		case a == "get quill/status":
			return "[f00000a9] decision: Phase 3 in progress\n", nil
		case a == "handoff list --json":
			return `{"handoffs": [{"name": "old-work", "items": 3, "saved": "2026-11-21T06:00:00-08:00"}], "api": 1}`, nil
		case a == "handoff get old-work --json":
			return `{"items": [{"id": "f0000a1"}, {"id": "f0000a2"}], "api": 1}`, nil
		case a == "task list --json":
			return `{"tasks": [{"id": "t1", "prio": 4, "status": "open", "title": "Do it", "topic": "load-opt", "owner": null}, {"id": "t2", "prio": 3, "status": "claimed", "title": "Mine", "topic": "quill", "owner": "quill", "checkpoint": "strip done"}], "api": 1}`, nil
		case a == "trash list":
			return binText, nil
		case a == "show f467b16":
			return fullText, nil
		}
		return "", nil
	}
	var started []string
	oldTerm := terminalCmd
	terminalCmd = func(kmemPath string, id ...string) *exec.Cmd {
		started = append(started, kmemPath+" "+strings.Join(id, " "))
		return exec.Command("true")
	}
	defer func() { terminalCmd = oldTerm }()
	var p *kmemPanel
	var kw fyne.Window
	h.ui(func() {
		h.d.showKmemPanel()
		p = h.d.kmemPanelNow
		if h.d.kmemWin == nil || p == nil || p.w != h.d.kmemWin {
			t.Fatal("the kmem button opens the kmem window with the panel on its left")
		}
		kw = h.d.kmemWin.win
	})
	defer h.ui(func() { h.d.kmemWin.close() })
	h.eventually("panel loaded", func() bool {
		return len(p.hand.Objects) > 1 && strings.Contains(labelsIn(p.over.Objects), "rules: read")
	})
	var b *binView
	h.ui(func() {
		over := labelsIn(p.over.Objects)
		if !strings.Contains(over, "quill/decisions (13 KB)") || !strings.Contains(over, "rules: read") {
			t.Fatalf("overview shows the brief and oversized sections: %s", over)
		}
		if !strings.Contains(over, "Waiting for you (1)") || !strings.Contains(over, "1 item in the recycle bin") {
			t.Fatalf("overview lists what kmem waits on the user for: %s", over)
		}
		for _, ti := range p.tabs.Items {
			if strings.Contains(ti.Text, "bin") {
				t.Fatal("the recycle bin moved to Settings; no bin tab in the kmem panel")
			}
		}
		if !strings.Contains(labelsIn(p.hand.Objects), "old-work") || !strings.Contains(labelsIn(p.tasks.Objects), "Do it") {
			t.Fatal("handoffs (as history) and tasks are listed")
		}
		tt := labelsIn(p.hand.Objects)
		for _, want := range []string{"quill", "load-opt", "Status: Phase 3 in progress", "Claimed [t2] Mine (owner quill); checkpoint: strip done", "Open [t1]", "History (read-only)", "Show"} {
			if !strings.Contains(tt, want) {
				t.Fatalf("topics tab misses %q: %s", want, tt)
			}
		}
		if buttonIn(p.hand.Objects, "Resume") != nil || buttonContaining(p.hand.Objects, "Delete") != nil || p.tabs.Items[1].Text != "Topics" {
			t.Fatal("the Topics tab has no Resume or Delete")
		}
		review := buttonIn(p.over.Objects, "Review bin")
		if review == nil {
			t.Fatal("Waiting for you has Review bin")
		}
		test.Tap(review)
		b = h.d.kmemBinNow
		if b == nil {
			t.Fatal("Review bin opens the bin dialog")
		}
	})
	h.eventually("bin loaded", func() bool { return len(b.box.Objects) > 2 })
	h.ui(func() {
		row := buttonContaining(b.box.Objects, "f467b16")
		if row == nil {
			t.Fatal("the bin lists its items as rows to click")
		}
		if buttonIn(b.box.Objects, "Restore") != nil {
			t.Fatal("no buttons in the list rows; they are in the item's window")
		}
		test.Tap(row)
		if b.item == nil {
			t.Fatal("clicking a row opens the item's window")
		}
	})
	h.eventually("full reason and text in the window", func() bool {
		if h.w.Canvas().Overlays().Top() != nil {
			t.Error("dialogs opened from the kmem window stay off the main window")
		}
		all := labelsIn([]fyne.CanvasObject{kw.Canvas().Overlays().Top()})
		return strings.Contains(all, longWhy) && strings.Contains(all, "all the way to the end")
	})
	h.ui(func() {
		var restore *widget.Button
		deepWalk(kw.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && b.Text == "Restore" {
				restore = b
			}
		})
		if restore == nil {
			t.Fatal("the window has Restore")
		}
		test.Tap(restore)
	})
	h.eventually("restored", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(calls, "|"), "trash restore f467b16")
	})
	h.ui(func() {
		b.emptyBin()
		confirmTopDialogOn(t, kw)
	})
	h.eventually("terminal opened", func() bool { return len(started) == 1 })
	if !strings.HasSuffix(strings.TrimSpace(started[0]), "kmem") && strings.Contains(started[0], "f467b16") {
		t.Fatalf("Empty bin purges everything, not one item: %q", started[0])
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() {
			p.tabs.SelectIndex(1)
			shot(t, kw, dir, "kmem-topics-tab.png")
		})
	}
	h.ui(func() {
		p.deleteHandoff("old-work", 3)
		confirmTopDialogOn(t, kw)
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
	confirmTopDialogOn(t, h.w)
}

func confirmTopDialogOn(t *testing.T, w fyne.Window) {
	t.Helper()
	ovs := w.Canvas().Overlays().List()
	if len(ovs) == 0 {
		t.Fatal("no dialog open")
	}
	b := buttonIn([]fyne.CanvasObject{ovs[len(ovs)-1]}, "Yes")
	if b == nil {
		t.Fatalf("the top dialog has no Yes button: %s", labelsIn([]fyne.CanvasObject{ovs[len(ovs)-1]}))
	}
	test.Tap(b)
}

func buttonContaining(objs []fyne.CanvasObject, part string) *widget.Button {
	var found *widget.Button
	for _, o := range objs {
		deepWalk(o, func(x fyne.CanvasObject) {
			if b, ok := x.(*widget.Button); ok && found == nil && strings.Contains(b.Text, part) {
				found = b
			}
		})
	}
	return found
}
