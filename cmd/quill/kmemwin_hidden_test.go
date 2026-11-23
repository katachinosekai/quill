package main

import (
	"strings"
	"sync"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// Hidden-session tests for the kmem window (docs/design/kmem-window.md §4,
// §12). None of them runs the real kmem: the harness keeps kmem off.

func hiddenSession(h *harness) *deskSession {
	s := h.d.addSession(h.proj, "default", "default", false)
	h.d.markHidden(s)
	return s
}

func newWinView(h *harness) *chatView {
	return newChatViewWith(h.d, func() float32 { return 600 }, func() float32 { return 0 })
}

func TestKmemHiddenFilters(t *testing.T) {
	h := newHarness(t)
	var hid string
	h.ui(func() {
		h.d.createSession(h.proj, "default", "default")
		s := hiddenSession(h)
		hid = s.id
		if !h.d.org.Hidden[s.id] || !s.hidden {
			t.Fatal("markHidden flags the session and remembers its id")
		}
		h.d.addTab(s.id)
		for _, id := range h.d.tabs {
			if id == s.id {
				t.Fatal("the hidden session never gets a tab")
			}
		}
		h.d.open(s.id)
		if h.d.current == s {
			t.Fatal("opening the hidden session raises the kmem window, not a tab")
		}
		if h.d.liveSession(s.id) {
			t.Fatal("the hidden session is not live in the sidebar")
		}
		h.d.rebuildRows()
		for _, r := range h.d.rows {
			if r.id == s.id {
				t.Fatal("the hidden session is not in the sidebar rows")
			}
		}
		for _, g := range h.d.groups {
			for _, id := range g.ids {
				if id == s.id {
					t.Fatal("the hidden session is in no sidebar group")
				}
			}
		}
		a := &phoneAPI{d: h.d}
		ids, _ := a.sessionIDs()
		for _, id := range ids {
			if id == s.id {
				t.Fatal("the hidden session is not in the phone list")
			}
		}
		// A folder only the hidden session uses stays off the phone picker.
		s.cwd = kmemWindowDir()
		for _, f := range a.knownFolders() {
			if f["path"] == kmemWindowDir() {
				t.Fatal("the kmem window folder is not in knownFolders")
			}
		}
		if n := h.d.deleteEmptySessions(); h.d.sessions[s.id] == nil {
			t.Fatalf("deleteEmptySessions keeps the hidden session (deleted %d)", n)
		}
		// The cwd alone also hides an id (the transcript scan has no session).
		if !h.d.hiddenID("not-a-session", kmemWindowDir()) || h.d.hiddenID("not-a-session", h.proj) {
			t.Fatal("hiddenID: the folder hides, other folders don't")
		}
	})
	if (&phoneAPI{d: h.d}).preload(hid) {
		t.Fatal("the phone can't load the hidden session")
	}
}

func TestKmemHiddenSaveRestore(t *testing.T) {
	h := newHarness(t)
	var id string
	h.ui(func() {
		s := hiddenSession(h)
		id = s.id
		st := &restoreState{}
		h.d.saveSessions(st)
		if !st.Meta[id].Hidden {
			t.Fatal("saveSessions writes Hidden")
		}
		// A fresh process: the session and the org entry are gone.
		delete(h.d.sessions, id)
		delete(h.d.org.Hidden, id)
		m := st.Meta[id]
		m.Persisted = false
		st.Meta = map[string]sessMeta{id: m}
		h.d.restoreSessions(st)
		ns := h.d.sessions[id]
		if ns == nil || !ns.hidden || !h.d.org.Hidden[id] {
			t.Fatal("a restore keeps it hidden")
		}
	})
}

func TestKmemHiddenOwnerAndViews(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := hiddenSession(h)
		h.d.org.Kmem[s.id] = "kmem-lock-audit"
		if h.d.handoffOwner("kmem-lock-audit", &deskSession{}) != nil {
			t.Fatal("a stopped hidden session with the window closed is not live")
		}
		v := newWinView(h)
		h.d.kmemWin = &kmemWindow{s: s, view: v}
		defer func() { h.d.kmemWin = nil }()
		if h.d.handoffOwner("kmem-lock-audit", &deskSession{}) != s {
			t.Fatal("handoffOwner returns the window's session")
		}
		if got := h.d.ownerLabel(s); got != "live in the kmem window" {
			t.Fatalf("owner label %q", got)
		}
		found := false
		for _, x := range h.d.allViews() {
			found = found || x == v
		}
		if !found {
			t.Fatal("allViews includes the window view while open")
		}
	})
}

