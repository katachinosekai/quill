package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKmemBundleAndSetup(t *testing.T) {
	bundled, _ := filepath.Abs(filepath.Join("..", "..", "third_party", "kmem"))
	t.Setenv("QUILL_KMEM_DIR", bundled)
	if bundledKmemDir() != bundled || bundledKmemVersion(bundled) == "" {
		t.Fatalf("bundled kmem found with its version: %q %q", bundledKmemDir(), bundledKmemVersion(bundled))
	}
	for _, f := range []string{"kmem", "kmem-guard"} {
		b, _ := os.ReadFile(filepath.Join(bundled, f))
		for _, bad := range personalPatterns() {
			if strings.Contains(string(b), bad) {
				t.Fatalf("bundled %s carries personal data %q", f, bad)
			}
		}
	}

	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	orig := `{"model":"opus","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"other-tool ensure"}]}]},"permissions":{"allow":["Bash(ls:*)"]}}`
	os.WriteFile(settings, []byte(orig), 0o600)
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.Local)
	done, err := setupKmem(home, bundled, now)
	if err != nil || len(done) != 3 {
		t.Fatalf("setup: %v %v", done, err)
	}
	for _, f := range []string{"kmem", "kmem-guard"} {
		st, err := os.Stat(filepath.Join(home, ".local", "bin", f))
		if err != nil || st.Mode()&0o111 == 0 {
			t.Fatalf("%s installed executable: %v", f, err)
		}
	}
	var got map[string]any
	raw, _ := os.ReadFile(settings)
	if json.Unmarshal(raw, &got) != nil || got["model"] != "opus" || got["permissions"] == nil {
		t.Fatalf("other settings kept: %s", raw)
	}
	hooks := got["hooks"].(map[string]any)
	ss := hooks["SessionStart"].([]any)
	if len(ss) != 2 || !hasHookCommand(ss, "other-tool ensure") || !hasHookCommand(ss, filepath.Join(home, ".local", "bin", "kmem")+" brief") {
		t.Fatalf("SessionStart keeps the user's hook and adds kmem brief: %v", ss)
	}
	if !hasHookCommand(hooks["PreToolUse"].([]any), "kmem-guard") {
		t.Fatal("PreToolUse gets kmem-guard")
	}
	if b, err := os.ReadFile(settings + ".bak-quill-20261121-040000"); err != nil || string(b) != orig {
		t.Fatal("the original settings are backed up first")
	}
	if changed, _, _ := mergeKmemHooks(settings, home, now); changed {
		t.Fatal("running setup twice adds nothing")
	}
	if _, err := setupKmem(home, bundled, now); err == nil || !strings.Contains(err.Error(), "never replaces") {
		t.Fatalf("an installed kmem is never replaced: %v", err)
	}
	broken := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(broken, []byte("{not json"), 0o600)
	if _, _, err := mergeKmemHooks(broken, home, now); err == nil {
		t.Fatal("invalid settings are left alone")
	}
	if b, _ := os.ReadFile(broken); string(b) != "{not json" {
		t.Fatal("and not rewritten")
	}
	if _, err := exec.LookPath("python3"); err == nil {
		cmd := exec.Command("python3", "-I", filepath.Join(home, ".local", "bin", "kmem"), "version")
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.HasPrefix(string(out), "kmem "+bundledKmemVersion(bundled)+" api 1") {
			t.Fatalf("the installed bundled kmem runs: %s %v", out, err)
		}
	}
}

// personalPatterns: this machine's home folder, host name and the user part
// of the git email, worked out at run time so the repo holds no personal data.
func personalPatterns() []string {
	var out []string
	if h, err := os.UserHomeDir(); err == nil && h != "/" {
		out = append(out, h)
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		out = append(out, h)
	}
	if b, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		if u, _, ok := strings.Cut(strings.TrimSpace(string(b)), "@"); ok && u != "" {
			out = append(out, u)
		}
	}
	return out
}
