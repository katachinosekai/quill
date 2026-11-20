package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestPlanApprovalGoesToCardOwner(t *testing.T) {
	h := newHarness(t)
	var a, b *deskSession
	var plan *chat.Item
	h.ui(func() {
		a = h.d.createSession(h.proj, "haiku", "default")
		b = h.d.createSession(h.proj, "haiku", "default")
		a.conv.AddUser("swap the server")
		plan = a.conv.AddPlan()
		h.d.open(b.id)
		h.d.answerPlan(b, plan, true)
	})
	count := func(s *deskSession) int {
		n := 0
		for _, it := range s.conv.Items {
			if it.Kind == chat.User && it.Text == planGoMessage {
				n++
			}
		}
		return n
	}
	h.ui(func() {
		if count(b) != 0 {
			t.Fatal("the focused session must never get another session's approval")
		}
		if count(a) != 1 || plan.PermState != chat.PermAllowed {
			t.Fatalf("the card's own session gets it: %d %v", count(a), plan.PermState)
		}
	})
	h.eventually("A replies", func() bool { return !a.conv.Busy && replied(a, plan) })
	h.ui(func() {
		h.d.answerPlan(a, plan, true)
		if count(a) != 1 {
			t.Fatal("no resend once the session has replied")
		}
	})

	h.ui(func() {
		c := h.d.createSession(h.proj, "haiku", "default")
		c.conv.AddUser("x")
		p2 := c.conv.AddPlan()
		c.conv = chat.New()
		h.d.answerPlan(b, p2, true)
		if count(b) != 0 || len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("a card whose session is gone shows an error and sends nothing")
		}
	})
}

func TestBlockedRetryGoesToCardOwner(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "haiku", "default")
		b := h.d.createSession(h.proj, "haiku", "default")
		blocked := &chat.Item{Seq: 900, Kind: chat.Tool, ToolName: "Bash", Result: "blocked"}
		a.conv.Items = append(a.conv.Items, blocked)
		h.d.open(b.id)
		h.d.retryBlocked(b, blocked, nil, "")
		for _, it := range b.conv.Items {
			if it.Kind == chat.User {
				t.Fatal("retry went to the focused session")
			}
		}
		if it := a.conv.Items[len(a.conv.Items)-1]; it.Kind != chat.User {
			t.Fatal("retry goes to the card's session")
		}
	})
}

func TestPlanCardSurvivesOtherTurns(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "default")
		s.conv.AddUser("fix the tabs")
		plan := s.conv.AddPlan()
		s.conv.Apply(&protocol.Event{Type: "result", Subtype: "success"})
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		if plan.PermState != chat.PermPending {
			t.Fatal("a turn ending (peer message, background task) must not take the plan card away")
		}
		pending := 0
		for _, it := range s.conv.PendingPermissions() {
			if it.Kind == chat.Plan {
				pending++
			}
		}
		if pending != 1 {
			t.Fatalf("one plan card at a time: %d", pending)
		}
		h.d.sendTo(s, "actually do something else")
		if plan.PermState != chat.PermCancelled {
			t.Fatal("a new request replaces the plan")
		}
	})
}

func TestRetryRunsOnlyTheBlockedCommand(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "auto")
		s.runAll = true
		blocked := &chat.Item{Seq: 900, Kind: chat.Tool, ToolName: "Bash", Input: []byte(`{"command":"git push origin master"}`), Result: "blocked"}
		s.conv.Items = append(s.conv.Items, blocked)
		h.d.retryBlocked(s, blocked, nil, "")
		if s.retryCmd != "git push origin master" || !s.runAll {
			t.Fatal("the retry message neither resets nor widens the approval")
		}
		s.runAll = false
		perm := &chat.Item{Kind: chat.Permission, ToolName: "Bash", RequestID: "r1", Input: []byte(`{"command":"git push origin master"}`), Request: &protocol.ControlRequest{Subtype: "can_use_tool"}}
		s.conv.Items = append(s.conv.Items, perm)
		h.d.planGate(s, &protocol.Event{Type: "control_request", RequestID: "r1", Request: perm.Request}, []*chat.Item{perm})
		if perm.PermState != chat.PermRetryAllowed || s.retryCmd != "" {
			t.Fatalf("the retried command is allowed once by the card's approval, state %v", perm.PermState)
		}
		other := &chat.Item{Kind: chat.Permission, ToolName: "Bash", RequestID: "r2", Input: []byte(`{"command":"git push --force"}`), Request: &protocol.ControlRequest{Subtype: "can_use_tool"}}
		s.conv.Items = append(s.conv.Items, other)
		h.d.planGate(s, &protocol.Event{Type: "control_request", RequestID: "r2", Request: other.Request}, []*chat.Item{other})
		if other.PermState == chat.PermRetryAllowed || other.PermState == chat.PermAutoAllowed {
			t.Fatal("the approval covers only one run of the exact blocked command")
		}
		h.d.retryBlocked(s, blocked, []protocol.Rule{{Tool: "Bash", Content: "git push *"}}, "session")
		again := &chat.Item{Kind: chat.Permission, ToolName: "Bash", RequestID: "r3", Input: []byte(`{"command":"git push origin master"}`), Request: &protocol.ControlRequest{Subtype: "can_use_tool"}}
		s.conv.Items = append(s.conv.Items, again)
		h.d.planGate(s, &protocol.Event{Type: "control_request", RequestID: "r3", Request: again.Request}, []*chat.Item{again})
		if again.PermState != chat.PermRuleAllowed || s.retryRules != nil {
			t.Fatal("Always allow sends its rules with the retried command's approval")
		}
		if !approves(planGoMessage) || approves(retryPrefix+"rm -rf x") {
			t.Fatal("a retry approves only its own command, never the whole plan")
		}
	})
}

func TestSuggestedRules(t *testing.T) {
	bash := func(c string) *chat.Item {
		in, _ := json.Marshal(map[string]string{"command": c})
		return &chat.Item{Kind: chat.Tool, ToolName: "Bash", Input: in}
	}
	for c, want := range map[string]string{
		"kill -TERM 1 2; sleep 3; ps -p 1 || echo gone":   "Bash(kill *)\nBash(sleep *)",
		"cd ~/x && for id in a b; do gio trash $id; done": "Bash(gio trash *)",
		"ssh pzserver powershell -File x.ps1":             "Bash(ssh pzserver *)",
		"git push origin master":                          "Bash(git push *)",
		"cat <<EOF > f\nx\nEOF":                           "Bash(cat *)",
	} {
		if got := strings.Join(suggestedRules(bash(c)), "\n"); got != want {
			t.Errorf("suggestedRules(%q) = %q, want %q", c, got, want)
		}
	}
	if got := suggestedRules(&chat.Item{Kind: chat.Tool, ToolName: "Edit"}); len(got) != 1 || got[0] != "Edit" {
		t.Fatalf("other tools suggest the tool name, got %v", got)
	}
	rules, err := parseRules("Bash(gio trash *)\n\nEdit\n")
	if err != nil || len(rules) != 2 || rules[0].Content != "gio trash *" || rules[1].Tool != "Edit" {
		t.Fatalf("parseRules: %v %v", rules, err)
	}
	for _, bad := range []string{"", "Bash(", "Bash()", "rm -rf x", "(x)"} {
		if _, err := parseRules(bad); err == nil {
			t.Errorf("parseRules(%q) should fail", bad)
		}
	}
}
