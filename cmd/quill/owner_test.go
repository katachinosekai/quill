package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestPlanApprovalGoesToCardOwner(t *testing.T) {
	h := newHarness(t)
	var a, b *deskSession
	var plan *chat.Item
	h.ui(func() {
		a = h.d.createSession(h.proj, "haiku", "default")
		b = h.d.createSession(h.proj, "haiku", "default")
		a.conv.AddUser("swap the server")
		plan = a.conv.AddPlan()
		h.d.open(b.id)
		h.d.answerPlan(b, plan, true)
	})
	count := func(s *deskSession) int {
		n := 0
		for _, it := range s.conv.Items {
			if it.Kind == chat.User && it.Text == planGoMessage {
				n++
			}
		}
		return n
	}
	h.ui(func() {
		if count(b) != 0 {
			t.Fatal("the focused session must never get another session's approval")
		}
		if count(a) != 1 || plan.PermState != chat.PermAllowed {
			t.Fatalf("the card's own session gets it: %d %v", count(a), plan.PermState)
		}
	})
	h.eventually("A replies", func() bool { return !a.conv.Busy && replied(a, plan) })
	h.ui(func() {
		h.d.answerPlan(a, plan, true)
		if count(a) != 1 {
			t.Fatal("no resend once the session has replied")
		}
	})

	h.ui(func() {
		c := h.d.createSession(h.proj, "haiku", "default")
		c.conv.AddUser("x")
		p2 := c.conv.AddPlan()
		c.conv = chat.New()
		h.d.answerPlan(b, p2, true)
		if count(b) != 0 || len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("a card whose session is gone shows an error and sends nothing")
		}
	})
}

func TestBlockedRetryGoesToCardOwner(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "haiku", "default")
		b := h.d.createSession(h.proj, "haiku", "default")
		blocked := &chat.Item{Seq: 900, Kind: chat.Tool, ToolName: "Bash", Result: "blocked"}
		a.conv.Items = append(a.conv.Items, blocked)
		h.d.open(b.id)
		h.d.retryBlocked(b, blocked)
		for _, it := range b.conv.Items {
			if it.Kind == chat.User {
				t.Fatal("retry went to the focused session")
			}
		}
		if it := a.conv.Items[len(a.conv.Items)-1]; it.Kind != chat.User {
			t.Fatal("retry goes to the card's session")
		}
	})
}

func TestPlanCardSurvivesOtherTurns(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "default")
		s.conv.AddUser("fix the tabs")
		plan := s.conv.AddPlan()
		s.conv.Apply(&protocol.Event{Type: "result", Subtype: "success"})
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		if plan.PermState != chat.PermPending {
			t.Fatal("a turn ending (peer message, background task) must not take the plan card away")
		}
		pending := 0
		for _, it := range s.conv.PendingPermissions() {
			if it.Kind == chat.Plan {
				pending++
			}
		}
		if pending != 1 {
			t.Fatalf("one plan card at a time: %d", pending)
		}
		h.d.sendTo(s, "actually do something else")
		if plan.PermState != chat.PermCancelled {
			t.Fatal("a new request replaces the plan")
		}
	})
}

func TestRetryRunsOnlyTheBlockedCommand(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "auto")
		s.runAll = true
		blocked := &chat.Item{Seq: 900, Kind: chat.Tool, ToolName: "Bash", Input: []byte(`{"command":"git push origin master"}`), Result: "blocked"}
		s.conv.Items = append(s.conv.Items, blocked)
		h.d.retryBlocked(s, blocked)
		if s.retryCmd != "git push origin master" || !s.runAll {
			t.Fatal("the retry message neither resets nor widens the approval")
		}
		s.runAll = false
		perm := &chat.Item{Kind: chat.Permission, ToolName: "Bash", RequestID: "r1", Input: []byte(`{"command":"git push origin master"}`), Request: &protocol.ControlRequest{Subtype: "can_use_tool"}}
		s.conv.Items = append(s.conv.Items, perm)
		h.d.planGate(s, &protocol.Event{Type: "control_request", RequestID: "r1", Request: perm.Request}, []*chat.Item{perm})
		if perm.PermState != chat.PermPending || s.retryCmd != "" {
			t.Fatal("the retried command skips plan-first once and waits for the normal Allow prompt")
		}
		if !approves(planGoMessage) || approves(retryPrefix+"rm -rf x") {
			t.Fatal("a retry approves only its own command, never the whole plan")
		}
	})
}
