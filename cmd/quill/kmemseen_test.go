package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

func issue(id, title string) kmemTask {
	return kmemTask{ID: id, Status: "open", Title: title, Topic: "kmem"}
}

func TestBadgeItemsOnlyWhatNeedsTheUser(t *testing.T) {
	n := kmemNeeds{Bin: 3, Audit: "chain gap", UserTasks: []kmemTask{{ID: "u1", Status: "open", Topic: "user"}}}
	if got := badgeItems(n, "", ""); len(got) != 0 {
		t.Fatalf("bin, audit and user tasks do not badge: %+v", got)
	}
	n.RolloutWait = "stage a: failed"
	n.Maint, n.MaintSig = 2, "w:x"
	n.Issues = []kmemTask{issue("t1", "kmem issue: a"), {ID: "t2", Status: "claimed", Title: "kmem issue: b", Topic: "kmem"}}
	got := badgeItems(n, "plan", "7")
	keys := map[string]bool{}
	for _, it := range got {
		keys[it.Key] = true
	}
	for _, want := range []string{"assist:plan", "rollout", "maint", "issue:t1"} {
		if !keys[want] {
			t.Errorf("missing %s in %+v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("a claimed issue is being handled: %+v", got)
	}
}

func TestBadgeSeenAndReturns(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		n := kmemNeeds{RolloutWait: "stage a: failed", Maint: 2, MaintSig: "w:one\ntwo", Issues: []kmemTask{issue("t1", "kmem issue: a")}}
		d.setKmemNeeds(n)
		if d.kmemBubble.text.Text != "3" {
			t.Fatalf("three new items: %q", d.kmemBubble.text.Text)
		}
		d.markKmemSeen(false)
		if d.kmemBubble.box.Visible() {
			t.Fatal("seen items do not badge")
		}
		if rec := d.loadKmemSeen()["issue:t1"]; rec.At.IsZero() || rec.Sig != "kmem issue: a" {
			t.Fatalf("per-item seen time kept in the preferences: %+v", rec)
		}
		d.setKmemNeeds(n) // the same needs read again
		if d.kmemBubble.box.Visible() {
			t.Fatal("a refresh of seen items does not bring the badge back")
		}
		// a new maint report with the same warnings is not news; different ones are
		n2 := n
		n2.MaintReport = "/x/maint-20261124-010000.txt"
		d.setKmemNeeds(n2)
		if d.kmemBubble.box.Visible() {
			t.Fatal("same warnings in a new report")
		}
		n2.MaintSig = "w:one\nthree"
		d.setKmemNeeds(n2)
		if d.kmemBubble.text.Text != "1" {
			t.Fatalf("different warnings are new: %q", d.kmemBubble.text.Text)
		}
		d.markKmemSeen(false)
		// a seen item that changes, and a new item
		n2.Issues = []kmemTask{issue("t1", "kmem issue: a (updated)"), issue("t9", "kmem issue: new")}
		d.setKmemNeeds(n2)
		if d.kmemBubble.text.Text != "2" {
			t.Fatalf("changed and new issue: %q", d.kmemBubble.text.Text)
		}
		d.markKmemSeen(false)
		// an item that went away and comes back is new again
		n3 := n2
		n3.RolloutWait = ""
		d.setKmemNeeds(n3)
		if _, ok := d.loadKmemSeen()["rollout"]; ok {
			t.Fatal("the record of a gone item is dropped")
		}
		d.setKmemNeeds(n2)
		if d.kmemBubble.text.Text != "1" {
			t.Fatalf("a paused rollout again: %q", d.kmemBubble.text.Text)
		}
	})
}

func TestBadgeSettingAndMarkAllRead(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		if !d.kmemBadgeOn() {
			t.Fatal("the badge is on by default")
		}
		d.setKmemNeeds(kmemNeeds{RolloutWait: "stage a"})
		if d.kmemBubble.text.Text != "1" {
			t.Fatal("badge")
		}
		d.app.Preferences().SetBool(prefKmemBadge, false)
		d.updateKmemBadge()
		if d.kmemBubble.box.Visible() {
			t.Fatal("switch off: no badge")
		}
		d.app.Preferences().SetBool(prefKmemBadge, true)
		d.updateKmemBadge()
		d.markKmemSeen(true)
		if d.kmemBubble.box.Visible() || d.kmemUnread() != 0 {
			t.Fatal("Mark all read")
		}
	})
}

func TestBadgeQuestionStaysUntilAnswered(t *testing.T) {
	h, _, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.ui(func() {
		d := h.d
		a := d.assist()
		d.setKmemNeeds(kmemNeeds{})
		if d.kmemBubble.box.Visible() {
			t.Fatal("nothing waits")
		}
		plan := s.conv.AddPlan()
		d.kmemAssistRefresh(a)
		if d.kmemBubble.text.Text != "1" {
			t.Fatalf("a plan waiting for approval badges: %q", d.kmemBubble.text.Text)
		}
		d.markKmemSeen(false) // the window shows the chat: seen, still waiting
		if d.kmemBubble.text.Text != "1" {
			t.Fatal("a question stays badged until answered, not just seen")
		}
		plan.PermState = chat.PermAllowed
		d.kmemAssistRefresh(a)
		if d.kmemBubble.box.Visible() {
			t.Fatal("answered: the badge goes")
		}
		p2 := s.conv.AddPlan()
		d.kmemAssistRefresh(a)
		if d.kmemBubble.text.Text != "1" {
			t.Fatal("the next plan badges")
		}
		d.markKmemSeen(true)
		if d.kmemBubble.box.Visible() {
			t.Fatal("Mark all read dismisses the question")
		}
		_ = p2
		s.conv.AddPlan()
		d.kmemAssistRefresh(a)
		if d.kmemBubble.text.Text != "1" {
			t.Fatal("a different question badges again")
		}
	})
}

func TestMaintSigIgnoresReportTime(t *testing.T) {
	dir := t.TempDir()
	write := func(name, warnings string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("kmem maint\nWARNINGS:\n"+warnings+"\nNEXT:\n"), 0o644)
		return p
	}
	a := write("maint-20261122-100000.txt", "  big topic x\n  stale claim y")
	b := write("maint-20261123-100000.txt", "  big topic x\n  stale claim y")
	c := write("maint-20261124-100000.txt", "  big topic x\n  moved folder z")
	if maintSig(a, 2) != maintSig(b, 2) || maintSig(a, 2) == maintSig(c, 2) {
		t.Fatal("same warnings in a new report are the same; different ones differ")
	}
	if maintSig(a, 0) != "" {
		t.Fatal("no warnings, no signature")
	}
}
