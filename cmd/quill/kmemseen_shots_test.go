package main

import (
	"os"
	"strings"
	"testing"
)

// QUILL_SHOTS_DIR=/tmp/x go test -run TestKmemBadgeShots writes the badge and
// the window's Overview as PNGs.
func TestKmemBadgeShots(t *testing.T) {
	dir := os.Getenv("QUILL_SHOTS_DIR")
	if dir == "" {
		t.Skip("set QUILL_SHOTS_DIR")
	}
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	oldOpen, oldClose := kmemWindowOpenHook, kmemWindowCloseHook
	kmemWindowOpenHook, kmemWindowCloseHook = nil, nil
	defer func() { kmemWindowOpenHook, kmemWindowCloseHook = oldOpen, oldClose }()
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "version --json":
			return `{"version": "1.8.1", "api": 1}`, nil
		case "brief --json":
			return `{"brief": "rules: read, unchanged\nbin: 2 item(s) waiting for the user\n", "api": 1}`, nil
		case "task list --json":
			return `{"tasks": [{"id": "t0000c1", "prio": 2, "status": "open", "title": "kmem issue: maint warns about a moved folder", "topic": "kmem", "owner": null}], "api": 1}`, nil
		case "rollout status --json":
			return `{"paused": {"stage": "demo", "err": "waiting for review"}, "api": 1}`, nil
		case "handoff list --json":
			return `{"handoffs": [], "api": 1}`, nil
		}
		return "", nil
	}
	h.ui(func() {
		h.d.setKmemNeeds(kmemNeeds{RolloutWait: "stage demo: waiting for review", Bin: 2,
			Issues: []kmemTask{{ID: "t0000c1", Status: "open", Title: "kmem issue: maint warns about a moved folder", Topic: "kmem"}}})
		shot(t, h.w, dir, "badge-on-button.png")
		h.d.openKmemWindow()
	})
	h.eventually("loaded", func() bool {
		return h.d.kmemWin != nil && h.d.kmemWin.panel.data != nil
	})
	h.ui(func() {
		shot(t, h.d.kmemWin.win, dir, "kmem-window-overview.png")
		shot(t, h.w, dir, "badge-after-window-shown.png")
		h.d.kmemWin.close()
	})
}
