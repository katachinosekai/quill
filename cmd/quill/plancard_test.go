package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// User 2026-11-21: a blocked call followed by an either/or question showed
// "Approve plan & run all"; the question needs its option buttons instead.
func TestPlanCardOnlyForApprovalQuestions(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		cards := func(s *deskSession) int {
			n := 0
			for _, it := range s.conv.Items {
				if it.Kind == chat.Plan && it.PermState == chat.PermPending {
					n++
				}
			}
			return n
		}
		end := func(blocked bool, text string) *deskSession {
			s := h.d.createSession(h.proj, "default", "default")
			h.d.open(s.id)
			s.conv.AddUser("a request")
			s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: text})
			s.planWait = blocked
			h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
			return s
		}
		which := "Nothing has run.\n\n**Which did you mean: option 1 (Quill's paired PCs and phones), option 2 (Claude Code's Remote Control through claude.ai), or both? Once you answer, I'll write the full plan for approval.**"
		s := end(true, which)
		if cards(s) != 0 || s.planWait {
			t.Fatal("a blocked call + a which-question: no plan card")
		}
		if _, opts, ok := parseChoices(s.conv.Items[len(s.conv.Items)-1].Text[len("Nothing has run.\n\n"):]); !ok || len(opts) != 3 || opts[0] != "Option 1" || opts[2] != "Both" {
			t.Fatalf("the question gets three option buttons: %v %v", opts, ok)
		}
		if s := end(true, "1. Edit x.\n\n**Do you approve steps 1–4?**"); cards(s) != 1 {
			t.Fatal("a blocked call + an approval question: plan card")
		}
		if s := end(true, "1. Edit x.\n2. Build."); cards(s) != 1 {
			t.Fatal("a blocked call and no question: plan card, as before")
		}
		next := "**Should I do the plan-card fix next (an either/or question gets option buttons instead of \"Approve plan & run all\"), or one of the missing panel options?**"
		s = end(false, next)
		if cards(s) != 0 {
			t.Fatal("a quoted \"Approve\" doesn't make a plan card")
		}
		if _, opts, ok := parseChoices(next); !ok || len(opts) != 2 || opts[0] != "Do the plan-card fix next" {
			t.Fatalf("it gets two option buttons: %v", opts)
		}
	})
}

func TestChoiceButtonsSkipWhQuestions(t *testing.T) {
	for line, want := range map[string]int{
		"**Which of the missing panel options (/rewind, /memory) should I build next, or is there something else first?**": 0,
		"**Which panels should I build: all of them in that order, only the first four, or a specific set you name?**":     3,
		"Should I do the plan-card fix next, or one of the missing panel options?":                                         2,
		"How should I name it, or should I skip it?":                                                                       0,
	} {
		_, opts, _ := parseChoices(line)
		if len(opts) != want {
			t.Errorf("%q: %d options %v, want %d", line, len(opts), opts, want)
		}
	}
}
