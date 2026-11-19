package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIncrementalScanMatchesFull(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-proj")
	os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "abc.jsonl")
	user := func(n string) string {
		return `{"type":"user","timestamp":"2026-11-19T10:00:0` + n + `Z","cwd":"/p","message":{"role":"user","content":"hello ` + n + `"}}` + "\n"
	}
	asst := func(n string) string {
		return `{"type":"assistant","timestamp":"2026-11-19T10:00:0` + n + `Z","message":{"id":"m` + n + `","role":"assistant","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n"
	}
	os.WriteFile(path, []byte(user("1")+asst("2")), 0o644)
	s := NewScanner(root)
	first, err := s.Scan()
	if err != nil || len(first) != 1 || first[0].Messages != 2 {
		t.Fatalf("first scan: %+v %v", first, err)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(user("3") + asst("4") + `{"type":"user","timestamp":"2026-11-19T10:00:05Z","message":{"role":"user","content":"half`)
	f.Close()
	time.Sleep(10 * time.Millisecond)
	inc, _ := s.Scan()
	full, _ := ScanFile(path)
	if inc[0].Messages != 4 || inc[0].Messages != full.Messages || inc[0].Usage != full.Usage || inc[0].Title != full.Title {
		t.Fatalf("incremental %+v != full %+v", inc[0], full)
	}
	if s.scans[path].offset >= mustSize(path) {
		t.Fatal("a half-written line must be read again next time")
	}
}

func mustSize(p string) int64 {
	st, _ := os.Stat(p)
	return st.Size()
}
