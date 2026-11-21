package main

import (
	"slices"
	"testing"
	"time"
)

func TestReviewFixes1121(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		s.restarting, s.failed = true, "could not start"
		if s.status() != stError {
			t.Fatal("a failed start shows the error, not restarting")
		}

		var tabs []*deskSession
		for len(h.d.tabs) < maxTabs {
			tabs = append(tabs, h.d.createSession(h.proj, "default", "default"))
		}
		first := h.d.tabs[0]
		victim := tabs[len(tabs)-1]
		h.d.open(victim.id)
		before := append([]string(nil), h.d.tabs...)
		ns := h.d.replaceWith(victim, "replaced")
		if len(h.d.tabs) != maxTabs || h.d.tabs[0] != first || slices.Index(h.d.tabs, ns.id) != slices.Index(before, victim.id) {
			t.Fatalf("a restart with full tabs takes the old tab's slot and evicts nothing: %v", h.d.tabs)
		}

		h.d.lastHandoffs = []kmemHandoff{{Name: "quill-android", Saved: time.Now()}}
		n := h.d.createSession(h.proj, "default", "default")
		h.d.kmemOff = false
		h.d.applyKmemChoice(n, kmemNewChoice, "Quill Android")
		if h.d.org.Kmem[n.id] == "quill-android" {
			t.Fatal("a new kmem name never takes a saved handoff's name")
		}
		h.d.kmemOff = true

		if !deviceCommandBlocked("/login") || !deviceCommandBlocked("/add-dir /etc") || deviceCommandBlocked("/status") || deviceCommandBlocked("hello /login") {
			t.Fatal("desktop-only commands are blocked for devices, others pass")
		}
		h.d.input.SetText("my unsent draft")
		h.d.runLocalCommandFor(h.d.current, "/status")
		if h.d.input.Text != "my unsent draft" {
			t.Fatal("a local command from a device keeps the desktop draft")
		}
		h.d.runLocalCommand("/status")
		if h.d.input.Text != "" {
			t.Fatal("typed on the desktop, the box clears as before")
		}
	})
}
