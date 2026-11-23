package main

import (
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Independent review of fix/plan-overlay (review/overlay-fable2, task
// t96bf4a). Tests named TestReviewFAILING_* fail on 974f316: each one shows a
// defect, with its cause in the comment above it. TestReviewOK_* pass (cases
// checked and fine). No product code is changed here.

// shownText: the text a rendered item shows, ordered list items with the
// number Fyne (and centerLists) gives them: their place in the list, from 1.
func shownText(o fyne.CanvasObject) string {
	var out []string
	deepWalk(o, func(x fyne.CanvasObject) {
		r, ok := x.(*widget.RichText)
		if !ok {
			return
		}
		var segs func(ss []widget.RichTextSegment) string
		segs = func(ss []widget.RichTextSegment) string {
			var b strings.Builder
			for _, sg := range ss {
				switch x := sg.(type) {
				case *widget.ListSegment:
					for i, item := range x.Items {
						mark := "• "
						if x.Ordered {
							mark = fmt.Sprintf("%d. ", i+1)
						}
						b.WriteString("\n" + mark + segs([]widget.RichTextSegment{item}))
					}
				case *widget.ParagraphSegment:
					b.WriteString(segs(x.Texts))
				case interface{ Textual() string }:
					b.WriteString(x.Textual())
				}
			}
			return b.String()
		}
		out = append(out, segs(r.Segments))
	})
	return strings.ReplaceAll(strings.Join(out, "\n"), "\u00a0", " ")
}

func allDisabled(bs []*widget.Button) bool {
	for _, b := range bs {
		if b.Text == "Show as written" || b.Text == "Answer in place" {
			continue // the toggle stays live
		}
		if !b.Disabled() {
			return false
		}
	}
	return true
}

// FAILING (high; user 2026-11-23, new requirement): an answered, replaced or
// stale plan must keep its controls inside the reply, greyed, with no card
// below. Cause: planoverlay.go:70 (planOverlayQs needs PermPending and
// planActive) and planoverlay.go:170 (renderPlanReply returns nil for a plan
// that isn't pending), so chatview.go:1775-1798 (renderPlan) draws the greyed
// card under the reply.
func TestReviewFAILING_AnsweredOrStalePlanStaysInItsReply(t *testing.T) {
	for _, tc := range []struct {
		name string
		do   func(s *deskSession, plan *chat.Item)
	}{
		{"rejected", func(s *deskSession, plan *chat.Item) { plan.PermState = chat.PermDenied }},
		{"approved", func(s *deskSession, plan *chat.Item) { plan.PermState = chat.PermAllowed; s.inTurn = true }},
		{"moved on", func(s *deskSession, plan *chat.Item) { s.conv.AddUser("something else first") }},
		{"replaced", func(s *deskSession, plan *chat.Item) {
			s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9900, Kind: chat.Assistant, Text: "1. Edit y.\n\n**Do you approve step 1?**"})
			s.conv.AddPlan()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			s, plan := planCardSession(t, h, mixedPlan)
			h.ui(func() {
				v := h.d.chat
				reply := planReply(s.conv, plan)
				tc.do(s, plan)
				if got := buttonLabels(v.renderPlan(plan)); len(got) != 0 {
					t.Errorf("a separate card under the reply: %q", got)
				}
				o := v.renderItem(reply, nil)
				bs := swapButtons(o)
				if len(bs) == 0 {
					t.Fatal("the reply shows no (greyed) controls for its plan")
				}
				if !allDisabled(bs) {
					t.Errorf("the reply's buttons are inactive: %q", buttonLabels(o))
				}
			})
		})
	}
}

// FAILING (medium; depends on how the user's "answered … inside the reply"
// is read for choice replies, see the report): an answered choice reply
// shows plain text, without its buttons greyed and the pick marked. Cause:
// choiceoverlay.go:48-50 (not latest: nil, plain).
func TestReviewFAILING_AnsweredChoiceReplyKeepsItsButtonsGreyed(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		msg := &chat.Item{Seq: 991, Kind: chat.Assistant, Text: "Some findings.\n\nShip now or later?"}
		s.conv.Items = append(s.conv.Items, msg)
		v.show(s)
		s.conv.AddUser(choiceReply(1, "Later"))
		o := v.renderItem(msg, nil)
		bs := swapButtons(o)
		if len(bs) != 2 || !allDisabled(bs) {
			t.Fatalf("an answered choice reply keeps its two buttons, greyed: %q", buttonLabels(o))
		}
	})
}

