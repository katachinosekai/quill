package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func guardMatchers(t *testing.T, settings string) []string {
	t.Helper()
	var s struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	raw, _ := os.ReadFile(settings)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range s.Hooks["PreToolUse"] {
		m, _ := g["matcher"].(string)
		out = append(out, m)
	}
	return out
}

func TestKmemGuardMatchesPowerShell(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		if m, _ := kmemHooksFor(goos, "/home/user", "python")["PreToolUse"]["matcher"].(string); m != kmemGuardMatcher {
			t.Fatalf("%s: new installs guard PowerShell: %q", goos, m)
		}
	}
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	old := `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"python \"C:/Users/user/.local/bin/kmem\" brief"}]}],
"PreToolUse":[{"matcher":"Bash|Agent|Task|Write|Edit|MultiEdit|NotebookEdit","hooks":[{"type":"command","command":"python \"C:/Users/user/.local/bin/kmem-guard\""}]},
{"matcher":"Bash","hooks":[{"type":"command","command":"other-tool check"}]}]}}`
	os.WriteFile(settings, []byte(old), 0o600)
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.UTC)
	changed, backup, err := mergeKmemHooks(settings, home, now)
	if err != nil || !changed || backup == "" {
		t.Fatalf("an old kmem-guard matcher gets PowerShell: %v %v %q", changed, err, backup)
	}
	got := guardMatchers(t, settings)
	if len(got) != 2 || got[0] != kmemGuardMatcher || got[1] != "Bash" {
		t.Fatalf("only the kmem-guard group changes: %q", got)
	}
	if b, _ := os.ReadFile(backup); string(b) != old {
		t.Fatal("the original settings are backed up first")
	}
	if changed, _, _ := mergeKmemHooks(settings, home, now); changed {
		t.Fatal("running it again changes nothing")
	}
}
