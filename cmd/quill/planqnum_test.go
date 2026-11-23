package main

import (
	"strings"
	"testing"
)

const approveYesNoPlan = "Plan\n- step one\n\n" +
	"1. Approve this server plan? yes (recommended) or no?\n" +
	"2. Run the check now? yes (recommended) or no?\n" +
	"3. Which name should it use?\n" +
	"4. Ship it in the next build once the test passes? yes (recommended) or no?"

// t831705 (b): "yes (recommended) or no?" repeated on every question gives each
// yes/no buttons, lower case or not.
func TestPlanYesNoLowercaseOnEveryQuestion(t *testing.T) {
	qs := planQuestions(approveYesNoPlan)
	var kinds []planQKind
	for _, q := range qs {
		kinds = append(kinds, q.Kind)
	}
	if len(qs) != 3 || kinds[0] != qYesNo || kinds[1] != qText || kinds[2] != qYesNo || qs[2].Recommended != 0 {
		t.Fatalf("questions %+v", qs)
	}
}

// t831705 (a): the shown number is the plan's own, a gap is noted, and the
// reply uses the same numbers.
func TestPlanQuestionNumbersKeepOriginals(t *testing.T) {
	qs := planQuestionsFor("Approve this server plan? yes (recommended) or no?", approveYesNoPlan)
	if len(qs) != 3 {
		t.Fatalf("questions %+v", qs)
	}
	var nums []int
	for i, q := range qs {
		nums = append(nums, planQNum(q, i))
	}
	if nums[0] != 2 || nums[1] != 3 || nums[2] != 4 {
		t.Fatalf("numbers %v", nums)
	}
	if g := planQGaps(qs); g != "1. answered by Approve" {
		t.Fatalf("gaps %q", g)
	}
	yes := true
	reply := planGoAnswers(qs, map[int]planAnswer{1: {Yes: &yes, Choice: -1}, 3: {Yes: &yes, Choice: -1}})
	if !strings.Contains(reply, " 2. Run the check now? → yes.") || !strings.Contains(reply, " 4. Ship it") || strings.Contains(reply, " 1. ") {
		t.Fatalf("reply %q", reply)
	}
	if g := planQGaps([]planQ{{Num: 1}, {Num: 2}}); g != "" {
		t.Fatalf("no gap, got %q", g)
	}
}

// A missing number that was a plan step, not the approval question, gets no note.
func TestPlanGapNoteOnlyForApprovalLine(t *testing.T) {
	text := "Plan\n\n1. Do the first thing\n2. Do the second thing\n\n" +
		"3. Approve this plan? yes (recommended) or no?\n4. Which name should it use?\n6. Run the check now? yes (recommended) or no?"
	qs := planQuestionsFor("Approve this plan? yes (recommended) or no?", text)
	if len(qs) != 2 {
		t.Fatalf("questions %+v", qs)
	}
	if g := planQGaps(qs); g != "3. answered by Approve" {
		t.Fatalf("gaps %q", g)
	}
	// Steps 1 and 2 are not mentioned; 5 is just missing.
	if g := planQGaps([]planQ{{Num: 4}, {Num: 6}}); g != "" {
		t.Fatalf("no approval line, got %q", g)
	}
}

func TestChoicePicksMultiReply(t *testing.T) {
	got := choicePicks("Question 1: I pick the first option: Red.\nQuestion 2: I pick the first option: Yes.")
	if len(got) != 2 || got[1] != "Yes" {
		t.Fatalf("picks %q", got)
	}
	if choicePicks("Question 1: I pick the first option: Red.\nand more") != nil || choicePicks("hello") != nil {
		t.Fatal("non-pick text read as picks")
	}
}
