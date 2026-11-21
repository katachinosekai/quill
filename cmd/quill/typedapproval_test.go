package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// A typed short approval right after a plan counts like the card, also when
// the plan's question has a note after it on the same line (user 2026-11-21).
func TestTypedApprovalAfterPlan(t *testing.T) {
	plan := "Plan:\n1. Bundle kmem 1.6.0.\n2. Check handoff saves.\n3. Test and commit.\n\n" +
		"**Do you approve steps 1–3? Your approval also covers the earlier question only if you say which option you want.**"
	for text, want := range map[string]bool{
		"Yes": true, "approved": true, "go ahead": true,
		"yes but skip step 2": false, "no": false, "what does step 2 do?": false,
	} {
		s := &deskSession{conv: chat.New()}
		s.conv.AddUser("bundle kmem 1.6.0")
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: plan})
		if got := approvesPlan(s, text); got != want {
			t.Errorf("approvesPlan(%q) = %v, want %v", text, got, want)
		}
	}
	idle := &deskSession{conv: chat.New()}
	idle.conv.AddUser("hi")
	idle.conv.Items = append(idle.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Done."})
	if approvesPlan(idle, "yes") {
		t.Error("a yes with no plan waiting approves nothing")
	}
}
