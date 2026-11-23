package main

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Review of fix/plan-overlay (task t96bf4a). Every test here describes the
// user's requirement; the ones that fail are open defects.

const reviewColourPlan = "Plan:\n1. Edit x.\n\n1. Which colour? Red or blue?\n\n**Do you approve step 1?**"

func radios(o fyne.CanvasObject) []*widget.RadioGroup {
	var out []*widget.RadioGroup
	deepWalk(o, func(x fyne.CanvasObject) {
		if r, ok := x.(*widget.RadioGroup); ok {
			out = append(out, r)
		}
	})
	return out
}

// User 2026-11-23: an answered or stale plan shows the answer inside the
// reply (the overlay, buttons greyed, the pick marked), never a greyed card
// below. Cause: renderPlanReply (planoverlay.go:170) returns nil unless the
// plan is pending, and renderPlan (chatview.go:1775) draws the greyed card.
func TestReviewAnsweredPlanAnswerInReply(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer func(h *harness, s *deskSession, plan *chat.Item, qs []planQ, ans map[int]planAnswer)
	}{
		{"approved", func(h *harness, s *deskSession, plan *chat.Item, qs []planQ, ans map[int]planAnswer) {
			h.d.answerPlanAnswers(s, plan, qs, ans)
		}},
		{"don't run", func(h *harness, s *deskSession, plan *chat.Item, _ []planQ, _ map[int]planAnswer) {
			h.d.answerPlan(s, plan, false)
		}},
		{"moved on", func(h *harness, s *deskSession, _ *chat.Item, _ []planQ, _ map[int]planAnswer) {
			s.conv.AddUser("something else")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			s, plan := planCardSession(t, h, reviewColourPlan)
			h.ui(func() {
				v := h.d.chat
				reply := planReply(s.conv, plan)
				qs := planItemQuestions(s, plan)
				if len(qs) != 1 {
					t.Fatalf("questions %+v", qs)
				}
				ans := v.planAnswers(plan, qs)
				ans[qs[0].ID] = planAnswer{Choice: 1} // Blue
				tc.answer(h, s, plan, qs, ans)
				if card := v.renderPlan(plan); len(swapButtons(card)) != 0 || strings.Contains(richTexts(card)+labelTexts(card), "Claude's plan is above") {
					t.Errorf("a greyed card is drawn below the reply: %q", buttonLabels(card))
				}
				o := v.renderPlanReply(reply)
				if o == nil {
					t.Fatal("the reply draws no overlay once the plan is answered or stale")
				}
				for _, b := range swapButtons(o) {
					if b.Text != "Show as written" && !b.Disabled() {
						t.Errorf("button %q is still live", b.Text)
					}
				}
				rs := radios(o)
				if len(rs) != 1 || rs[0].Selected != "Blue" || !rs[0].Disabled() {
					t.Errorf("the pick is marked and greyed under its question: %d radios", len(rs))
				}
			})
		})
	}
}

func labelTexts(o fyne.CanvasObject) string {
	var out []string
	deepWalk(o, func(x fyne.CanvasObject) {
		if l, ok := x.(*widget.Label); ok {
			out = append(out, l.Text)
		}
	})
	return strings.Join(out, "\n")
}

// Lead 2 (ruled out, guard): a choice question "2." drawn on its own keeps
// its number though choiceoverlay.go:97 keeps a normal space, because Fyne's
// ListSegment keeps the list's start (centerLists, which numbers from 1, is
// only used by a test).
func TestReviewChoiceOverlayKeepsNumberCentred(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.center = true
		v := h.d.chat
		msg := &chat.Item{Seq: 992, Kind: chat.Assistant, Text: "Two things.\n\n1. Which disk? A or B?\n2. Which size? Small or large?"}
		s.conv.Items = append(s.conv.Items, msg)
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		if o == nil {
			t.Fatal("no overlay")
		}
		found := false
		deepWalk(o, func(x fyne.CanvasObject) {
			r, ok := x.(*widget.RichText)
			if !ok {
				return
			}
			for _, sg := range r.Segments {
				if l, ok := sg.(*widget.ListSegment); ok && l.Ordered && strings.Contains(richTexts(widget.NewRichText(l)), "Which size?") {
					found = true
					if l.StartNumber() != 2 {
						t.Errorf("question 2 renders as %d.", l.StartNumber())
					}
				}
			}
		})
		if !found {
			t.Log("question 2 is no list (no-break space)")
		}
		// a non-list paragraph (no-break space) keeps "2." as text
	})
}

