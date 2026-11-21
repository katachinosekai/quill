package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

type kmemStub struct {
	mu    sync.Mutex
	calls []string
	saved map[string]bool
}

func (k *kmemStub) run(prog string, args ...string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	line := strings.Join(args, " ")
	k.calls = append(k.calls, line)
	switch {
	case line == "version --json":
		return fmt.Sprintf(`{"api":%d,"version":"test"}`, kmemAPI), nil
	case strings.HasPrefix(line, "handoff list"):
		var hs []string
		for n := range k.saved {
			hs = append(hs, fmt.Sprintf(`{"name":%q,"items":2,"saved":%q}`, n, time.Now().Add(-time.Hour).Format(time.RFC3339)))
		}
		return fmt.Sprintf(`{"api":%d,"handoffs":[%s]}`, kmemAPI, strings.Join(hs, ",")), nil
	case strings.HasPrefix(line, "handoff get"):
		return fmt.Sprintf(`{"api":%d,"name":"x","items":[{"id":"f0000a1","kind":"fact","text":"t"},{"id":"f0000a2","kind":"path","text":"p"}],"text":""}`, kmemAPI), nil
	}
	return "", nil
}

func (k *kmemStub) has(prefix string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, c := range k.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type timers struct {
	fns []func()
	ds  []time.Duration
}

func (t *timers) after(d time.Duration, f func()) { t.fns = append(t.fns, f); t.ds = append(t.ds, d) }

func (t *timers) fire(d time.Duration) {
	for i, x := range t.ds {
		if x == d && t.fns[i] != nil {
			f := t.fns[i]
			t.fns[i] = nil
			f()
		}
	}
}

func identityHarness(t *testing.T) (*harness, *kmemStub, *timers) {
	h := newHarness(t)
	k := &kmemStub{saved: map[string]bool{}}
	tm := &timers{}
	h.d.kmemOff = false
	h.d.kmemRun = k.run
	h.d.after = tm.after
	return h, k, tm
}

func runningSession(t *testing.T, h *harness, busy bool) *deskSession {
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	if busy {
		h.ui(func() { s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Tool, ToolName: "Bash"}) })
	}
	return s
}

func TestCloseTabEndsAndSavesIntoInactive(t *testing.T) {
	h, _, tm := identityHarness(t)
	s := runningSession(t, h, true)
	h.ui(func() {
		h.d.closeSessionTab(s.id)
		if h.d.placement(s.id) != placeInactive || s.pendingEnd == 0 {
			t.Fatalf("close moves the session to Inactive and schedules the end: place=%q pending=%d", h.d.placement(s.id), s.pendingEnd)
		}
		for _, id := range h.d.tabs {
			if id == s.id {
				t.Fatal("the tab closes at once")
			}
		}
		h.d.open(s.id)
		if s.pendingEnd != 0 || h.d.placement(s.id) != placeActive {
			t.Fatal("opening the session again before the timer keeps it running in Active")
		}
	})
	tm.fire(closeUndo)
	h.ui(func() {
		if s.kmemSave != nil {
			t.Fatal("a cancelled end must not start later")
		}
		h.d.closeSessionTab(s.id)
	})
	tm.fire(closeUndo)
	h.ui(func() {
		if s.kmemSave == nil || !s.kmemSave.end || s.kmemSave.place != placeInactive {
			t.Fatalf("after the undo window the session is asked to save its handoff: %+v", s.kmemSave)
		}
		name := h.d.org.Kmem[s.id]
		if name == "" {
			t.Fatal("an unlinked session gets a kmem name when it is closed")
		}
		h.d.finishKmemEnd(s, name, s.kmemSave.place)
		if h.d.placement(s.id) != placeInactive || !h.d.org.Ended[s.id] {
			t.Fatal("after the save the session is in Inactive and marked ended")
		}
	})
}

func TestEndTimeoutKeepsSessionRunning(t *testing.T) {
	h, _, tm := identityHarness(t)
	s := runningSession(t, h, true)
	h.ui(func() {
		h.d.endWithSave(s.id, placeInactive)
		if s.kmemSave == nil {
			t.Fatal("end asks for a handoff")
		}
	})
	tm.fire(kmemEndTimeout)
	h.ui(func() {
		if s.kmemSave != nil || !s.running() {
			t.Fatal("no save within the timeout: the end is cancelled and the session keeps running")
		}
	})
}

func TestTrivialSessionEndsWithoutHandoff(t *testing.T) {
	h, k, _ := identityHarness(t)
	s := runningSession(t, h, false)
	h.ui(func() {
		h.d.endWithSave(s.id, placeInactive)
		if s.kmemSave != nil || k.has("handoff save") {
			t.Fatal("a trivial session is just stopped")
		}
		if h.d.placement(s.id) != placeInactive {
			t.Fatal("and moved to Inactive")
		}
	})
}

