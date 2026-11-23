package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/katachinosekai/quill/internal/session"
)

// Hidden-session roles (docs/design/quill-operator.md §5.2): kmem and quill
// share the hidden plumbing; storage stays readable by older builds.

func TestHiddenRoleOrgRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		k := h.d.addSession(h.proj, "default", "default", false)
		q := h.d.addSession(h.proj, "default", "default", false)
		h.d.markHidden(k)
		h.d.markHiddenAs(q, roleQuill)
		if hiddenRole(k) != roleKmem || hiddenRole(q) != roleQuill {
			t.Fatalf("roles %q %q", hiddenRole(k), hiddenRole(q))
		}
		h.d.saveOrg()
		b := h.d.app.Preferences().String("sidebarOrg")
		if strings.Contains(b, `"`+k.id+`":"kmem"`) {
			t.Fatalf("kmem keeps today's format: %s", b)
		}
		delete(h.d.sessions, k.id)
		delete(h.d.sessions, q.id)
		h.d.loadOrg()
		if r := h.d.hiddenRoleOf(k.id, ""); r != roleKmem {
			t.Fatalf("kmem after reload: %q", r)
		}
		if r := h.d.hiddenRoleOf(q.id, h.proj); r != roleQuill {
			t.Fatalf("quill after reload: %q (its folder is an ordinary one)", r)
		}
		if h.d.hiddenRoleOf("not-hidden", h.proj) != "" {
			t.Fatal("an unknown id is visible")
		}
	})
}

func TestHiddenRoleOldOrgFile(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetString("sidebarOrg", `{"place":{},"titles":{},"names":{},"folders":[],"hidden":{"old-id":true}}`)
		h.d.loadOrg()
		if !h.d.hiddenID("old-id", "") || h.d.hiddenRoleOf("old-id", "") != roleKmem {
			t.Fatal("an old hidden id reads as kmem")
		}
		// Re-marking it as kmem writes nothing new; as quill it gets a role.
		s := &deskSession{id: "old-id", cwd: h.proj}
		h.d.markHiddenAs(s, roleQuill)
		var o sideOrg
		_ = json.Unmarshal([]byte(h.d.app.Preferences().String("sidebarOrg")), &o)
		if !o.Hidden["old-id"] || o.HiddenRoles["old-id"] != roleQuill {
			t.Fatalf("saved org: %+v %+v", o.Hidden, o.HiddenRoles)
		}
		h.d.markHiddenAs(s, roleKmem)
		if _, ok := h.d.org.HiddenRoles["old-id"]; ok || hiddenRole(s) != roleKmem {
			t.Fatal("back to kmem drops the role entry")
		}
	})
}

func TestHiddenRoleRestoreAndReload(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		k := h.d.addSession(h.proj, "default", "default", false)
		q := h.d.addSession(h.proj, "default", "default", false)
		h.d.markHidden(k)
		h.d.markHiddenAs(q, roleQuill)
		st := &restoreState{}
		h.d.saveSessions(st)
		if m := st.Meta[k.id]; !m.Hidden || m.Role != "" {
			t.Fatalf("kmem meta %+v", m)
		}
		if m := st.Meta[q.id]; !m.Hidden || m.Role != roleQuill {
			t.Fatalf("quill meta %+v", m)
		}
		// A fresh process: sessions and org entries are gone.
		for _, id := range []string{k.id, q.id} {
			delete(h.d.sessions, id)
			delete(h.d.org.Hidden, id)
			delete(h.d.org.HiddenRoles, id)
			m := st.Meta[id]
			m.Persisted = false
			st.Meta[id] = m
		}
		h.d.restoreSessions(st)
		if hiddenRole(h.d.sessions[k.id]) != roleKmem || hiddenRole(h.d.sessions[q.id]) != roleQuill || h.d.hiddenRoleOf(q.id, "") != roleQuill {
			t.Fatal("a restore keeps each role")
		}
	})
	// Reload and keeper hand handedSession over as JSON; an older build's
	// entry has no role and stays kmem.
	for _, tc := range []struct{ js, role string }{
		{`{"id":"r1","hidden":true}`, roleKmem},
		{`{"id":"r2","hidden":true,"role":"quill"}`, roleQuill},
		{`{"id":"r3"}`, ""},
	} {
		var hs handedSession
		if err := json.Unmarshal([]byte(tc.js), &hs); err != nil {
			t.Fatal(err)
		}
		hs.Handle = session.Handoff{PID: 0x3ffffff0} // not running: Attach fails, the session restarts
		h.ui(func() {
			h.d.applyRestore(&restoreState{Handed: []handedSession{hs}})
			s := h.d.sessions[hs.ID]
			if hiddenRole(s) != tc.role {
				t.Fatalf("%s: role %q", tc.js, hiddenRole(s))
			}
			if got, role := handedRole(s); got != (tc.role != "") || role != strings.TrimPrefix(tc.role, roleKmem) {
				t.Fatalf("%s: handed %v %q", tc.js, got, role)
			}
		})
	}
}

