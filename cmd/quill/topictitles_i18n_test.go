package main

import (
	"strings"
	"testing"
)

func TestPromptTitleSanitised(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Plain title", "Plain title"},
		{"日本語メモ", "日本語メモ"},
		{"a \"b\" `c` $(d) \\e 'f'", "a b c (d) e f"},
		{"line1\nline2\r\n\ttab\x00nul", "line1 line2 tab nul"},
		{"  many    spaces  ", "many spaces"},
		{"x y z", "x y z"},
	} {
		if got := promptTitle(c.in); got != c.want {
			t.Errorf("promptTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, briefing := range []bool{true, false} {
		p := topicPromptFor(topicInfo{Slug: "topic-1", Title: "Évil \"$(rm -x)\" `id`\nnext", Scope: []string{"topic-1"}}, briefing, true)
		for _, bad := range []string{"$(", "\nnext", "`id`", "\\\""} {
			if strings.Contains(p, bad) {
				t.Errorf("briefing=%v: prompt has %q: %s", briefing, bad, p)
			}
		}
		if !strings.Contains(p, "Évil (rm -x) id next coordinator") {
			t.Errorf("briefing=%v: sanitised title missing: %s", briefing, p)
		}
	}
}

func TestTitleKeyFolding(t *testing.T) {
	if titleKey("  Новый   ПРОЕКТ ") != titleKey("новый проект") {
		t.Error("case and spaces fold")
	}
	if titleKey("é") != titleKey("é") {
		t.Error("NFC")
	}
}

func TestTopicTitlesPersistAndReopen(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	var first, second string
	h.ui(func() {
		h.d.newSessionDialog()
		n := h.d.current
		h.d.pickNewTopic(n, "  日本語メモ ")
		first = h.d.org.Kmem[n.id]
		if first != "topic-1" || h.d.org.TopicTitles[first] != "日本語メモ" {
			t.Fatalf("typed title persisted: %q %v", first, h.d.org.TopicTitles)
		}
		h.d.newSessionDialog()
		m := h.d.current
		h.d.pickNewTopic(m, "项目计划")
		second = h.d.org.Kmem[m.id]
		if second != "topic-2" {
			t.Fatalf("second slug %q", second)
		}
	})
	// Restart: a fresh cache and the org reloaded from preferences.
	resetTopicCache()
	h.ui(func() {
		h.d.loadOrg()
		if h.d.org.TopicTitles[first] != "日本語メモ" {
			t.Fatalf("survives reload: %v", h.d.org.TopicTitles)
		}
		if tp, ok := cachedTopic(first); !ok || tp.Title != "日本語メモ" {
			t.Fatalf("cachedTopic after reload: %+v %v", tp, ok)
		}
		// All reopen paths resolve to the same slug (preview, pick, phone use topicSlugFor).
		if got := topicSlugFor("日本語メモ"); got != first {
			t.Fatalf("reopen by title: %q", got)
		}
		if got := topicSlugFor("新規"); got != "topic-3" {
			t.Fatalf("new title skips saved slugs: %q", got)
		}
	})
	// kmem knows topic-1 (it has facts) and has no label: the typed title is kept
	// in the loaded list; a kmem config title would win.
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "topics" {
			return "topic-1/notes  2 facts, 1 KB\n", nil
		}
		return "", nil
	}
	list := h.d.loadTopics()
	if len(list) != 1 || list[0].Title != "日本語メモ" {
		t.Fatalf("list title: %+v", list)
	}
	if got := topicSlugFor(" 日本語メモ "); got != "topic-1" {
		t.Fatalf("reopen via list: %q", got)
	}
	l2 := []topicInfo{{Slug: "topic-1", Title: "Topic-1"}}
	applySavedTitles(l2, map[string]kmemSessionCfg{"topic-1": {Title: "Config"}})
	if l2[0].Title != "Topic-1" {
		t.Fatalf("kmem config title wins: %+v", l2)
	}
}

func TestOldOrgWithoutTopicTitles(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetString("sidebarOrg", `{"place":{},"titles":{},"names":{},"folders":[]}`)
		h.d.loadOrg()
		if len(h.d.org.TopicTitles) != 0 {
			t.Fatal("backward compatible")
		}
	})
}

func TestPhoneReopensNonLatinTitle(t *testing.T) {
	h, a := topicPhone(t, true)
	open := func(title string) (int, map[string]any) {
		code, m, _ := a.do("POST", "/phone/sessions/new", wkey(), map[string]any{"openTopic": map[string]string{"new": title}})
		return code, m
	}
	code, m := open("Новый проект")
	if code != 200 || m["switched"] != false {
		t.Fatalf("first: %d %v", code, m)
	}
	tp, _ := m["topic"].(map[string]any)
	slug, _ := tp["slug"].(string)
	if !strings.HasPrefix(slug, "topic-") {
		t.Fatalf("slug %v", tp)
	}
	code, m = open("новый  ПРОЕКТ")
	tp, _ = m["topic"].(map[string]any)
	if code != 200 || m["switched"] != true || tp["slug"] != slug {
		t.Fatalf("reopen: %d %v", code, m)
	}
	code, m, _ = a.do("GET", "/phone/topics/slug?title="+"%D0%9D%D0%BE%D0%B2%D1%8B%D0%B9+%D0%BF%D1%80%D0%BE%D0%B5%D0%BA%D1%82", wkey(), nil)
	if code != 200 || m["slug"] != slug || m["exists"] != true || m["title"] != "Новый проект" {
		t.Fatalf("slug endpoint: %d %v", code, m)
	}
	h.ui(func() {
		if h.d.org.TopicTitles[slug] != "Новый проект" {
			t.Fatalf("persisted: %v", h.d.org.TopicTitles)
		}
	})
}
