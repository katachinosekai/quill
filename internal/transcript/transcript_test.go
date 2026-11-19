package transcript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestScanSynthetic(t *testing.T) {
	info, err := ScanFile("testdata/edits.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if info.Title != "Rename greeting" || info.Cwd != "/home/user/proj" || info.ID != "edits" {
		t.Fatalf("%+v", info)
	}
	if info.Messages != 4 {
		t.Fatalf("messages = %d", info.Messages)
	}
	if info.Last.Format("15:04:05") != "13:31:00" {
		t.Fatalf("last %v", info.Last)
	}
}

func TestScanRealSmoke(t *testing.T) {
	info, err := ScanFile("testdata/real-smoke.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if info.Cwd != "/tmp/ws" || info.Messages < 4 {
		t.Fatalf("%+v", info)
	}
	if info.Title == "" || info.Title == "(untitled)" {
		t.Fatalf("title %q", info.Title)
	}
}

func TestLoadSynthetic(t *testing.T) {
	c, err := Load("testdata/edits.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var ks []chat.Kind
	for _, it := range c.Items {
		ks = append(ks, it.Kind)
	}
	want := []chat.Kind{chat.User, chat.User, chat.Assistant, chat.Tool, chat.Tool, chat.Assistant, chat.Notice}
	if len(ks) != len(want) {
		t.Fatalf("kinds %v", ks)
	}
	for i := range want {
		if ks[i] != want[i] {
			t.Fatalf("kinds %v", ks)
		}
	}
	if c.Items[0].Text != "/clear" {
		t.Fatalf("command text %q", c.Items[0].Text)
	}
	edit := c.Items[3]
	if edit.ToolName != "Edit" || !edit.HasResult || edit.ResultError || edit.DiffPath != "/home/user/proj/main.go" {
		t.Fatalf("edit %+v", edit)
	}
	if a, r := chat.DiffStats(edit.Diff); a != 1 || r != 1 {
		t.Fatalf("diff %+v", edit.Diff)
	}
	bash := c.Items[4]
	if !bash.ResultError || bash.Summary != "Build: go build ./..." || bash.Result == "" {
		t.Fatalf("bash %+v", bash)
	}
	if c.Cwd != "/home/user/proj" {
		t.Fatal(c.Cwd)
	}
}

func TestLoadRealSmoke(t *testing.T) {
	c, err := Load("testdata/real-smoke.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	tools := 0
	for _, it := range c.Items {
		if it.Kind == chat.Tool {
			tools++
			if !it.HasResult {
				t.Errorf("tool without result %+v", it)
			}
		}
	}
	if tools != 2 {
		t.Fatalf("tools = %d", tools)
	}
}

func TestScannerGroupsAndCaches(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, Sanitize("/home/user/proj"))
	os.MkdirAll(dir, 0o755)
	b, _ := os.ReadFile("testdata/edits.jsonl")
	os.WriteFile(filepath.Join(dir, "11111111-2222-3333-4444-555555555555.jsonl"), b, 0o600)
	os.WriteFile(filepath.Join(dir, "empty.jsonl"), []byte(`{"type":"mode"}`+"\n"), 0o600)
	s := NewScanner(root)
	infos, err := s.Scan()
	if err != nil || len(infos) != 1 {
		t.Fatalf("%v %+v", err, infos)
	}
	again, _ := s.Scan()
	if len(again) != 1 || again[0].Title != infos[0].Title {
		t.Fatal("cache")
	}
	if Sanitize("/run/media/a b.c") != "-run-media-a-b-c" {
		t.Fatal(Sanitize("/run/media/a b.c"))
	}
}
