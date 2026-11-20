package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
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
