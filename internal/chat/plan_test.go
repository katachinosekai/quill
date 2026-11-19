package chat

import "testing"

func TestPlanTextMovesIntoApproval(t *testing.T) {
	c := New()
	c.AddUser("fix it")
	c.add(&Item{Kind: Permission, PermState: PermPlanAsked})
	c.add(&Item{Kind: Assistant, Text: "1. run tests"})
	c.add(&Item{Kind: Assistant, Text: "2. edit file"})
	marked := c.MarkPlanText()
	plan := c.AddPlan()
	if len(marked) != 2 || c.PlanText(plan) != "1. run tests\n\n2. edit file" {
		t.Fatalf("plan text: %q", c.PlanText(plan))
	}
	c.AddUser("next")
	c.add(&Item{Kind: Assistant, Text: "other"})
	if c.Items[len(c.Items)-1].InPlan {
		t.Fatal("later replies must stay in the chat")
	}
}
