package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDesktopTitleAndScratch(t *testing.T) {
	dir := t.TempDir()
	tr := filepath.Join(dir, "abc.jsonl")
	if desktopTitle(tr) != "" {
		t.Fatal("no title file, no title")
	}
	os.MkdirAll(filepath.Join(dir, "abc"), 0o755)
	os.WriteFile(filepath.Join(dir, "abc", "custom-title.json"), []byte(`{"customTitle":"Say hi"}`), 0o644)
	if got := desktopTitle(tr); got != "Say hi" {
		t.Fatalf("desktop app title: %q", got)
	}
	for cwd, want := range map[string]bool{
		`C:\Users\user\AppData\Roaming\Claude\scratch-workspaces\org\acct\scratch-2026-11-22-1`: true,
		"C--Users-user-AppData-Roaming-Claude-scratch-workspaces-org":                           true,
		"/home/user/projects/app": false,
	} {
		if DesktopScratch(cwd) != want {
			t.Errorf("DesktopScratch(%q) != %v", cwd, want)
		}
	}
}
