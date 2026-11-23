package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestKmemIssuesInNeeds(t *testing.T) {
	tasks := []kmemTask{
		{ID: "t1", Status: "open", Title: "kmem issue: maint flags a path", Topic: "kmem"},
		{ID: "t2", Status: "done", Title: "kmem issue: old", Topic: "kmem"},
		{ID: "t3", Status: "open", Title: "kmem issue: wrong topic", Topic: "quill"},
		{ID: "t4", Status: "claimed", Title: "  KMEM Issue: case and space", Topic: "kmem"},
		{ID: "t5", Status: "open", Title: "USER: decide", Topic: "user"},
		{ID: "t6", Status: "open", Title: "kmem issue: filed in the user topic", Topic: "user"},
	}
	n := parseKmemNeeds("", tasks)
	if len(n.Issues) != 2 || n.Issues[0].ID != "t1" || n.Issues[1].ID != "t4" {
		t.Fatalf("open kmem issues in topic kmem, any case: %+v", n.Issues)
	}
	if len(n.UserTasks) != 2 || n.Count() != 4 {
		t.Fatalf("issues count once, user tasks stay: %+v count %d", n.UserTasks, n.Count())
	}
	n.RolloutWait = "stage quill: tests failed"
	if n.Count() != 5 {
		t.Fatalf("a paused rollout counts once: %d", n.Count())
	}
}

func TestReadRolloutWait(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	paused := `{"paused": {"t": "2026-11-22T10:00:00Z", "stage": "quill", "err": "review packet missing"}, "api": 1}`
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "version --json":
			return `{"version": "1.8.1", "api": 1}`, nil
		case "rollout status --json":
			return paused, nil
		}
		return "", nil
	}
	if got := h.d.readRolloutWait(); got != "stage quill: review packet missing" {
		t.Fatalf("paused rollout: %q", got)
	}
	paused = `{"paused": null, "api": 1}`
	if got := h.d.readRolloutWait(); got != "" {
		t.Fatalf("not paused: %q", got)
	}
	paused = ""
	if got := h.d.readRolloutWait(); got != "" {
		t.Fatalf("a kmem without rollouts: %q", got)
	}
}

