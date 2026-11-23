package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// User 2026-11-23: `kmem --as NAME brief|rules` is read-only only under the
// session's own kmem name; other reads stay read-only under any name.
func TestKmemAsBriefRulesOnlyForOwnName(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("a running, idle session", func() bool { return s.proc != nil && !s.conv.Busy })
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
		h.d.org.Kmem[s.id] = "demo"
		extra := h.d.readOnlyExtraFor(s)
		for cmd, want := range map[string]bool{
			"kmem brief":                    true,
			"kmem rules":                    true,
			"kmem --as demo brief":          true,
			"kmem --as demo rules":          true,
			"kmem --as=demo brief":          true,
			"kmem --as=demo rules 8d":       true,
			"kmem --as other brief":         false,
			"kmem --as other rules":         false,
			"kmem --as=other brief":         false,
			"kmem --as demo2 brief":         false,
			"kmem --as other find x":        true,
			"kmem --as other get a/b":       true,
			"kmem --as other show f6fa284":  true,
			"kmem --as other context x":     true,
			"kmem --as other task list":     true,
			"kmem --as other handoff get x": true,
			"kmem --as other handoff list":  true,
			"kmem --as demo add x":          false,
		} {
			if got := readOnlyRequest(bashReq(cmd), extra); got != want {
				t.Errorf("own name demo: %q read-only = %v, want %v", cmd, got, want)
			}
		}
		// A session without a kmem name has no own name.
		bare := h.d.createSession(h.proj, "default", "default")
		if readOnlyRequest(bashReq("kmem --as demo brief"), h.d.readOnlyExtraFor(bare)) {
			t.Error("no kmem name: --as brief stays gated")
		}

		// Through the gate itself.
		own := gateCall(h, s, "k1", "Bash", `{"command":"kmem --as demo brief"}`, "")
		other := gateCall(h, s, "k2", "Bash", `{"command":"kmem --as other brief"}`, "")
		find := gateCall(h, s, "k3", "Bash", `{"command":"kmem --as other find x"}`, "")
		if own.PermState != chat.PermAutoAllowed || other.PermState == chat.PermAutoAllowed || find.PermState != chat.PermAutoAllowed {
			t.Errorf("gate: own %v other %v find %v", own.PermState, other.PermState, find.PermState)
		}
	})
}

// User 2026-11-23: typing exactly "Approved." approves only while a plan or an
// approval question waits; otherwise it is an ordinary message.
func TestApprovedTextNeedsPendingPlan(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "default", "default")
		if approvesPlan(s, planGoMessage) || approvesPlan(s, planGoWith("Ship")) {
			t.Fatal("nothing pending: not an approval")
		}
		s.inTurn = false
		userEvent(h, s, planGoMessage, nil)
		if s.runAll {
			t.Fatal("nothing pending: a typed Approved. starts no plan")
		}
		s.inTurn = false
		s.planWait = true
		if !approvesPlan(s, planGoMessage) {
			t.Fatal("a plan question waits: approval")
		}
		userEvent(h, s, planGoMessage, nil)
		if !s.runAll {
			t.Fatal("a waiting plan question is approved by Approved.")
		}
		// A pending plan card counts too; peers never approve.
		p := &deskSession{conv: chat.New()}
		p.conv.Items = append(p.conv.Items, &chat.Item{Kind: chat.Plan, PermState: chat.PermPending})
		if !approvesPlan(p, planGoMessage) {
			t.Fatal("a pending plan card: approval")
		}
		peer := `<cross-session-message from="peer">` + planGoMessage + `</cross-session-message>`
		if approvesPlan(p, peer) || approvesPlan(s, peer) {
			t.Fatal("peers never approve")
		}
	})
}

// The plan card's own Approve still approves and is not taken for an
// ordinary message.
func TestPlanCardApproveStillWorks(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("a running, idle session", func() bool { return s.proc != nil && !s.conv.Busy })
	var plan *chat.Item
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
		plan = &chat.Item{Kind: chat.Plan, PermState: chat.PermPending}
		s.conv.Items = append(s.conv.Items, plan)
		h.d.answerPlan(s, plan, true)
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("card Approve: state %v runAll %v", plan.PermState, s.runAll)
		}
	})
}