// FAILING (medium; regression against master, which overlaid this): two
// questions on one numbered line fall back to the separate card. Cause:
// planoverlay.go:90 (`r.line < at`: the second question starts on the first
// one's line, which is before `at = r.end`); the draw loop at
// planoverlay.go:232 expects several questions per line.
func TestReviewFAILING_TwoQuestionsOnOneLineStillOverlay(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "Plan:\n1. Edit x.\n\nQuestions:\n1. Should the beta channel get it first? And what should the tag say?\n2. Do you approve step 1?")
	h.ui(func() {
		v := h.d.chat
		if n := len(planItemQuestions(s, plan)); n != 2 {
			t.Fatalf("questions: %d, want 2", n)
		}
		if !v.planOverlaid(plan) || len(swapButtons(v.renderPlan(plan))) != 0 {
			t.Fatal("two questions on one line: a separate card instead of the overlay")
		}
	})
}

// The reply of the user's screenshot of 2026-11-23 02:23 (question 2).
const prefQ2 = "Typing exactly \"Approved.\" approves even when no card is showing, because it's the card's own text, so it would cover whatever runs next. Count it only while a plan or approval question is waiting (recommended), or keep it as is? Only while waiting, or keep?"
const prefReply = "Two things to settle.\n\n1. Should the notice stay as it is? Yes (recommended) or no?\n2. " + prefQ2

// FAILING (high): a preference question that only mentions approving is
// taken for the plan's approval question, so the turn gets a plan card (and
// "Don't run"). Cause: internal/chat/chat.go:406 with chat.go:431-437
// (lastQuestionOf reads everything before the first "?", earlier sentences
// included) and chat.go:139 (approvalAsk's `\bapprov` matches "approves" and
// "approval question"); chat.go:314 (firstQuestion) matches the same way.
func TestReviewFAILING_PreferenceQuestionIsNoApproval(t *testing.T) {
	if q := chat.ClosingApproval(prefReply); q != "" {
		t.Errorf("ClosingApproval takes the preference question for the approval: %q", q)
	}
	c := &chat.Conversation{}
	c.AddUser("a request")
	c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: prefReply})
	if c.AsksApproval() {
		t.Error("AsksApproval: the reply asks for no approval")
	}
}

// FAILING (high): the same question gets two sets of controls, a radio group
// (the question's first sentence) and approval buttons (its last sentence).
// Cause: planquestions.go:582-590 (planQuestionsFor drops only the question
// whose text equals the title's LAST sentence) against planapproval.go:47-49
// (planApprovalOptions reads options from that last sentence), so the first
// sentence of the title stays as a question of its own.
func TestReviewFAILING_ApprovalQuestionNotAskedTwice(t *testing.T) {
	a := planApprovalOptions(prefQ2)
	start, end := approvalSpan(prefReply, prefQ2)
	for _, q := range planQuestionsFor(prefQ2, prefReply) {
		if a != nil && q.line >= start && q.line <= end {
			t.Errorf("line %d has the approval buttons %q and its own control %q", q.line, a.Options, q.Options)
		}
	}
}

// FAILING (medium): the buttons lose the recommendation the question gives
// ("Count it only while … (recommended), or keep it as is? Only while
// waiting, or keep?"). Cause: planapproval.go:59 (recommendedOption reads
// only the options sentence, not the question sentence before it).
func TestReviewFAILING_ApprovalButtonsKeepRecommended(t *testing.T) {
	a := planApprovalOptions(prefQ2)
	if a == nil {
		t.Skip("no approval options")
	}
	if a.Recommended != 0 {
		t.Fatalf("recommended = %d, want 0 (%q)", a.Recommended, a.Options)
	}
}

// FAILING (high): a pick between two preferences gets a red "Don't run".
// Cause: planapproval.go:77-81 (any title with options and no stop option
// gets DontRun, whether or not it asks to run anything).
func TestReviewFAILING_NoDontRunOnPreferenceQuestion(t *testing.T) {
	if a := planApprovalOptions(prefQ2); a != nil && a.DontRun {
		t.Fatalf("\"Don't run\" on a preference question: %+v", *a)
	}
}

// FAILING (medium): a numbered step between a reply's questions is shown
// renumbered ("3. Then run x." becomes "1."). Cause: choiceoverlay.go:84-88
// (prose draws the lines between two questions as their own markdown block,
// whose list starts at 1 again); only question lines get the no-break space
// that keeps their number (choiceoverlay.go:97).
func TestReviewFAILING_ChoiceOverlayKeepsStepNumber(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		msg := &chat.Item{Seq: 990, Kind: chat.Assistant, Text: choiceMsg}
		s.conv.Items = append(s.conv.Items, msg)
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		if o == nil {
			t.Fatal("no overlay")
		}
		if txt := shownText(o); !strings.Contains(txt, "3. Then run") {
			t.Fatalf("step 3 keeps its number:\n%s", txt)
		}
	})
}

