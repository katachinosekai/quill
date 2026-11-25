package main

import (
	"strings"
	"testing"
)

// Check-in with a running assistant: a message, no new process.
func TestKmemCheckinMessagesRunningAssistant(t *testing.T) {
	h, _, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.ui(func() {
		if got := h.d.kmemAssistDeliver("Monday check-in: ask how it went"); !strings.Contains(got, "running") {
			t.Fatalf("delivered: %q", got)
		}
	})
	h.eventually("the assistant got the message", func() bool { return lastUser(s) == "Monday check-in: ask how it went" })
	h.ui(func() {
		if runningHidden(h.d) != 1 || h.d.assist().s != s {
			t.Fatal("still the one assistant")
		}
	})
}

func TestKmemCheckinWaitsForStart(t *testing.T) {
	h, k, _ := assistSetup(t)
	h.ui(func() {
		if got := h.d.kmemAssistDeliver("Monday check-in"); got != startAgentFirst {
			t.Fatalf("unexpected result from an idle agent: %q", got)
		}
		if runningHidden(h.d) != 0 || h.d.assist().s != nil || len(h.d.assist().pending) != 0 || resumeCalls(k) != 0 {
			t.Fatal("the check-in started a session or queued a prompt")
		}
	})
}

// A second start under a live kmem name is refused and the live one is used.
func TestKmemSecondSessionRefused(t *testing.T) {
	h, _, _ := assistSetup(t)
	var tab, ended *deskSession
	h.ui(func() {
		tab = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[tab.id] = "topic-one"
		h.d.send("start")
		ended = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[ended.id] = "topic-one"
		h.d.org.Ended = map[string]bool{ended.id: true}
	})
	h.eventually("the tab runs", func() bool { return tab.running() })
	h.ui(func() {
		before := len(h.d.sessions)
		if got := h.d.resumeFromHandoff(ended.id); got != tab {
			t.Fatal("redirected to the live session")
		}
		if len(h.d.sessions) != before {
			t.Fatal("no new session")
		}
		other := h.d.createSession(h.proj, "default", "default")
		before = len(h.d.sessions)
		h.d.applyKmemChoice(other, kmemResumeOf+"topic-one", "")
		if h.d.org.Kmem[other.id] == "topic-one" || len(h.d.sessions) != before {
			t.Fatal("a tab can't take a live handoff")
		}
	})
}

// Rotation: the old session stops first, then the new one starts.
func TestKmemRotationStopsThenStarts(t *testing.T) {
	h, _, _ := assistSetup(t)
	var old *deskSession
	h.ui(func() {
		old = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[old.id] = "topic-two"
		h.d.send("start")
	})
	h.eventually("old runs", func() bool { return old.running() && !old.conv.Busy })
	var ns *deskSession
	h.ui(func() {
		h.d.finishKmemRelaunch(old, "topic-two", "resume prompt")
		for id, n := range h.d.org.Kmem {
			if n == "topic-two" && id != old.id {
				ns = h.d.sessions[id]
			}
		}
		if ns == nil || ns.running() {
			t.Fatal("the new session must not run while the old one still does")
		}
	})
	h.eventually("new runs after the old stopped", func() bool {
		return ns.running() && !old.running() && h.d.sessions[old.id] == nil
	})
}

func TestKmemMessageIPC(t *testing.T) {
	h, _, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	path := t.TempDir() + "/q.sock"
	h.d.listenInstance(path)
	if !sendCommand(path, "kmem-message check in please") {
		t.Fatal("answered")
	}
	h.eventually("delivered", func() bool { return lastUser(s) == "check in please" })
}
