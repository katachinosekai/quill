package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func setTopicCache(list ...topicInfo) {
	topicCache.mu.Lock()
	topicCache.list, topicCache.loaded, topicCache.at = list, true, time.Now()
	topicCache.mu.Unlock()
}

func TestTopicTitlesAndOldNames(t *testing.T) {
	h := newHarness(t)
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	setTopicCache(
		topicInfo{Slug: "zomboid", Title: "Zomboid", Scope: []string{"zomboid", "launcher", "network"}, Labeled: true},
		topicInfo{Slug: "quill", Title: "Quill", Scope: []string{"quill"}, Labeled: true},
		topicInfo{Slug: "beta", Title: "Beta", Scope: []string{"beta"}},
	)
	h.d.kmemOff = false
	h.d.kmemRun = func(string, ...string) (string, error) { return "", errors.New("unexpected") }
	h.ui(func() {
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		top := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[top.id] = "zomboid"
		h.d.org.Titles[top.id] = "Old custom title"
		title, meta, _ := h.d.rowText(top.id)
		if title != "Zomboid" || strings.Contains(meta, "kmem") {
			t.Fatalf("topic tab: title %q meta %q", title, meta)
		}

		for name, want := range map[string]string{
			"zomboid-launcher": "Zomboid",
			"networking":       "Zomboid", // scope word "network"
			"quill-android-2":  "Quill",
		} {
			old := h.d.createSession(h.proj, "default", "default")
			h.d.org.Kmem[old.id] = name
			h.d.org.Titles[old.id] = "Old " + name
			title, meta, _ := h.d.rowText(old.id)
			if title != "Old "+name+" → merging into "+want || !strings.Contains(meta, "kmem "+name) {
				t.Fatalf("%s: title %q meta %q", name, title, meta)
			}
			if lbl := h.d.tabLabel(old.id, title); !strings.HasSuffix(lbl, "merging into "+want) {
				t.Fatalf("tab label keeps the tail: %q", lbl)
			}
		}

		// No matching topic, an unlabeled topic, kmem itself: the plain title.
		for _, name := range []string{"scratch", "beta", "kmem-lock-audit"} {
			s := h.d.createSession(h.proj, "default", "default")
			h.d.org.Kmem[s.id] = name
			h.d.org.Titles[s.id] = "Plain"
			if title, _, _ := h.d.rowText(s.id); strings.Contains(title, "merging") {
				t.Fatalf("%s: no merge target, got %q", name, title)
			}
		}

		// Claude-memory mode: titles stay as they were.
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
		old := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[old.id] = "zomboid-launcher"
		h.d.org.Titles[old.id] = "Launcher"
		if title, _, _ := h.d.rowText(old.id); title != "Launcher" {
			t.Fatalf("claude mode title %q", title)
		}
	})
}

func TestTopicTabMenu(t *testing.T) {
	h := newHarness(t)
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	setTopicCache(topicInfo{Slug: "zomboid", Title: "Zomboid", Scope: []string{"zomboid"}, Labeled: true})
	h.ui(func() {
		top := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[top.id] = "zomboid"
		h.d.org.Titles[top.id] = "Zomboid"
		plain := h.d.createSession(h.proj, "default", "default")

		m := strings.Join(menuLabels(h.d.tabMenuItems(top.id)), "|")
		if strings.Contains(m, "Rename") || strings.Contains(m, "Restore original name") || !strings.Contains(m, "Switch topic...") {
			t.Fatalf("topic tab menu: %s", m)
		}
		m = strings.Join(menuLabels(h.d.tabMenuItems(plain.id)), "|")
		if !strings.Contains(m, "Rename...") || strings.Contains(m, "Switch topic") {
			t.Fatalf("other tab menu keeps Rename: %s", m)
		}
	})
}

func TestSwitchTopicOpensCard(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
	h.ui(func() {
		top := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[top.id] = "alpha"
		h.d.tabs = append(h.d.tabs, top.id)
		setTopicCache(topicInfo{Slug: "alpha", Title: "Alpha", Scope: []string{"alpha"}, Labeled: true})
		h.d.switchTopic(top.id)
		for _, id := range h.d.tabs {
			if id == top.id {
				t.Fatal("the old topic tab is closed")
			}
		}
		if h.d.current == nil || !h.d.current.pickPending {
			t.Fatal("Switch topic opens the Open a topic card")
		}
	})
}
