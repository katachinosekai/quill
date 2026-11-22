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
		if _, ok := s.agentGrants["toolu_agentA"]; ag.PermState != chat.PermAutoAllowed || !ok {
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

// streamEvent parses a stream-json line the way the process reader does.
func streamEvent(t *testing.T, line string) *protocol.Event {
	t.Helper()
	ev, err := protocol.ParseLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// subagentToolUse: a subagent's assistant event with one tool_use block.
func subagentToolUse(t *testing.T, h *harness, s *deskSession, parent, id, tool string) {
	ev := streamEvent(t, `{"type":"assistant","message":{"id":"msg_`+id+`","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"tool_use","id":"`+id+`","name":"`+tool+`","input":{}}]},"parent_tool_use_id":"`+parent+`","session_id":"s1"}`)
	h.d.planGate(s, ev, s.conv.Apply(ev))
}

// User 2026-11-22, "track by subagent": a granted subagent's calls go ahead
// while the user talks to the session meanwhile; the session's own calls and
// an ungranted subagent's calls still meet the gate.
func TestGrantsTrackedBySubagent(t *testing.T) {
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
		bg := gateCall(h, s, "a1", "Agent", `{"prompt":"do the feature","run_in_background":true}`, "toolu_agentBG")
		fg := gateCall(h, s, "a2", "Agent", `{"prompt":"review it"}`, "toolu_agentFG")
		if bg.PermState != chat.PermAutoAllowed || fg.PermState != chat.PermAutoAllowed || len(s.agentGrants) != 2 {
			t.Fatalf("both subagents granted: %v %v %v", bg.PermState, fg.PermState, s.agentGrants)
		}
		// The turn ends; the user sends a message while they run: a new
		// turn, and the approval is reset.
		s.inTurn = false
		userEvent(h, s, "how is it going?", nil)
		if !s.inTurn || s.runAll {
			t.Fatalf("a new user turn without approval: inTurn %v runAll %v", s.inTurn, s.runAll)
		}
		// (1) the granted subagent's calls still go ahead.
		subagentToolUse(t, h, s, "toolu_agentBG", "toolu_bgEdit", "Edit")
		if e := gateCall(h, s, "e1", "Edit", `{"file_path":"/tmp/wt/a.go"}`, "toolu_bgEdit"); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("a granted subagent's call mid-turn goes ahead: %v", e.PermState)
		}
		// (2) the main session's own call in this turn is gated.
		if e := gateCall(h, s, "e2", "Edit", `{"file_path":"/tmp/wt/b.go"}`, "toolu_mainEdit"); e.PermState == chat.PermAutoAllowed {
			t.Fatal("the session's own call follows the gate")
		}
		// (3) a subagent nobody granted is gated.
		subagentToolUse(t, h, s, "toolu_agentOther", "toolu_otherEdit", "Edit")
		if e := gateCall(h, s, "e3", "Edit", `{"file_path":"/tmp/wt/c.go"}`, "toolu_otherEdit"); e.PermState == chat.PermAutoAllowed {
			t.Fatal("an ungranted subagent's call is gated")
		}
		// (5) a nested agent inherits its root grant.
		subagentToolUse(t, h, s, "toolu_agentBG", "toolu_nested", "Agent")
		if e := gateCall(h, s, "n1", "Agent", `{"prompt":"sub-step"}`, "toolu_nested"); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("the granted subagent starts a nested agent: %v", e.PermState)
		}
		subagentToolUse(t, h, s, "toolu_nested", "toolu_nestedBash", "Bash")
		if e := gateCall(h, s, "n2", "Bash", `{"command":"go test ./..."}`, "toolu_nestedBash"); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("the nested agent's call inherits the grant: %v", e.PermState)
		}
		if s.agentCalls["toolu_nestedBash"] != "toolu_agentBG" {
			t.Fatalf("nested call mapped to the root grant: %v", s.agentCalls)
		}
		// The foreground agent ends with its tool result; the background one doesn't.
		subagentToolUse(t, h, s, "toolu_agentFG", "toolu_fgRead", "Bash")
		res := streamEvent(t, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_agentFG","type":"tool_result","content":"done"},{"tool_use_id":"toolu_agentBG","type":"tool_result","content":"Async agent launched"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		h.d.planGate(s, res, s.conv.Apply(res))
		if _, ok := s.agentGrants["toolu_agentFG"]; ok {
			t.Fatal("the foreground agent's grant ends with its result")
		}
		if _, ok := s.agentCalls["toolu_fgRead"]; ok {
			t.Fatal("its calls are forgotten")
		}
		if _, ok := s.agentGrants["toolu_agentBG"]; !ok {
			t.Fatal("a background agent's launch result doesn't end its grant")
		}
		// (4) the notification ends the background grant; later calls are gated.
		userEvent(h, s, "<task-notification>\n<task-id>x</task-id>\n<tool-use-id>toolu_agentBG</tool-use-id>\n<status>completed</status>\n</task-notification>", nil)
		if len(s.agentGrants) != 0 || len(s.agentCalls) != 0 {
			t.Fatalf("grant and its calls end: %v %v", s.agentGrants, s.agentCalls)
		}
		subagentToolUse(t, h, s, "toolu_agentBG", "toolu_lateEdit", "Edit")
		if e := gateCall(h, s, "e4", "Edit", `{"file_path":"/tmp/wt/d.go"}`, "toolu_lateEdit"); e.PermState == chat.PermAutoAllowed {
			t.Fatal("after the notification its calls are gated")
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
