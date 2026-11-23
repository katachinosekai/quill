package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// extKmem stubs kmem for the external-session tests: the version with or
// without the "sessions" feature, the topics, and `kmem sessions --live`.
func extKmem(t *testing.T, h *harness, features string, sessions string) *int {
	resetExtSessions()
	t.Cleanup(resetExtSessions)
	var mu sync.Mutex
	n := new(int)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	oldCfg := kmemCfgPath
	kmemCfgPath = func() string { return t.TempDir() + "/none.json" }
	resetKmemSessions()
	t.Cleanup(func() { kmemCfgPath = oldCfg; resetKmemSessions() })
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case a == "version --json":
			return `{"version": "1.10.0", "api": 1, "features": [` + features + `]}`, nil
		case a == "topics":
			return "alpha/core  2 facts, 1 KB\nbeta/open  1 facts, 1 KB\n", nil
		case a == "sessions --live --json":
			mu.Lock()
			*n++
			mu.Unlock()
			return sessions, nil
		}
		return "", errors.New("unexpected " + a)
	}
	return n
}

const extLive = `{"api":1,"sessions":[
 {"session":"ext-1","name":"alpha","names":["alpha"],"live":"live"},
 {"session":"ext-2","name":"beta","names":["beta"],"live":"gone"},
 {"session":"ext-3","name":"beta","names":["beta"],"live":"unknown"},
 {"session":"ext-4","name":"nosuchtopic","names":["nosuchtopic"],"live":"live"}]}`

func TestExternalLiveUnderTopic(t *testing.T) {
	h := newHarness(t)
	n := extKmem(t, h, `"sessions"`, extLive)
	h.d.loadTopics()
	h.d.loadExtSessions()
	h.ui(func() {
		ext := h.d.externalLive()
		if len(ext) != 1 || len(ext["alpha"]) != 1 || ext["alpha"][0].ID != "ext-1" {
			t.Fatalf("only the live session with a topic name shows, gone/unknown/unmapped do not: %v", ext)
		}
		var found *extSession
		for _, g := range h.d.projectGroups(nil) {
			if g.name == "Alpha" && len(g.entries) == 0 {
				found = &g.ext[0]
			}
			if g.name == "Beta" {
				t.Fatal("a gone or unknown session never makes a group")
			}
		}
		if found == nil || found.Name != "alpha" {
			t.Fatal("the external session is a row under its topic group")
		}
		// Quill's own session with that id is not external.
		h.d.sessions["ext-1"] = &deskSession{id: "ext-1"}
		if len(h.d.externalLive()) != 0 {
			t.Fatal("a session Quill runs is not 'running elsewhere'")
		}
		delete(h.d.sessions, "ext-1")
	})
	_ = n
}

func TestExternalOldNameNeedsConfigMapping(t *testing.T) {
	h := newHarness(t)
	extKmem(t, h, `"sessions"`, `{"api":1,"sessions":[{"session":"e","name":"Zomboid Launcher","live":"live"}]}`)
	h.d.loadTopics()
	h.d.loadExtSessions()
	h.ui(func() {
		if len(h.d.externalLive()) != 0 {
			t.Fatal("an old name without an explicit config mapping is not guessed")
		}
	})
}

func TestExternalFeatureAbsent(t *testing.T) {
	h := newHarness(t)
	n := extKmem(t, h, ``, extLive)
	h.d.loadTopics()
	if got := h.d.loadExtSessions(); len(got) != 0 || *n != 0 {
		t.Fatalf("without the sessions feature kmem sessions is never run: %v calls %d", got, *n)
	}
	h.ui(func() {
		if len(h.d.externalLive()) != 0 {
			t.Fatal("nothing external without the feature")
		}
		s := h.d.createSession(homeDir(), "default", "default")
		s.pickPending = true
		a, _ := cachedTopic("alpha")
		h.d.pickTopic(s, a, false)
		if s.pickPending {
			t.Fatal("behaves as before: the topic opens")
		}
	})
}

func TestOneLiveCheckExternal(t *testing.T) {
	h := newHarness(t)
	extKmem(t, h, `"sessions"`, extLive)
	h.d.loadTopics()
	h.d.loadExtSessions()
	h.ui(func() {
		s := h.d.createSession(homeDir(), "default", "default")
		s.pickPending = true
		a, _ := cachedTopic("alpha")
		h.d.pickTopic(s, a, false)
		if !s.pickPending || h.d.org.Kmem[s.id] != "" {
			t.Fatal("a topic held by an external live session is refused")
		}
		if !strings.Contains(sentText(s), "already live outside Quill Desktop") {
			t.Fatalf("the refusal says why: %q", sentText(s))
		}
		if h.d.openTopic("alpha") != nil {
			t.Fatal("openTopic refuses too")
		}
		b, _ := cachedTopic("beta")
		h.d.pickTopic(s, b, false)
		if s.pickPending {
			t.Fatal("a topic whose session is gone opens")
		}
	})
}

func TestExternalCache(t *testing.T) {
	h := newHarness(t)
	n := extKmem(t, h, `"sessions"`, extLive)
	h.d.loadTopics()
	h.d.kmemAPILevel()
	h.ui(func() {
		for i := 0; i < 5; i++ {
			h.d.projectGroups(nil)
		}
	})
	h.eventually("first load", func() bool { return *n == 1 })
	h.ui(func() {
		for i := 0; i < 5; i++ {
			h.d.projectGroups(nil)
		}
	})
	if *n != 1 {
		t.Fatalf("sidebar refreshes reuse the cache: %d kmem calls", *n)
	}
	extCache.mu.Lock()
	extCache.at = extCache.at.Add(-2 * extRefresh)
	extCache.mu.Unlock()
	h.ui(func() { h.d.projectGroups(nil) })
	h.eventually("reload after 30 s", func() bool { return *n == 2 })
	h.ui(func() { h.d.refreshExtSessions(true) })
	h.eventually("forced reload", func() bool { return *n == 3 })
}
