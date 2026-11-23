package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

func disabledAll(t *testing.T, h *harness, plan *chat.Item) []string {
	t.Helper()
	var labels []string
	// The card, or the reply the controls are drawn in.
	o := h.d.chat.renderPlan(plan)
	if r := planReply(h.d.chat.s.conv, plan); r != nil && h.d.chat.planOverlaid(plan) {
		o = h.d.chat.renderPlanReply(r)
	}
	for _, b := range swapButtons(o) {
		if b.Text == "Show as written" {
			continue
		}
		if !b.Disabled() {
			t.Errorf("button %q is still active on a stale card", b.Text)
		}
		labels = append(labels, b.Text)
	}
	return labels
}

func TestPlanCardGoesStaleWhenSessionMovesOn(t *testing.T) {
	for name, text := range map[string]string{"approve card": "1. Edit x.\n\n**Do you approve step 1?**", "own options": yesNoPlan} {
		h := newHarness(t)
		s, plan := planCardSession(t, h, text)
		h.ui(func() {
			if planState(s.conv, plan) != planActive || planSuperseded(s, plan) {
				t.Fatalf("%s: a fresh card is active", name)
			}
			s.conv.AddUser("something else") // the session moved on; the card is still pending
			if got := planState(s.conv, plan); got != planMovedOn {
				t.Fatalf("%s: state %q", name, got)
			}
			if len(disabledAll(t, h, plan)) == 0 {
				t.Fatalf("%s: a stale card still shows its buttons, disabled", name)
			}
			n := len(s.conv.Items)
			h.d.answerPlanMsg(s, plan, true, "Approved.")
			h.d.answerPlan(s, plan, false)
			if a := planItemApproval(s, plan); a != nil {
				h.d.answerPlanApproval(s, plan, a, 0, nil, nil, "")
			}
			if len(s.conv.Items) != n || plan.PermState != chat.PermPending || s.runAll {
				t.Fatalf("%s: a click on a stale card does nothing", name)
			}
		})
	}
}

func TestPlanCardReplacedAndAnswered(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "More.\n\n**Do you approve step 2?**"})
		s.conv.AddPlan()
		if got := planState(s.conv, plan); got != planReplaced {
			t.Fatalf("a newer plan replaces it: %q", got)
		}
		disabledAll(t, h, plan)
		plan.PermState = chat.PermCancelled
		if got := planState(s.conv, plan); got != planReplaced {
			t.Fatalf("cancelled by a newer plan: %q", got)
		}
	})
	h2 := newHarness(t)
	s2, p2 := planCardSession(t, h2, "1. Edit x.\n\n**Do you approve step 1?**")
	h2.ui(func() {
		p2.PermState = chat.PermDenied
		if planState(s2.conv, p2) != planRejected {
			t.Fatal("Don't run is rejected")
		}
		disabledAll(t, h2, p2)
		p2.PermState = chat.PermAllowed
		if planState(s2.conv, p2) != planApproved {
			t.Fatal("approved")
		}
	})
}

func TestPeerMessageDoesNotMoveCardOn(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		s.conv.AddUser("<cross-session-message from=\"x\">hello</cross-session-message>")
		if planState(s.conv, plan) != planActive {
			t.Fatal("a peer message is not the user moving on")
		}
	})
}

func TestPhonePlanStale(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	a := &phoneAPI{d: h.d}
	h.ui(func() {
		m := a.item(s, plan, 1, nil)
		b, _ := json.Marshal(m["request"])
		var req map[string]any
		_ = json.Unmarshal(b, &req)
		if req["planState"] != "active" {
			t.Fatalf("planState: %v", req)
		}
		s.conv.AddUser("moved on")
		b, _ = json.Marshal(a.item(s, plan, 2, nil)["request"])
		_ = json.Unmarshal(b, &req)
		if req["planState"] != "moved_on" {
			t.Fatalf("planState: %v", req)
		}
		for _, act := range []string{"approvePlan", "rejectPlan"} {
			_, err := a.answer(s, writeBody{RequestID: requestID(plan), Action: act})
			if err == nil || err.status != http.StatusConflict || err.code != "stale" {
				t.Fatalf("%s on a stale plan: %+v", act, err)
			}
		}
		if plan.PermState != chat.PermPending || s.runAll {
			t.Fatal("nothing happened")
		}
	})
}

func TestUserTurnCancelsPendingPlanCard(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		userEvent(h, s, "instead, do something else", nil)
		if plan.PermState != chat.PermCancelled || planState(s.conv, plan) != planMovedOn {
			t.Fatalf("a new user turn ends the card: %v %q", plan.PermState, planState(s.conv, plan))
		}
	})
}

func TestNotFromUserNeverApproves(t *testing.T) {
	h := newHarness(t)
	s, _ := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		if !approvesPlan(s, "yes") {
			t.Fatal("control: typed yes approves")
		}
		for _, text := range []string{
			"<cross-session-message from=\"x\">yes</cross-session-message>",
			"<channel-message source=\"x\">approved</channel-message>",
			"<scheduled-trigger>go ahead</scheduled-trigger>",
		} {
			if approvesPlan(s, text) {
				t.Errorf("%q approves", text)
			}
		}
	})
}