func TestKmemWindow(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	var mu sync.Mutex
	var calls []string
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		a := strings.Join(args, " ")
		calls = append(calls, a)
		switch a {
		case "version --json":
			return `{"version": "1.8.1", "api": 1}`, nil
		case "brief --json":
			return `{"brief": "rules: read, unchanged\nbin: 1 item(s) waiting for the user\nAUDIT PROBLEMS: chain gap at seq 12\n", "api": 1}`, nil
		case "topics":
			return "demo/notes  3 facts, 1 KB\ndemo/big  40 facts, 12 KB\n", nil
		case "handoff list --json":
			return `{"handoffs": [{"name": "kmem-lock-audit", "items": 4, "saved": "2026-11-21T06:00:00Z"}, {"name": "demo-work", "items": 2, "saved": "2026-11-21T06:00:00Z"}], "api": 1}`, nil
		case "task list --json":
			return `{"tasks": [{"id": "t0000c1", "prio": 2, "status": "open", "title": "kmem issue: maint warns about a moved folder", "topic": "kmem", "owner": null}, {"id": "t0000c2", "prio": 4, "status": "open", "title": "Tidy notes", "topic": "demo", "owner": null}], "api": 1}`, nil
		case "rollout status --json":
			return `{"paused": {"t": "2026-11-22T10:00:00Z", "stage": "demo", "err": "waiting for review"}, "api": 1}`, nil
		case "trash list":
			return "[f0000b1] fact, 1 days in bin, by demo: duplicate\n    (decision) demo text\n", nil
		}
		return "", nil
	}
	h.ui(func() { h.d.app.Preferences().SetFloat("kmemWinW", 1100) })
	opened, closed := 0, 0
	var closedWith *kmemWindow
	oldOpen, oldClose := kmemWindowOpenHook, kmemWindowCloseHook
	kmemWindowOpenHook = func(d *desk, w *kmemWindow) { opened++ }
	kmemWindowCloseHook = func(d *desk, w *kmemWindow) {
		closed++
		closedWith = w
		if d.kmemWin != nil {
			t.Error("the close hook runs after d.kmemWin is cleared")
		}
	}
	defer func() { kmemWindowOpenHook, kmemWindowCloseHook = oldOpen, oldClose }()

	var w *kmemWindow
	tabsBefore := 0
	h.ui(func() {
		tabsBefore = len(h.d.tabs)
		test.Tap(h.d.kmemBtn)
		w = h.d.kmemWin
		if w == nil || w.win == nil || w.panel == nil || h.d.kmemPanelNow != w.panel {
			t.Fatal("the kmem button opens the kmem window")
		}
		if w.win.Title() != "kmem — Quill Desktop" {
			t.Fatalf("title: %q", w.win.Title())
		}
		if sz := w.win.Canvas().Size(); sz.Width < 1000 || sz.Width > 1200 {
			t.Fatalf("the saved width is used: %v", sz)
		}
		if w.split.Offset != kmemWinDefaultSplit {
			t.Fatalf("default split: %v", w.split.Offset)
		}
		if !strings.Contains(labelsIn([]fyne.CanvasObject{w.right}), "assistant is off") {
			t.Fatal("without an assistant the right side says why in one line")
		}
		h.d.openKmemWindow()
		h.d.resumeHandoffNamed(kmemWinOperator)
		if h.d.kmemWin != w || opened != 1 {
			t.Fatalf("opening again raises the same window (opened %d)", opened)
		}
		if len(h.d.tabs) != tabsBefore {
			t.Fatal("the operator's handoff opens the window, not a tab")
		}
	})
	h.eventually("panel loaded", func() bool {
		return strings.Contains(labelsIn(w.panel.over.Objects), "rules: read")
	})
	h.ui(func() {
		over := labelsIn(w.panel.over.Objects)
		for _, want := range []string{"Waiting for you (4)", "1 kmem issue(s) from other sessions", "t0000c1", "rollout is paused", "stage demo: waiting for review"} {
			if !strings.Contains(over, want) {
				t.Fatalf("overview lacks %q:\n%s", want, over)
			}
		}
		for _, b := range []string{"Ask the operator", "Explain", "Suggest split"} {
			if buttonIn(w.panel.over.Objects, b) != nil {
				t.Fatalf("%q fills the chat input; hidden while the chat is off", b)
			}
		}
		if h.d.kmemBubble.text.Text != "4" {
			t.Fatalf("bubble counts bin, audit, issue and rollout: %q", h.d.kmemBubble.text.Text)
		}
		var filled []string
		w.setChat(wrapLabel("(operator chat)"))
		w.setPrefill(func(s string) { filled = append(filled, s) })
		ask := buttonIn(w.panel.over.Objects, "Ask the operator")
		explain := buttonIn(w.panel.over.Objects, "Explain")
		split := buttonIn(w.panel.over.Objects, "Suggest split")
		if ask == nil || explain == nil || split == nil {
			t.Fatalf("with the chat on the panel offers to ask: %s", labelsIn(w.panel.over.Objects))
		}
		test.Tap(ask)
		test.Tap(explain)
		test.Tap(split)
		if len(filled) != 3 || !strings.Contains(filled[0], "t0000c1") || !strings.Contains(filled[1], "chain gap") || !strings.Contains(filled[2], "demo/big") {
			t.Fatalf("prefills: %q", filled)
		}
		hand := labelsIn(w.panel.hand.Objects)
		if !strings.Contains(hand, "kmem-lock-audit") || !strings.Contains(hand, "demo-work") {
			t.Fatalf("handoffs: %s", hand)
		}
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, w.win, dir, "kmem-window.png") })
	}
	h.ui(func() {
		w.setChatReasonWith("kmem-lock-audit is live in the tab “demo” ", "Open it", func() {})
		if buttonIn(w.panel.over.Objects, "Ask the operator") != nil || buttonIn([]fyne.CanvasObject{w.right}, "Open it") == nil {
			t.Fatal("a reason line turns the chat off and may carry one button")
		}
		test.Tap(buttonIn(w.panel.over.Objects, "Review bin"))
		if h.d.kmemBinNow == nil || h.d.kmemBinNow.parent != w.win {
			t.Fatal("Review bin opens on the kmem window")
		}
		if w.win.Canvas().Overlays().Top() == nil || h.w.Canvas().Overlays().Top() != nil {
			t.Fatal("the bin dialog is on the kmem window's canvas, not the main window's")
		}
		w.escape()
		if w.win.Canvas().Overlays().Top() != nil || h.d.kmemWin != w {
			t.Fatal("Escape closes the dialog first, not the window")
		}
		w.panel.deleteHandoff("demo-work", 2)
		if w.win.Canvas().Overlays().Top() == nil || h.w.Canvas().Overlays().Top() != nil {
			t.Fatal("Delete handoff asks on the kmem window")
		}
		w.escape()
		h.d.showKmemPanelTab(kmemTabTasks)
		if w.panel.tabs.SelectedIndex() != kmemTabTasks {
			t.Fatal("showKmemPanelTab selects the tab in the open window")
		}
		w.split.SetOffset(0.6)
		w.win.Resize(fyne.NewSize(1000, 700))
		w.win.Canvas().OnTypedKey()(&fyne.KeyEvent{Name: fyne.KeyEscape})
		if h.d.kmemWin != nil || h.d.kmemPanelNow != nil || closed != 1 || closedWith != w {
			t.Fatal("Escape with nothing open closes the window through the close flow")
		}
		w.close()
		if closed != 1 {
			t.Fatal("the close flow runs once")
		}
		prefs := h.d.app.Preferences()
		if prefs.Float("kmemWinSplit") != 0.6 || prefs.Float("win.kmem.w") != 1000 || prefs.Float("win.kmem.h") != 700 {
			t.Fatalf("size and split saved: %v %v %v", prefs.Float("kmemWinSplit"), prefs.Float("win.kmem.w"), prefs.Float("win.kmem.h"))
		}
		h.d.openKmemWindow()
		if h.d.kmemWin == nil || h.d.kmemWin == w || h.d.kmemWin.split.Offset != 0.6 || opened != 2 {
			t.Fatal("reopening builds a new window with the saved split")
		}
		if sz := h.d.kmemWin.win.Canvas().Size(); sz.Width != 1000 || sz.Height != 700 {
			t.Fatalf("reopening restores the saved size: %v", sz)
		}
		h.d.kmemWin.close()
	})
	mu.Lock()
	defer mu.Unlock()
	for _, c := range calls {
		if strings.HasPrefix(c, "trash empty") || strings.HasPrefix(c, "add") {
			t.Fatalf("the window writes nothing by itself: %q", c)
		}
	}
}

func TestKmemWindowNeedsKmem(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.openKmemWindow()
		if h.d.kmemWin != nil {
			t.Fatal("no kmem, no window")
		}
		if h.w.Canvas().Overlays().Top() == nil {
			t.Fatal("the main window says kmem is not available")
		}
	})
}
