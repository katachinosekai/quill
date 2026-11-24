package chat

import "testing"

// td0cabd (user 2026-11-24): closing yes/no proposals are approval questions.
func TestYesNoProposalForms(t *testing.T) {
	for text, want := range map[string]bool{
		"**Fix both and commit? Yes (recommended) or no?**":                       true,
		"**Fix both and commit? Yes (recommended) / no?**":                        true,
		"**Fix both and commit? (yes/no)**":                                       true,
		"**Make the change and rerun the tests? Yes (recommended) or no?**":       true,
		"**Next: wire it into the gate and add tests? Yes (recommended) or no?**": true,
		"**I'd fix both and push. OK? Yes (recommended) or no?**":                 true,
		"**Fix both? Yes (recommended) or no?**\n\n1. Which branch: main or dev?": true,
		"**Use the new layout for all tabs? Yes (recommended) or no?**":           true, // user 2026-11-24
		"**Did the card show up? Yes or no?**":                                    false,
		"**Is that placement OK? Yes or no?**":                                    false,
		"The tests passed. OK? Yes or no?":                                        false,
		"**Which branch: main or dev?**":                                          false,
	} {
		if got := TextAsksApproval(text); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
	if got := NormYesNo("Merge it? Yes (recommended) / no?"); got != "Merge it? Yes (recommended) or no?" {
		t.Errorf("slash form: %q", got)
	}
	if got := NormYesNo("Merge it? (y/n)"); got != "Merge it? Yes or no?" {
		t.Errorf("paren form: %q", got)
	}
	// An explicit approval among the numbered questions wins over a yes/no
	// proposal there: that one is a choice.
	if q := ClosingApproval("1. Do you approve steps 1–2?\n2. Should I push the tag too? Yes (recommended) or no?"); q != "Do you approve steps 1–2?" {
		t.Errorf("explicit approval first: %q", q)
	}
}
