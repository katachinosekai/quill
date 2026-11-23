package main

import (
	"encoding/json"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

func dialogItem(in json.RawMessage) *chat.Item {
	return &chat.Item{Kind: chat.Permission, ToolName: "AskUserQuestion", Input: in}
}

func dialogInput(pre map[string]string, qs ...map[string]any) json.RawMessage {
	in := map[string]any{"questions": qs}
	if pre != nil {
		in["answers"] = pre
	}
	b, _ := json.Marshal(in)
	return b
}

func dq(question string, multi bool, labels ...string) map[string]any {
	var opts []map[string]string
	for _, l := range labels {
		opts = append(opts, map[string]string{"label": l, "description": "d"})
	}
	return map[string]any{"question": question, "header": "H", "multiSelect": multi, "options": opts}
}

func TestDialogApproval(t *testing.T) {
	yn := dq("Merge it?", false, "Yes (recommended)", "No")
	other := dq("Which color?", false, "Red", "Blue")
	for name, c := range map[string]struct {
		in      json.RawMessage
		answers map[string]string
		goAhead bool
		stop    bool
	}{
		"go pick":             {dialogInput(nil, yn), map[string]string{"Merge it?": "Yes (recommended)"}, true, false},
		"stop pick":           {dialogInput(nil, yn), map[string]string{"Merge it?": "No"}, false, true},
		"can I run":           {dialogInput(nil, dq("Can I run steps 1-3?", false, "Run them", "Stop")), map[string]string{"Can I run steps 1-3?": "Run them"}, true, false},
		"with another":        {dialogInput(nil, other, yn), map[string]string{"Which color?": "Red", "Merge it?": "Yes (recommended)"}, true, false},
		"stop wins":           {dialogInput(nil, yn, dq("Fix 2 and 3?", false, "Yes", "No")), map[string]string{"Merge it?": "Yes (recommended)", "Fix 2 and 3?": "No"}, false, true},
		"other question only": {dialogInput(nil, other), map[string]string{"Which color?": "Red"}, false, false},
		"information yes/no":  {dialogInput(nil, dq("Is that placement OK?", false, "Yes", "No")), map[string]string{"Is that placement OK?": "Yes"}, false, false},
		"no stop option":      {dialogInput(nil, dq("Merge it?", false, "Yes", "Later")), map[string]string{"Merge it?": "Yes"}, false, false},
		"no go option":        {dialogInput(nil, dq("Merge it?", false, "No", "Stop")), map[string]string{"Merge it?": "Yes"}, false, false},
		"typed other text":    {dialogInput(nil, yn), map[string]string{"Merge it?": "yes please"}, false, false},
		"multi select":        {dialogInput(nil, dq("Merge it?", true, "Yes", "No")), map[string]string{"Merge it?": "Yes"}, false, false},
		"pre-filled":          {dialogInput(map[string]string{"Merge it?": "Yes"}, yn), map[string]string{"Merge it?": "Yes"}, false, false},
	} {
		g, s := dialogApproval(c.in, c.answers)
		if g != c.goAhead || s != c.stop {
			t.Errorf("%s: go=%v stop=%v, want go=%v stop=%v", name, g, s, c.goAhead, c.stop)
		}
	}
}

func TestDialogGate(t *testing.T) {
	yn := dialogInput(nil, dq("Fix 2 and 3 (the stale cards)?", false, "Yes (recommended)", "No"))
	h := newHarness(t)
	s, plan := planCardSession(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		d := h.d
		d.planFirst = true
		d.dialogGate(s, dialogItem(yn), map[string]string{"Fix 2 and 3 (the stale cards)?": "Yes (recommended)"})
		if !s.runAll || !s.turnCovered {
			t.Fatal("a go-ahead pick starts the plan")
		}
		d.dialogGate(s, dialogItem(yn), map[string]string{"Fix 2 and 3 (the stale cards)?": "No"})
		if s.runAll || !s.planRevoked {
			t.Fatal("a stop pick is Don't run")
		}
		other := dialogInput(nil, dq("Which color?", false, "Red", "Blue"))
		d.dialogGate(s, dialogItem(other), map[string]string{"Which color?": "Red"})
		if s.runAll {
			t.Fatal("other answers never approve")
		}
		s.autoTurn = true // a peer, channel or scheduled message started this turn
		d.dialogGate(s, dialogItem(yn), map[string]string{"Fix 2 and 3 (the stale cards)?": "Yes (recommended)"})
		if s.runAll {
			t.Fatal("an automatic turn is never approved by the dialog")
		}
		s.autoTurn = false
		d.planFirst = false
		d.dialogGate(s, dialogItem(yn), map[string]string{"Fix 2 and 3 (the stale cards)?": "Yes (recommended)"})
		if s.runAll {
			t.Fatal("without plan-first nothing starts")
		}
	})
	_ = plan
}
