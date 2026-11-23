package main

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// ecKmem stubs kmem for the end-of-session check: topics alpha and beta,
// `kmem version` with or without the end-check feature, and per kmem name a
// queue of end-check replies (the last one repeats).
type ecKmem struct {
	mu       sync.Mutex
	features bool
	replies  map[string][]ecReply
	calls    []string
}

type ecReply struct {
	out string
	err error
}

var (
	ecOK      = ecReply{out: `{"ok": true, "name": "alpha", "session": "s", "since": null, "blockers": [], "notes": [], "api": 1}`}
	ecBlocked = ecReply{out: `NOTICE (once): rules changed
{"ok": false, "name": "alpha", "session": "s", "since": "2026-11-23T10:00:00Z",
 "blockers": [
  {"kind": "claim", "id": "t1", "title": "Ship it", "need": "checkpoint or release"},
  {"kind": "lock", "name": "alpha:batch", "need": "release"},
  {"kind": "status", "id": null, "need": "kmem add alpha/status ... --replaces"}],
 "notes": [], "api": 1}`, err: errors.New("exit status 1")}
	ecBroken = ecReply{out: "Traceback: database is locked", err: errors.New("exit status 2")}
)

func endCheckKmem(t *testing.T, h *harness, features bool) *ecKmem {
	resetTopicCache()
	t.Cleanup(resetTopicCache)
	oldCfg := kmemCfgPath
	kmemCfgPath = func() string { return t.TempDir() + "/none.json" }
	resetKmemSessions()
	t.Cleanup(func() { kmemCfgPath = oldCfg; resetKmemSessions() })
	k := &ecKmem{features: features, replies: map[string][]ecReply{}}
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		k.mu.Lock()
		defer k.mu.Unlock()
		k.calls = append(k.calls, prog+" "+strings.Join(args, " "))
		switch {
		case prog != "kmem" || len(args) == 0:
		case args[0] == "version":
			if k.features {
				return `{"version": "1.10.0", "api": 1, "features": ["topic-context", "end-check", "sessions"]}`, nil
			}
			return `{"version": "1.9.1", "api": 1}`, nil
		case args[0] == "topics":
			return "alpha/core  2 facts, 1 KB\nbeta/open  1 facts, 1 KB\n", nil
		case args[0] == "task" && len(args) > 1 && args[1] == "list":
			return `{"tasks": [], "api": 1}`, nil
		case args[0] == "handoff" && len(args) > 1 && args[1] == "list":
			return `{"handoffs": [], "api": 1}`, nil
		case args[0] == "--as" && len(args) > 2 && args[2] == "end-check":
			q := k.replies[args[1]]
			if len(q) == 0 {
				return "", errors.New("no reply set")
			}
			r := q[0]
			if len(q) > 1 {
				k.replies[args[1]] = q[1:]
			}
			return r.out, r.err
		}
		return "", errors.New("unexpected")
	}
	h.d.kmemVer.reset() // the harness read the stub's version already
	h.d.kmemAPILevel()  // the UI thread reads the feature from this cache
	h.d.loadTopics()
	return k
}

func (k *ecKmem) set(name string, rs ...ecReply) {
	k.mu.Lock()
	k.replies[name] = rs
	k.mu.Unlock()
}

func (k *ecKmem) called(sub string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, c := range k.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// topicSession starts a running, idle, non-trivial session for topic slug.
func topicSession(h *harness, slug string) *deskSession {
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = slug
		h.d.send("start")
	})
	h.eventually("running and idle", func() bool { return s.running() && !s.conv.Busy && !s.inTurn })
	h.ui(func() {
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, ToolName: "Bash"})
	})
	return s
}

func TestEndCheckFeatureAbsentKeepsHandoffSave(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, false)
	s := topicSession(h, "alpha")
	h.ui(func() {
		if h.d.endCheckFor(s) {
			t.Fatal("no end-check feature: no end-of-session check")
		}
		h.d.endWithSave(s.id, placeInactive)
		if s.endWait != nil || s.kmemSave == nil || !s.kmemSave.end {
			t.Fatal("End saves the handoff as before")
		}
		if !strings.Contains(sentText(s), "kmem handoff save alpha") {
			t.Fatalf("handoff save request: %s", sentText(s))
		}
		if len(h.d.quitCheckSessions()) != 0 {
			t.Fatal("Quit runs no check without the feature")
		}
	})
	if k.called("end-check") {
		t.Fatal("end-check is never run without the feature")
	}
	// The topic prompt falls back to the ranking briefing too.
	if strings.Contains(h.d.topicPrompt(topicInfo{Slug: "alpha", Title: "Alpha"}, false), "`kmem context --topic alpha`") {
		t.Fatal("no topic-context feature: ranking fallback")
	}
}