func TestHiddenRoleViewsAndLabels(t *testing.T) {
	h := newHarness(t)
	var mu sync.Mutex
	var notes []string
	h.d.notifySink = func(sid, title, body string) {
		mu.Lock()
		notes = append(notes, title)
		mu.Unlock()
	}
	h.ui(func() {
		visible := h.d.createSession(h.proj, "default", "default")
		k := h.d.addSession(h.proj, "default", "default", false)
		q := h.d.addSession(h.proj, "default", "default", false)
		h.d.markHidden(k)
		h.d.markHiddenAs(q, roleQuill)
		h.d.org.Kmem[k.id] = "kmem-lock-audit"
		h.d.org.Kmem[q.id] = "quill"
		h.d.current = visible
		if got := h.d.ownerLabel(k); got != "live in the kmem window" {
			t.Fatalf("kmem owner label %q", got)
		}
		if got := h.d.ownerLabel(q); got != "live in the Quill operator" {
			t.Fatalf("quill owner label %q", got)
		}
		if h.d.viewFor(q) != nil || h.d.handoffOwner("quill", &deskSession{}) != nil {
			t.Fatal("no host: no view, a stopped session is not live")
		}
		kv, qv := newWinView(h), newWinView(h)
		h.d.kmemWin = &kmemWindow{s: k, view: kv}
		defer func() { h.d.kmemWin = nil; h.d.setHiddenHost(roleQuill, nil) }()
		shown, opened := false, 0
		h.d.setHiddenHost(roleQuill, &hiddenHost{
			session: func() *deskSession { return q },
			view:    func() *chatView { return qv },
			shown:   func() bool { return shown },
			open:    func() { opened++ },
		})
		if h.d.viewFor(k) != kv || h.d.viewFor(q) != qv {
			t.Fatal("viewFor routes each role to its host")
		}
		if h.d.handoffOwner("quill", &deskSession{}) != q || h.d.handoffOwner("kmem-lock-audit", &deskSession{}) != k {
			t.Fatal("handoffOwner counts each host's session")
		}
		found := false
		for _, v := range h.d.allViews() {
			found = found || v == qv
		}
		if !found {
			t.Fatal("allViews includes the quill host's view")
		}
		h.d.foreground = true
		if h.d.seen(q) {
			t.Fatal("not seen while its host is closed")
		}
		shown = true
		if !h.d.seen(q) || !h.d.seen(k) {
			t.Fatal("seen while each host shows it")
		}
		h.d.openHidden(q.id)
		if opened != 1 {
			t.Fatal("openHidden opens the role's host")
		}
		shown = false
		h.d.foreground = false
		h.d.alert(q, "finished", "done")
		h.d.alert(k, "finished", "done")
	})
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 2 || notes[0] != "Quill operator finished" || notes[1] != "kmem assistant finished" {
		t.Fatalf("desktop notice titles: %v", notes)
	}
}

func TestHiddenRolePhoneFilters(t *testing.T) {
	h := newHarness(t)
	var ids []string
	h.ui(func() {
		h.d.createSession(h.proj, "default", "default")
		k := h.d.addSession(h.proj, "default", "default", false)
		q := h.d.addSession(h.proj, "default", "default", false)
		h.d.markHidden(k)
		h.d.markHiddenAs(q, roleQuill)
		ids = []string{k.id, q.id}
		a := &phoneAPI{d: h.d}
		list, _ := a.sessionIDs()
		for _, id := range list {
			if id == k.id || id == q.id {
				t.Fatalf("hidden %s is in the phone list", id)
			}
		}
		for _, id := range ids {
			h.d.addTab(id)
			for _, t2 := range h.d.tabs {
				if t2 == id {
					t.Fatal("a hidden role never gets a tab")
				}
			}
		}
		// A transcript with no session: only the org marks the operator,
		// whose folder is an ordinary one.
		delete(h.d.sessions, q.id)
		if !h.d.hiddenID(q.id, h.proj) {
			t.Fatal("the org alone hides a quill id")
		}
	})
	for _, id := range ids {
		if (&phoneAPI{d: h.d}).preload(id) {
			t.Fatalf("the phone can't load hidden %s", id)
		}
	}
}
