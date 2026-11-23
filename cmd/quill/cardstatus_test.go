package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

func statusReply(h *harness, s *deskSession, n int, text string, wait bool) {
	userEvent(h, s, notification("b"+text, "toolu_1"), nil)
	s.planWait = wait
	s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9500 + n, Kind: chat.Assistant, Text: text})
}

// Plain status replies (no question) never supersede an unanswered card, even
// when a blocked call in the turn asked for a plan card.
func TestPlanCardStaysAfterStatusReplies(t *testing.T) {
	for _, wait := range []bool{false, true} {
		h := newHarness(t)
		s, plan := planCardSession(t, h, pendingQs)
		h.ui(func() {
			for i, txt := range []string{"Noted, build 1 done.", "Build 2 done.", "Still waiting on your answers."} {
				statusReply(h, s, i, txt, wait)
				endTurn(t, h, s)
			}
			if plansIn(s) != 1 || planState(s.conv, plan) != planActive || planSuperseded(s, plan) {
				t.Fatalf("wait=%v: card stays live: plans %d, state %q", wait, plansIn(s), planState(s.conv, plan))
			}
		})
	}
}

// A newer approval question supersedes the card.
func TestPlanCardSupersededByNewQuestion(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, pendingQs)
	h.ui(func() {
		statusReply(h, s, 0, "Build done.", false)
		endTurn(t, h, s)
		statusReply(h, s, 1, "New work found.\n\n**Can I run the deploy?**", false)
		endTurn(t, h, s)
		if plansIn(s) != 2 || planState(s.conv, plan) != planReplaced {
			t.Fatalf("plans %d, state %q", plansIn(s), planState(s.conv, plan))
		}
	})
}

// The phone's open question (and the desktop choice buttons' liveness) follow
// the same rule: status replies keep it, a newer question or the user's reply
// moves it.
func TestOpenChoiceFollowsQuestions(t *testing.T) {
	c := chat.New()
	q := &chat.Item{Seq: 1, Kind: chat.Assistant, Text: "Done.\n\nRed (recommended) or Blue?"}
	c.Items = append(c.Items, q)
	for i, txt := range []string{"Build 1 done.", "Build 2 done.", "Still waiting on you."} {
		c.Items = append(c.Items, &chat.Item{Seq: 2 + i, Kind: chat.Assistant, Text: txt})
		if openChoiceItem(c) != q {
			t.Fatalf("status reply %d superseded the question", i)
		}
	}
	n := &chat.Item{Seq: 9, Kind: chat.Assistant, Text: "Next.\n\nGreen or Grey?"}
	c.Items = append(c.Items, n)
	if openChoiceItem(c) != n {
		t.Fatal("a newer question is the open one")
	}
	c.Items = append(c.Items, &chat.Item{Seq: 10, Kind: chat.User, Text: "Green"})
	if openChoiceItem(c) != nil {
		t.Fatal("the user's reply closes it")
	}
}