// A question in a markdown table row: the overlay must not split the table
// or show the row as raw "| … |" text. Cause: renderPlanReply cuts the
// reply at the question's line (planoverlay.go:225-230), so the header rows
// and the question row become separate markdown blocks.
func TestReviewPlanOverlayTableRowQuestion(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "Plan:\n1. Edit x.\n\n| # | Question |\n|---|---|\n| 1 | Which colour? Red or blue? |\n| 2 | What should the tag say? |\n\n**Do you approve step 1?**")
	h.ui(func() {
		o := h.d.chat.renderPlanReply(planReply(s.conv, plan))
		if o == nil {
			return // the card fallback is fine for a table
		}
		if txt := richTexts(o); strings.Contains(txt, "| 1 |") || strings.Contains(txt, "| 2 |") {
			t.Fatalf("the table is split, rows drawn as raw pipes:\n%s", txt)
		}
	})
}

// Two numbered questions with the same number (markdown auto-numbering
// writes "1." on every item): a pick on one must not mark the other.
// Cause: choicePicks is keyed by q.num (choiceoverlay.go:105,109,115).
func TestReviewChoicePicksSameNumber(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		msg := &chat.Item{Seq: 993, Kind: chat.Assistant, Text: "Two things.\n\n1. Which disk? A or B?\n1. Which size? Small or large?"}
		s.conv.Items = append(s.conv.Items, msg)
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		if o == nil {
			t.Fatal("no overlay")
		}
		v.btnChanged = time.Time{}
		tapLabel(t, h, o, "A")
		o, _ = v.renderChoiceReply(msg)
		bs := swapButtons(o)
		if send := bs[len(bs)-1]; send.Text != "Send answers" || !send.Disabled() {
			t.Fatalf("one pick for two questions enables %q", send.Text)
		}
	})
}

// Screenshot 2026-11-23 02:23: a reply ending with two numbered preference
// questions, the last "… Count it only while … (recommended), or keep it as
// is? Only while waiting, or keep?". Cause: planQuestion (chatview.go:1956)
// takes that last line as the approval title; planQuestionsFor
// (planquestions.go:583,588) drops a question only when it equals the
// title's LAST sentence, so it stays as a radio too; planApprovalOptions
// (planapproval.go:59) reads "recommended" from that last sentence only and
// (planapproval.go:75-81) adds DontRun for a plain preference pick.
const reviewShotReply = "Findings.\n\n1. Rule 12 is read twice. Merge the two reads into one (recommended), or leave both? Merge or leave?\n" +
	"2. Typing exactly \"Approved.\" approves even when no card is showing, because it's the card's own text, so it would cover whatever runs next. Count it only while a plan or approval question is waiting (recommended), or keep it as is? Only while waiting, or keep?"

// (b) one control per question: not a radio group and buttons for the same question.
func TestReviewShotNoDuplicateControls(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, reviewShotReply)
	h.ui(func() {
		_ = s
		o := h.d.chat.planUI(plan)
		n := 0
		for _, r := range radios(o) {
			for _, opt := range r.Options {
				if strings.HasPrefix(opt, "Count it only while") {
					n++
				}
			}
		}
		for _, l := range buttonLabels(o) {
			if strings.HasPrefix(l, "Only while waiting") || strings.HasPrefix(l, "Count it only while") {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("question 2 has %d controls (radio + buttons): %q", n, buttonLabels(o))
		}
	})
}

// (c) the approval buttons mark the option the question recommends.
func TestReviewShotApprovalButtonsRecommended(t *testing.T) {
	title := "Typing exactly \"Approved.\" approves even when no card is showing. Count it only while a plan or approval question is waiting (recommended), or keep it as is? Only while waiting, or keep?"
	if a := planApprovalOptions(title); a != nil && a.Recommended != 0 {
		t.Fatalf("recommended %d, want 0 (%q)", a.Recommended, a.Options)
	}
}

// (d) a preference question is no approval: no "Don't run" beside its options.
func TestReviewShotPreferenceIsNoApproval(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, reviewShotReply)
	h.ui(func() {
		if a := planItemApproval(s, plan); a != nil {
			t.Fatalf("the preference question is read as the approval: %+v", a)
		}
	})
}