func TestKmemHiddenRouting(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var notes []string
	h.d.notifySink = func(sid, title, body string) {
		mu.Lock()
		notes = append(notes, title)
		mu.Unlock()
	}
	var s *deskSession
	var v *chatView
	h.ui(func() {
		h.d.autoRC = true
		visible := h.d.createSession(h.proj, "default", "default")
		s = hiddenSession(h)
		v = newWinView(h)
		v.show(s)
		h.d.kmemWin = &kmemWindow{s: s, view: v}
		h.d.current = visible
		h.d.chat.show(visible)
		h.d.notify(s, "hello window")
		if v.box == nil || len(s.conv.Items) == 0 {
			t.Fatal("notice not added")
		}
		if err := h.d.ensureProcess(s); err != nil {
			t.Fatal(err)
		}
		h.d.sendTo(s, "hello")
	})
	t.Cleanup(func() { h.ui(func() { h.d.kmemWin = nil }) })
	h.eventually("reply in the window session", func() bool {
		ok := false
		for _, it := range s.conv.Items {
			ok = ok || it.Kind == chat.Assistant && strings.Contains(it.Text, "hello")
		}
		return ok
	})
	h.ui(func() {
		if h.d.chat.s == s {
			t.Fatal("the hidden session never shows in the main chat")
		}
		if h.d.viewFor(s) != v {
			t.Fatal("viewFor routes to the window view")
		}
		if s.rcOn {
			t.Fatal("Remote Control is never started for the hidden session")
		}
		for _, r := range h.d.runningVisible() {
			if r == s {
				t.Fatal("runningVisible skips the hidden session")
			}
		}
		h.d.foreground = true
		h.d.hidden = true
		if !h.d.seen(s) {
			t.Fatal("the window's session is seen while the app is foreground")
		}
		h.d.foreground = false
		if h.d.seen(s) {
			t.Fatal("not seen in the background")
		}
		h.d.hidden = false
		h.d.alert(s, "finished", "done")
	})
	mu.Lock()
	defer mu.Unlock()
	if len(notes) == 0 || !strings.HasPrefix(notes[len(notes)-1], "kmem assistant") {
		t.Fatalf("desktop notice title: %v", notes)
	}
}

func TestKmemHiddenReplaceAndEnd(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		old := hiddenSession(h)
		v := newWinView(h)
		h.d.kmemWin = &kmemWindow{s: old, view: v}
		defer func() { h.d.kmemWin = nil }()
		tabs := len(h.d.tabs)
		ns := h.d.replaceWith(old, "kmem assistant")
		if !ns.hidden || !h.d.org.Hidden[ns.id] || h.d.kmemWin.s != ns || len(h.d.tabs) != tabs {
			t.Fatal("context rotation keeps the session hidden, swaps it into the window, adds no tab")
		}
		h.d.org.Kmem[ns.id] = "kmem-lock-audit"
		h.d.finishKmemEnd(ns, "kmem-lock-audit", "")
		if !h.d.org.Ended[ns.id] || h.d.org.Place[ns.id] != "" {
			t.Fatal("ending the hidden session marks it ended, with no placement")
		}
	})
}

func TestQuitMessage(t *testing.T) {
	if m := quitMessage(2, 0, 0); !strings.HasPrefix(m, "2 session(s)") || strings.Contains(m, "kmem") {
		t.Fatal(m)
	}
	if m := quitMessage(0, 1, 0); !strings.Contains(m, "kmem assistant") || strings.Contains(m, "session(s)") {
		t.Fatal(m)
	}
	if m := quitMessage(1, 1, 0); !strings.Contains(m, "1 session(s)") || !strings.Contains(m, "kmem assistant") {
		t.Fatal(m)
	}
}
