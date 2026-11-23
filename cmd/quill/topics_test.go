package main

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func TestBuildTopics(t *testing.T) {
	out := "alpha/core  2 facts, 1 KB\nalpha/status  1 facts, 1 KB\nbeta/open/x  3 facts, 2 KB\nkmem/rules  9 facts, 4 KB\nside-a/notes  1 facts, 1 KB\n"
	labels := map[string]kmemSessionCfg{
		"beta":     {As: "Beta Work", Scope: scopeList{"beta", "side-a"}},
		"old-name": {As: "Old Name", Topic: "something"},
	}
	tasks := []kmemTask{
		{ID: "t1", Status: "claimed", Topic: "side-a/open"},
		{ID: "t2", Status: "open", Topic: "beta"},
		{ID: "t3", Status: "open", Topic: "alpha"},
		{ID: "t4", Status: "open", Topic: "kmem"},
	}
	ts := buildTopics(out, labels, tasks, map[string]string{"alpha": "state of alpha"})
	var slugs []string
	for _, x := range ts {
		slugs = append(slugs, x.Slug)
	}
	if strings.Join(slugs, ",") != "beta,alpha,side-a" {
		t.Fatalf("labeled first, then other top-level topics; kmem is never a tab: %v", slugs)
	}
	if b := ts[0]; b.Title != "Beta Work" || !b.Labeled || b.Claimed != 1 || b.Open != 1 {
		t.Fatalf("label title and tasks counted over the scope: %+v", b)
	}
	if a := ts[1]; a.Title != "Alpha" || a.Status != "state of alpha" || a.Open != 1 || len(a.Scope) != 1 {
		t.Fatalf("unlabeled topic: %+v", a)
	}
	if got := topicButtonLabel(ts[1]); got != "Alpha · state of alpha · 0 claimed · 1 open" {
		t.Fatalf("button label %q", got)
	}
}