func TestEndCheckOKEnds(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecOK)
	s := topicSession(h, "alpha")
	h.ui(func() {
		if !contains(h.d.tabs, s.id) {
			t.Fatal("the session has a tab")
		}
		h.d.endWithSave(s.id, placeInactive)
		if s.endWait == nil || s.kmemSave != nil {
			t.Fatal("End runs the end-of-session check, not a handoff save")
		}
	})
	h.eventually("stopped and closed", func() bool { return s.stopping && s.endWait == nil })
	h.ui(func() {
		if contains(h.d.tabs, s.id) || !h.d.org.Ended[s.id] || h.d.sessions[s.id] == nil {
			t.Fatal("the tab closes, the session is marked ended and kept")
		}
		if txt := sentText(s); strings.Contains(txt, "handoff save") || strings.Contains(txt, "lists:") {
			t.Fatalf("nothing asked of a session that passes: %s", txt)
		}
	})
	if !k.called("kmem --as alpha end-check --session "+s.id+" --pid ") || !k.called("--json") {
		t.Fatalf("end-check call: %v", k.calls)
	}
}

func TestEndCheckBlockersAskSession(t *testing.T) {
	h := newHarness(t)
	h.d.planFirst = true
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecBlocked, ecOK)
	s := topicSession(h, "alpha")
	h.ui(func() { h.d.endWithSave(s.id, placeInactive) })
	h.eventually("blockers shown and sent", func() bool {
		return strings.Contains(sentText(s), "End-of-session check: 3 blocker(s)")
	})
	h.ui(func() {
		txt := sentText(s)
		for _, want := range []string{"claim t1 “Ship it”: checkpoint or release", "the shared lock turn alpha:batch: release", "the status fact", "`kmem --as alpha end-check` lists:"} {
			if !strings.Contains(txt, want) {
				t.Fatalf("lacks %q: %s", want, txt)
			}
		}
		if s.endWait != nil { // else the reply turn already passed the re-check
			if _, meta, _ := h.d.rowText(s.id); !strings.Contains(meta, "end-of-session check: 3 blocker(s)") {
				t.Fatalf("meta: %q", meta)
			}
		}
		r, _ := parseEndCheck(ecBlocked.out)
		x := &deskSession{endWait: &endWait{blockers: r.Blockers}}
		if !h.d.kmemSaveAllowed(x, bashReq("pzlock release alpha:batch")) || !h.d.kmemSaveAllowed(x, bashReq("kmem --as alpha task checkpoint t1 'done'")) {
			t.Fatal("the listed lock release and kmem checkpoints skip plan-first")
		}
		if h.d.kmemSaveAllowed(x, bashReq("git commit -am x")) || h.d.kmemSaveAllowed(x, bashReq("pzlock release other")) {
			t.Fatal("commits and other locks still need a plan")
		}
	})
	// The session's reply turn finishes; the re-run passes and the session ends.
	h.eventually("ended after the re-check", func() bool { return s.stopping && s.endWait == nil })
	h.ui(func() {
		if !h.d.org.Ended[s.id] {
			t.Fatal("marked ended")
		}
	})
}

func TestEndCheckBlockersCancel(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecBlocked)
	s := topicSession(h, "alpha")
	h.ui(func() { h.d.relaunchViaKmem(s.id, "alpha") })
	h.eventually("rotation cancelled", func() bool {
		return s.endWait == nil && strings.Contains(sentText(s), "so the rotation was cancelled; nothing was stopped")
	})
	h.ui(func() {
		if !s.running() || s.stopping || h.d.sessions[s.id] == nil {
			t.Fatal("nothing stopped or deleted")
		}
	})
}

