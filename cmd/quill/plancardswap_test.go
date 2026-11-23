package main

import (
	"reflect"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func swapButtons(card fyne.CanvasObject) []*widget.Button {
	var bs []*widget.Button
	walk(card, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok {
			bs = append(bs, b)
		}
	})
	return bs
}

// t4981e5: every numbered question with options gets its own buttons.
func TestEveryNumberedQuestionGetsButtons(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		msg := &chat.Item{Seq: 980, Kind: chat.Assistant, Text: "Findings.\n\n" +
			"**1. Use blue or red?**\n**2. Ship now or later?**\n**3. Keep the old file or replace it?**\n" +
			"**4. Small or large?**\n**5. Can I run `tool find x`? Yes (recommended) or no?**"}
		s.conv.Items = append(s.conv.Items, msg)
		h.d.chat.show(s)
		body, card := h.d.chat.choiceCard(msg)
		if card == nil || body != "Findings.\n" {
			t.Fatalf("body %q, card %v", body, card)
		}
		var labels []string
		for _, b := range swapButtons(card) {
			labels = append(labels, b.Text)
		}
		want := []string{"Use blue", "Red", "Ship now", "Later", "Keep the old file", "Replace it", "Small", "Large", "Yes", "No"}
		if !reflect.DeepEqual(labels, want) {
			t.Fatalf("buttons %q, want %q", labels, want)
		}
		// A click answers its own question.
		h.d.chat.btnChanged = time.Time{}
		swapButtons(card)[3].OnTapped() // "Later" of question 2
		last := s.conv.Items[len(s.conv.Items)-1]
		if last.Kind != chat.User || last.Text != "Question 2: I pick the second option: Later." {
			t.Fatalf("reply %+v", last)
		}
	})
}

// tf2db21: a quick-choice Yes to "Can I run X? Yes (recommended) or no?" is
// plan approval even with no step list and no plan words.
func TestQuickYesToCanIRunApproves(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "auto")
		s.conv.AddUser("a request")
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Found it.\n\n**1. A or B?**\n**2. Can I run `tool find x`? Yes (recommended) or no?**"})
		if !approvesPlan(s, "Question 2: "+choiceReply(0, "Yes (recommended)")) {
			t.Fatal("Yes on a Can I run question approves the plan")
		}
		if approvesPlan(s, "Question 2: "+choiceReply(1, "No")) {
			t.Fatal("No doesn't")
		}
	})
}

// tb704f5: clicks right after a card's buttons change are ignored, and the
// buttons don't change while a turn is under way.
func TestChoiceClickGuard(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		msg := &chat.Item{Seq: 981, Kind: chat.Assistant, Text: "Ready.\n\n**Push now, or wait for the release?**"}
		s.conv.Items = append(s.conv.Items, msg)
		h.d.chat.show(s)
		_, card := h.d.chat.choiceCard(msg)
		n := len(s.conv.Items)
		swapButtons(card)[0].OnTapped()
		if len(s.conv.Items) != n {
			t.Fatal("a click right after the buttons appeared is ignored")
		}
		_, card = h.d.chat.choiceCard(msg) // same buttons again: no new guard window
		h.d.chat.btnChanged = time.Now().Add(-time.Second)
		_, card = h.d.chat.choiceCard(msg)
		swapButtons(card)[0].OnTapped()
		if len(s.conv.Items) != n+1 {
			t.Fatal("after the guard time the click goes through")
		}
	})
}

func TestResendApprovalNotDuringTurn(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan."})
		plan := s.conv.AddPlan()
		plan.PermState = chat.PermAllowed
		h.d.chat.show(s)
		count := func() int {
			n := 0
			for _, b := range swapButtons(h.d.chat.renderPlan(plan)) {
				if b.Text == "Resend approval" {
					n++
				}
			}
			return n
		}
		s.inTurn = true
		if count() != 0 {
			t.Fatal("no Resend approval while a turn is under way")
		}
		s.inTurn = false
		if count() != 1 {
			t.Fatal("Resend approval when idle")
		}
	})
}
