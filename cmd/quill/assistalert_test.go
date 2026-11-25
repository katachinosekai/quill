package main

import (
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// t98af12: an approval waiting in a hidden assistant alerts while its box is
// closed or shows the other assistant: the button turns amber, a desktop
// notification fires (desktopNotify), clicking it opens the box on that
// assistant; answered, the state clears.
func TestAssistApprovalAlert(t *testing.T) {
	h := newHarness(t)
	testBoxView(h, boxKmem, "kmem assistant")
	var mu sync.Mutex
	var notes []string
	h.d.notifySink = func(sid, title, body string) {
		mu.Lock()
		notes = append(notes, sid+" "+title)
		mu.Unlock()
	}
	noted := func(part string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, s := range notes {
			if strings.Contains(s, part) {
				n++
			}
		}
		return n
	}
	alerting := func(role string) bool {
		bt := h.d.box().btns[role]
		h.d.syncAssistButtons()
		on := h.d.assistAlerting(role)
		amber := bt.btn.Importance == widget.WarningImportance && bt.side.Importance == widget.WarningImportance && bt.dot.Visible() && bt.dot.FillColor == colWaiting &&
			bt.bubble.box.Visible() && bt.bubble.dot.FillColor == colWaiting
		if on != amber {
			t.Fatalf("%s: alerting %v but the button shows %v (importance %v, dot %v, bubble %v)", role, on, amber,
				bt.btn.Importance, bt.dot.FillColor, bt.bubble.box.Visible())
		}
		return on
	}
	var q, k *deskSession
	h.ui(func() {
		d := h.d
		d.foreground = true
		q = d.addSession(h.proj, "", "default", false)
		d.markHiddenAs(q, roleQuill)
		a := d.quillAssist()
		a.s, a.state = q, asLive
		// The box shows the other assistant: the operator's prompt is not seen.
		d.openAssistBox(boxKmem)
		d.sendTo(q, "demo")
	})
	h.eventually("the operator's permission prompt", func() bool { return len(q.conv.PendingPermissions()) > 0 })
	h.ui(func() {
		d := h.d
		if noted(q.id+" Quill operator needs approval") != 1 {
			t.Fatalf("a desktop notification while the box shows kmem: %v", notes)
		}
		if !alerting(boxQuill) || alerting(boxKmem) {
			t.Fatal("the Quill button turns amber, the kmem one doesn't")
		}
		d.openAssistBox(boxQuill)
		if alerting(boxQuill) {
			t.Fatal("shown in the box: no alert state")
		}
		d.closeAssistBox()
		if !alerting(boxQuill) {
			t.Fatal("box closed: amber again until answered")
		}
		// The notification's click: the main window comes up with the box on
		// the operator.
		d.hidden = true
		d.notifyClicked(q.id)
		if d.hidden || d.assistBoxRole() != boxQuill {
			t.Fatalf("the click shows the window and the operator's box: hidden %v box %q", d.hidden, d.assistBoxRole())
		}
		d.closeAssistBox()
		d.answer(q, q.conv.PendingPermissions()[0], protocol.Allow)
	})
	h.eventually("the answered turn", func() bool { return !q.conv.Busy && !q.inTurn && len(q.conv.PendingPermissions()) == 0 })
	h.ui(func() {
		if alerting(boxQuill) || h.d.box().btns[boxQuill].bubble.box.Visible() {
			t.Fatal("answered: the alert and the badge clear")
		}
		// A finished turn asking for approval without a card (plan-first
		// off) waits too.
		h.d.sendTo(q, "Approve this plan?")
	})
	h.eventually("the approval question", func() bool { return !q.conv.Busy && !q.inTurn && q.conv.AsksApproval() })
	h.ui(func() {
		if !alerting(boxQuill) || noted(q.id+" Quill operator has a question") != 1 {
			t.Fatalf("an approval question alerts as one: %v", notes)
		}
		h.d.sendTo(q, "thanks")
	})
	h.eventually("the reply", func() bool { return !q.conv.Busy && !q.inTurn && !q.conv.AsksApproval() })

	// The kmem assistant: a plan card while the operator's box is open, with
	// desktop notifications off.
	h.ui(func() {
		d := h.d
		d.planFirst = true
		d.app.Preferences().SetBool("desktopNotify", false)
		k = d.addSession(h.proj, "", "default", false)
		d.markHidden(k)
		a := d.assist()
		a.s, a.state = k, asLive
		d.openAssistBox(boxQuill)
		d.sendTo(k, "demo")
	})
	h.eventually("the kmem assistant's plan card", func() bool {
		return !k.conv.Busy && !k.inTurn && len(k.conv.PendingPermissions()) > 0
	})
	h.ui(func() {
		if !alerting(boxKmem) || alerting(boxQuill) {
			t.Fatal("the kmem button turns amber while the box shows the operator")
		}
		if noted(k.id) != 0 {
			t.Fatalf("desktopNotify off: no notification: %v", notes)
		}
		h.d.sendTo(k, "not now")
	})
	h.eventually("the plan replaced", func() bool {
		return !k.conv.Busy && !k.inTurn && len(k.conv.PendingPermissions()) == 0
	})
	h.ui(func() {
		if alerting(boxKmem) {
			t.Fatal("the plan was answered: the kmem button clears")
		}
	})
}

func TestActionableAssistantRequestFiltersPlanHistory(t *testing.T) {
	c := chat.New()
	c.AddUser("Please check this.")
	reply := c.AddNotice("Can I run steps 1–2? Yes (recommended) or no?")
	reply.Kind = chat.Assistant
	plan := c.AddPlan()
	if request := actionableAssistRequest(c, false); !request.alert || request.kind != "plan" {
		t.Fatalf("the live plan should alert: %+v", request)
	}
	plan.PermState = chat.PermAllowed
	if request := actionableAssistRequest(c, false); request.kind != "" {
		t.Fatalf("an answered plan and its original question should clear: %+v", request)
	}

	stale := c.AddPlan()
	c.AddUser("Move on from that request.")
	if stale.PermState != chat.PermPending || actionableAssistRequest(c, false).kind != "" {
		t.Fatal("a pending card in history should not show an alert")
	}
	current := c.AddPlan()
	if request := actionableAssistRequest(c, false); !request.alert || request.kind != "plan" {
		t.Fatalf("a new current plan should alert: %+v", request)
	}
	current.PermState = chat.PermDenied
	if request := actionableAssistRequest(c, false); request.kind != "" {
		t.Fatalf("rejecting the current plan should clear it: %+v", request)
	}

	reply = c.AddNotice("Can I build the app now?")
	reply.Kind = chat.Assistant
	if request := actionableAssistRequest(c, false); !request.alert || request.kind != "question" {
		t.Fatalf("a current written approval question should alert: %+v", request)
	}
	if request := actionableAssistRequest(c, true); request.kind != "" {
		t.Fatalf("streaming turns should not alert for a written question yet: %+v", request)
	}
}
