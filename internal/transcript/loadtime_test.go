package transcript

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestLoadTiming(t *testing.T) {
	if os.Getenv("QUILL_LOAD_TIMING") == "" {
		t.Skip()
	}
	home, _ := os.UserHomeDir()
	files, _ := filepath.Glob(filepath.Join(home, ".claude/projects/-home-kata/*.jsonl"))
	sort.Slice(files, func(i, j int) bool {
		a, _ := os.Stat(files[i])
		b, _ := os.Stat(files[j])
		return a.Size() > b.Size()
	})
	for _, f := range files[:3] {
		st, _ := os.Stat(f)
		start := time.Now()
		c, err := Load(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d MB, %d items: %v", st.Size()>>20, len(c.Items), time.Since(start))
	}
}
