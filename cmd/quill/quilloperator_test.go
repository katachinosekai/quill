package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// quillSetup: a harness with kmem memory on and a kmem that has the topic
// briefing and the end-of-session check; nothing started yet.
func quillSetup(t *testing.T) (*harness, *ecKmem) {
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	k := endCheckKmem(t, h, true)
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem) })
	return h, k
}

// bootQuill starts the operator as at launch and waits for it to run idle.
func bootQuill(h *harness) (*assistant, *deskSession) {
	h.t.Helper()
	var a *assistant
	h.ui(func() {
		a = h.d.quillAssist()
		h.d.assistBoot(a)
	})
	var s *deskSession
	h.eventually("the Quill operator runs", func() bool {
		s = a.s
		return s != nil && a.state == asLive && s.running() && !s.conv.Busy && !s.inTurn
	})
	return a, s
}

func TestQuillOperatorStartsHidden(t *testing.T) {
	h, _ := quillSetup(t)
	h.ui(func() {
		if !h.d.quillOpOn() || h.d.quillAssist().role != quillRole || quillRole.name != "quill" || quillRole.label != "Quill operator" {
			t.Fatal("the quill role is on with kmem memory")
		}
	})
	tabs := len(h.d.tabs)
	h.ui(h.d.assistantsBoot) // as at launch (desk.go watch)
	a, s := bootQuill(h)     // adopts what the launch started
	h.ui(func() {
		if hiddenRole(s) != roleQuill || !h.d.org.Hidden[s.id] || h.d.org.HiddenRoles[s.id] != roleQuill {
			t.Fatal("a hidden session of role quill")
		}
		if contains(h.d.tabs, s.id) || len(h.d.tabs) != tabs || h.d.current == s {
			t.Fatal("no tab, never current")
		}
		if h.d.org.Kmem[s.id] != "quill" || s.cwd != quillOpHomeDir() {
			t.Fatalf("kmem name quill, its own folder without a source folder: %q %q", h.d.org.Kmem[s.id], s.cwd)
		}
		first := ""
		for _, it := range s.conv.Items {
			if it.Text != "" && strings.Contains(it.Text, "coordinator") {
				first = it.Text
				break
			}
		}
		if !strings.Contains(first, "You are the Quill coordinator") || !strings.Contains(first, "kmem context --topic quill") || !strings.Contains(first, quillOpStartNote) {
			t.Fatalf("fresh from the topic briefing: %q", first)
		}
		if strings.Contains(first, "kmem handoff get") {
			t.Fatal("no handoff resume for a topic session")
		}
		for _, r := range h.d.runningVisible() {
			if r == s {
				t.Fatal("not a visible running session")
			}
		}
		if h.d.assistOf(s) != a || h.d.assistOf(s) == h.d.assist() {
			t.Fatal("its own assistant, not kmem's")
		}
	})
}

func TestQuillOperatorHintNeedsSourceFolder(t *testing.T) {
	if n := len([]rune(quillOperatorHintFor("/home/user/Projects/quill"))); n > 1000 {
		t.Fatalf("operator hint too long: %d", n)
	}
	for _, want := range []string{"kmem --as quill", "`quill settings`", "set ID VALUE --why", "`undo [N]`", "never edit preference files", "Security settings", "`quill issue:` tasks (topic quill)", "Quill Desktop and Quill Android"} {
		if !strings.Contains(quillOperatorHint, want) {
			t.Fatalf("hint names %q", want)
		}
	}
	h, _ := quillSetup(t)
	src := t.TempDir()
	h.ui(func() {
		s := &deskSession{id: "op"}
		s.hidden, s.role = true, roleQuill
		p := h.d.sessionPromptFor(s, true)
		if !strings.Contains(p, quillOperatorHint) || !strings.Contains(p, quillNoSourceHint) || strings.Contains(p, "Developer:") {
			t.Fatal("settings only without a source folder")
		}
		if strings.Contains(p, topicCoordinatorHint("quill")) || strings.Contains(p, kmemAssistantHint) {
			t.Fatal("no coordinator hint and never the kmem assistant's")
		}
		if h.d.quillOpDir() != quillOpHomeDir() {
			t.Fatal("own folder without a source folder")
		}
		h.d.app.Preferences().SetString(prefQuillSource, filepath.Join(src, "gone"))
		if h.d.quillSource() != "" || strings.Contains(h.d.sessionPromptFor(s, true), "Developer:") {
			t.Fatal("a missing folder counts as unset")
		}
		h.d.app.Preferences().SetString(prefQuillSource, src)
		p = h.d.sessionPromptFor(s, true)
		if !strings.Contains(p, "Developer:") || !strings.Contains(p, src) || !strings.Contains(p, topicCoordinatorHint("quill")) || strings.Contains(p, quillNoSourceHint) {
			t.Fatal("developer role and the coordinator hint with a source folder")
		}
		if h.d.quillOpDir() != src {
			t.Fatal("the source folder is the working folder")
		}
	})
}

