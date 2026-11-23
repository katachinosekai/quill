package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKmemSlugSluglessGivesSessionN(t *testing.T) {
	none := func(string) bool { return false }
	for _, title := range []string{"日本語", "!!!", "  ", "Привет"} {
		if got := kmemSlug(title, none); got != "session-1" {
			t.Fatalf("%q -> %q", title, got)
		}
	}
	taken := map[string]bool{"session-1": true, "session-2": true}
	if got := kmemSlug("日本語", func(n string) bool { return taken[n] }); got != "session-3" {
		t.Fatalf("got %q", got)
	}
	if got := kmemSlug("Fix the bug", none); got != "fix-the-bug" {
		t.Fatalf("latin unchanged: %q", got)
	}
}

func TestKmemNameForKeepsOldForSlugless(t *testing.T) {
	none := func(string) bool { return false }
	if got := kmemNameFor("日本語", "session-3", none); got != "session-3" {
		t.Fatalf("keeps the old name: %q", got)
	}
	if got := kmemNameFor("日本語", "fix-bug", none); got != "fix-bug" {
		t.Fatalf("keeps a latin old name: %q", got)
	}
	if got := kmemNameFor("日本語", "", none); got != "session-1" {
		t.Fatalf("new: %q", got)
	}
	if got := kmemNameFor("New Plan", "session-3", none); got != "new-plan" {
		t.Fatalf("latin title renames: %q", got)
	}
	if !followsTitle("日本語", "session-7") || followsTitle("日本語", "fix-bug") || !followsTitle("Fix Bug", "fix-bug") {
		t.Fatal("followsTitle")
	}
}

func TestEnsureKmemKeepsTypedTitle(t *testing.T) {
	h, _, _ := identityHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.org.Titles[s.id] = "日本語のメモ"
		name := h.d.ensureKmem(s.id)
		if name != "session-1" {
			t.Fatalf("name %q", name)
		}
		if h.d.org.KmemTitles[name] != "日本語のメモ" {
			t.Fatalf("title kept: %v", h.d.org.KmemTitles)
		}
		for _, c := range kmemChoicesTitled([]kmemHandoff{{Name: name}}, h.d.org.KmemTitles) {
			if strings.Contains(c, "日本語のメモ") && choiceHandoff(c) == name {
				return
			}
		}
		t.Fatal("the resume choice shows the title and still parses to the ASCII name")
	})
}

func TestRetitleToNonLatinKeepsKmemName(t *testing.T) {
	h, k, _ := identityHarness(t)
	k.saved["old-name"] = true
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "old-name"
		h.d.renameSession(s.id, "新しい名前")
		if h.d.org.Kmem[s.id] != "old-name" || h.d.org.Titles[s.id] != "新しい名前" {
			t.Fatalf("kmem %q title %q", h.d.org.Kmem[s.id], h.d.org.Titles[s.id])
		}
		if k.has("handoff rename") {
			t.Fatal("no rename for a title without Latin letters")
		}
		if got := h.d.kmemNamePreview(s.id, "別の名前"); !strings.Contains(got, "stays old-name") {
			t.Fatalf("preview: %q", got)
		}
		if got := h.d.kmemNamePreview(s.id, "Brand New"); !strings.Contains(got, "old-name becomes brand-new") {
			t.Fatalf("preview: %q", got)
		}
	})
}

func TestRetitleGeneratedNameStableAndMovesTitle(t *testing.T) {
	h, k, _ := identityHarness(t)
	k.saved["session-3"] = true
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "session-3"
		h.d.renameSession(s.id, "日本語")
		if h.d.org.Kmem[s.id] != "session-3" || h.d.org.KmemTitles["session-3"] != "日本語" {
			t.Fatalf("%v %v", h.d.org.Kmem, h.d.org.KmemTitles)
		}
		h.d.renameSession(s.id, "Real Name")
	})
	h.eventually("renamed", func() bool { return k.has("handoff rename session-3 real-name") })
	h.eventually("stored title dropped", func() bool { _, ok := h.d.org.KmemTitles["session-3"]; return !ok })
}

func TestKmemNameRejectsNonASCII(t *testing.T) {
	if plainKmemName("日本語") || plainKmemName("a b") || !plainKmemName("session-1") {
		t.Fatal("plainKmemName")
	}
}

func TestKmemPanelTopicSearchByTitle(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"sessions":{"garden":{"title":"Garden Planner","scope":["garden"]}}}`), 0o600)
	old := kmemCfgPath
	kmemCfgPath = func() string { return cfg }
	defer func() { kmemCfgPath = old }()
	resetKmemSessions()
	defer resetKmemSessions()
	h := newHarness(t)
	h.d.org.TopicTitles = map[string]string{"topic-3": "日本語 メモ"}
	h.d.org.KmemTitles = map[string]string{"session-2": "Привет"}
	p := &kmemPanel{d: h.d, topicList: []kmemTopic{{Name: "topic-3/status"}, {Name: "topic-4"}, {Name: "garden/notes"}, {Name: "session-2"}}}
	names := func(q string) string {
		p.filterTopics(q)
		var out []string
		for _, t := range p.topicShown {
			out = append(out, t.Name)
		}
		return strings.Join(out, ",")
	}
	for q, want := range map[string]string{
		"日本語":      "topic-3/status",
		" 日本語  メモ": "topic-3/status",
		"topic":    "topic-3/status,topic-4",
		"planner":  "garden/notes",
		"ПРИВЕТ":   "session-2",
		"":         "topic-3/status,topic-4,garden/notes,session-2",
		"nothing":  "",
	} {
		if got := names(q); got != want {
			t.Errorf("%q: got %q want %q", q, got, want)
		}
	}
}
