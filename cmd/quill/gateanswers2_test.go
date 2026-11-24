package main

import (
	"net/http"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// td0cabd (user 2026-11-24): the plan-first gate's answers.

// pendingDialog adds a pending AskUserQuestion request to s.
func pendingDialog(s *deskSession, id string, q map[string]any) *chat.Item {
	in := dialogInput(nil, q)
	it := &chat.Item{Kind: chat.Permission, ToolName: "AskUserQuestion", RequestID: id, Input: in, PermState: chat.PermPending,
		Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: "AskUserQuestion", Input: in, ToolUseID: "toolu_" + id}}
	s.conv.Items = append(s.conv.Items, it)
	return it
}

// (a)+(b): the user's go-ahead in the dialog, answered from the phone,
// approves the plan and answers the pending plan card: its buttons go
// inactive and the phone gets 409 stale for it and for the answered dialog.
func TestDialogGoAheadApprovesAndSettlesCard(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	a := &phoneAPI{d: h.d, tracks: map[string]*itemTrack{}, lastSess: map[string]string{}, lastCtl: map[string]bool{}, wake: make(chan struct{})}
	h.ui(func() {
		s.inTurn = true // the dialog is answered inside a turn (a task notification started it)
		dlg := pendingDialog(s, "q1", dq("Fix 2 and 3?", false, "Yes (recommended)", "No"))
		if _, err := a.answer(s, writeBody{RequestID: "q1", Action: "answer", Answers: map[string][]string{"Fix 2 and 3?": {"Yes (recommended)"}}}); err != nil {
			t.Fatalf("phone answer: %+v", err)
		}
		if dlg.PermState != chat.PermAllowed || !s.runAll || !s.turnCovered {
			t.Fatalf("the dialog's go-ahead approves: dialog %v runAll %v", dlg.PermState, s.runAll)
		}
		if plan.PermState != chat.PermAllowed || planState(s.conv, plan) != planApproved {
			t.Fatalf("the plan card is answered: %v %q", plan.PermState, planState(s.conv, plan))
		}
		disabledAll(t, h, plan)
		if _, err := a.answer(s, writeBody{RequestID: "q1", Action: "answer", Answers: map[string][]string{"Fix 2 and 3?": {"No"}}}); err == nil || err.status != http.StatusConflict || err.code != "stale" {
			t.Fatalf("a second dialog answer is stale: %+v", err)
		}
		if _, err := a.answer(s, writeBody{RequestID: requestID(plan), Action: "rejectPlan"}); err == nil || err.status != http.StatusConflict || err.code != "stale" {
			t.Fatalf("the answered card is stale on the phone: %+v", err)
		}
		if !s.runAll {
			t.Fatal("the stale answers changed nothing")
		}
	})
}

// (a)+(b): the dialog's stop option ("Don't run") is "Not approved.": the
// plan is revoked and the pending card answered (rejected).
func TestDialogStopRejectsAndSettlesCard(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		s.runAll = true
		dlg := pendingDialog(s, "q2", dq("Can I run the release?", false, "Run it", "Don't run"))
		h.d.answerQuestions(s, dlg, map[string]string{"Can I run the release?": "Don't run"})
		if s.runAll || !s.planRevoked {
			t.Fatal("Don't run in the dialog revokes the plan")
		}
		if plan.PermState != chat.PermDenied || planState(s.conv, plan) != planRejected {
			t.Fatalf("the card is answered: %v %q", plan.PermState, planState(s.conv, plan))
		}
		disabledAll(t, h, plan)
		// A click on the answered card does nothing.
		if ap := planItemApproval(s, plan); ap != nil {
			h.d.answerPlanApproval(s, plan, ap, 0, nil, nil, "")
		}
		h.d.answerPlan(s, plan, false)
		if s.runAll || plan.PermState != chat.PermDenied {
			t.Fatal("a stale card click changes nothing")
		}
	})
}

// (c): a closing yes/no question that proposes an action is an approval
// question: it gets the card with its own Yes and No, and no copy of itself
// as a question under them.
func TestClosingYesNoProposalGetsCard(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		for _, blocked := range []bool{false, true} {
			for _, text := range []string{
				"1. Edit x.\n2. Build.\n\n**Fix both and commit? Yes (recommended) or no?**",
				"1. Edit x.\n\n**Fix both and commit? Yes (recommended) / no?**",
				"1. Edit x.\n\n**Fix both and commit? (yes/no)**",
				"1. Edit x.\n\n**Make the change and rerun the tests? Yes (recommended) or no?**",
				"1. Edit x.\n\n**Next: wire it into the gate and add tests? Yes (recommended) or no?**",
				"1. Edit x.\n\n**I'd fix both and push. OK? Yes (recommended) or no?**",
				"1. Edit x.\n\n**Kick off the full suite now? Yes (recommended) or no?**",
			} {
				s := h.d.createSession(h.proj, "default", "default")
				s.conv.AddUser("a request")
				s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: text})
				s.planWait = blocked
				h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
				plan := livePlan(s.conv)
				if plan == nil {
					t.Errorf("blocked=%v: no card for %q", blocked, text)
					continue
				}
				ap := planItemApproval(s, plan)
				if ap == nil || len(ap.Options) != 2 || ap.Options[0] != "Yes" || !ap.Stop[1] {
					t.Errorf("%q: the card's own Yes/No: %+v", text, ap)
				}
				if qs := planItemQuestions(s, plan); len(qs) != 0 {
					t.Errorf("%q: the approval shows again as a question: %+v", text, qs)
				}
				if !approvesPlan(s, "I pick the first option: Yes.") {
					t.Errorf("%q: a quick-choice Yes approves", text)
				}
			}
		}
		// Information questions stay questions.
		for _, text := range []string{"**Did the card show up? Yes or no?**", "**Is that placement OK? Yes or no?**", "**Which branch, main or dev?**"} {
			s := h.d.createSession(h.proj, "default", "default")
			s.conv.AddUser("a request")
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: text})
			h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
			if livePlan(s.conv) != nil || approvesPlan(s, "I pick the first option: Yes.") {
				t.Errorf("no card and no approval for %q", text)
			}
		}
	})
}

