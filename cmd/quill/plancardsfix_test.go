package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// t96bf4a / tc3712f (user 2026-11-23): plan and choice cards overlay their
// questions inside the reply, stay live until answered or replaced by a newer
// question, and options with commas or colons are kept whole.

// TestChoiceOptionsKeepCommasAndColons: the real failures of 2026-11-23.
func TestChoiceOptionsKeepCommasAndColons(t *testing.T) {
	cases := []struct {
		line string
		want []string
		rec  int
	}{
		// two options with commas of their own (was "second option: About 30%")
		{"Strength: light, about 30% (recommended), or medium, about 50%?",
			[]string{"Light, about 30%", "Medium, about 50%"}, 0},
		{"Which strength? Light, about 30% (recommended), or medium, about 50%?",
			[]string{"Light, about 30%", "Medium, about 50%"}, 0},
		// a colon inside a quoted option (was just '<suggestion>"')
		{`Keep the name, as in "Message the Quill operator: <suggestion> (Tab)" (recommended), or leave it as it is?`,
			[]string{`Keep the name, as in "Message the Quill operator: <suggestion> (Tab)"`, "Leave it as it is"}, 0},
		// the same without quotes: a colon after the first comma stays
		{"Keep the label, as in Message the operator: hint (recommended), or leave it?",
			[]string{"Keep the label, as in Message the operator: hint", "Leave it"}, 0},
		// parallel "with" options are not joined (was 'Replaced with Pop out later, with Pop out now')
		{"Old kmem window: replaced with Pop out later (recommended), with Pop out now, or keep both?",
			[]string{"Replaced with Pop out later", "With Pop out now", "Keep both"}, 0},
		// commas and "or" inside quotes or parentheses don't split
		{`Label: "Later, or never" (recommended), or "Now"?`,
			[]string{`"Later, or never"`, `"Now"`}, 0},
		{"Mode: fast (skips a, b or c), or safe (recommended)?",
			[]string{"Fast", "Safe"}, 1},
		// still lists
		{"Colour: red, green (recommended), or blue?", []string{"Red", "Green", "Blue"}, 1},
		{"Red, green or blue?", []string{"Red", "Green", "Blue"}, -1},
		{"Yes, 2 subagents (recommended), or no, I'll do it myself?", []string{"Yes, 2 subagents", "No, I'll do it myself"}, 0},
		{"For the icon, use blue, green, or red?", []string{"Use blue", "Green", "Red"}, -1},
	}
	for _, c := range cases {
		_, opts, ok := parseChoices(c.line)
		if !ok || strings.Join(opts, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q\n got %q (ok %v)\nwant %q", c.line, opts, ok, c.want)
			continue
		}
		if rec := choiceRecommended(c.line, len(opts)); rec != c.rec {
			t.Errorf("%q: recommended %d, want %d", c.line, rec, c.rec)
		}
	}
	// The pick sent back names the option whole.
	line := "Strength: light, about 30% (recommended), or medium, about 50%?"
	_, opts, _ := parseChoices(line)
	if got := choiceReply(0, opts[0]); got != "I pick the first option: Light, about 30%." {
		t.Errorf("reply %q", got)
	}
}

// operatorPlan: the plan format the Quill operator writes.
const operatorPlan = "Plan:\n1. Add the setting.\n2. Wire it into the box.\n3. Test it.\n\n" +
	"**Can I run steps 1–3? Yes (recommended) or no?**\n" +
	"4. Strength: light, about 30% (recommended), or medium, about 50%?\n" +
	"5. Keep the name, as in \"Message the Quill operator: <suggestion> (Tab)\" (recommended), or leave it as it is?\n" +
	"6. Old kmem window: replaced with Pop out later (recommended), with Pop out now, or keep both?"

// TestOperatorPlanQuestionsWhole: the plan card's questions keep their
// options whole, and the approval sends them so.
func TestOperatorPlanQuestionsWhole(t *testing.T) {
	qs := planQuestions(operatorPlan)
	want := [][]string{
		{"Light, about 30%", "Medium, about 50%"},
		{`Keep the name, as in "Message the Quill operator: <suggestion> (Tab)"`, "Leave it as it is"},
		{"Replaced with Pop out later", "With Pop out now", "Keep both"},
	}
	if len(qs) != len(want) {
		t.Fatalf("questions %+v", qs)
	}
	ans := map[int]planAnswer{}
	for i, q := range qs {
		if strings.Join(q.Options, "|") != strings.Join(want[i], "|") || q.Recommended != 0 {
			t.Errorf("q%d options %q rec %d, want %q rec 0", q.Num, q.Options, q.Recommended, want[i])
		}
		ans[q.ID] = planAnswer{Choice: q.Recommended}
	}
	got := planAnswerList(qs, ans)
	wantList := ` Answers: 4 → Light, about 30%; 5 → Keep the name, as in "Message the Quill operator: <suggestion> (Tab)"; 6 → Replaced with Pop out later.`
	if got != wantList {
		t.Errorf("answers\n got %q\nwant %q", got, wantList)
	}
}