func TestQuillSourceSuggestion(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cmd", "quill")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if quillRepoAbove(sub) != "" {
		t.Fatal("no go.mod: no suggestion")
	}
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/other\n"), 0o644)
	if quillRepoAbove(sub) != "" {
		t.Fatal("another module is not the Quill source")
	}
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte(quillModule+"\n\ngo 1.24\n"), 0o644)
	if quillRepoAbove(sub) != root {
		t.Fatal("the folder above with Quill's go.mod")
	}
	h, _ := quillSetup(t)
	h.ui(func() {
		s := h.d.createSession(sub, "default", "default")
		h.d.org.Kmem[s.id] = "quill"
		if h.d.suggestQuillSource() != root {
			t.Fatalf("suggested from the quill session's folder: %q", h.d.suggestQuillSource())
		}
		if h.d.app.Preferences().String(prefQuillSource) != "" {
			t.Fatal("only suggested, never set")
		}
	})
}

func TestQuillOperatorMoveInKeepsSession(t *testing.T) {
	h, _ := quillSetup(t)
	o := topicSession(h, "quill")
	h.ui(func() {
		h.d.setAutoApprove(o, true)
		if !o.autoApprove || !contains(h.d.tabs, o.id) {
			t.Fatal("setup: a running quill tab with auto-approve")
		}
	})
	var a *assistant
	h.ui(func() {
		a = h.d.quillAssist()
		h.d.assistBoot(a)
		if a.state != asElsewhere || !strings.Contains(a.reason, "The Quill operator is open in the tab") || a.button != "Move it here" {
			t.Fatalf("elsewhere: %v %q %q", a.state, a.reason, a.button)
		}
	})
	var pid int
	h.ui(func() {
		proc, id := o.proc, o.id
		pid = proc.PID()
		h.d.quillMoveIn(a, o)
		if a.s != o || a.state != asLive || o.proc != proc || o.id != id || !o.running() || o.stopping {
			t.Fatal("the same running session and process, now the operator's")
		}
		if hiddenRole(o) != roleQuill || contains(h.d.tabs, o.id) || h.d.current == o || h.d.org.Kmem[o.id] != "quill" {
			t.Fatal("hidden, no tab, still kmem name quill")
		}
		if o.autoApprove {
			t.Fatal("moving the tab ends its auto-approve")
		}
		if !hasNotice(o, "keeps running here") {
			t.Fatal("the box notice")
		}
		if h.d.handoffOwner("quill", &deskSession{}) != o {
			t.Fatal("still the one live quill session")
		}
	})
	h.eventually("the operator line arrived", func() bool { return lastUser(o) == quillOpMovedLine })
	h.ui(func() {
		if o.proc.PID() != pid || runningHidden(h.d) != 1 {
			t.Fatal("never restarted, never doubled")
		}
	})
}