func TestTopicParsing(t *testing.T) {
	if got := statusLine("[f99] fact: older\n[f12ab34] fact: Zero open bugs; release next\n"); got != "Zero open bugs; release next" {
		t.Fatalf("status line %q", got)
	}
	if statusLine("no facts in x/status\n") != "" {
		t.Fatal("no fact, no status")
	}
	var c struct {
		Sessions map[string]kmemSessionCfg `json:"sessions"`
	}
	raw := `{"sessions": {"a": {"as": "A", "scope": ["a", " b "]}, "c": {"scope": "c, d"}, "e": {"scope": 5}}}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Sessions["a"].Scope, ",") != "a,b" || strings.Join(c.Sessions["c"].Scope, ",") != "c,d" || len(c.Sessions["e"].Scope) != 0 {
		t.Fatalf("scope forms: %+v", c.Sessions)
	}
}

func TestTopicPrompt(t *testing.T) {
	tp := topicInfo{Slug: "alpha", Title: "Alpha", Scope: []string{"alpha", "side-a"}}
	old := topicPromptFor(tp, false, false)
	for _, want := range []string{"Alpha coordinator", `kmem name "alpha"`, "`kmem --as alpha`", "`kmem brief`", "`kmem task list`", "--topic alpha,side-a", "`alpha/status`", "--replaces"} {
		if !strings.Contains(old, want) {
			t.Fatalf("fallback prompt lacks %q: %s", want, old)
		}
	}
	if strings.Contains(old, "handoff") || strings.Contains(old, "resume") {
		t.Fatalf("no handoff, no resume: %s", old)
	}
	if kmemNameIn(old) != "alpha" {
		t.Fatal("the prompt names the kmem name in the form plan-first recognises")
	}
	k2 := topicPromptFor(tp, true, true)
	if !strings.Contains(k2, "`kmem context --topic alpha`") || strings.Contains(k2, "kmem task list") || !strings.Contains(k2, "is new") {
		t.Fatalf("topic briefing prompt: %s", k2)
	}
}

func topicKmem(t *testing.T, h *harness) (*sync.Mutex, *[]string) {
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	oldCfg := kmemCfgPath
	kmemCfgPath = func() string { return t.TempDir() + "/none.json" }
	resetKmemSessions()
	t.Cleanup(func() { kmemCfgPath = oldCfg; resetKmemSessions() })
	var mu sync.Mutex
	var calls []string
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		mu.Lock()
		calls = append(calls, prog+" "+strings.Join(args, " "))
		mu.Unlock()
		switch {
		case prog == "kmem" && len(args) > 0 && args[0] == "version":
			return `{"version": "1.8.1", "api": 1}`, nil
		case prog == "kmem" && len(args) > 0 && args[0] == "topics":
			return "alpha/core  2 facts, 1 KB\nalpha/status  1 facts, 1 KB\nbeta/open  1 facts, 1 KB\n", nil
		case prog == "kmem" && len(args) > 1 && args[0] == "task" && args[1] == "list":
			return `{"tasks": [{"id": "t1", "status": "claimed", "topic": "alpha", "owner": "alpha"}], "api": 1}`, nil
		case prog == "kmem" && len(args) > 1 && args[0] == "get" && args[1] == "alpha/status":
			return "[f1] fact: alpha is half done\n", nil
		case prog == "kmem" && len(args) > 1 && args[0] == "handoff" && args[1] == "list":
			return `{"handoffs": [], "api": 1}`, nil
		}
		return "", errors.New("unexpected")
	}
	return &mu, &calls
}

func cardButtons(h *harness) []string {
	var labels []string
	deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok {
			labels = append(labels, b.Text)
		}
	})
	return labels
}

func sentText(s *deskSession) string {
	var b strings.Builder
	for _, it := range s.conv.Items {
		b.WriteString(it.Text + "\n")
	}
	return b.String()
}

func TestOpenTopicCard(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	var s *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		s = h.d.current
	})
	browseAll(h)
	h.eventually("topic buttons", func() bool {
		return strings.Contains(strings.Join(cardButtons(h), "|"), "Alpha · alpha is half done · 1 claimed · 0 open")
	})
	if j := strings.Join(cardButtons(h), "|"); !strings.Contains(j, "Beta") || !strings.Contains(j, "Browse") && !strings.Contains(j, "Show recent") {
		t.Fatalf("card buttons: %s", j)
	}
	h.ui(func() {
		a, _ := cachedTopic("alpha")
		h.d.pickTopic(s, a, false)
		if s.pickPending || h.d.org.Titles[s.id] != "Alpha" || h.d.org.Kmem[s.id] != "alpha" {
			t.Fatalf("title %q kmem %q", h.d.org.Titles[s.id], h.d.org.Kmem[s.id])
		}
		if h.d.topicOf(s.id) != "alpha" || h.d.projectOf(s.id) != "Alpha" {
			t.Fatal("a topic session groups under its topic")
		}
	})
	h.eventually("fresh topic prompt sent", func() bool {
		return strings.Contains(sentText(s), "You are the Alpha coordinator")
	})
	if txt := sentText(s); strings.Contains(txt, "handoff") || !strings.Contains(txt, "--topic alpha") {
		t.Fatalf("fresh start, scoped briefing: %s", txt)
	}
	// One live session per topic: a second tab picking Alpha switches.
	var r *deskSession
	h.ui(func() {
		h.d.newSessionDialog()
		r = h.d.current
	})
	h.eventually("live topic labelled", func() bool {
		return strings.Contains(strings.Join(cardButtons(h), "|"), "Alpha · open in tab")
	})
	h.ui(func() {
		a, _ := cachedTopic("alpha")
		h.d.pickTopic(r, a, false)
		if h.d.current != s || !r.pickPending || h.d.org.Kmem[r.id] != "" {
			t.Fatal("picking a live topic switches to its tab and leaves the new tab unpicked")
		}
		if h.d.openTopic("alpha") != s {
			t.Fatal("openTopic switches to the live session")
		}
		// A free name that is an existing topic opens it; a new one gets a slug.
		h.d.pickNewTopic(r, "BETA")
		if h.d.org.Kmem[r.id] != "beta" || h.d.org.Titles[r.id] != "Beta" {
			t.Fatalf("existing topic by name: %q %q", h.d.org.Kmem[r.id], h.d.org.Titles[r.id])
		}
		h.d.newSessionDialog()
		n := h.d.current
		h.d.pickNewTopic(n, "kmem")
		if !n.pickPending {
			t.Fatal("kmem is the kmem window, never a tab")
		}
		h.d.pickNewTopic(n, "Garden Planner")
		if h.d.org.Kmem[n.id] != "garden-planner" || h.d.org.Titles[n.id] != "Garden Planner" || h.d.topicOf(n.id) != "garden-planner" {
			t.Fatalf("new topic: %q %q", h.d.org.Kmem[n.id], h.d.org.Titles[n.id])
		}
		s = n
	})
	h.eventually("new topic prompt", func() bool { return strings.Contains(sentText(s), "This topic is new") })
}

func TestLegacyMergeNotice(t *testing.T) {
	h := newHarness(t)
	topicKmem(t, h)
	h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
	h.d.app.Preferences().SetString(legacyNoticeKey, "")
	var old, topic *deskSession
	h.ui(func() {
		old = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[old.id] = "old-name"
		topic = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[topic.id] = "alpha"
		h.d.tabs = append(h.d.tabs, old.id, topic.id)
		h.d.rebuildRows()
	})
	h.eventually("merge notice on the old-name session", func() bool {
		return strings.Contains(sentText(old), legacyNotice)
	})
	h.ui(func() {
		h.d.rebuildRows()
		if strings.Count(sentText(old), legacyNotice) != 1 {
			t.Fatal("the merge notice is shown once")
		}
		if strings.Contains(sentText(topic), legacyNotice) {
			t.Fatal("a topic coordinator gets no merge notice")
		}
		if h.d.projectOf(topic.id) != "Alpha" {
			t.Fatalf("topic group %q", h.d.projectOf(topic.id))
		}
	})
}