// FAILING (low): a table row with a "?" in the closing block becomes a
// question ("| Use blue" / "Red"), and the overlay cuts the table apart.
// Cause: planquestions.go:137-148 (every closing line with a "?" is a
// question; table rows aren't left out).
func TestReviewFAILING_TableRowIsNoQuestion(t *testing.T) {
	for _, q := range planQuestions("Plan:\n1. Edit x.\n\n| Question | Options |\n|---|---|\n| Use blue or red? | blue, red |\n\nDo you approve step 1?") {
		if strings.HasPrefix(q.Text, "|") {
			t.Fatalf("a table row became a question: %q %q", q.Text, q.Options)
		}
	}
}

// FAILING (medium; open doubt, see the report): a plan whose reply ends
// without a question ("**Plan (needs your yes):**" and its steps, or a
// blocked call's turn) has the made-up title "Approve this plan?", which
// isn't in the reply, so its buttons stay a separate card although the reply
// is known. Cause: planoverlay.go:78-81 (approvalSpan < 0: no overlay).
func TestReviewFAILING_PlanWithoutClosingQuestionOverlays(t *testing.T) {
	h := newHarness(t)
	_, plan := planCardSession(t, h, "**Plan (needs your yes):**\n1. Edit x.\n2. Build.")
	h.ui(func() {
		v := h.d.chat
		if !v.planOverlaid(plan) || len(swapButtons(v.renderPlan(plan))) != 0 {
			t.Fatal("the approval buttons are a separate card under the reply")
		}
	})
}

// FAILING (low): with centring on, the choice overlay's note stays left.
// Cause: choiceoverlay.go:127-129 (no note.Alignment = v.align(), unlike
// approvalSend's note at planapproval.go:292).
func TestReviewFAILING_ChoiceNoteFollowsCentring(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		h.d.center = true
		msg := &chat.Item{Seq: 990, Kind: chat.Assistant, Text: choiceMsg}
		s.conv.Items = append(s.conv.Items, msg)
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		found := false
		deepWalk(o, func(x fyne.CanvasObject) {
			if l, ok := x.(*widget.Label); ok && strings.HasPrefix(l.Text, "Pick an answer") {
				found = true
				if l.Alignment != fyne.TextAlignCenter {
					t.Errorf("the note is not centred: %v", l.Alignment)
				}
			}
		})
		if !found {
			t.Fatal("no note")
		}
	})
}

// OK: a later reply that repeats the pending plan's questions (questionSig)
// gets no second set of buttons, and the plan stays overlaid in its reply.
func TestReviewOK_RepeatedQuestionSetKeepsOneOverlay(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, mixedPlan)
	h.ui(func() {
		v := h.d.chat
		again := &chat.Item{Seq: 9901, Kind: chat.Assistant, Text: mixedPlan}
		s.conv.Items = append(s.conv.Items, again)
		if !v.planOverlaid(plan) {
			t.Fatal("the plan stays overlaid")
		}
		if o := v.renderItem(again, nil); len(swapButtons(o)) != 0 {
			t.Fatalf("the repeat has buttons: %q", buttonLabels(o))
		}
	})
}

// OK: "Show as written" moves the controls to the card and back, and the
// overlay keeps numbers, bold/italic question text and each control kind.
func TestReviewOK_ShowAsWrittenAndControls(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, mixedPlan)
	h.ui(func() {
		v := h.d.chat
		reply := planReply(s.conv, plan)
		o := v.renderPlanReply(reply)
		txt := shownText(o)
		for _, w := range []string{"1. Use blue or red?", "2. Which parts", "3. What should the tag say?", "4. Can I run steps"} {
			if !strings.Contains(txt, w) {
				t.Errorf("the overlay keeps %q:\n%s", w, txt)
			}
		}
		var radio, check, entry int
		deepWalk(o, func(x fyne.CanvasObject) {
			switch x.(type) {
			case *widget.RadioGroup:
				radio++
			case *widget.CheckGroup:
				check++
			case *widget.Entry:
				entry++
			}
		})
		if radio != 2 || check != 1 || entry != 1 {
			t.Errorf("controls: %d radio (the approval question is one), %d check, %d entry", radio, check, entry)
		}
		v.planPlain = map[*chat.Item]bool{plan: true}
		if got := buttonLabels(v.renderPlanReply(reply)); len(got) != 1 || got[0] != "Answer in place" {
			t.Errorf("as written: the reply has only the toggle: %q", got)
		}
		if len(swapButtons(v.renderPlan(plan))) == 0 {
			t.Error("as written: the card has the buttons")
		}
	})
}

// OK: questions inside a fenced code block are not questions.
func TestReviewOK_FencedQuestionsAreNoQuestions(t *testing.T) {
	if qs := planQuestions("Plan:\n1. Edit x.\n\n```\n1. Use blue or red?\n```\n\nDo you approve step 1?"); len(qs) != 0 {
		t.Fatalf("%+v", qs)
	}
}
