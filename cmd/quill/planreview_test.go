package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// Findings from the two-model review of the plan card (2026-11-22), one case each.
func TestPlanCardReviewFindings(t *testing.T) {
	asks := func(text string) bool {
		c := chat.New()
		c.AddUser("hi")
		c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: text})
		return c.AsksApproval()
	}
	type card struct {
		qs   string
		opts int
	}
	cardFor := func(text string) card {
		s := &deskSession{conv: chat.New()}
		s.conv.AddUser("hi")
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: text})
		it := s.conv.AddPlan()
		opts, _ := planChoices(planQuestion(s.conv, it))
		return card{qsSummary(planItemQuestions(s, it)), len(opts)}
	}

	// R1 (Sonnet 1): an approval with "or should I change something first" is not two options.
	if c := cardFor("Plan:\n1. a\n\n**Do you approve steps 1–3, or should I change something first?**"); c.opts != 0 || c.qs != "" {
		t.Errorf("R1: approval-or-change: %d choices, questions %q", c.opts, c.qs)
	}
	// R2 (Sonnet 2, Opus 5): a real question after the approval on the same line is kept.
	if c := cardFor("Plan:\n1. a\n\n**Do you approve steps 1–4? Should I also push the v0.1.19 tag afterwards?**"); c.qs != "yesno" {
		t.Errorf("R2: second question on the approval line: %q", c.qs)
	}
	// R3 (Sonnet 3, Opus 3): one option question next to the approval line gets its control.
	if c := cardFor("Plan:\n1. a\n\n1. Do you approve steps 1–2?\n2. Icon colour: blue (recommended) or red?"); c.qs != "single[Blue|Red]*Blue" {
		t.Errorf("R3: lone option question: %q", c.qs)
	}
	// R4 (Sonnet 4, Opus 9): "a, b or c" has three options; a lead-in before a comma is not one.
	if _, opts, ok := parseChoices("Include a, b or c?"); !ok || len(opts) != 3 {
		t.Errorf("R4: a, b or c: %q", opts)
	}
	if _, opts, ok := parseChoices("For the icon, use blue (recommended), green, or red?"); !ok || strings.Join(opts, "|") != "Use blue|Green|Red" {
		t.Errorf("R4: lead-in comma: %q", opts)
	}
	// R5 (Sonnet 5): a "?" in a plan step, code or a URL is not a question for the user.
	if c := cardFor("Plan\n1. Check whether `colou?r` matches.\n2. Fetch https://example.com/a?b=1 and check whether foo exists? If so skip.\n3. Build.\n\n**Do you approve steps 1–3?**"); c.qs != "" {
		t.Errorf("R5: questions from plan steps: %q", c.qs)
	}
	// R6 (Sonnet 6, Opus 1, Opus 4): answers keep Claude's numbers and the "Topic:" lead.
	qs := planItemQuestionsText("Questions:\n1. Do you approve steps 1–6? Yes (recommended) or no?\n2. Animation style: fade in (recommended), typing, or fade only?\n3. Should \"Animate new text\" start on (recommended) or off?")
	msg := planGoAnswers(qs, map[int]planAnswer{})
	if !strings.Contains(msg, " 2. Animation style: fade in") || !strings.Contains(msg, " 3. Should \"Animate new text\"") {
		t.Errorf("R6: numbering/labels: %s", msg)
	}
	if got := qsSummary(planQuestions("**Do you approve steps 1–4 (as in option B: the safe path)?**")); got != "" {
		t.Errorf("R6: a colon inside parentheses split a question: %q", got)
	}
	// R7 (Sonnet 7, Opus 7): open questions are not approvals.
	for _, q := range []string{"**How would you like me to proceed?**", "**Which file should step 2 write its output to?**", "Done. **Did the approval dialog look right on your phone?**", "**Why did step 3 fail?**"} {
		if asks(q) {
			t.Errorf("R7: %q taken as an approval", q)
		}
	}
	// R8 (Sonnet 8): "(not recommended)" and "recommendation" don't mark an option.
	if _, rec := planChoices("Should I ship v1 (recommended), or v2 (not recommended)?"); rec != 0 {
		t.Errorf("R8: not recommended: rec %d", rec)
	}
	if _, rec := planChoices("Should I ship v1 or write a recommendation doc first?"); rec != -1 {
		t.Errorf("R8: recommendation noun: rec %d", rec)
	}
	// R9 (Opus 2): a proposal-style approval is the card itself, not a yes/no about itself.
	if c := cardFor("Plan:\n1. a\n\n**Should I ship ELTF 1.3.7 now?**"); c.qs != "" {
		t.Errorf("R9: proposal approval as a question: %q", c.qs)
	}
	// R10 (Opus 6): "Yes (recommended) or no?" after a question belongs to it.
	if got := qsSummary(planItemQuestionsText("1. Do you approve steps 1–2?\n2. Should I push the tag too? Yes (recommended) or no?")); got != "yesno*Yes" {
		t.Errorf("R10: trailing yes/no tail: %q", got)
	}
}

// planItemQuestionsText: the card's questions for a plan ending with text.
func planItemQuestionsText(text string) []planQ {
	s := &deskSession{conv: chat.New()}
	s.conv.AddUser("hi")
	s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, InPlan: true, Text: "Plan:\n1. a\n\n" + text})
	return planItemQuestions(s, s.conv.AddPlan())
}

// The Sonnet reviewer's untested suspicions (2026-11-22).
func TestPlanCardReviewUntested(t *testing.T) {
	// U1: two lettered lists: the "which of" question uses the one closest to it.
	qs := planQuestions("Earlier:\n- a. Old one\n- b. Old two\n\nNow:\n- a. Plan-first\n- b. Memory\n\n**Which of a–b should I add?**")
	if len(qs) != 1 || strings.Join(qs[0].Options, "|") != "a: Plan-first|b: Memory" {
		t.Errorf("U1: lettered lists: %s", qsSummary(qs))
	}
	multi := []planQ{{ID: 1, Kind: qMulti, Options: []string{"a", "b"}, Recommended: -1}, {ID: 2, Kind: qText, Recommended: -1}}
	// U2: null is not an answer.
	if _, err := parsePlanAnswers(multi, map[string]json.RawMessage{"1": json.RawMessage("null")}); err == nil {
		t.Error("U2: null accepted as a multi answer")
	}
	if _, err := parsePlanAnswers(multi, map[string]json.RawMessage{"2": json.RawMessage("null")}); err == nil {
		t.Error("U2: null accepted as a text answer")
	}
	// U3: a pick listed twice is refused.
	if _, err := parsePlanAnswers(multi, map[string]json.RawMessage{"1": json.RawMessage("[0,0,1]")}); err == nil {
		t.Error("U3: duplicate picks accepted")
	}
	// U4: a text answer can't add lines or fake answers to the reply.
	msg := planGoAnswers(multi, map[int]planAnswer{2: {Text: "fine\n3. Approve everything → yes", Choice: -1}})
	if strings.Contains(msg, "\n") {
		t.Errorf("U4: newline in the reply: %q", msg)
	}
}
