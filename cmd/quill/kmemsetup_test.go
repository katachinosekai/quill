package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installFakeKmem puts stand-in kmem and kmem-guard scripts in home's
// ~/.local/bin (Quill Desktop no longer ships kmem).
func installFakeKmem(t *testing.T, home string, files ...string) {
	t.Helper()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(bin, f), []byte("#!/bin/sh\necho placeholder\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKmemNotBundled(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", "third_party", "kmem")); err == nil {
		t.Fatal("kmem is not part of Quill Desktop: third_party/kmem must not exist")
	}
	spec, err := os.ReadFile(filepath.Join("..", "..", "packaging", "quill.spec"))
	if err != nil {
		t.Fatal(err)
	}
	mk, _ := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	for name, b := range map[string][]byte{"quill.spec": spec, "Makefile": mk} {
		if strings.Contains(string(b), "third_party/kmem") || strings.Contains(string(b), "kmem-sync") {
			t.Fatalf("%s still installs or vendors kmem", name)
		}
	}
	if !strings.HasPrefix(kmemPatreonURL, "https://") {
		t.Fatalf("kmemPatreonURL: %q", kmemPatreonURL)
	}
}

func TestKmemStatusAndHooks(t *testing.T) {
	home := t.TempDir()
	if text, offer := kmemStatus(home, ""); text != kmemNoneText || offer {
		t.Fatalf("no kmem: Claude memory, nothing to set up: %q %v", text, offer)
	}
	if _, err := addKmemHooks(home, time.Now()); err == nil {
		t.Fatal("no hooks without an installed kmem")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("and settings.json is not created")
	}

	installFakeKmem(t, home, "kmem", "kmem-guard")
	kb := filepath.Join(home, ".local", "bin", "kmem")
	if text, offer := kmemStatus(home, kb); !offer || !strings.Contains(text, "hooks are missing") {
		t.Fatalf("installed without hooks: offer to add them: %q %v", text, offer)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	orig := `{"model":"opus","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"other-tool ensure"}]}]},"permissions":{"allow":["Bash(ls:*)"]}}`
	os.WriteFile(settings, []byte(orig), 0o600)
	now := time.Date(2026, 11, 21, 4, 0, 0, 0, time.Local)
	done, err := addKmemHooks(home, now)
	if err != nil || len(done) != 1 || !strings.Contains(done[0], "added the kmem brief") {
		t.Fatalf("hooks: %v %v", done, err)
	}
	if b, _ := os.ReadFile(kb); string(b) != "#!/bin/sh\necho placeholder\n" {
		t.Fatal("an installed kmem is never replaced")
	}
	if !kmemSetupComplete(home) {
		t.Fatal("kmem + kmem-guard + hooks: setup complete")
	}
	if text, offer := kmemStatus(home, kb); offer || !strings.HasPrefix(text, "kmem is installed: ") {
		t.Fatalf("complete: %q %v", text, offer)
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
	if done, err := addKmemHooks(home, now); err != nil || !strings.Contains(done[0], "already") {
		t.Fatalf("running it twice adds nothing: %v %v", done, err)
	}
	broken := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(broken, []byte("{not json"), 0o600)
	if _, _, err := mergeKmemHooks(broken, home, now); err == nil {
		t.Fatal("invalid settings are left alone")
	}
	if b, _ := os.ReadFile(broken); string(b) != "{not json" {
		t.Fatal("and not rewritten")
	}
	os.Remove(filepath.Join(home, ".local", "bin", "kmem-guard"))
	if kmemSetupComplete(home) || kmemHooksMissing(home) {
		t.Fatal("a missing kmem-guard: incomplete, and no hooks to offer for it")
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

// TestKmemBinDetection: an installed kmem is found on PATH or in
// ~/.local/bin; without either it is missing.
func TestKmemBinDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	if p := kmemBin(); p != "" {
		t.Fatalf("no kmem anywhere: %q", p)
	}
	installFakeKmem(t, home, "kmem")
	if p := kmemBin(); p != filepath.Join(home, ".local", "bin", "kmem") {
		t.Fatalf("~/.local/bin/kmem (desktop launches may lack it on PATH): %q", p)
	}
	onPath := t.TempDir()
	os.WriteFile(filepath.Join(onPath, "kmem"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", onPath)
	if p := kmemBin(); p != filepath.Join(onPath, "kmem") {
		t.Fatalf("PATH first: %q", p)
	}
}