// (c): Yes on the card approves the whole reply's plan; No is "Not approved."
// Numbered choice questions after the yes/no line stay on the card.
func TestClosingYesNoCardButtons(t *testing.T) {
	text := "1. Edit x.\n2. Build.\n3. Commit.\n\n**Fix both and commit? Yes (recommended) or no?**\n\n1. Which branch: main or dev?\n2. Tag it: v1 or v2?"
	h := newHarness(t)
	s, plan := planCardSession(t, h, text)
	h.ui(func() {
		ap := planItemApproval(s, plan)
		if ap == nil || ap.Options[0] != "Yes" || !ap.Stop[1] {
			t.Fatalf("Yes/No on the card: %+v", ap)
		}
		qs := planItemQuestions(s, plan)
		if len(qs) != 2 {
			t.Fatalf("the two choice questions stay: %+v", qs)
		}
		ans := map[int]planAnswer{}
		for _, q := range qs {
			ans[q.ID] = planAnswer{Choice: 0}
		}
		h.d.answerPlanApproval(s, plan, ap, 0, qs, ans, "")
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("Yes approves the plan: %v runAll %v", plan.PermState, s.runAll)
		}
	})
	h2 := newHarness(t)
	s2, p2 := planCardSession(t, h2, "1. Edit x.\n\n**Fix both and commit? Yes (recommended) or no?**")
	h2.ui(func() {
		h2.d.answerPlanApproval(s2, p2, planItemApproval(s2, p2), 1, nil, nil, "")
		if p2.PermState != chat.PermDenied || s2.runAll {
			t.Fatalf("No is Not approved: %v", p2.PermState)
		}
	})
}

// blockedThenQuestion: a turn that asks first (before), makes a call the
// gate blocks, and ends with another question (after).
func blockedThenQuestion(t *testing.T, h *harness, before, after string) *deskSession {
	t.Helper()
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hello")
	})
	h.eventually("a running, idle session", func() bool { return s.proc != nil && !s.conv.Busy })
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
		userEvent(h, s, "a request", nil)
		if before != "" {
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: before})
		}
		if p := gateCall(h, s, "b1", "Bash", `{"command":"git commit -am x"}`, "toolu_b1"); p.PermState != chat.PermPlanAsked {
			t.Fatalf("the call is blocked: %v", p.PermState)
		}
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: after})
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
	})
	return s
}

// (d): a blocked call between the approval question and the answer doesn't
// clear the question: the user's pick of its Yes still approves.
func TestBlockedCallKeepsApprovalQuestion(t *testing.T) {
	ask := "1. Edit x.\n2. Commit.\n\n**Fix both and commit? Yes (recommended) or no?**"
	which := "That needs your approval first.\n\n**Which branch should I commit to: main or dev?**"
	for _, pick := range []string{"I pick the first option: Yes.", "Question 1: I pick the first option: Yes."} {
		h := newHarness(t)
		s := blockedThenQuestion(t, h, ask, which)
		h.ui(func() {
			if !s.planWait || livePlan(s.conv) != nil {
				t.Fatalf("the approval question still waits, no card for the which-question: wait %v", s.planWait)
			}
			// Negatives first: relayed text and Claude's own text never approve.
			for _, relayed := range []string{
				`<cross-session-message from="coordinator">` + pick + `</cross-session-message>`,
				`<cross-session-message from="coordinator">Approved.</cross-session-message>`,
			} {
				userEvent(h, s, relayed, nil)
				if s.runAll || approvesPlan(s, relayed) {
					t.Fatalf("a relayed %q approves", relayed)
				}
			}
			feed(t, h, s, `{"type":"assistant","message":{"id":"msg_c1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"Approved. `+pick+`"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
			if s.runAll {
				t.Fatal("Claude's text approves")
			}
			s.inTurn, s.autoTurn = false, false
			userEvent(h, s, pick, nil)
			if !s.runAll || s.planWait {
				t.Fatalf("%q approves the kept question: runAll %v wait %v", pick, s.runAll, s.planWait)
			}
		})
	}
}

// (d) keeps today's rule: without an approval question in the turn, a
// blocked call plus another question leaves no wait, so a Yes to that
// question approves nothing; and another message from the user ends a kept
// wait.
func TestBlockedCallWithoutApprovalQuestion(t *testing.T) {
	h := newHarness(t)
	s := blockedThenQuestion(t, h, "", "**Did you mean the desktop app? Yes or no?**")
	h.ui(func() {
		if s.planWait || approvesPlan(s, "I pick the first option: Yes.") {
			t.Fatal("no approval question: the pick approves nothing")
		}
	})
	h2 := newHarness(t)
	s2 := blockedThenQuestion(t, h2, "**Fix both and commit? Yes (recommended) or no?**", "**Which branch: main or dev?**")
	h2.ui(func() {
		s2.inTurn = false
		h2.d.sendTo(s2, "I pick the first option: Main.")
		if s2.runAll || s2.planWait {
			t.Fatalf("another answer ends the kept wait: runAll %v wait %v", s2.runAll, s2.planWait)
		}
	})
}

// The typed-yes switch: today a typed go-ahead approves a waiting plan.
func TestTypedApprovalCountsToday(t *testing.T) {
	if !typedApprovalCounts() {
		t.Fatal("typed approval counts until the user decides otherwise")
	}
}
