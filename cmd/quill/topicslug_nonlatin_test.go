package main

import "testing"

// Names with no Latin letter or digit get "topic-N" (first free N) instead of
// an empty or shared slug; the title stays what the user typed.
var nonLatinNames = []string{"日本語", "!!!", "   ---  ", "Привет"}

func resetTopicExtra(t *testing.T) {
	t.Helper()
	clear := func() {
		topicCache.mu.Lock()
		topicCache.extra = nil
		topicCache.mu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func TestTopicSlugForNonLatin(t *testing.T) {
	resetTopicExtra(t)
	for _, c := range []struct{ title, want string }{
		{"Garden Planner", "garden-planner"},
		{"日本語", "topic-1"},
		{"!!!", "topic-1"},
		{"   ---  ", "topic-1"},
		{"日本語 2", "2"},
		{"Ünï", "n"},
	} {
		if got := topicSlugFor(c.title); got != c.want {
			t.Errorf("topicSlugFor(%q) = %q, want %q", c.title, got, c.want)
		}
	}
	rememberNewTopic(topicInfo{Slug: "topic-1", Title: "日本語"})
	rememberNewTopic(topicInfo{Slug: "topic-3", Title: "x"})
	if got := topicSlugFor("!!!"); got != "topic-2" {
		t.Errorf("first free N: %q", got)
	}
	rememberNewTopic(topicInfo{Slug: "topic-2", Title: "!!!"})
	if got := topicSlugFor("日本語"); got != "topic-1" { // same title: reopens it
		t.Errorf("same title reopens: %q", got)
	}
	if got := topicSlugFor("日本語 2"); got != "2" {
		t.Errorf("latin digit slug: %q", got)
	}
	if got := topicSlugFor("新しい"); got != "topic-4" {
		t.Errorf("skips remembered: %q", got)
	}
}

func TestPickNewTopicNonLatin(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	resetTopicExtra(t)
	h.ui(func() {
		for i, name := range nonLatinNames {
			h.d.newSessionDialog()
			n := h.d.current
			want := "topic-" + string(rune('1'+i))
			if got := topicSlugFor(name); got != want { // the card's preview
				t.Fatalf("preview %q = %q, want %q", name, got, want)
			}
			h.d.pickNewTopic(n, name)
			if n.pickPending || h.d.org.Kmem[n.id] != want || h.d.topicOf(n.id) != want {
				t.Fatalf("pick %q: pending %v kmem %q", name, n.pickPending, h.d.org.Kmem[n.id])
			}
			if h.d.org.Titles[n.id] == "" || h.d.org.Titles[n.id] == want {
				t.Fatalf("title stays what the user typed: %q", h.d.org.Titles[n.id])
			}
		}
	})
}