func TestEndCheckKmemErrorFallsBack(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecBroken)
	s := topicSession(h, "alpha")
	h.ui(func() { h.d.endWithSave(s.id, placeInactive) })
	// The handoff save request goes out (the stub has no saved handoff, so
	// the fake session's reply then cancels the end, as before).
	h.eventually("handoff save fallback", func() bool {
		txt := sentText(s)
		return strings.Contains(txt, "kmem could not run the end-of-session check (exit status 2: Traceback: database is locked)") &&
			strings.Contains(txt, "kmem handoff save alpha")
	})
	h.ui(func() {
		if s.endWait != nil {
			t.Fatal("the check is over")
		}
	})
}

func TestEndCheckRotationStartsFreshTopic(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecOK)
	s := topicSession(h, "alpha")
	old := s.id
	h.ui(func() { h.d.relaunchViaKmem(s.id, "alpha") })
	var ns *deskSession
	h.eventually("fresh session", func() bool {
		if h.d.sessions[old] != nil {
			return false
		}
		for id, x := range h.d.sessions {
			if h.d.org.Kmem[id] == "alpha" && strings.Contains(sentText(x), "You are the Alpha coordinator") {
				ns = x
				return true
			}
		}
		return false
	})
	h.ui(func() {
		txt := sentText(ns)
		if !strings.Contains(txt, "`kmem context --topic alpha`") || strings.Contains(txt, "handoff") {
			t.Fatalf("fresh topic prompt with the K2 briefing: %s", txt)
		}
		if !contains(h.d.tabs, ns.id) {
			t.Fatal("the new session takes the tab")
		}
	})
}

func TestEndCheckQuitSeveralSessions(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecOK)
	k.set("beta", ecBlocked)
	a := topicSession(h, "alpha")
	b := topicSession(h, "beta")
	other := topicSession(h, "old-name") // not a topic: no check
	var failed []quitFail
	calls := 0
	h.ui(func() {
		checks := h.d.quitCheckSessions()
		if len(checks) != 2 || contains([]string{checks[0].id, checks[1].id}, other.id) {
			t.Fatalf("checks the two topic sessions: %d", len(checks))
		}
		if m := quitMessage(3, 0, 2); !strings.Contains(m, "Each topic runs its end-of-session check first") {
			t.Fatalf("quit text: %s", m)
		}
		h.d.quitEndChecks(checks, func(f []quitFail) { calls++; failed = f })
	})
	h.eventually("both checks done", func() bool { return calls > 0 })
	h.ui(func() {
		if calls != 1 || len(failed) != 1 || failed[0].title != h.titleOf(b.id) {
			t.Fatalf("one topic fails: %d %+v", calls, failed)
		}
		msg := quitFailMessage(failed)
		if !strings.Contains(msg, "claim t1") || !strings.Contains(msg, "Stop and quit anyway?") {
			t.Fatalf("second confirm: %s", msg)
		}
		if !strings.Contains(sentText(b), "Quill Desktop is quitting") {
			t.Fatal("the blocked topic was asked to resolve its blockers")
		}
		if strings.Contains(sentText(a), "Quill Desktop is quitting") {
			t.Fatal("a passing topic is not asked anything")
		}
		for _, s := range []*deskSession{a, b, other} {
			if !s.running() || s.stopping || s.endWait != nil {
				t.Fatal("the checks stop nothing; Quit stops after the confirm")
			}
		}
	})
}

func (h *harness) titleOf(id string) string {
	t, _, _ := h.d.rowText(id)
	return t
}

func TestEndCheckCheckpointNowReports(t *testing.T) {
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	k.set("alpha", ecBlocked)
	s := topicSession(h, "alpha")
	h.ui(func() {
		h.d.saveHandoffNow(s)
		if s.endWait == nil || s.kmemSave != nil || !strings.Contains(sentText(s), "Checkpoint now: run `kmem task checkpoint`") {
			t.Fatalf("Checkpoint now asks first: %s", sentText(s))
		}
	})
	h.eventually("report after the reply", func() bool {
		return s.endWait == nil && strings.Contains(sentText(s), "End-of-session check still lists: claim t1")
	})
	h.ui(func() {
		if !s.running() || s.stopping || strings.Contains(sentText(s), "cancelled") {
			t.Fatal("the session goes on")
		}
	})
}