func TestQuillTopicOpensOperator(t *testing.T) {
	h, _ := quillSetup(t)
	opened := 0
	h.ui(func() { h.d.setHiddenHost(roleQuill, &hiddenHost{open: func() { opened++ }}) })
	t.Cleanup(func() { h.ui(func() { h.d.setHiddenHost(roleQuill, nil) }) })
	tabs := len(h.d.tabs)
	h.ui(func() {
		if s := h.d.openTopic("quill"); s != nil && !isHiddenSession(s) {
			t.Fatal("no tab for topic quill")
		}
		if opened != 1 || len(h.d.tabs) != tabs {
			t.Fatal("the operator's box opens, no tab")
		}
	})
	a, s := bootQuill(h)
	h.ui(func() {
		if got := h.d.openTopic("quill"); got != s || len(h.d.tabs) != tabs || opened != 2 {
			t.Fatal("a live operator is brought forward")
		}
		// The "Open a topic" card: the pending tab is not linked to quill.
		c := h.d.createSession(h.proj, "default", "default")
		c.pickPending = true
		h.d.pickTopic(c, quillTopic(), false)
		if h.d.org.Kmem[c.id] != "" || opened != 3 || a.s != s {
			t.Fatal("the card opens the operator, never a second quill session")
		}
		// Other topics still open tabs.
		if ns := h.d.openTopic("alpha"); ns == nil || isHiddenSession(ns) || !contains(h.d.tabs, ns.id) {
			t.Fatal("other topics keep their tabs")
		}
	})
}

func TestQuillOperatorOneLive(t *testing.T) {
	h, _ := quillSetup(t)
	a, s := bootQuill(h)
	h.ui(func() {
		if h.d.liveKmemOwner("quill", &deskSession{}) != s || h.d.topicOwner("quill", &deskSession{}) != s {
			t.Fatal("the operator is the live quill session")
		}
		if h.d.ownerLabel(s) != "live in the Quill operator" {
			t.Fatalf("owner label: %q", h.d.ownerLabel(s))
		}
		h.d.assistBoot(a)
		h.d.assistantsBoot()
		h.d.openTopic("quill")
		n := 0
		for id, k := range h.d.org.Kmem {
			if k == "quill" && h.d.sessions[id] != nil && h.d.sessions[id].running() {
				n++
			}
		}
		if n != 1 || a.s != s {
			t.Fatalf("one live quill session: %d", n)
		}
	})
}

func TestQuillOperatorBadge(t *testing.T) {
	tasks := []kmemTask{
		{ID: "t1", Status: "open", Title: "quill issue: the chip overlaps", Topic: "quill"},
		{ID: "t2", Status: "claimed", Title: "Quill issue: slow start", Topic: "quill"},
		{ID: "t3", Status: "open", Title: "kmem issue: lock", Topic: "kmem"},
		{ID: "t4", Status: "open", Title: "quill issue: wrong topic", Topic: "zomboid"},
		{ID: "t5", Status: "done", Title: "quill issue: old", Topic: "quill"},
		{ID: "t6", Status: "open", Title: "Ship the box", Topic: "quill"},
	}
	n := parseKmemNeeds("", tasks)
	if len(n.QuillIssues) != 2 || len(n.Issues) != 1 || n.Count() != 1 {
		t.Fatalf("quill issue: tasks of topic quill, not on kmem's count: %+v", n.QuillIssues)
	}
	items := quillIssueItems(n.QuillIssues)
	if len(items) != 1 || items[0].Key != "issue:t1" {
		t.Fatalf("open ones badge: %+v", items)
	}
	h, _ := quillSetup(t)
	b := newBubble()
	old := quillOpBubble
	quillOpBubble = func(*desk) *bubble { return b }
	t.Cleanup(func() { quillOpBubble = old })
	h.ui(func() {
		a := h.d.quillAssist()
		h.d.setKmemNeeds(n)
		if got := h.d.assistNeeds(a); len(got) != 1 || got[0].Key != "issue:t1" {
			t.Fatalf("needs: %+v", got)
		}
		if !b.box.Visible() || b.text.Text != "1" {
			t.Fatal("the Quill bubble counts the issue")
		}
		h.d.markAssistSeen(a, true)
		if b.box.Visible() || h.d.assistUnread(h.d.assist()) != 1 { // kmem's own issue t3
			t.Fatal("seen; kmem's badge unaffected")
		}
	})
	// A plan waiting in the operator badges too (assistWait).
	_, s := bootQuill(h)
	h.ui(func() {
		s.conv.Busy = false
		if k, _ := h.d.assistWait(h.d.quillAssist()); k != "" {
			t.Fatalf("nothing waits yet: %q", k)
		}
	})
}

