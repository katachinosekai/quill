package main

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

var searchTopicsFixture = []topicInfo{
	{Slug: "alpha", Title: "Alpha", Scope: []string{"alpha"}, Status: "alpha is half done"},
	{Slug: "beta", Title: "Beta", Scope: []string{"beta"}},
	{Slug: "garden-planner", Title: "Garden Planner", Scope: []string{"garden-planner"}, Status: "beds drawn"},
	{Slug: "zomboid", Title: "Project Zomboid", Scope: []string{"zomboid", "launcher"}},
}

func none(string) bool { return false }

func TestMatchTopicsNameTitleOrderAndFolding(t *testing.T) {
	ts := searchTopicsFixture
	slugs := func(h []topicHit) string {
		var s []string
		for _, x := range h {
			s = append(s, x.Topic.Slug+":"+x.Why)
		}
		return strings.Join(s, ",")
	}
	if got := slugs(matchTopicsLocal("gard", ts, none)); got != "garden-planner:name" {
		t.Fatalf("prefix: %s", got)
	}
	// title word prefix beats substring; folding ignores case
	if got := slugs(matchTopicsLocal("ZOMB", ts, none)); got != "zomboid:name" {
		t.Fatalf("fold: %s", got)
	}
	if got := slugs(matchTopicsLocal("planner", ts, none)); got != "garden-planner:name" && got != "garden-planner:title" {
		t.Fatalf("word: %s", got)
	}
	// prefix matches come before substring matches
	two := []topicInfo{{Slug: "xalpha", Title: "Xalpha"}, {Slug: "alphabet", Title: "Alphabet"}}
	if got := slugs(matchTopicsLocal("alpha", two, none)); !strings.HasPrefix(got, "alphabet") {
		t.Fatalf("prefix before substring: %s", got)
	}
	if got := slugs(matchTopicsLocal("   ", ts, none)); got != "" {
		t.Fatalf("blank: %s", got)
	}
	if h := matchTopicsLocal("pro", ts, none); len(h) != 1 || h[0].Why != "title" {
		t.Fatalf("title match: %+v", h)
	}
}

func TestDidYouMean(t *testing.T) {
	resetTopicCache()
	defer resetTopicCache()
	topicCache.mu.Lock()
	topicCache.list = searchTopicsFixture
	topicCache.mu.Unlock()
	for q, want := range map[string]string{
		"Garden Planer":   "garden-planner", // typo
		"garden":          "garden-planner", // prefix
		"Alfa":            "alpha",
		"Project Zomboyd": "zomboid",
		"Quarterly taxes": "",
		"Alpha":           "", // an existing topic: it opens that one
		"ab":              "", // too short
	} {
		got := didYouMean(q, searchTopicsFixture)
		if (got == nil) != (want == "") || got != nil && got.Slug != want {
			t.Errorf("%q: got %v want %q", q, got, want)
		}
	}
}

func TestRecentTrackingOrderAndFallback(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		ts := searchTopicsFixture
		// nothing opened: status topics stand in
		if got := d.recentTopics(ts, 5); len(got) != 2 || got[0].Slug != "alpha" || got[1].Slug != "garden-planner" {
			t.Fatalf("fallback: %+v", got)
		}
		for _, s := range []string{"alpha", "beta", "zomboid", "alpha", "gone"} {
			d.noteRecentTopic(s)
		}
		got := d.recentTopics(ts, 5) // newest first, a re-open moves up, unknown dropped, then fallback fills
		var order []string
		for _, x := range got {
			order = append(order, x.Slug)
		}
		if strings.Join(order, ",") != "alpha,zomboid,beta,garden-planner" {
			t.Fatalf("order: %v", order)
		}
		for _, s := range []string{"a1", "a2", "a3", "a4", "a5", "a6"} {
			d.noteRecentTopic(s)
		}
		ts2 := []topicInfo{{Slug: "a1"}, {Slug: "a2"}, {Slug: "a3"}, {Slug: "a4"}, {Slug: "a5"}, {Slug: "a6"}}
		if got := d.recentTopics(ts2, 5); len(got) != 5 || got[0].Slug != "a6" || got[4].Slug != "a2" {
			t.Fatalf("last five: %+v", got)
		}
		d.setDormant("a6", true)
		if got := d.recentTopics(ts2, 5); got[0].Slug != "a5" {
			t.Fatalf("dormant skipped: %+v", got)
		}
	})
}

