package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// demoTranscripts writes n transcripts of lines records each (demo data).
func demoTranscripts(b *testing.B, n, lines int) string {
	root := b.TempDir()
	rec := `{"type":"user","timestamp":"2026-11-20T09:15:00.000Z","cwd":"/srv/app","sessionId":"s","message":{"role":"user","content":"rename the greeting in main.go"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-11-20T09:15:02.000Z","message":{"role":"assistant","model":"claude","content":[{"type":"text","text":"Done, the greeting is renamed."}],"usage":{"input_tokens":120,"output_tokens":40}}}` + "\n"
	body := strings.Repeat(rec, lines/2)
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, fmt.Sprintf("-srv-app-%d", i%40))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("%08d-0000-0000-0000-000000000000.jsonl", i)), []byte(body), 0o644)
	}
	return root
}

// Start-up: the first scan of 500 transcripts with 200 records each.
func BenchmarkScanFirst500(b *testing.B) {
	root := demoTranscripts(b, 500, 200)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := NewScanner(root)
		if _, err := s.Scan(); err != nil {
			b.Fatal(err)
		}
	}
}

// The 5-second refresh: 500 transcripts, 7 of them growing between scans.
func BenchmarkRescan500Growing7(b *testing.B) {
	root := demoTranscripts(b, 500, 200)
	s := NewScanner(root)
	s.Scan()
	var grow []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, _ error) error {
		if len(grow) < 7 && strings.HasSuffix(p, ".jsonl") {
			grow = append(grow, p)
		}
		return nil
	})
	add := []byte(`{"type":"assistant","timestamp":"2026-11-20T09:16:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"more"}]}}` + "\n")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range grow {
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			f.Write(add)
			f.Close()
		}
		if _, err := s.Scan(); err != nil {
			b.Fatal(err)
		}
	}
}
