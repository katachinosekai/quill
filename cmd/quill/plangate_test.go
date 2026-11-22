package main

import (
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func gateCall(h *harness, s *deskSession, id, tool, input, toolUseID string) *chat.Item {
	perm := &chat.Item{Kind: chat.Permission, ToolName: tool, RequestID: id, Input: []byte(input),
		Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: tool, Input: []byte(input), ToolUseID: toolUseID}}
	s.conv.Items = append(s.conv.Items, perm)
	h.d.planGate(s, &protocol.Event{Type: "control_request", RequestID: id, Request: perm.Request}, []*chat.Item{perm})
	return perm
}

func userEvent(h *harness, s *deskSession, text string, parent *string) {
	it := s.conv.AddUser(text)
	h.d.planGate(s, &protocol.Event{Type: "user", ParentToolUseID: parent}, []*chat.Item{it})
}

// User 2026-11-22: an approved plan covers the subagents it starts, and peer
// messages, task notifications and subagent traffic neither approve nor end
// an approved plan (Kmem Lock Audit's findings).
func TestApprovedPlanCoversItsSubagents(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("a running, idle session", func() bool { return s.proc != nil && !s.conv.Busy })
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
		userEvent(h, s, planGoMessage, nil)
		if !s.runAll {
			t.Fatal("approved")
		}
		ag := gateCall(h, s, "a1", "Agent", `{"prompt":"do the feature","run_in_background":true}`, "toolu_agentA")
		if ag.PermState != chat.PermAutoAllowed || !s.agentGrants["toolu_agentA"] {
			t.Fatalf("the approved plan starts its subagent and covers it: state %v grants %v proc %v runAll %v", ag.PermState, s.agentGrants, s.proc != nil, s.runAll)
		}
		// The turn ends with a question; the approval ends for this session.
		s.inTurn = false
		s.runAll = false
		// A subagent's own message and a peer message don't change anything.
		parent := "toolu_agentA"
		userEvent(h, s, "subagent prompt", &parent)
		userEvent(h, s, `<cross-session-message from="x">hi</cross-session-message>`, nil)
		if s.inTurn {
			t.Fatal("not the user: no new turn")
		}
		edit := gateCall(h, s, "e1", "Edit", `{"file_path":"/tmp/wt/x.go"}`, "")
		if edit.PermState != chat.PermAutoAllowed {
			t.Fatalf("a subagent's call while this session's turn is over goes ahead: %v", edit.PermState)
		}
		// Its notification ends the grant; later calls need a plan again.
		userEvent(h, s, "<task-notification>\n<task-id>x</task-id>\n<tool-use-id>toolu_agentA</tool-use-id>\n<status>completed</status>\n</task-notification>", nil)
		if len(s.agentGrants) != 0 {
			t.Fatal("the finished subagent's grant ends")
		}
		s.inTurn = false
		late := gateCall(h, s, "e2", "Edit", `{"file_path":"/tmp/wt/y.go"}`, "")
		if late.PermState == chat.PermAutoAllowed {
			t.Fatal("no grant, no approval: the gate asks")
		}
	})
}

func TestPeerMessagesDontEndApproval(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "auto")
		userEvent(h, s, planGoMessage, nil)
		s.inTurn = false // between turns of the approved plan
		userEvent(h, s, `<cross-session-message from="peer">status</cross-session-message>`, nil)
		userEvent(h, s, "<task-notification>\n<tool-use-id>toolu_x</tool-use-id>\n</task-notification>", nil)
		if !s.runAll {
			t.Fatal("a peer message or task notification doesn't end the approved plan")
		}
		userEvent(h, s, "do something else instead", nil)
		if s.runAll {
			t.Fatal("a new request from the user still ends it")
		}
	})
}

func TestChoiceYesApprovesThePlan(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "haiku", "auto")
		s.conv.AddUser("a request")
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan.\n\n1. Can I run steps 1–6?\nYes (recommended) or no?"})
		if !approvesPlan(s, choiceReply(0, "Yes")) {
			t.Fatal("picking Yes on the approval question approves the plan")
		}
		if approvesPlan(s, choiceReply(1, "No")) {
			t.Fatal("No doesn't")
		}
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Which colour: blue (recommended) or red?"})
		if approvesPlan(s, choiceReply(0, "Blue")) {
			t.Fatal("a pick that isn't an approval doesn't approve")
		}
	})
}

func TestReadOnlyKmem(t *testing.T) {
	for cmd, want := range map[string]bool{
		"kmem brief":                            true,
		`kmem --as "Quill" get quill/decisions`: false,
		"kmem get quill/decisions":              true,
		"kmem find lock audit":                  true,
		"kmem show f37efdb":                     true,
		"kmem task list --json":                 true,
		"kmem handoff get quill":                true,
		"kmem trash list":                       true,
		"kmem fixed":                            true,
		"kmem fixed quill/decisions":            false,
		"kmem add quill/x text":                 false,
		"kmem task claim t1":                    false,
		"kmem handoff save quill":               false,
		"kmem trash empty":                      false,
		"kmem trash f1 why":                     false,
	} {
		if got := readOnlyCommand(cmd, nil); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