// recallRunner answers recall for the card and phone tests.
func recallRunner(h *harness, calls *[]string) {
	base := h.d.kmemRun
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		a := strings.Join(args, " ")
		if prog == "kmem" && len(args) > 0 && args[0] == "recall" {
			if calls != nil {
				*calls = append(*calls, a)
			}
			if args[1] == "zebra" {
				return "alpha/core: [f9] fact: the zebra crossing pattern is used in\nthe signal code\n" +
					"alpha/status: [f8] fact: zebra again\n" +
					"kmem/rules: [f1] rule: zebra\n" +
					"beta/open: [f7] fact: " + strings.Repeat("long text ", 20) + "zebra at the end\n", nil
			}
			return "", nil
		}
		return base(prog, args...)
	}
}

func TestContentHitsMapToTopLevelTopics(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	var calls []string
	recallRunner(h, &calls)
	ts := searchTopicsFixture
	hits := h.d.contentHits("zebra", ts, map[string]bool{}, none)
	if len(hits) != 2 || hits[0].Topic.Slug != "alpha" || hits[1].Topic.Slug != "beta" {
		t.Fatalf("one hit per top-level topic, kmem's own left out: %+v", hits)
	}
	if hits[0].Why != "content" || !strings.Contains(hits[0].Snippet, "zebra crossing") || strings.Contains(hits[0].Snippet, "\n") {
		t.Fatalf("snippet: %q", hits[0].Snippet)
	}
	if n := len([]rune(hits[1].Snippet)); n > snippetMax+2 || !strings.Contains(hits[1].Snippet, "zebra") {
		t.Fatalf("long snippet cut around the word: %d %q", n, hits[1].Snippet)
	}
	if len(calls) != 1 || calls[0] != "recall zebra -n 8" {
		t.Fatalf("calls %v", calls)
	}
	if got := h.d.contentHits("zebra", ts, map[string]bool{"alpha": true}, none); len(got) != 1 {
		t.Fatalf("skip: %+v", got)
	}
	// a labeled scope maps its member to the topic
	if got := h.d.contentHits("zebra", []topicInfo{{Slug: "zomboid", Scope: []string{"zomboid", "alpha"}}}, nil, none); len(got) != 1 || got[0].Topic.Slug != "zomboid" {
		t.Fatalf("scope: %+v", got)
	}
}

func TestSearchTopicsNameFirstThenMentionsNoDuplicate(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	recallRunner(h, nil)
	res := h.d.searchTopics("zebra", searchTopicsFixture)
	if len(res.Hits) != 2 {
		t.Fatalf("%+v", res.Hits)
	}
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "recall" {
			return "alpha/core: [f9] fact: alpha stuff\nbeta/open: [f7] fact: alpha too\n", nil
		}
		return "", errors.New("x")
	}
	res = h.d.searchTopics("alpha", searchTopicsFixture)
	if len(res.Hits) != 2 || res.Hits[0].Why != "name" || res.Hits[1].Topic.Slug != "beta" || res.Hits[1].Why != "content" {
		t.Fatalf("a name match is not repeated as a mention: %+v", res.Hits)
	}
}

// browseAll waits for the card's "Browse all" button and taps it: the card
// opens on Recent, and the older card tests look at the whole list.
func browseAll(h *harness) {
	var all *widget.Button
	h.eventually("browse all", func() bool {
		all = nil
		deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && strings.HasPrefix(b.Text, "Browse all") {
				all = b
			}
		})
		return all != nil
	})
	h.ui(func() { test.Tap(all) })
}

