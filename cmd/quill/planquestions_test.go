package main

import (
	"strings"
	"testing"
)

func qsSummary(qs []planQ) string {
	var out []string
	for _, q := range qs {
		s := string(q.Kind)
		if len(q.Options) > 0 && q.Kind != qYesNo {
			s += "[" + strings.Join(q.Options, "|") + "]"
		}
		if q.Recommended >= 0 {
			s += "*" + q.Options[q.Recommended]
		}
		out = append(out, s)
	}
	return strings.Join(out, " ; ")
}

func TestPlanQuestions(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"**Suggested improvements (pick any):**\n- **a. Plan-first on/off:** ask first or let Claude work.\n- **b. Memory:** kmem or auto memory.\n- **c. Pair a phone:** optional last step.\n- **d. A Skip button on every step**, keeping defaults.\n\n**Do you approve A (steps 1–4) and B (steps 5–9), and which of improvements a–d should I include?**",
			"multi[a: Plan-first on/off|b: Memory|c: Pair a phone|d: A Skip button on every step]"},
		{"Plan:\n1. Bundle.\n2. Test.\n\n**Do you approve steps 1–7, and do you also want the optional rule for numbering plan questions?**",
			"yesno"},
		{"1. Should v0.1.20 ship now (recommended), or wait for Quill Android's card?\n2. Should rule 21 stay as it is?\n3. What should the Welcome's title say?\n\n**Do you approve steps 1–3?**",
			"single[v0.1.20 ship now|Wait for Quill Android's card]*v0.1.20 ship now ; yesno ; text"},
		{"**Do you approve steps 1–5, including the push and the public release?**", ""},
		{"Plan:\n1. a\n\nQuestions:\n1. Do you approve steps 1–6? Yes (recommended) or no?\n2. Animation style: fade in each finished paragraph plus smooth reveal of the last one (recommended), typing effect character by character, or fade only?\n3. Should \"Animate new text\" start on (recommended) or off?",
			"single[Fade in each finished paragraph plus smooth reveal of the last one|Typing effect character by character|Fade only]*Fade in each finished paragraph plus smooth reveal of the last one ; single[\"Animate new text\" start on|Off]*\"Animate new text\" start on"},
		{"```\nwhat? or this?\n```\n**Approve this plan?**", ""},
		// User 2026-11-22: a question and its options on one line, bold.
		{"Plan.\n\n**1. Where should the chat live? Inside the window (recommended) or a linked session?**\n**2. Which model should it use? Same as new sessions (recommended), a cheaper model set separately, or none (no assistant)?**\n**3. Should I use 2 subagents? Yes, 2 subagents (recommended) or no, I'll do it myself?**",
			"single[Inside the window|A linked session]*Inside the window ; single[Same as new sessions|A cheaper model set separately|None]*Same as new sessions ; single[Yes, 2 subagents|No, I'll do it myself]*Yes, 2 subagents"},
		// … and on the next line, without a "?".
		{"Plan.\n\n1. Where should the chat live?\nInside the window (recommended) or a linked session\n2. Can I run steps 1–5?\nYes (recommended) or no",
			"single[Inside the window|A linked session]*Inside the window"},
		// A quoted example in prose above the numbered questions isn't one.
		{"The card split it: the problem is a question and its options on one line: \"Where…? A or B?\".\nAlso see \"why? because\".\n\n1. Should I fix it? Yes (recommended) or no?",
			"yesno*Yes"},
	} {
		if got := qsSummary(planQuestions(c.text)); got != c.want {
			t.Errorf("%q\n got: %s\nwant: %s", c.text[:min(60, len(c.text))], got, c.want)
		}
	}
}

func TestPlanGoAnswersText(t *testing.T) {
	qs := planQuestions("1. Should v0.1.20 ship now (recommended), or wait for Quill Android's card?\n2. Should rule 21 stay as it is?\n" +
		"- a. Plan-first\n- b. Memory\n- c. Pair a phone\n3. Which of a–c should I add?\n4. What should the title say?\n\n**Do you approve steps 1–3?**")
	if len(qs) != 4 {
		t.Fatalf("four questions: %s", qsSummary(qs))
	}
	yes := true
	msg := planGoAnswers(qs, map[int]planAnswer{
		1: {Choice: 1}, 2: {Yes: &yes, Choice: -1}, 3: {Picks: []int{0, 2}, Choice: -1},
	})
	for _, want := range []string{planGoMessage, "1. Should v0.1.20 ship now (recommended), or wait for Quill Android's card? → Wait for Quill Android's card.",
		"2. Should rule 21 stay as it is? → yes.", "3. Which of a–c should I add? → a: Plan-first; c: Pair a phone.", "4. What should the title say? → no answer."} {
		if !strings.Contains(msg, want) {
			t.Errorf("reply lacks %q:\n%s", want, msg)
		}
	}
	if !isPlanGo(msg) {
		t.Fatal("the reply is an approval")
	}
}

// Quill Android 2026-11-22: inline lettered options after a colon were a
// text box; they are a multi question, "All of a–e (recommended)" picking all.
func TestPlanQuestionsInlineLetters(t *testing.T) {
	const line = "1. Which parts should be built? All of a–e (recommended), or pick from: a) local check, b) remember home network, c) hardware-key proof, d) screen lock replaces the password, e) guardrails?"
	for _, c := range []struct {
		text, want string
		picks      int
	}{
		{line + "\n\n**Do you approve steps 1–4?**",
			"multi[a: local check|b: remember home network|c: hardware-key proof|d: screen lock replaces the password|e: guardrails]", 5},
		{"Plan.\n\n1. Which should I add? All (recommended), or pick from: a) a log, b) a badge?",
			"multi[a: a log|b: a badge]", 2},
		{"Plan.\n\n1. Which extras should I add? a) a log b) a badge c) a sound?",
			"multi[a: a log|b: a badge|c: a sound]", 0},
		{"Plan.\n\n1. Pick any of a–c: a) a log, b) a badge, or c) a sound?",
			"multi[a: a log|b: a badge|c: a sound]", 0},
		{"Optional parts: a) a log, b) a badge, c) a sound.\n\n1. Which should I add, pick any of a–c?",
			"multi[a: a log|b: a badge|c: a sound]", 0},
		{"Optional parts:\na) a log\nb) a badge\nc) a sound\n\n1. Which should I add, pick any of a–c?",
			"multi[a: a log|b: a badge|c: a sound]", 0},
	} {
		qs := planQuestions(c.text)
		if got := qsSummary(qs); got != c.want {
			t.Errorf("%q\n got: %s\nwant: %s", c.text[:min(60, len(c.text))], got, c.want)
			continue
		}
		if len(qs[0].RecommendedPicks) != c.picks {
			t.Errorf("%q: recommended picks %v, want %d", c.text[:min(60, len(c.text))], qs[0].RecommendedPicks, c.picks)
		}
	}
	if opts, rec := planChoices(line); opts != nil || rec != -1 {
		t.Errorf("planChoices(%q) = %q, %d; want no buttons", line, opts, rec)
	}
	// The card picks every checkbox to start with.
	qs := planQuestions(line)
	v := &chatView{}
	ans := v.planAnswers(nil, qs)
	if got := ans[qs[0].ID].Picks; len(got) != 5 || got[0] != 0 || got[4] != 4 {
		t.Errorf("starting picks %v, want all 5", got)
	}
}
