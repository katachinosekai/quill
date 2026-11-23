package main

import (
	"strings"
	"testing"
	"time"
)

// fakeRole is a second assistant role, configured like the Quill operator will
// be in phase 2: its own kmem name, folder, badge items and seen records.
func fakeRole(t *testing.T, items *[]badgeItem) (*assistRole, *[]string) {
	dir := t.TempDir()
	var reasons []string
	b := newBubble()
	return &assistRole{
		id:          "fake",
		name:        "fake-op",
		label:       "fake assistant",
		hidden:      roleQuill,
		dir:         func() string { return dir },
		startedPref: "fakeStarted",
		startNote:   "You are the fake assistant.",
		stopMessage: func() string { return "stop and save fake-op" },
		bootOK:      func(d *desk) bool { return true },
		available:   func(d *desk) bool { return true },
		model:       func(d *desk) string { return d.kmemAssistModel() },
		unavailable: "fake is not available.",
		offReason:   "fake is off.",
		items:       func(d *desk) []badgeItem { return *items },
		seenPref:    "fakeSeen",
		bubble:      func(d *desk) *bubble { return b },
		reason: func(d *desk, a *assistant, reason, button string, tap func()) {
			reasons = append(reasons, reason)
		},
	}, &reasons
}

func TestAssistSecondRoleIndependent(t *testing.T) {
	h, k, _ := assistSetup(t)
	var items []badgeItem
	role, _ := fakeRole(t, &items)
	var fa *assistant
	h.ui(func() { fa = h.d.assistFor(role) })
	if fa == h.d.assist() || fa.role != role || h.d.assistFor(role) != fa {
		t.Fatal("one assistant per role, a different one from kmem's")
	}

	// Queue while it starts: the message waits, then reaches the session.
	h.ui(func() {
		h.d.assistBoot(fa)
		if got := h.d.assistDeliver(fa, "hello fake"); !strings.HasPrefix(got, "queued") {
			t.Fatalf("queued while probing: %q", got)
		}
	})
	var fs *deskSession
	h.eventually("the second role runs", func() bool {
		fs = fa.s
		return fs != nil && fa.state == asLive && fs.running() && !fs.conv.Busy
	})
	h.eventually("the queued message arrived", func() bool { return lastUser(fs) == "hello fake" })

	// kmem's role starts separately; neither touches the other.
	ks := openAssist(h, &kmemWindow{})
	h.ui(func() {
		ka := h.d.assist()
		if ks == fs || ka.s == fa.s || h.d.org.Kmem[fs.id] != "fake-op" || h.d.org.Kmem[ks.id] != kmemAssistName {
			t.Fatal("own session and own kmem name")
		}
		if !fs.hidden || !ks.hidden || runningHidden(h.d) != 2 {
			t.Fatalf("two hidden sessions: %d", runningHidden(h.d))
		}
		// The one-live guard is per name: a second start for either role adopts.
		h.d.assistBoot(fa)
		h.d.assistBoot(ka)
		if fa.s != fs || ka.s != ks {
			t.Fatal("never two under one name")
		}
		if h.d.assistOwner(fa) != fs || h.d.assistOwner(ka) != ks {
			t.Fatal("each name has its own live owner")
		}
	})

	// Badge: the second role's items badge its own bubble and seen records.
	h.ui(func() {
		items = []badgeItem{{Key: "issue:1", Sig: "a"}, {Key: "issue:2", Sig: "b"}}
		h.d.updateAssistBadge(fa)
		b := role.bubble(h.d)
		if !b.box.Visible() || b.text.Text != "2" || h.d.assistUnread(fa) != 2 {
			t.Fatalf("own badge: %q", b.text.Text)
		}
		if h.d.kmemBubble != nil && h.d.kmemBubble.box.Visible() {
			t.Fatal("kmem's bubble is not affected")
		}
		h.d.markAssistSeen(fa, true)
		if b.box.Visible() || h.d.assistUnread(fa) != 0 {
			t.Fatal("Mark all read clears the role's items")
		}
		if len(h.d.loadSeen("fakeSeen")) != 2 || len(h.d.loadKmemSeen()) != 0 {
			t.Fatal("seen records are per role")
		}
		items = []badgeItem{{Key: "issue:1", Sig: "a2"}}
		h.d.updateAssistBadge(fa)
		if h.d.assistUnread(fa) != 1 || len(h.d.loadSeen("fakeSeen")) != 1 {
			t.Fatal("a changed item is unread, a gone one's record is dropped")
		}
	})

	// Stopping the second role leaves kmem's running.
	h.ui(func() { h.d.assistSaveStop(fa, false, nil) })
	k.save("fake-op", time.Now().Add(time.Minute))
	h.eventually("the second role stopped", func() bool { return fa.state == asClosed && !fs.running() })
	h.ui(func() {
		if h.d.assist().state != asLive || !ks.running() || h.d.assistOf(ks) != h.d.assist() || h.d.assistOf(fs) != fa {
			t.Fatal("kmem's assistant keeps running")
		}
	})
}