func cardRowLabels(h *harness) string { return strings.Join(cardButtons(h), "|") }

func cardSearchEntry(h *harness) *widget.Entry {
	var e *widget.Entry
	deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
		if x, ok := o.(*widget.Entry); ok && strings.HasPrefix(x.PlaceHolder, "Search topics") {
			e = x
		}
	})
	return e
}

func cardTexts(h *harness) string {
	var out []string
	deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
		if x, ok := o.(*canvas.Text); ok {
			out = append(out, x.Text)
		}
	})
	return strings.Join(out, "|")
}

func openSearchCard(t *testing.T, h *harness) *deskSession {
	topicSearchDelay = 0
	t.Cleanup(func() { topicSearchDelay = 200 * 1e6 })
	var s *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		s = h.d.current
	})
	h.eventually("card", func() bool { return cardSearchEntry(h) != nil && strings.Contains(cardRowLabels(h), "Browse all") })
	return s
}

func TestCardEmptyShowsRecentAndBrowseAll(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	h.ui(func() {
		h.d.noteRecentTopic("beta")
		h.d.setDormant("alpha", true)
	})
	openSearchCard(t, h)
	h.ui(func() {
		got := cardRowLabels(h)
		if !strings.Contains(got, "Beta") || strings.Contains(got, "Alpha") || !strings.Contains(got, "Browse all (2)") {
			t.Fatalf("empty search: Recent only, dormant not shown: %s", got)
		}
		for _, b := range cardButtons(h) {
			if b == "Show again" {
				t.Fatal("dormant must not show while the box is empty")
			}
		}
		var all *widget.Button
		deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && strings.HasPrefix(b.Text, "Browse all") {
				all = b
			}
		})
		test.Tap(all)
		if acc := cardAccordion(h); acc == nil || acc.Items[0].Title != "Dormant (1)" {
			t.Fatalf("Browse all has the dormant fold: %v", cardRowLabels(h))
		}
	})
}

func TestCardTypingMatchesMentionsDormantAndDidYouMean(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	var calls []string
	h.eventually("setup", func() bool { return true })
	recallRunner(h, &calls)
	h.ui(func() { h.d.setDormant("beta", true) })
	openSearchCard(t, h)
	h.ui(func() { cardSearchEntry(h).SetText("alp") })
	h.eventually("name match", func() bool { return strings.Contains(cardRowLabels(h), "Alpha") })
	h.ui(func() {
		if strings.Contains(cardRowLabels(h), "Browse all") || strings.Contains(cardRowLabels(h), "Beta") {
			t.Fatalf("typing replaces Recent: %s", cardRowLabels(h))
		}
		if !strings.Contains(cardRowLabels(h), "New topic: alp") {
			t.Fatalf("new topic action: %s", cardRowLabels(h))
		}
	})
	// dormant appears only when typed, marked
	h.ui(func() { cardSearchEntry(h).SetText("beta") })
	h.eventually("dormant", func() bool { return strings.Contains(cardRowLabels(h), "Beta · dormant") })
	// content mention
	h.ui(func() { cardSearchEntry(h).SetText("zebra") })
	h.eventually("mentions", func() bool { return strings.Contains(cardTexts(h), "mentions the zebra crossing pattern") })
	h.ui(func() {
		if !strings.Contains(cardRowLabels(h), "Alpha") {
			t.Fatalf("a mention row opens its topic: %s", cardRowLabels(h))
		}
	})
	// did you mean sits with New topic, and picking it opens the existing topic
	h.ui(func() { cardSearchEntry(h).SetText("Alpa") })
	h.eventually("dym", func() bool { return strings.Contains(cardRowLabels(h), "Did you mean Alpha?") })
	s := h.d.current
	h.ui(func() {
		for _, b := range cardButtons(h) {
			if b == "New topic: Alpa" {
				goto found
			}
		}
		t.Fatal("New topic action stays below")
	found:
		var dym *widget.Button
		deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok && strings.HasPrefix(b.Text, "Did you mean") {
				dym = b
			}
		})
		test.Tap(dym)
		if s.pickPending || h.d.org.Kmem[s.id] != "alpha" {
			t.Fatalf("did you mean opens alpha: %v %q", s.pickPending, h.d.org.Kmem[s.id])
		}
		if r := h.d.app.Preferences().StringList(recentTopicsKey); len(r) == 0 || r[0] != "alpha" {
			t.Fatalf("opening records Recent: %v", r)
		}
	})
}

