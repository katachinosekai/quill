package main

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/live"
)

// extKmem stubs kmem for the external-session tests: the version with or
// without the "sessions" feature, the topics, and `kmem sessions --live`.
func extKmem(t *testing.T, h *harness, features string, sessions string) *atomic.Int64 {
	resetExtSessions()
	t.Cleanup(resetExtSessions)
	n := new(atomic.Int64)
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
			n.Add(1)
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
	extKmem(t, h, `"sessions"`, extLive)
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
	if got := h.d.loadExtSessions(); len(got) != 0 || n.Load() != 0 {
		t.Fatalf("without the sessions feature kmem sessions is never run: %v calls %d", got, n.Load())
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
	// n counts the kmem call, which returns before loadExtSessions stores the
	// list and clears busy; back-dating the cache earlier would be overwritten.
	h.eventually("first load", func() bool {
		extCache.mu.Lock()
		defer extCache.mu.Unlock()
		return n.Load() == 1 && extCache.loaded && !extCache.busy
	})
	h.ui(func() {
		for i := 0; i < 5; i++ {
			h.d.projectGroups(nil)
		}
	})
	if n.Load() != 1 {
		t.Fatalf("sidebar refreshes reuse the cache: %d kmem calls", n.Load())
	}
	extCache.mu.Lock()
	extCache.at = extCache.at.Add(-2 * extRefresh)
	extCache.mu.Unlock()
	h.ui(func() { h.d.projectGroups(nil) })
	h.eventually("reload after 30 s", func() bool { return n.Load() == 2 })
	h.eventually("reload stored", func() bool {
		extCache.mu.Lock()
		defer extCache.mu.Unlock()
		return !extCache.busy
	})
	h.ui(func() { h.d.refreshExtSessions(true) })
	h.eventually("forced reload", func() bool { return n.Load() == 3 })
}

// takeOverSetup: the external session "alpha" is the harness's fake terminal
// process (a `sleep`, never a Claude), its transcript is known, and Release is
// replaced by a recorder so nothing is ever signalled.
func takeOverSetup(t *testing.T, h *harness, features string) (released *[]string) {
	extKmem(t, h, features, `{"api":1,"sessions":[{"session":"`+smokeID+`","name":"alpha","names":["alpha"],"live":"live"}]}`)
	h.d.loadTopics()
	h.d.loadExtSessions()
	released = new([]string)
	h.d.releaseFn = func(p live.Process) error {
		*released = append(*released, p.SessionID)
		return nil
	}
	return released
}

func TestTakeOverConfirmAndFlow(t *testing.T) {
	h := newHarness(t)
	rel := takeOverSetup(t, h, `"sessions"`)
	var pend *deskSession
	h.ui(func() {
		e, ok := h.d.topicExternal("alpha")
		if !ok || h.d.extTakeOverBlock(e) != "" {
			t.Fatalf("a live local external session can be taken over: %v %q", ok, h.d.extTakeOverBlock(e))
		}
		pend = h.d.createSession(homeDir(), "default", "default")
		pend.pickPending = true
		h.d.offerTakeOver(e, pend)
	})
	h.eventually("confirm", func() bool { return overlayButton(h, "Yes") != nil })
	h.ui(func() {
		if len(*rel) != 0 {
			t.Fatal("nothing is released before the user confirms")
		}
	})
	var msg string
	h.ui(func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if l, ok := x.(*widget.Label); ok && strings.HasPrefix(l.Text, "Take over Alpha") {
					msg = l.Text
				}
			})
		}
		test.Tap(overlayButton(h, "Yes"))
	})
	if !strings.Contains(msg, "from the terminal? The terminal session stops and continues here.") {
		t.Fatalf("confirm text: %q", msg)
	}
	h.eventually("released and resumed", func() bool {
		s := h.d.sessions[smokeID]
		return len(*rel) == 1 && s != nil && s.running() && !s.takeover && h.d.current == s
	})
	h.ui(func() {
		if (*rel)[0] != smokeID {
			t.Fatalf("released the matching process: %v", *rel)
		}
		if h.d.org.Kmem[smokeID] != "alpha" {
			t.Fatalf("kmem name = topic: %q", h.d.org.Kmem[smokeID])
		}
		if h.d.sessions[pend.id] != nil && h.d.sessions[pend.id].pickPending {
			t.Fatal("the pending card tab is closed")
		}
	})
}

func TestTakeOverDeclined(t *testing.T) {
	h := newHarness(t)
	rel := takeOverSetup(t, h, `"sessions"`)
	h.ui(func() {
		e, _ := h.d.topicExternal("alpha")
		h.d.offerTakeOver(e, nil)
	})
	h.eventually("confirm", func() bool { return overlayButton(h, "No") != nil })
	h.ui(func() { test.Tap(overlayButton(h, "No")) })
	h.ui(func() {
		if len(*rel) != 0 || h.d.org.Kmem[smokeID] != "" || h.d.sessions[smokeID] != nil && h.d.sessions[smokeID].running() {
			t.Fatal("declining changes nothing")
		}
	})
}

func TestTakeOverRefusals(t *testing.T) {
	h := newHarness(t)
	rel := takeOverSetup(t, h, `"sessions"`)
	h.ui(func() {
		e, _ := h.d.topicExternal("alpha")
		// Another machine: kmem lists it, no local process.
		saved := h.d.terms
		h.d.terms = map[string]live.Process{}
		if why := h.d.extTakeOverBlock(e); !strings.Contains(why, "not running on this computer") {
			t.Fatalf("remote session: %q", why)
		}
		h.d.offerTakeOver(e, nil)
		h.d.terms = saved
		// Transcript missing.
		info := h.d.infos[smokeID]
		delete(h.d.infos, smokeID)
		if h.d.extTakeOverBlock(e) == "" {
			t.Fatal("no transcript, no take over")
		}
		h.d.infos[smokeID] = info
		// A name that maps to no topic.
		if h.d.extTakeOverBlock(extSession{ID: smokeID, Name: "nosuchtopic"}) == "" {
			t.Fatal("not a topic")
		}
	})
	h.eventually("reason shown", func() bool { return overlayButton(h, "OK") != nil })
	h.ui(func() {
		test.Tap(overlayButton(h, "OK"))
		if len(*rel) != 0 {
			t.Fatal("a refused take over releases nothing")
		}
	})
	// Without the sessions feature nothing is offered.
	h2 := newHarness(t)
	takeOverSetup(t, h2, ``)
	h2.ui(func() {
		if why := h2.d.extTakeOverBlock(extSession{ID: smokeID, Name: "alpha"}); why == "" {
			t.Fatal("no sessions feature: no take over")
		}
	})
}

func TestTakeOverCardEntry(t *testing.T) {
	h := newHarness(t)
	takeOverSetup(t, h, `"sessions"`)
	h.ui(func() {
		s := h.d.createSession(homeDir(), "default", "default")
		s.pickPending = true
		h.d.open(s.id)
	})
	h.eventually("card entry offers take over", func() bool {
		var ok bool
		walk(h.d.chat.box, func(x fyne.CanvasObject) {
			if b, is := x.(*widget.Button); is && strings.Contains(b.Text, "running elsewhere (alpha) · Take over") && !b.Disabled() {
				ok = true
			}
		})
		return ok
	})
}
