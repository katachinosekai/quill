package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/session"
)

func TestMemoryModeUsesKmem(t *testing.T) {
	cases := []struct {
		mode               string
		setup, avail, want bool
	}{
		{"", true, true, true},
		{"", false, true, false},
		{"", true, false, false},
		{memoryModeKmem, false, true, true},
		{memoryModeKmem, true, false, false},
		{memoryModeClaude, true, true, false},
	}
	for _, c := range cases {
		if got := memoryModeUsesKmem(c.mode, c.setup, c.avail); got != c.want {
			t.Errorf("mode %q setup %v avail %v: got %v want %v", c.mode, c.setup, c.avail, got, c.want)
		}
	}
}

func TestSessionPromptAndEnv(t *testing.T) {
	if !strings.HasPrefix(sessionPrompt(false, false, false), quillPromptHint) || !strings.Contains(sessionPrompt(false, false, false), "(recommended)") || sessionMemoryEnv(false) != nil {
		t.Fatal("claude mode must keep the plain hint and env")
	}
	if strings.Contains(sessionPrompt(false, false, false), buildGateHint) || strings.Contains(sessionPrompt(true, false, false), buildGateHint) {
		t.Fatal("build gate off must leave out the hint")
	}
	if !strings.Contains(sessionPrompt(false, true, false), buildGateHint) {
		t.Fatal("build gate on must add the hint")
	}
	p := sessionPrompt(true, true, true)
	if strings.Contains(sessionPrompt(true, true, false), coordinatorHint) {
		t.Fatal("coordinator off must leave out the hint")
	}
	if !strings.HasPrefix(p, quillPromptHint) || !strings.Contains(p, "kmem brief") || !strings.Contains(p, buildGateHint) || !strings.Contains(p, coordinatorHint) {
		t.Fatalf("kmem prompt: %q", p)
	}
	if !slices.Equal(sessionMemoryEnv(true), []string{autoMemoryOffEnv}) {
		t.Fatal("kmem mode must turn off auto memory")
	}
	args := strings.Join(session.Options{Prompt: p}.Args(), "\n")
	if !strings.Contains(args, "--append-system-prompt\n"+p) {
		t.Fatal("prompt not passed to claude")
	}
}

func TestBuildGatePreference(t *testing.T) {
	d := newHarness(t).d
	if !d.buildGateOn() {
		t.Fatal("ask before building must default to on")
	}
	d.app.Preferences().SetBool(prefBuildGate, false)
	if d.buildGateOn() {
		t.Fatal("turned off in settings must stay off")
	}
}

func TestCoordinatorPreference(t *testing.T) {
	d := newHarness(t).d
	if !d.coordinatorOn("default") || !d.coordinatorOn("claude-opus-5-5") {
		t.Fatal("online sessions coordinate by default")
	}
	if d.coordinatorOn("ollama:qwen3:8b") {
		t.Fatal("local models work alone")
	}
	d.app.Preferences().SetBool(prefCoordinator, false)
	if d.coordinatorOn("default") {
		t.Fatal("turned off in settings must stay off")
	}
}

func TestKmemMemoryPreference(t *testing.T) {
	d := newHarness(t).d
	d.kmemOff = false
	d.kmemRun = func(string, ...string) (string, error) { return "", nil }
	d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
	if d.kmemMemory() {
		t.Fatal("claude preference must win")
	}
	d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
	if !d.kmemMemory() {
		t.Fatal("kmem preference with kmem available must use kmem")
	}
	d.kmemOff = true
	if d.kmemMemory() {
		t.Fatal("kmem off must fall back to claude memory")
	}
}

func TestPinnedSection(t *testing.T) {
	ctx := "KMEM-BRIEF: x\nrules:\n  1. a\npinned:\n  [f1] keep replies short\n  [f2] ask in bold\nfacts:\n  [f3] other"
	got := pinnedSection(ctx)
	if got != "pinned:\n  [f1] keep replies short\n  [f2] ask in bold" {
		t.Fatalf("got %q", got)
	}
	if pinnedSection("facts:\n  [f3] x") != "" {
		t.Fatal("no pinned section expected")
	}
}

func TestTopicHintTexts(t *testing.T) {
	topic := topicHints{topic: "alpha", endCheck: true}
	m := topicMemoryHint(topic)
	for _, k := range []string{"`kmem brief`", "`kmem context --topic alpha`", "kmem --as alpha", "kmem task checkpoint", "`alpha/status`", "--replaces", "end-of-session check", "MEMORY.md"} {
		if !strings.Contains(m, k) {
			t.Errorf("topic memory hint lacks %q", k)
		}
	}
	if strings.Contains(topicMemoryHint(topicHints{topic: "alpha"}), "end-of-session") {
		t.Error("no end-check feature: the hint must not mention the check")
	}
	c := topicCoordinatorHint("alpha")
	for _, k := range []string{"`kmem context \"<task>\" --topic alpha`", "stable part first", "checkpoint your task", "Only you write `alpha/status`"} {
		if !strings.Contains(c, k) {
			t.Errorf("topic coordinator hint lacks %q", k)
		}
	}
	for _, h := range []string{m, c} {
		if l := strings.ToLower(h); strings.Contains(l, "handoff") || strings.Contains(l, "resume") {
			t.Errorf("topic hint mentions handoff/resume: %q", h)
		}
	}
	// today's hints stay for non-topic sessions
	plain := sessionPromptWith(true, true, true, topicHints{})
	if plain != sessionPrompt(true, true, true) || strings.Contains(plain, "--topic") || !strings.Contains(plain, kmemMemoryHint) {
		t.Error("non-topic session keeps today's hints")
	}
	full := sessionPromptWith(true, true, true, topic)
	if !strings.Contains(full, planFormatHint) || !strings.Contains(full, buildGateHint) || strings.Contains(full, coordinatorHint) || strings.Contains(full, kmemMemoryHint) {
		t.Error("topic prompt swaps both kmem hints and keeps the others")
	}
	if n := len(full); n > 3000 || n > len(sessionPrompt(true, true, true))+300 {
		t.Errorf("topic prompt too long: %d (today %d)", n, len(sessionPrompt(true, true, true)))
	}
	if len(m) > 700 {
		t.Errorf("topic memory hint %d chars", len(m))
	}
}

func TestSessionPromptForTopicFeatures(t *testing.T) {
	for _, feat := range []bool{false, true} {
		h := newHarness(t)
		endCheckKmem(t, h, feat)
		s := topicSession(h, "alpha")
		h.d.kmemAPILevel()
		var p string
		h.ui(func() {
			h.d.topicOf(s.id)
			p = h.d.sessionPromptFor(s, true)
		})
		if feat {
			if !strings.Contains(p, "`kmem context --topic alpha`") || !strings.Contains(p, "end-of-session check") || !strings.Contains(p, "Only you write `alpha/status`") {
				t.Errorf("features present: topic hints missing: %q", p)
			}
		} else if strings.Contains(p, "--topic") || strings.Contains(p, "end-of-session") || !strings.Contains(p, kmemMemoryHint) {
			t.Errorf("features absent: today's hints expected: %q", p)
		}
		if !strings.Contains(p, planApprovalHint) && h.d.planFirst {
			t.Error("plan approval hint must stay")
		}
	}
}
