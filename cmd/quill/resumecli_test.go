package main

import (
	"strings"
	"testing"
)

func TestQuillResumeName(t *testing.T) {
	for n, want := range map[string]bool{"pz-lock-audit": true, "quill": true, "Quill2": true, "": false, "a b": false, "../x": false, "x;rm": false} {
		if validKmemName(n) != want {
			t.Errorf("validKmemName(%q) != %v", n, want)
		}
	}
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if strings.Join(args, " ") == "handoff list" {
			return "pz-lock-audit  6 items, saved 2026-11-21T06:50:26-08:00\n", nil
		}
		return "", nil
	}
	var live *deskSession
	h.ui(func() {
		live = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[live.id] = "quill-live"
		other := h.d.createSession(h.proj, "default", "default")
		h.d.open(other.id)
		h.d.resumeNamedFromCLI("quill-live")
	})
	h.eventually("a live session with the name is opened", func() bool { return h.d.current == live })
	h.ui(func() { h.d.resumeNamedFromCLI("no-such-name") })
	h.eventually("unknown name noted", func() bool {
		c := h.d.current
		return c != nil && len(c.conv.Items) > 0 && strings.Contains(c.conv.Items[len(c.conv.Items)-1].Text, "no kmem handoff or live session called “no-such-name”")
	})
	var before int
	h.ui(func() {
		before = len(h.d.sessions)
		h.d.resumeNamedFromCLI("pz-lock-audit")
	})
	h.eventually("a fresh session starts from the handoff", func() bool { return len(h.d.sessions) == before+1 })
}
