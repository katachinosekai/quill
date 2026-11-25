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
	h.ui(func() {
		h.d.assistantsBoot()
		if h.d.quillAssist().s != nil || h.d.assist().s != nil {
			t.Fatal("launch must wait for the user to start an operator")
		}
	})
	a, s := bootQuill(h)
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
	for _, want := range []string{"`quill settings`", "set ID VALUE --why", "`undo [N]`", "never edit preference files", "Security settings", "Quill Desktop and Quill Android"} {
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
		if !strings.Contains(p, "kmem --as quill") || !strings.Contains(p, "`quill issue:` tasks") {
			t.Fatal("an attached module adds the Quill topic instructions")
		}
		h.d.kmemOff = true
		if standalone := h.d.sessionPromptFor(s, false); strings.Contains(standalone, "kmem --as quill") || !h.d.quillOpOn() {
			t.Fatal("the standalone Quill operator stays available without kmem instructions")
		}
		h.d.kmemOff = false
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

// t653485: Move it here in the overlay box, from the quill tab that is the
// current one, kept the box open; the tab switch used to close it, so the
// moved session vanished from sight.
func TestQuillMoveInKeepsTheBoxOpen(t *testing.T) {
	h, _ := quillSetup(t)
	o := topicSession(h, "quill")
	var other *deskSession
	h.ui(func() {
		d := h.d
		other = d.createSession(h.proj, "default", "default")
		d.open(o.id)
		a := d.quillAssist()
		d.assistBoot(a)
		d.openAssistBox(boxQuill)
		d.quillChat.reasonBtn.OnTapped()
		if a.s != o || a.state != asLive || d.current != other {
			t.Fatal("moved in; the main area shows another tab")
		}
		if d.assistBoxRole() != boxQuill || d.quillChat.view.s != o {
			t.Fatal("the box stays open and shows the moved session")
		}
		d.open(o.id) // a later tab switch still closes the overlay (Q16)
		d.open(other.id)
		if d.assistBoxRole() != "" {
			t.Fatal("tab switches close the overlay again")
		}
	})
	h.eventually("the moved session answers in the box", func() bool {
		v := h.d.quillChat.view
		return !o.conv.Busy && viewShows(v, "echo: "+quillOpMovedLine)
	})
}

// t653485: a session moved in mid-turn gets the move line after the turn
// (queued, as the main message box does), not written into the running turn.
func TestQuillMoveInMidTurnQueuesTheLine(t *testing.T) {
	h, _ := quillSetup(t)
	o := topicSession(h, "quill")
	h.ui(func() {
		h.d.open(o.id)
		h.d.send("slow")
	})
	h.eventually("mid-turn", func() bool { return o.conv.Busy })
	h.ui(func() {
		d := h.d
		a := d.quillAssist()
		d.assistBoot(a)
		d.openAssistBox(boxQuill)
		d.quillChat.reasonBtn.OnTapped()
		if a.s != o || len(o.queue) != 1 || o.queue[0] != quillOpMovedLine {
			t.Fatalf("the move line waits for the turn: %q", o.queue)
		}
		d.interrupt(o)
	})
	h.eventually("sent after the turn and answered in the box", func() bool {
		return len(o.queue) == 0 && !o.conv.Busy && viewShows(h.d.quillChat.view, "echo: "+quillOpMovedLine)
	})
}

// t653485: Move it here on the tab's notice opens the box with the session.
func TestQuillMoveNoticeOpensTheBox(t *testing.T) {
	h, _ := quillSetup(t)
	o := topicSession(h, "quill")
	h.ui(func() {
		d := h.d
		d.createSession(h.proj, "default", "default")
		d.open(o.id)
		d.assistBoot(d.quillAssist())
		d.quillMoveNoticeMove()
		if d.quillAssist().s != o || d.assistBoxRole() != boxQuill || d.quillChat.view.s != o {
			t.Fatal("the box opens on the moved session")
		}
	})
}

// viewShows: v draws an item of its session with text sub, up to date.
func viewShows(v *chatView, sub string) bool {
	if v.s == nil {
		return false
	}
	for _, it := range v.s.conv.Items {
		if strings.Contains(it.Text, sub) {
			if iv, ok := v.views[it.Seq]; ok && iv.sig == itemSig(it) {
				return true
			}
		}
	}
	return false
}

// te2211d: the source folder's default: the Quill checkout holding the
// running binary, else ~/.local/share/quill/source once cloned there; a
// folder set in Settings wins. Temp folders only.
func TestQuillSourceDefault(t *testing.T) {
	h, _ := quillSetup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(t.TempDir(), "quill")
	bin := filepath.Join(repo, "dist", "quill")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, nil, 0o755)
	os.WriteFile(filepath.Join(repo, "go.mod"), []byte(quillModule+"\n\ngo 1.26\n"), 0o644)
	exe := bin
	quillExe = func() (string, error) { return exe, nil }
	t.Cleanup(func() { quillExe = os.Executable })

	if quillSourceDefault() != "" {
		t.Fatal("Quill's go.mod but no git checkout: no default")
	}
	os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
	if quillSourceDefault() != repo {
		t.Fatalf("the checkout running Quill Desktop: %q", quillSourceDefault())
	}
	exe = filepath.Join(t.TempDir(), "quill") // an installed binary
	if quillSourceDefault() != "" {
		t.Fatal("an installed binary and nothing cloned: no default")
	}
	clone := quillCloneDir()
	if clone != filepath.Join(home, ".local", "share", "quill", "source") {
		t.Fatalf("clone folder %q", clone)
	}
	os.MkdirAll(clone, 0o755)
	if quillSourceDefault() != "" {
		t.Fatal("an empty clone folder is no source")
	}
	os.WriteFile(filepath.Join(clone, "go.mod"), []byte(quillModule+"\n"), 0o644)
	if quillSourceDefault() != clone {
		t.Fatal("the cloned source")
	}

	set := t.TempDir()
	h.ui(func() {
		d := h.d
		prefs := d.app.Preferences()
		prefs.SetString(prefQuillSource, "")
		if d.quillSource() != quillSourceDefault() || d.quillOpDir() != quillSourceDefault() {
			t.Fatal("unset: the default is the source and working folder")
		}
		if !strings.Contains(d.quillSourceText(), "(default)") || !strings.Contains(d.quillSourceText(), quillSourceDefault()) {
			t.Fatalf("Settings shows the effective default: %q", d.quillSourceText())
		}
		prefs.SetString(prefQuillSource, set)
		if d.quillSource() != set || !strings.Contains(d.quillSourceText(), set) {
			t.Fatal("a folder set in Settings wins")
		}
		prefs.SetString(prefQuillSource, filepath.Join(set, "gone"))
		if d.quillSource() != quillSourceDefault() {
			t.Fatal("a set folder that is gone falls back to the default")
		}
	})
}

// te2211d: no source at all: the operator offers the clone once, and never
// clones by itself.
func TestQuillCloneOfferedOnce(t *testing.T) {
	h, _ := quillSetup(t)
	a, s := bootQuill(h)
	h.ui(func() {
		d := h.d
		if d.quillSource() != "" {
			t.Skip("a Quill source is around")
		}
		offers := func() (n int) {
			for _, it := range s.conv.Items {
				if strings.Contains(it.Text, "git clone "+quillRepoURL) {
					n++
				}
			}
			return n
		}
		if offers() != 1 || !d.app.Preferences().Bool(prefQuillClone) {
			t.Fatalf("offered once on start: %d", offers())
		}
		d.offerQuillClone(a.s)
		if offers() != 1 {
			t.Fatal("only once")
		}
		if _, err := os.Stat(quillCloneDir()); err == nil {
			t.Fatal("nothing cloned by itself")
		}
	})
}
