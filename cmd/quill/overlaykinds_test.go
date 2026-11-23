package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// User 2026-11-23: every card kind draws inside its reply, not as a card
// below it; the card is only the fallback when the reply can't be matched.

func richTexts(o fyne.CanvasObject) string {
	var out []string
	deepWalk(o, func(x fyne.CanvasObject) {
		if r, ok := x.(*widget.RichText); ok {
			var segs func(ss []widget.RichTextSegment)
			segs = func(ss []widget.RichTextSegment) {
				for _, sg := range ss {
					switch x := sg.(type) {
					case *widget.ListSegment:
						segs(x.Items)
					case *widget.ParagraphSegment:
						segs(x.Texts)
					case interface{ Textual() string }:
						out = append(out, x.Textual())
					}
				}
			}
			segs(r.Segments)
		}
	})
	return strings.Join(out, "\n")
}

func TestPlanKindsOverlayTheReply(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []string // buttons, the toggle left out
		absent     string   // text the reply no longer shows (its buttons answer it)
		present    string   // text the reply keeps
	}{
		{"approval radio", yesNoPlan, []string{"Send answers"}, "", "Can I run steps 1"},
		{"approve card", "1. Edit x.\n2. Build.\n\n**Do you approve steps 1–2?**", []string{"Approve plan & run all", "Don't run"}, "", "Do you approve"},
		{"unnumbered questions", "Plan:\n1. Bundle x.\n\nShould rule 21 stay as it is?\nWhat should the title say?\n\n**Do you approve step 1?**", []string{"Approve plan & send answers", "Don't run"}, "Do you approve", "What should the title say?"},
		{"approval and questions", mixedPlan, []string{"Send answers"}, "", "What should the tag say?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			s, plan := planCardSession(t, h, tc.text)
			h.ui(func() {
				v := h.d.chat
				reply := planReply(s.conv, plan)
				if !v.planOverlaid(plan) {
					t.Fatal("the reply carries the controls, no separate card")
				}
				if o := v.renderPlan(plan); len(swapButtons(o)) != 0 {
					t.Fatalf("the card draws nothing: %q", buttonLabels(o))
				}
				o := v.renderPlanReply(reply)
				if o == nil {
					t.Fatal("nothing drawn in the reply")
				}
				if got := buttonLabels(o); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("buttons %q, want %q", got, tc.want)
				}
				txt := richTexts(o)
				if !strings.Contains(txt, tc.present) || tc.absent != "" && strings.Contains(txt, tc.absent) {
					t.Fatalf("reply text:\n%s", txt)
				}
				// The click guard holds right after the buttons change.
				v.noteButtons(plan.Seq, "x")
				swapButtons(o)[len(swapButtons(o))-1].OnTapped()
				if plan.PermState != chat.PermPending {
					t.Fatal("a click inside the guard window is ignored")
				}
			})
		})
	}
}

func TestPlanOverlayApproveCardApproves(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		tapLabel(t, h, h.d.chat.renderPlanReply(planReply(s.conv, plan)), "Approve plan & run all")
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatalf("Approve in the reply approves: %v %v", plan.PermState, s.runAll)
		}
	})
	h2 := newHarness(t)
	s2, plan2 := planCardSession(t, h2, "1. Edit x.\n\n**Do you approve step 1?**")
	h2.ui(func() {
		tapLabel(t, h2, h2.d.chat.renderPlanReply(planReply(s2.conv, plan2)), "Don't run")
		if plan2.PermState != chat.PermDenied {
			t.Fatalf("Don't run in the reply: %v", plan2.PermState)
		}
	})
}

// The plan text isn't in the last reply: the card stays.
func TestPlanOverlayFallsBackWithoutApprovalQuestion(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() {
		s := h.d.sessions[editsID]
		s.conv.AddUser("next")
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: yesNoPlan})
		reply := &chat.Item{Kind: chat.Assistant, Text: "Running the checks first."}
		s.conv.Items = append(s.conv.Items, reply)
		plan := s.conv.AddPlan()
		h.d.chat.show(s)
		if h.d.chat.planOverlaid(plan) || h.d.chat.renderPlanReply(reply) != nil {
			t.Fatal("no overlay when the reply has no approval question")
		}
		if got := radioOptions(h.d.chat.renderPlan(plan)); !reflect.DeepEqual(got, []string{"Yes (recommended)", "No"}) {
			t.Fatalf("the card: %q", got)
		}
	})
}

const choiceMsg = "Plan:\n\n1. Back up the file.\n2. Add the line.\n\nIntro words.\n\n" +
	"1. Where should it go? On disk A, or on disk B (recommended)?\n" +
	"2. Which size?\n   Small or large?\n" +
	"3. Then run `x`.\n" +
	"4. What should it be called?"

func TestChoiceRepliesOverlay(t *testing.T) {
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
		h.d.chat.show(s)
		o, plain := v.renderChoiceReply(msg)
		if o == nil || plain {
			t.Fatal("several questions: overlay")
		}
		if got := buttonLabels(o); !reflect.DeepEqual(got, []string{"On disk A", "On disk B", "Small", "Large", "Send answers"}) {
			t.Fatalf("buttons %q", got)
		}
		txt := richTexts(o)
		for _, w := range []string{"Back up the file", "Intro words", "Then run", "What should it be called?"} {
			if !strings.Contains(txt, w) {
				t.Errorf("the reply keeps %q:\n%s", w, txt)
			}
		}
		// Picks mark, nothing is sent until Send answers (guard cleared).
		v.btnChanged = time.Time{}
		swapButtons(o)[0].OnTapped()
		if s.conv.Items[len(s.conv.Items)-1] != msg {
			t.Fatal("a pick sends nothing")
		}
		o, _ = v.renderChoiceReply(msg)
		bs := swapButtons(o)
		if !bs[len(bs)-1].Disabled() {
			t.Fatal("Send waits for every question")
		}
		v.btnChanged = time.Time{}
		bs[3].OnTapped()
		o, _ = v.renderChoiceReply(msg)
		bs = swapButtons(o)
		if bs[len(bs)-1].Disabled() {
			t.Fatal("all picked: Send is on")
		}
		v.btnChanged = time.Time{}
		bs[len(bs)-1].OnTapped()
		if got := lastUserText(s); !strings.Contains(got, "On disk A") || !strings.Contains(got, "Large") {
			t.Fatalf("Send answers: %q", got)
		}
		// Answered (a user turn after it): the buttons stay, greyed, with the picks marked.
		o, plain = v.renderChoiceReply(msg)
		if o == nil || plain {
			t.Fatal("an answered reply keeps its buttons")
		}
		for _, b := range swapButtons(o) {
			if !b.Disabled() {
				t.Errorf("button %q is live on an answered reply", b.Text)
			}
		}
		marked := 0
		for _, b := range swapButtons(o) {
			if b.Icon != nil && b.Text != "Send answers" {
				marked++
			}
		}
		if marked != 2 {
			t.Errorf("picks marked: %d, want 2", marked)
		}
	})
}

func TestChoiceReplySingleOverlay(t *testing.T) {
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
		o, plain := v.renderChoiceReply(msg)
		if o == nil || plain || !reflect.DeepEqual(buttonLabels(o), []string{"Ship now", "Later"}) || !strings.Contains(richTexts(o), "Some findings.") {
			t.Fatalf("single question overlay: %v %q", plain, buttonLabels(o))
		}
		tapLabel(t, h, o, "Later")
		if got := lastUserText(s); !strings.Contains(got, "Later") {
			t.Fatalf("the pick is sent: %q", got)
		}
	})
}
