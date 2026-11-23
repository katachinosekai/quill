package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/transcript"
)

func writeConv(t *testing.T, dir, id, user, reply string) string {
	t.Helper()
	p := filepath.Join(dir, id+".jsonl")
	lines := `{"type":"user","uuid":"u1","message":{"role":"user","content":"` + user + `"},"timestamp":"2026-01-02T10:00:00.000Z","cwd":"/tmp/demo"}` + "\n" +
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"role":"assistant","id":"m1","content":[{"type":"text","text":"` + reply + `"}]},"timestamp":"2026-01-02T10:00:05.000Z"}` + "\n"
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindOld(t *testing.T) {
	dir := t.TempDir()
	p1 := writeConv(t, dir, "s1", "please plan the widget cache", "The widget cache needs an eviction rule.")
	p2 := writeConv(t, dir, "s2", "fix the gadget", "Gadget fixed.")
	now := time.Now()
	infos := []transcript.Info{
		{ID: "s1", Path: p1, Title: "Widget cache plan", Cwd: "/tmp/demo", Last: now.Add(-time.Hour)},
		{ID: "s2", Path: p2, Title: "Gadget fix", Kmem: "gadget-work", Cwd: "/tmp/demo", Last: now},
		{ID: "s3", Title: "Widget without file"},
	}
	if h := findOld(infos, "WIDGET"); len(h) != 1 || h[0].ID != "s1" {
		t.Fatalf("title match, case-insensitive, file-less skipped: %+v", h)
	}
	if h := findOld(infos, "gadget-work"); len(h) != 1 || h[0].ID != "s2" {
		t.Fatalf("kmem name match: %+v", h)
	}
	if h := findOld(infos, "demo"); len(h) != 2 || h[0].ID != "s2" {
		t.Fatalf("newest first: %+v", h)
	}
	if h := findOld(infos, "  "); len(h) != 0 {
		t.Fatalf("empty query finds nothing: %+v", h)
	}
	// full text: the word is only in what was said
	if sn, ok := fullTextHit(p1, queryWords("eviction")); !ok || !strings.Contains(sn, "eviction") {
		t.Fatalf("full text hit: %q %v", sn, ok)
	}
	if _, ok := fullTextHit(p2, queryWords("eviction")); ok {
		t.Fatal("no hit expected")
	}
	if _, ok := fullTextHit(filepath.Join(dir, "missing.jsonl"), queryWords("x")); ok {
		t.Fatal("missing file is no hit")
	}
	c, err := transcript.Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	if v := convText(c); !strings.Contains(v, "You: please plan") || !strings.Contains(v, "Claude: The widget") {
		t.Fatalf("view text: %q", v)
	}
}

func TestFactTextAndArgs(t *testing.T) {
	if got := factText("  picked ", "whole"); got != "picked" {
		t.Fatalf("selection wins: %q", got)
	}
	long := strings.Repeat("é", 3000)
	if got := factText("", long); len(got) > findFactMax+4 || !strings.HasSuffix(got, " …") {
		t.Fatalf("cut on a rune boundary: %d", len(got))
	}
	a := addFactArgs("quill/notes", "Widget cache plan", "--new text")
	if a[0] != "add" || a[1] != "quill/notes" || a[len(a)-2] != "--" || a[len(a)-1] != "--new text" {
		t.Fatalf("text goes after --: %v", a)
	}
}

// Opening the find dialog and a read-only view never starts a session (one
// live session per topic).
func TestFindOldStartsNoSession(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	dir := t.TempDir()
	path := writeConv(t, dir, "old1", "note the placeholder decision", "Recorded the placeholder decision.")
	h.d.kmemRun = func(prog string, args ...string) (string, error) { return "", nil }
	in := transcript.Info{ID: "old1", Path: path, Title: "Placeholder decision", Cwd: "/tmp/demo", Last: time.Now()}
	var p *kmemPanel
	h.ui(func() {
		h.d.order = []transcript.Info{in}
		h.d.showKmemPanel()
		p = h.d.kmemPanelNow
	})
	defer h.ui(func() { h.d.kmemWin.close() })
	h.eventually("panel loaded", func() bool { return p.data != nil })
	before := len(h.d.sessions)
	h.ui(func() {
		p.showFindOld()
		p.viewOld(in)
	})
	time.Sleep(200 * time.Millisecond)
	h.ui(func() {
		if len(h.d.sessions) != before {
			t.Fatalf("sessions changed: %d -> %d", before, len(h.d.sessions))
		}
		if !strings.Contains(labelsIn(p.hand.Objects), "Find old conversation") {
			t.Fatal("the Topics tab offers Find old conversation")
		}
	})
}

func TestFactTopicPrefill(t *testing.T) {
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	setTopicCache(topicInfo{Slug: "zomboid", Title: "Zomboid", Labeled: true})
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"sessions":{"old-name":{"topic":"quill/notes"},"nomap":{"as":"x"}}}`), 0o600)
	oldCfg := kmemCfgPath
	kmemCfgPath = func() string { return cfg }
	resetKmemSessions()
	t.Cleanup(func() { kmemCfgPath = oldCfg; resetKmemSessions() })
	for name, want := range map[string]string{
		"old-name": "quill/notes", // explicit mapping
		"zomboid":  "zomboid",     // the topic tab itself
		"nomap":    "",
		"other":    "",
		"":         "",
	} {
		if got := topicForName(name); got != want {
			t.Errorf("topicForName(%q) = %q, want %q", name, got, want)
		}
	}
}
