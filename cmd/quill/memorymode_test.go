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
	if sessionPrompt(false) != quillPromptHint || sessionMemoryEnv(false) != nil {
		t.Fatal("claude mode must keep the plain hint and env")
	}
	p := sessionPrompt(true)
	if !strings.HasPrefix(p, quillPromptHint) || !strings.Contains(p, "kmem brief") {
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