// TestOperatorPlanOverlaid: the operator's plan draws its controls inside the
// reply (no card below it), while pending and once answered.
func TestOperatorPlanOverlaid(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, operatorPlan)
	h.ui(func() {
		v := h.d.chat
		if !v.planOverlaid(plan) {
			t.Fatal("the plan is not overlaid in its reply")
		}
		if card := v.renderPlan(plan); len(swapButtons(card)) != 0 {
			t.Errorf("a card is drawn below the reply: %q", buttonLabels(card))
		}
		reply := planReply(s.conv, plan)
		o := v.renderPlanReply(reply)
		if o == nil {
			t.Fatal("no overlay")
		}
		if n := len(radios(o)); n != 3 {
			t.Errorf("%d radio groups, want one per question", n)
		}
		if got := strings.Join(buttonLabels(o), "|"); !strings.Contains(got, "Approve plan & send answers|Don't run") {
			t.Errorf("approval buttons %q", got)
		}
		qs := planItemQuestions(s, plan)
		h.d.answerPlanAnswers(s, plan, qs, v.planAnswers(plan, qs))
		if card := v.renderPlan(plan); len(swapButtons(card)) != 0 {
			t.Errorf("an answered plan draws a card below the reply: %q", buttonLabels(card))
		}
		if v.renderPlanReply(reply) == nil {
			t.Error("the answered plan's reply draws no overlay")
		}
	})
}

// TestStatusRepliesKeepQuestionLive (tc3712f): one-line status replies to
// task notifications after an unanswered question don't grey it.
func TestStatusRepliesKeepQuestionLive(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, operatorPlan)
	h.ui(func() {
		// A notification turn: no user item, one status line, a blocked call.
		s.planWait = true
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9500, Kind: chat.Assistant, Text: "Build finished; still waiting for your answer above."})
		endTurn(t, h, s)
		// A scheduled message is no user turn either.
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9501, Kind: chat.User, Text: "<scheduled-trigger>check</scheduled-trigger>"})
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9502, Kind: chat.Assistant, Text: "Checked: nothing new."})
		if st := planState(s.conv, plan); st != planActive || plan.PermState != chat.PermPending {
			t.Fatalf("plan state %q / %v after status replies", st, plan.PermState)
		}
		for _, it := range s.conv.Items {
			if it.Kind == chat.Plan && it != plan {
				t.Errorf("a status reply added a plan card")
			}
		}
		h.d.chat.show(s)
		o := h.d.chat.renderPlanReply(planReply(s.conv, plan))
		if o == nil {
			t.Fatal("no overlay")
		}
		if strings.Contains(richTexts(o)+labelTexts(o), "No longer needed") {
			t.Error("the plan shows 'No longer needed'")
		}
		live := 0
		for _, r := range radios(o) {
			if !r.Disabled() {
				live++
			}
		}
		if live == 0 {
			t.Error("the plan's controls are greyed")
		}
	})
}

// TestChoiceStaysLiveThroughStatusReplies (tc3712f): a choice question
// stays live through status replies and automated messages, and goes stale
// when a newer reply asks a question or the user answers.
func TestChoiceStaysLiveThroughStatusReplies(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		v := h.d.chat
		q := &chat.Item{Seq: 9600, Kind: chat.Assistant, Text: "Two ways.\n\nStrength: light, about 30% (recommended), or medium, about 50%?"}
		s.conv.Items = append(s.conv.Items, q,
			&chat.Item{Seq: 9601, Kind: chat.Assistant, Text: "Task 2 finished."},
			&chat.Item{Seq: 9602, Kind: chat.User, Text: "<cross-session-message from=\"x\">done</cross-session-message>"},
			&chat.Item{Seq: 9603, Kind: chat.Assistant, Text: "Noted."})
		v.show(s)
		if ok, live := v.choiceGate(q); !ok || !live {
			t.Fatalf("gate ok %v live %v after status replies", ok, live)
		}
		if a := v.answerAfter(q); a != "" {
			t.Errorf("an automated message counts as the answer: %q", a)
		}
		if openChoiceItem(s.conv) != q {
			t.Error("the phone's open question is not this one")
		}
		o, _ := v.renderChoiceReply(q)
		var labels []string
		deepWalk(o, func(x fyne.CanvasObject) {
			if b, ok := x.(*widget.Button); ok && !b.Disabled() {
				labels = append(labels, b.Text)
			}
		})
		if strings.Join(labels, "|") != "Light, about 30% (recommended)|Medium, about 50%" {
			t.Errorf("live buttons %q", labels)
		}
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9604, Kind: chat.Assistant, Text: "Also: tabs or spaces?"})
		if _, live := v.choiceGate(q); live {
			t.Error("a newer question leaves the old one live")
		}
	})
}

// TestStatusTurnAddsNoCardOverQuestion (tc3712f): a status-only turn with a
// blocked call adds no plan card over an open choice question.
func TestStatusTurnAddsNoCardOverQuestion(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hello")
	})
	h.eventually("idle", func() bool { return s.proc != nil && !s.conv.Busy })
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
		userEvent(h, s, "a request", nil)
		q := &chat.Item{Seq: 9700, Kind: chat.Assistant, Text: "Strength: light, about 30% (recommended), or medium, about 50%?"}
		s.conv.Items = append(s.conv.Items, q)
		endTurn(t, h, s)
		s.planWait = true // a call in the notification's turn was blocked
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9701, Kind: chat.Assistant, Text: "Task 3 finished."})
		endTurn(t, h, s)
		for _, it := range s.conv.Items {
			if it.Kind == chat.Plan {
				t.Fatal("a status reply added a plan card over the open question")
			}
		}
		h.d.chat.show(s)
		if _, live := h.d.chat.choiceGate(q); !live {
			t.Error("the question is no longer live")
		}
		// A status reply that asks for approval still gets its card.
		s.planWait = true
		s.conv.Items = append(s.conv.Items, &chat.Item{Seq: 9702, Kind: chat.Assistant, Text: "1. Run x.\n\n**Can I run step 1? Yes (recommended) or no?**"})
		endTurn(t, h, s)
		found := false
		for _, it := range s.conv.Items {
			found = found || it.Kind == chat.Plan
		}
		if !found {
			t.Error("an approval question after it got no card")
		}
	})
}
