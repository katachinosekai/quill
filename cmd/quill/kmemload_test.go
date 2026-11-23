package main

import (
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// countingKmem is a fake kmem that counts calls per command.
type countingKmem struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *countingKmem) run(prog string, args ...string) (string, error) {
	a := strings.Join(args, " ")
	c.mu.Lock()
	c.n[a]++
	c.mu.Unlock()
	time.Sleep(20 * time.Millisecond) // a real kmem takes time; overlapping loads must join
	switch a {
	case "version --json":
		return `{"version": "1.8.1", "api": 1}`, nil
	case "brief --json":
		return `{"brief": "bin: 1 item(s) waiting for the user\n", "api": 1}`, nil
	case "handoff list --json":
		return `{"handoffs": [], "api": 1}`, nil
	case "task list --json":
		return `{"tasks": [], "api": 1}`, nil
	case "rollout status --json":
		return `{"paused": null, "api": 1}`, nil
	case "trash list":
		return "[f0000b1] fact, 1 days in bin, by demo: duplicate\n    (decision) demo text\n", nil
	}
	return "", nil
}

func (c *countingKmem) reset() {
	c.mu.Lock()
	c.n = map[string]int{}
	c.mu.Unlock()
}

func (c *countingKmem) max() (string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	worst, hi := "", 0
	for k, v := range c.n {
		if v > hi {
			worst, hi = k, v
		}
	}
	return worst, hi
}

// One window event (open, raise, tab change, bin change, the timer's refresh)
// loads each kmem read once, not once per refresh path.
func TestKmemWindowEventsLoadOnce(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	ck := &countingKmem{n: map[string]int{}}
	h.d.kmemRun = ck.run
	oldOpen, oldClose := kmemWindowOpenHook, kmemWindowCloseHook
	kmemWindowOpenHook, kmemWindowCloseHook = nil, nil
	defer func() { kmemWindowOpenHook, kmemWindowCloseHook = oldOpen, oldClose }()
	settle := func() { time.Sleep(300 * time.Millisecond) }
	check := func(event string) {
		t.Helper()
		settle()
		if k, n := ck.max(); n > 1 {
			t.Fatalf("%s: %q ran %d times: %v", event, k, n, ck.n)
		}
	}

	// open: the panel reload, the badge refresh and the rename scan fire together
	h.ui(func() {
		test.Tap(h.d.kmemBtn)
		h.d.refreshKmemNeeds()
	})
	check("open")
	if ck.n["brief --json"] != 1 || ck.n["trash list"] != 1 {
		t.Fatalf("open reads kmem once: %v", ck.n)
	}

	// raise (button tapped again) and tab switch load nothing twice
	ck.reset()
	time.Sleep(kmemCoalesceFor)
	h.ui(func() {
		h.d.openKmemWindow()
		h.d.showKmemPanelTab(kmemTabTasks)
		h.d.refreshKmemNeeds()
	})
	check("raise and tab switch")

	// the bin changing: the bin view, the badge and the panel each reload
	ck.reset()
	time.Sleep(kmemCoalesceFor)
	h.ui(func() {
		b := h.d.newBinView()
		b.changed()
	})
	check("bin change")
	if ck.n["trash list"] != 1 {
		t.Fatalf("bin change lists the bin once: %v", ck.n)
	}

	// a write drops the shared answers: the next read is fresh
	ck.reset()
	h.d.runKmem("kmem", "trash", "restore", "f0000b1")
	h.d.runKmem("kmem", "trash", "list")
	if ck.n["trash list"] != 1 {
		t.Fatalf("read after write: %v", ck.n)
	}
	h.d.runKmem("kmem", "trash", "restore", "f0000b2")
	h.d.runKmem("kmem", "trash", "list")
	if ck.n["trash list"] != 2 {
		t.Fatalf("a write invalidates the shared answer: %v", ck.n)
	}
	h.ui(func() { h.d.kmemWin.close() })
}
