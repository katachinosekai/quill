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
	os.WriteFile(filepath.Join(home, ".local", "bin", "kmem"), []byte("#!/bin/sh\necho mine\n"), 0o755)
	os.Remove(filepath.Join(home, ".local", "bin", "kmem-guard"))
	if kmemSetupComplete(home) {
		t.Fatal("a missing kmem-guard means setup is incomplete")
	}
	done2, err := setupKmem(home, bundled, now)
	if err != nil || !strings.Contains(strings.Join(done2, "|"), "kept the existing") {
		t.Fatalf("finishing setup keeps the installed kmem and adds what is missing: %v %v", done2, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".local", "bin", "kmem")); string(b) != "#!/bin/sh\necho mine\n" {
		t.Fatal("an installed kmem is never replaced")
	}
	if !kmemSetupComplete(home) {
		t.Fatal("and setup is complete afterwards")
	}
	link := filepath.Join(t.TempDir(), "settings-link.json")
	os.Symlink(settings, link)
	if _, _, err := mergeKmemHooks(link, home, now); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("a symlinked settings.json stays a symlink")
	}
	odd := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(odd, []byte(`{"hooks":{"PreToolUse":{"x":1}}}`), 0o600)
	if _, _, err := mergeKmemHooks(odd, home, now); err == nil {
		t.Fatal("an unexpected hooks shape is left alone")
	}
	if shellQuote("/home/a b/.local/bin/kmem") != "'/home/a b/.local/bin/kmem'" || shellQuote("/home/k/kmem") != "/home/k/kmem" {
		t.Fatal("paths with spaces are quoted in the hook command")
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
