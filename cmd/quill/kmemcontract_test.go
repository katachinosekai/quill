package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKmemContract(t *testing.T) {
	src, _ := filepath.Abs(filepath.Join("..", "..", "..", "kmem", "kmem"))
	if _, err := os.Stat(src); err != nil {
		t.Skip("Projects/kmem not present")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "off"), 0o700)
	run := func(textOnly bool) func(string, ...string) (string, error) {
		return func(prog string, args ...string) (string, error) {
			if prog != "kmem" {
				return "", os.ErrNotExist
			}
			if textOnly && len(args) > 0 && args[0] == "version" {
				return "", os.ErrNotExist
			}
			cmd := exec.Command("python3", append([]string{"-I", src, "--as", "contract"}, args...)...)
			cmd.Env = append(os.Environ(), "HOME="+dir, "KMEM_DB="+filepath.Join(dir, "kmem.db"), "KMEM_OFFSITE="+filepath.Join(dir, "off", "kmem"),
				"KMEM_SERVER=off", "KMEM_CLAUDE_PID=0", "KMEM_CS=contract")
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
	}
	h := newHarness(t)
	h.d.kmemRun = run(false)
	if _, err := h.d.kmemRun("kmem", "handoff", "save", "contract-demo", "--state", "where we are"); err != nil {
		t.Fatal(err)
	}
	if h.d.kmemAPILevel() != kmemAPI {
		t.Fatal("the repo's kmem speaks api 1")
	}
	for _, mode := range []string{"json", "text"} {
		if mode == "text" {
			h.d.kmemRun = run(true)
			h.d.kmemVer.reset()
			if h.d.kmemAPILevel() != 0 {
				t.Fatal("without version, Quill falls back to text")
			}
		}
		hs, err := h.d.kmemHandoffList()
		if err != nil || len(hs) != 1 || hs[0].Name != "contract-demo" || hs[0].Saved.IsZero() {
			t.Fatalf("%s handoff list: %v %v", mode, hs, err)
		}
		if txt := h.d.kmemHandoffText("contract-demo"); !strings.Contains(txt, "session cs:contract)") || !strings.Contains(txt, "where we are") {
			t.Fatalf("%s handoff get: %q", mode, txt)
		}
		p, ok := h.d.kmemPolicy()
		if !ok || p.Soft != 50 || p.Hard != 75 || p.Mode != "auto" {
			t.Fatalf("%s policy: %+v %v", mode, p, ok)
		}
	}
}

func TestKmemModuleCompatibility(t *testing.T) {
	for _, tt := range []struct {
		api     int
		version string
		want    bool
	}{
		{1, "1.15.1", true}, {1, "1.15.2", true}, {1, "1.15.0", false},
		{1, "1.14.9", false}, {1, "1.16.0", false}, {2, "1.15.2", false},
		{1, "1.15.1-beta", false}, {1, "", false},
	} {
		if got := supportedKmemVersion(tt.api, tt.version); got != tt.want {
			t.Errorf("API %d, version %q: %t, want %t", tt.api, tt.version, got, tt.want)
		}
	}
}
