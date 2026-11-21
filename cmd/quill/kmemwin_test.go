package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestKmemOnWindows(t *testing.T) {
	h := kmemHooksFor("windows", `C:\Users\user`, "python")
	brief := h["SessionStart"]["hooks"].([]any)[0].(map[string]any)["command"].(string)
	guard := h["PreToolUse"]["hooks"].([]any)[0].(map[string]any)["command"].(string)
	// On Linux the test's filepath.Join keeps the backslashes of the fake home;
	// the hook must use forward slashes and quotes either way.
	if !strings.HasPrefix(brief, `python "C:/Users/user/.local/bin/kmem" brief`) && !strings.HasPrefix(brief, `python "C:/Users/user/`) {
		t.Fatalf("Windows brief hook: %q", brief)
	}
	if strings.Contains(brief, `\`) || strings.Contains(guard, `\`) || !strings.HasSuffix(guard, `kmem-guard"`) || !strings.HasPrefix(guard, `python "`) {
		t.Fatalf("Windows hooks use quoted forward-slash paths: %q / %q", brief, guard)
	}
	groups := []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": brief}}}}
	if !hasHookCommand(groups, "kmem brief") || !isKmemHook(brief) || !isKmemHook(guard) {
		t.Fatal("quoted Windows hooks are recognised as kmem's")
	}
	lin := kmemHooksFor("linux", "/home/user", "python3")
	if c := lin["SessionStart"]["hooks"].([]any)[0].(map[string]any)["command"].(string); c != "/home/user/.local/bin/kmem brief 2>&1 || true" {
		t.Fatalf("Linux hook unchanged: %q", c)
	}
	if p, a := kmemCommand("windows", "py", `C:/k/kmem`, "brief"); p != "py" || strings.Join(a, " ") != "C:/k/kmem brief" {
		t.Fatalf("Windows runs kmem through Python: %q %v", p, a)
	}
	if p, a := kmemCommand("linux", "", "/k/kmem", "brief"); p != "/k/kmem" || strings.Join(a, " ") != "brief" {
		t.Fatalf("Linux runs kmem directly: %q %v", p, a)
	}
}

// TestWindowsKmemLive runs only on Windows with QUILL_KMEM_LIVE=<bundled kmem
// folder>: kmem setup into a temporary home, hooks, and kmem through Python.
func TestWindowsKmemLive(t *testing.T) {
	bundled := os.Getenv("QUILL_KMEM_LIVE")
	if bundled == "" || runtime.GOOS != "windows" {
		t.Skip("Windows only, with QUILL_KMEM_LIVE")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	py := windowsPython()
	if py == "" {
		t.Fatal("no Python 3 found")
	}
	done, err := setupKmem(home, bundled, time.Now())
	if err != nil {
		t.Fatalf("setup: %v %v", done, err)
	}
	if !kmemSetupComplete(home) {
		t.Fatal("setup complete: binaries + hooks")
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(raw), py+` \"`) || strings.Contains(string(raw), `\\\\`) {
		t.Fatalf("python hooks with forward slashes: %s", raw)
	}
	p, args := kmemCommand("windows", py, filepath.Join(home, ".local", "bin", "kmem"), "version", "--json")
	out, err := exec.Command(p, args...).CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"platform": "windows"`) && !strings.Contains(string(out), `"platform":"windows"`) {
		t.Fatalf("kmem runs on Windows through Python: %v %s", err, out)
	}
	if v := kmemScriptVersion(filepath.Join(home, ".local", "bin", "kmem")); v != bundledKmemVersion(os.Getenv("QUILL_KMEM_LIVE")) {
		t.Fatalf("the bundled kmem is installed: %s", v)
	}
	t.Logf("kmem version: %s", strings.TrimSpace(string(out)))
}