func TestQuillOperatorStopRunsEndCheck(t *testing.T) {
	h, k := quillSetup(t)
	k.set("quill", ecOK)
	a, s := bootQuill(h)
	h.ui(func() {
		if !h.d.endCheckFor(s) {
			t.Fatal("the operator ends through the end-of-session check")
		}
		if qs := h.d.quitCheckSessions(); len(qs) != 1 || qs[0] != s {
			t.Fatal("Quit checks the operator with the topic tabs")
		}
		h.d.assistSaveStop(a, false, nil)
		if s.kmemSave != nil {
			t.Fatal("no handoff save")
		}
	})
	h.eventually("stopped after the check", func() bool { return a.state == asClosed && !s.running() })
	if !k.called("kmem --as quill end-check --session " + s.id) {
		t.Fatal("ran end-check for quill")
	}
	h.ui(func() {
		if !hasNotice(s, "End-of-session check passed") || !h.d.org.Ended[s.id] {
			t.Fatal("stopped and marked ended")
		}
	})
}

func TestQuillOperatorRotationStaysHidden(t *testing.T) {
	h, k := quillSetup(t)
	k.set("quill", ecOK)
	a, old := bootQuill(h)
	h.ui(func() { h.d.rotateTopic(old.id) })
	h.eventually("a fresh operator session", func() bool {
		return a.s != old && a.s != nil && a.s.running() && !old.running()
	})
	h.ui(func() {
		ns := a.s
		if hiddenRole(ns) != roleQuill || contains(h.d.tabs, ns.id) || h.d.org.Kmem[ns.id] != "quill" {
			t.Fatal("rotation keeps it hidden behind the Quill button")
		}
	})
}

func TestQuillMoveNoticeOnceMoveAndLater(t *testing.T) {
	h, _ := quillSetup(t)
	o := topicSession(h, "quill")
	h.ui(func() {
		d := h.d
		d.open(o.id)
		a := d.quillAssist()
		d.assistBoot(a)
		n := d.quillMove
		if !n.box.Visible() || !d.app.Preferences().Bool(prefQuillMoveSeen) {
			t.Fatal("shown on the running quill tab, remembered in prefs")
		}
		if a.state != asElsewhere || hiddenRole(o) == roleQuill || !contains(d.tabs, o.id) {
			t.Fatal("never moves by itself")
		}
		n.box.Hide() // a later start: no second notice
		n.owner = nil
		d.assistProbe(a)
		if n.box.Visible() || n.owner != nil {
			t.Fatal("shown once only")
		}
		if a.state != asElsewhere || a.button != "Move it here" {
			t.Fatal("the box keeps offering")
		}
		d.app.Preferences().SetBool(prefQuillMoveSeen, false)
		n.shown = false
		d.assistProbe(a)
		if !n.box.Visible() {
			t.Fatal("armed again for the test")
		}
		d.quillMoveNoticeLater()
		if n.box.Visible() || hiddenRole(o) == roleQuill || a.state != asElsewhere || a.button != "Move it here" {
			t.Fatal("Later dismisses; nothing moved; the box keeps offering")
		}
		d.app.Preferences().SetBool(prefQuillMoveSeen, false)
		d.assistProbe(a)
		d.setAutoApprove(o, true)
		d.quillMoveNoticeMove()
		if a.s != o || a.state != asLive || hiddenRole(o) != roleQuill || o.autoApprove || n.box.Visible() {
			t.Fatal("Move it here calls the move-in: same session, auto-approve ended")
		}
	})
}
