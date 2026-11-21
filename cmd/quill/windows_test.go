package main

import (
	"slices"
	"testing"
)

func TestWindowsAndStaleTabs(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "haiku", "default")
		s.conv.Stats.Model, s.conv.Stats.ContextMax, s.conv.Stats.ContextUsed = "claude-haiku-x", 1000000, 37055
		h.d.noteWindow(s)
		s.conv.Stats.ContextMax = 0
		h.d.windows = nil
		h.d.noteWindow(s)
		if s.conv.Stats.ContextMax != 1000000 {
			t.Fatalf("a reloaded session gets its model's remembered window: %d", s.conv.Stats.ContextMax)
		}

		good := h.d.createSession(h.proj, "default", "default")
		stale := h.d.createSession(h.proj, "default", "default")
		stale.cwd = "."
		ghost := "00000000-0000-0000-0000-00000000dead"
		h.d.tabs = append(h.d.tabs, ghost)
		if h.d.staleTab(good.id) || !h.d.staleTab(stale.id) || !h.d.staleTab(ghost) || h.d.staleTab(editsID) {
			t.Fatal("stale = no transcript, no folder, no messages, no process")
		}
		stale.queue = []string{"x"}
		if h.d.staleTab(stale.id) {
			t.Fatal("a queued message keeps it")
		}
		stale.queue = nil
		h.d.pruneStaleTabs()
		if slices.Contains(h.d.tabs, stale.id) || slices.Contains(h.d.tabs, ghost) || !slices.Contains(h.d.tabs, good.id) {
			t.Fatalf("only stale tabs are closed: %v", h.d.tabs)
		}
		if h.d.sessions[good.id] == nil {
			t.Fatal("nothing else is touched")
		}
	})
}