func TestCardNoDidYouMeanForExactOrUnrelated(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	openSearchCard(t, h)
	for _, q := range []string{"Alpha", "Quarterly taxes"} {
		h.ui(func() { cardSearchEntry(h).SetText(q) })
		h.ui(func() {
			if strings.Contains(cardRowLabels(h), "Did you mean") {
				t.Fatalf("%q: %s", q, cardRowLabels(h))
			}
		})
	}
}

func TestRelatedHiddenWithoutFeature(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	if got := h.d.relatedTopics("alpha"); got != nil {
		t.Fatalf("no relate feature: %v", got)
	}
	base := h.d.kmemRun
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "version --json":
			return `{"version": "1.12.0", "api": 1, "features": ["relate"]}`, nil
		case a == "relate alpha --json":
			return `{"related": [{"topic": "beta/open"}, "garden", "alpha"], "api": 1}`, nil
		}
		return base(prog, args...)
	}
	h.d.kmemVer.reset()
	if got := h.d.relatedTopics("alpha"); strings.Join(got, ",") != "beta,garden" {
		t.Fatalf("related: %v", got)
	}
}

func TestPhoneTopicSearchAndRecent(t *testing.T) {
	h, a := topicPhone(t, true)
	recallRunner(h, nil)
	h.ui(func() {
		h.d.noteRecentTopic("beta")
	})
	_, m, _ := a.do("GET", "/phone/topics", "", nil)
	if be := topicByslug(m, "beta"); be == nil || be["recent"] != true {
		t.Fatalf("recent flag: %v", be)
	}
	h.ui(func() { h.d.setDormant("beta", true) })
	code, m, _ := a.do("GET", "/phone/topics/search?q=bet", "", nil)
	ms, _ := m["matches"].([]any)
	if code != 200 || len(ms) != 1 {
		t.Fatalf("search: %d %v", code, m)
	}
	e := ms[0].(map[string]any)
	if e["name"] != "beta" || e["title"] != "Beta" || e["match"] != "name" || e["dormant"] != true || e["live"] != "" {
		t.Fatalf("match: %v", e)
	}
	_, m, _ = a.do("GET", "/phone/topics/search?q=zebra", "", nil)
	ms, _ = m["matches"].([]any)
	if len(ms) != 2 || ms[0].(map[string]any)["match"] != "content" || !strings.Contains(ms[0].(map[string]any)["content"].(string), "zebra") {
		t.Fatalf("content: %v", m)
	}
	_, m, _ = a.do("GET", "/phone/topics/search?q=Alpa", "", nil)
	if d, _ := m["didYouMean"].(map[string]any); d["name"] != "alpha" || d["title"] != "Alpha" {
		t.Fatalf("didYouMean: %v", m)
	}
	_, m, _ = a.do("GET", "/phone/topics/search?q=", "", nil)
	ms, _ = m["matches"].([]any)
	if len(ms) == 0 || ms[0].(map[string]any)["match"] != "recent" || ms[0].(map[string]any)["name"] != "alpha" {
		t.Fatalf("empty q = Recent: %v", m)
	}
	if m["didYouMean"] != nil {
		t.Fatalf("didYouMean null: %v", m)
	}
}

func TestPhoneTopicSearchKmemOff(t *testing.T) {
	h, a := topicPhone(t, true)
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude) })
	if code, m, _ := a.do("GET", "/phone/topics/search?q=x", "", nil); code != 503 || errCode(m) != "kmem_unavailable" {
		t.Fatalf("%d %v", code, m)
	}
}
