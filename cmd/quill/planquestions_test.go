package main

import (
	"strings"
	"testing"
)

func qsSummary(qs []planQ) string {
	var out []string
	for _, q := range qs {
		s := string(q.Kind)
		if len(q.Options) > 0 {
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
