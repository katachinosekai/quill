package main

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// User 2026-11-23 (fdd52bd): blockquoted questions stay quotes.
func TestBlockquotedQuestionIsNoQuestion(t *testing.T) {
	if qs := planQuestions("Plan:\n1. Edit x.\n\n> Which colour? Red or blue?\n\nDo you approve step 1?"); len(qs) != 0 {
		t.Fatalf("a quote became a question: %+v", qs)
	}
	if _, _, ok := parseChoices("> Ship now or later?"); ok {
		t.Fatal("a quoted line has no choices")
	}
}

// A free-text question in a choice reply gets a text field, and its answer
// goes with the picks.
func TestChoiceReplyFreeTextQuestion(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		msg := &chat.Item{Seq: 994, Kind: chat.Assistant, Text: "Two things.\n\n1. Which disk? A or B?\n2. What should it be called?"}
		s.conv.Items = append(s.conv.Items, msg)
		v.show(s)
		o, _ := v.renderChoiceReply(msg)
		if o == nil {
			t.Fatal("no overlay")
		}
		n := 0
		deepWalk(o, func(x fyne.CanvasObject) {
			if _, ok := x.(*widget.Entry); ok {
				n++
			}
		})
		if n != 1 {
			t.Fatalf("text fields: %d", n)
		}
	})
}
