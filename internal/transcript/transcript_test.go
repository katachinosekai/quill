package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestScanUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "u.jsonl")
	day := time.Now().UTC().Format(time.RFC3339Nano)
	lines := []string{
		`{"type":"user","timestamp":"` + day + `","cwd":"/x","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"` + day + `","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"a"}],"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}`,
		`{"type":"assistant","timestamp":"` + day + `","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t","name":"Bash","input":{}}],"usage":{"input_tokens":1,"output_tokens":20,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}}`,
		`{"type":"assistant","timestamp":"` + day + `","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"b"}],"usage":{"input_tokens":5,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	info, err := ScanFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := Tokens{In: 6, Out: 25, CacheRead: 3, CacheWrite: 4}
	if info.Usage != want || info.Days[time.Now().Format("2006-01-02")] != want {
		t.Fatalf("%+v %+v", info.Usage, info.Days)
	}
}

func TestLoadEndsTasksFromAttachments(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	lines := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"q1","name":"Bash","input":{"command":"./t.sh","run_in_background":true}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"q1","content":"Command running in background with ID: b2b3pwaaq. Output is being written to: /tmp/o"}]}}
{"parentUuid":"x","attachment":{"type":"queued_command","prompt":"<task-notification>\\n<task-id>b2b3pwaaq</task-id>\\n<status>completed</status>\\n</task-notification>"},"type":"attachment"}
`
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Background() != 0 {
		t.Fatalf("a reloaded transcript doesn't resurrect finished tasks: %v %d", err, c.Background())
	}
}

func TestLoadFollowsTheRewoundBranch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	u := func(id, parent, text string) string {
		return `{"type":"user","uuid":"` + id + `","parentUuid":"` + parent + `","message":{"role":"user","content":"` + text + `"}}` + "\n"
	}
	a := func(id, parent, text string) string {
		return `{"type":"assistant","uuid":"` + id + `","parentUuid":"` + parent + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
	}
	lines := u("u0", "", "before compaction") + a("a0", "u0", "old answer") +
		u("u1", "", "after compaction") + a("a1", "u1", "answer one") +
		u("u2", "a1", "abandoned question") + a("a2", "u2", "abandoned answer") +
		u("u3", "a1", "rewritten question") + a("a3", "u3", "new answer")
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range c.Items {
		got = append(got, it.Text)
	}
	want := "before compaction|old answer|after compaction|answer one|rewritten question|new answer"
	if strings.Join(got, "|") != want {
		t.Fatalf("a rewound branch replaces the abandoned one, compacted history stays: %v", got)
	}
	if c.Items[4].UUID != "u3" || c.Items[3].UUID != "a1" {
		t.Fatal("items keep their message ids")
	}
}

func TestParallelToolResultIsNotAnAbandonedBranch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	lines := `{"type":"user","uuid":"u1","parentUuid":"","message":{"role":"user","content":"run the test"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"./check.sh","run_in_background":true}}]}}
{"type":"assistant","uuid":"a2","parentUuid":"a1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t2","name":"SendMessage","input":{"to":"x","message":"taking the lock"}}]}}
{"type":"user","uuid":"r1","parentUuid":"a1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Command running in background with ID: bkisea9gg. Output is being written to: /tmp/o"}]}}
{"type":"user","uuid":"r2","parentUuid":"a2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"sent"}]}}
{"type":"assistant","uuid":"a3","parentUuid":"r2","message":{"role":"assistant","content":[{"type":"text","text":"The test is running."}]}}
`
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Background() != 1 || c.Working() != 1 {
		t.Fatalf("the background Bash started next to another tool call is tracked after a reload: %d", c.Background())
	}
	for _, it := range c.Items {
		if it.ToolID == "t1" && !it.HasResult {
			t.Fatal("and its result is shown")
		}
	}
}
