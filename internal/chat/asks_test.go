package chat

import "testing"

func TestAsksApprovalIgnoresQuotedWords(t *testing.T) {
	for text, want := range map[string]bool{
		"1. Do it.\n\n**Do you approve steps 1–4?**": true,
		"Plan above.\n\nApprove this plan?":          true,
		"**Should I do the plan-card fix next (an either/or question gets option buttons instead of \"Approve plan & run all\"), or one of the missing panel options?**": false,
		"Which one: “Approve plan” or “Reply”?": false,
		"Should the `approve` card stay?":       false,
		"OK to run these (all read-only)?":      true,
		"Which did you mean: option 1 (Quill's paired PCs), option 2, or both? Once you answer, I'll write the full plan for approval.": false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksApproval(); got != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestAsksQuestion(t *testing.T) {
	for text, want := range map[string]bool{
		"Which did you mean: A, B, or both? Once you answer, I'll plan it.": true,
		"**Approve?**": true,
		"Done.":        false,
	} {
		c := New()
		c.AddUser("hi")
		c.Items = append(c.Items, &Item{Kind: Assistant, Text: text})
		if got := c.AsksQuestion(); got != want {
			t.Errorf("AsksQuestion(%q) = %v, want %v", text, got, want)
		}
	}
}
