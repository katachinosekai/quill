package main

import (
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

const pendingQs = "1. Edit x.\n2. Build.\n\n1. Which name? Red (recommended) or Blue?\n2. Keep the old file? Yes (recommended) or no?\n\nCan I run steps 1–2?"

func plansIn(s *deskSession) (n int) {
	for _, it := range s.conv.Items {
		if it.Kind == chat.Plan {
			n++
		}
	}
	return
}

// A turn a task notification starts, ending with the same questions, keeps
// the unanswered card (no "replaced"); picks stay.
func TestPendingCardKeptOnSameQuestions(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, pendingQs)
	h.ui(func() {
		qs := planItemQuestions(s, plan)
		if len(qs) == 0 {
			t.Fatal("the plan has questions")
		}
		h.d.chat.planAnswers(plan, qs)[1] = planAnswer{Choice: 1}
		userEvent(h, s, notification("b1", "toolu_1"), nil)
		again := strings.ReplaceAll(pendingQs, "Which name?", "**Which  name?**")
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9500, Kind: chat.Assistant, Text: "Still waiting.\n\n" + again})
		endTurn(t, h, s)
		if plansIn(s) != 1 || planState(s.conv, plan) != planActive || planSuperseded(s, plan) {
			t.Fatalf("the card stays active: plans %d, state %q", plansIn(s), planState(s.conv, plan))
		}
		if a := h.d.chat.planAnswers(plan, qs)[1]; a.Choice != 1 {
			t.Fatalf("pick kept: %+v", a)
		}
		for _, b := range swapButtons(h.d.chat.renderPlan(plan)) {
			if b.Disabled() {
				t.Errorf("button %q disabled on the kept card", b.Text)
			}
		}
	})
}

func TestPendingCardReplacedOnDifferentQuestions(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, pendingQs)
	h.ui(func() {
		userEvent(h, s, notification("b1", "toolu_1"), nil)
		other := strings.ReplaceAll(pendingQs, "Red (recommended) or Blue?", "Red (recommended) or Green?")
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9500, Kind: chat.Assistant, Text: other})
		endTurn(t, h, s)
		if plansIn(s) != 2 || planState(s.conv, plan) != planReplaced {
			t.Fatalf("different questions replace: plans %d, state %q", plansIn(s), planState(s.conv, plan))
		}
	})
}

func TestPlanFirstHintNoAssumedAnswer(t *testing.T) {
	for _, w := range []string{"reply to task notifications with one status line", "don't repeat the question", "ask again later", "never assume the recommended option"} {
		if !strings.Contains(planApprovalHint, w) {
			t.Errorf("hint lacks %q", w)
		}
	}
}