func TestMoveRunningSessionToArchiveEndsIt(t *testing.T) {
	h, _, _ := identityHarness(t)
	s := runningSession(t, h, true)
	h.ui(func() {
		h.d.placeSession(s.id, placeArchive)
		if s.kmemSave == nil || s.kmemSave.place != placeArchive {
			t.Fatal("moving a running session to Archive ends it with a handoff")
		}
	})
}

func TestResumeEndedSessionFromHandoff(t *testing.T) {
	h, _, _ := identityHarness(t)
	var old *deskSession
	h.ui(func() {
		old = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[old.id] = "demo-work"
		h.d.markEnded(old.id)
		if !h.d.offerResume(old.id) {
			t.Fatal("an ended session with a handoff offers the resume choice")
		}
		h.d.escape(nil)
		h.d.resumeFromHandoff(old.id)
		if h.d.org.Kmem[old.id] != "" || h.d.org.Ended[old.id] {
			t.Fatal("the kmem name leaves the old session")
		}
		found := false
		for id, n := range h.d.org.Kmem {
			found = found || (n == "demo-work" && id != old.id)
		}
		if !found {
			t.Fatal("the new session carries the kmem name")
		}
	})
}

func TestRenameMovesKmemName(t *testing.T) {
	h, k, _ := identityHarness(t)
	k.saved["old-name"] = true
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "old-name"
		h.d.renameSession(s.id, "New Name")
	})
	h.eventually("kmem renamed", func() bool { return k.has("handoff rename old-name new-name") })
	h.eventually("link renamed", func() bool { return h.d.org.Kmem[s.id] == "new-name" })
}

func TestDeleteTrashesHandoff(t *testing.T) {
	h, k, _ := identityHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "gone-work"
		h.d.trashHandoff(s.id)
	})
	h.eventually("handoff items trashed", func() bool { return k.has("trash f0000a1") && k.has("trash f0000a2") })
}

func TestNewSessionsAlwaysGetKmem(t *testing.T) {
	for _, c := range kmemChoices([]kmemHandoff{{Name: "x"}}) {
		if c == kmemNoChoice {
			t.Fatal("new sessions always get a kmem name")
		}
	}
}

func TestResumeAllFromHandoffs(t *testing.T) {
	h, _, _ := identityHarness(t)
	h.ui(func() {
		mk := func(name string, ended bool) *deskSession {
			s := h.d.createSession(h.proj, "default", "default")
			if name != "" {
				h.d.org.Kmem[s.id] = name
			}
			if ended {
				h.d.markEnded(s.id)
			}
			return s
		}
		a, b := mk("alpha-work", true), mk("beta-work", true)
		noName := mk("", true)
		h.d.org.Ended[noName.id] = true
		held := mk("gamma-work", true)
		holder := mk("", false)
		h.d.org.Kmem[holder.id] = "gamma-work"
		h.d.addTab(holder.id)
		notEnded := mk("delta-work", false)
		h.d.groups = append(h.d.groups, bookGroup{key: placeInactive, ids: []string{a.id, b.id, noName.id, held.id, notEnded.id}})
		got := h.d.resumableIn(placeInactive)
		if len(got) != 2 || got[0] != a.id || got[1] != b.id {
			t.Fatalf("only ended sessions with a free kmem name are resumable: %v", got)
		}
		if it := h.d.resumeAllMenuItem(placeInactive); it == nil || it.Label != "Resume all from handoffs (2)" {
			t.Fatalf("menu item: %+v", it)
		}
		if h.d.resumeAllMenuItem(placeArchive) != nil {
			t.Fatal("no item when nothing in the folder can be resumed")
		}
		h.d.resumeAll(got)
		for _, s := range []*deskSession{a, b} {
			if h.d.org.Kmem[s.id] != "" || h.d.org.Ended[s.id] {
				t.Fatal("the kmem names move to the new sessions")
			}
		}
		moved := 0
		for _, n := range h.d.org.Kmem {
			if n == "alpha-work" || n == "beta-work" {
				moved++
			}
		}
		if moved != 2 {
			t.Fatalf("two fresh sessions carry the names: %d", moved)
		}
	})
}

func TestResumeAllCommand(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "q.sock")
	h, _, _ := identityHarness(t)
	var old *deskSession
	h.ui(func() {
		old = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[old.id] = "cmd-work"
		h.d.org.Place[old.id] = placeInactive
		h.d.markEnded(old.id)
	})
	h.d.listenInstance(sock)
	if !sendCommand(sock, "resume-all") {
		t.Fatal("resume-all not acknowledged")
	}
	h.eventually("resumed by command", func() bool { return h.d.org.Kmem[old.id] == "" && !h.d.org.Ended[old.id] })
}
