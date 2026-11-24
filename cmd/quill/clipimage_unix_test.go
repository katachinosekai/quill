//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A clipboard tool that, like wl-copy and xclip, leaves a child serving the
// selection must not hold up the copy.
func TestRunClipToolDetached(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "got.png")
	tool := filepath.Join(dir, "fake-copy")
	script := "#!/bin/sh\ncat > \"$1\"\nsleep 30 &\nexit 0\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	data := append(append([]byte{}, pngMagic...), "payload"...)
	start := time.Now()
	if err := runClipTool([]string{tool, out}, data); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("copy waited for the background child")
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, data) {
		t.Fatalf("tool got %q", got)
	}
	if err := runClipTool([]string{filepath.Join(dir, "missing")}, data); err == nil {
		t.Fatal("a missing tool should fail")
	}
}
