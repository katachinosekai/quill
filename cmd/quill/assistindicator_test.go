package main

import (
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
)

// The assistant box header shows the main chat's indicator (t9a3a6a): the
// state from liveStatus, the subagents and the model.
func TestAssistIndicatorStates(t *testing.T) {
	h, _ := quillSetup(t)
	a, s := bootQuill(h)
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	withClock(t, &now)
	h.ui(func() {
		d := h.d
		d.openAssistBox(boxQuill)
		header := func() string { d.refreshBoxHeader(); return d.box().status.Text }
		model := " · " + s.model
		if s.model == "" {
			model = ""
		}
		if got := header(); got != "Ready"+model {
			t.Fatalf("idle: %q", got)
		}
		s.conv.Busy, s.conv.Thinking = true, true
		if got := header(); got != "✻ Thinking…"+model {
			t.Fatalf("thinking: %q", got)
		}
		s.conv.Thinking = false
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant",
			Content: []byte(`[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test ./..."}}]`)}})
		if got := header(); !strings.HasPrefix(got, "● Bash") || !strings.HasSuffix(got, model) {
			t.Fatalf("running tool: %q", got)
		}
		s.agentGrants = map[string]agentGrant{"x": {}, "y": {}}
		if got := header(); !strings.HasPrefix(got, "● Bash") || !strings.Contains(got, " · 2 subagents") {
			t.Fatalf("subagents: %q", got)
		}
		s.agentGrants = nil
		s.conv.Busy, s.planWait = false, true
		if got := header(); got != "Waiting for your approval"+model {
			t.Fatalf("plan waiting: %q", got)
		}
		s.planWait = false
		old := a.state
		a.state = asStopping
		if got := header(); got != "Stopped" {
			t.Fatalf("not running: %q", got)
		}
		a.reason = "Quill operator is off."
		if got := header(); got != a.reason {
			t.Fatalf("reason line: %q", got)
		}
		a.state, a.reason = old, ""
	})
}

// "last activity N s ago" from 3 s on, reset by each event; the header
// pulses once the turn has run 30 s; the injected clock drives both.
func TestAssistIndicatorAgeAndPulse(t *testing.T) {
	h, _ := quillSetup(t)
	_, s := bootQuill(h)
	now := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	withClock(t, &now)
	h.ui(func() {
		d := h.d
		d.openAssistBox(boxQuill)
		b := d.box()
		header := func() string { d.refreshBoxHeader(); return b.status.Text }
		s.lastAct, s.turnStart = time.Time{}, time.Time{}
		s.conv.Busy = true
		start := now
		if got := header(); strings.Contains(got, "last activity") || b.stPulse.pulsing {
			t.Fatalf("fresh turn: %q pulse %v", got, b.stPulse.pulsing)
		}
		now = start.Add(2 * time.Second)
		if got := header(); strings.Contains(got, "last activity") {
			t.Fatalf("2 s: no age yet: %q", got)
		}
		now = start.Add(12 * time.Second)
		if got := header(); !strings.Contains(got, " · last activity 12 s ago") {
			t.Fatalf("12 s: %q", got)
		}
		s.noteActivity(now) // an event
		if got := header(); strings.Contains(got, "last activity") {
			t.Fatalf("an event resets the age: %q", got)
		}
		now = start.Add(29 * time.Second)
		if got := header(); !strings.Contains(got, "last activity 17 s ago") || b.stPulse.pulsing {
			t.Fatalf("29 s: %q, no pulse yet (%v)", got, b.stPulse.pulsing)
		}
		now = start.Add(31 * time.Second)
		header()
		if !b.stPulse.pulsing || b.stPulse.pulse == nil {
			t.Fatal("31 s: the header pulses")
		}
		s.conv.Busy = false
		if got := header(); strings.Contains(got, "last activity") || b.stPulse.pulsing || b.stPulse.pulse != nil {
			t.Fatalf("idle: %q pulse %v", got, b.stPulse.pulsing)
		}
	})
}
