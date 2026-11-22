package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/transcript"
)

func TestDesktopAppSessionsAndAutoMemory(t *testing.T) {
	if got := folderLabel(`C:\Users\user\AppData\Roaming\Claude\scratch-workspaces\o\a\scratch-1`); got != desktopScratchLabel {
		t.Fatalf("scratch folder label: %q", got)
	}
	if got := folderLabel("/srv/app"); got != "/srv/app" {
		t.Fatalf("other folders keep their path: %q", got)
	}
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	cwd := "/srv/app"
	if got := autoMemoryText(cwd); !strings.Contains(got, "No auto memory") {
		t.Fatalf("no memory folder: %q", got)
	}
	mem := filepath.Join(transcript.DefaultRoot(), transcript.Sanitize(cwd), "memory")
	os.MkdirAll(mem, 0o755)
	os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Build](build.md): how to build\n"), 0o644)
	os.WriteFile(filepath.Join(mem, "build.md"), []byte("Run make.\n"), 0o644)
	got := autoMemoryText(cwd)
	if !strings.Contains(got, "── MEMORY.md ──") || !strings.Contains(got, "how to build") || !strings.Contains(got, "── build.md ──") || !strings.Contains(got, "Run make.") {
		t.Fatalf("auto memory text: %q", got)
	}
	if strings.Index(got, "MEMORY.md ──") > strings.Index(got, "build.md ──") {
		t.Fatal("MEMORY.md comes first")
	}
}
