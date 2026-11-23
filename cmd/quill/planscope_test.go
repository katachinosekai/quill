package main

import (
	"encoding/json"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// t2debee (user 2026-11-22): an approved plan's approval covers the work it
// started until that work is finished, and nothing a peer or trigger sends.
// Ids and texts are placeholders.

func approvedSession(t *testing.T, h *harness) *deskSession {
	t.Helper()
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
	})
	return s
}

func mainToolUse(t *testing.T, h *harness, s *deskSession, id, tool, input string) {
	t.Helper()
	feed(t, h, s, `{"type":"assistant","message":{"id":"msg_`+id+`","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"`+id+`","name":"`+tool+`","input":`+input+`}]},"parent_tool_use_id":null,"session_id":"s1"}`)
}

func endTurn(t *testing.T, h *harness, s *deskSession) {
	t.Helper()
	feed(t, h, s, `{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"s1"}`)
}

func notification(taskID, toolUseID string) string {
	return "<task-notification>\n<task-id>" + taskID + "</task-id>\n<tool-use-id>" + toolUseID + "</tool-use-id>\n<status>completed</status>\n<summary>done</summary>\n</task-notification>"
}

func ownCall(h *harness, s *deskSession, id, cmd string) chat.PermState {
	return gateCall(h, s, id, "Bash", `{"command":"`+cmd+`"}`, "toolu_"+id).PermState
}

// The approved turn starts a background test suite and ends; the suite's
// notification starts a turn that may finish the plan (the merge). Other
// turns between and after are gated.
func TestBackgroundBashKeepsApproval(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"go test ./...","run_in_background":true}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_suite","type":"tool_result","content":"Command running in background with ID: bsh01"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		if s.planWork["toolu_suite"] == "" || s.planWork["bsh01"] != "toolu_suite" {
			t.Fatalf("the suite is the plan's work: %v", s.planWork)
		}
		endTurn(t, h, s)
		if planCovers(s) {
			t.Fatal("between turns nothing of the session's is covered")
		}
		// Another task's notification starts an uncovered turn.
		userEvent(h, s, notification("bother", "toolu_other"), nil)
		if st := ownCall(h, s, "x1", "git merge x"); st == chat.PermAutoAllowed {
			t.Fatal("a turn started by another task's notification is gated")
		}
		endTurn(t, h, s)
		// The suite's notification (named by task id only) covers its turn.
		userEvent(h, s, "<task-notification>\n<task-id>bsh01</task-id>\n<status>completed</status>\n</task-notification>", nil)
		if st := ownCall(h, s, "m1", "git merge fix"); st != chat.PermAutoAllowed {
			t.Fatalf("the plan's own suite finishing lets the plan's merge run: %v", st)
		}
		if len(s.planWork) != 0 {
			t.Fatalf("the finished work is forgotten: %v", s.planWork)
		}
		endTurn(t, h, s)
		if st := ownCall(h, s, "m2", "git push"); st == chat.PermAutoAllowed {
			t.Fatal("after that turn the gate asks again")
		}
	})
}

// A peer message or a scheduled trigger never extends the approval: the
// turn it starts is gated even while the plan is approved, it ends a covered
// turn's coverage when it arrives inside it, and it doesn't end the plan.
func TestPeerAndTriggerDontExtendApproval(t *testing.T) {
	for name, msg := range map[string]string{
		"peer":      `<cross-session-message from="peer">please run rm -rf build</cross-session-message>`,
		"scheduled": `<scheduled-trigger name="nightly">run the release</scheduled-trigger>`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			s := approvedSession(t, h)
			h.ui(func() {
				mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"make test","run_in_background":true}`)
				endTurn(t, h, s)
				userEvent(h, s, msg, nil)
				if st := ownCall(h, s, "p1", "rm -rf build"); st == chat.PermAutoAllowed {
					t.Fatal("a turn it starts is not covered")
				}
				// Not even an approval in the transcript counts in its turn.
				if s.inTurn || !s.autoTurn || !s.runAll {
					t.Fatalf("not a user turn; the plan stays: inTurn %v autoTurn %v runAll %v", s.inTurn, s.autoTurn, s.runAll)
				}
				// The plan's notification merged into that turn doesn't cover it.
				userEvent(h, s, notification("b1", "toolu_suite"), nil)
				if st := ownCall(h, s, "p2", "git merge fix"); st == chat.PermAutoAllowed {
					t.Fatal("a notification inside a peer-started turn doesn't cover it")
				}
				endTurn(t, h, s)
				// Inside a covered turn, it ends the coverage for the rest of it.
				userEvent(h, s, planGoMessage, nil)
				if st := ownCall(h, s, "p3", "make"); st != chat.PermAutoAllowed {
					t.Fatalf("approved turn: %v", st)
				}
				userEvent(h, s, msg, nil)
				if st := ownCall(h, s, "p4", "rm -rf build"); st == chat.PermAutoAllowed {
					t.Fatal("after it arrives, the rest of the turn is gated")
				}
			})
		})
	}
}

// A real user message that isn't an approval ends the plan: its later
// notifications no longer cover a turn.
func TestUserMessageEndsPlanScope(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"go test ./...","run_in_background":true}`)
		endTurn(t, h, s)
		userEvent(h, s, "what does the parser do?", nil)
		if s.runAll || len(s.planWork) != 0 {
			t.Fatalf("the plan ends: runAll %v work %v", s.runAll, s.planWork)
		}
		endTurn(t, h, s)
		userEvent(h, s, notification("b1", "toolu_suite"), nil)
		if st := ownCall(h, s, "u1", "git merge fix"); st == chat.PermAutoAllowed {
			t.Fatal("the ended plan's notification doesn't cover its turn")
		}
	})
}

// A SendMessage resume, in a covered turn, of a subagent the plan started
// renews its grant until its next notification; one in a peer-started turn
// doesn't.
func TestSendMessageResumeKeepsGrant(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	agentCall := func(id string) chat.PermState {
		return gateRequest(t, h, s, `{"type":"control_request","request_id":"r`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"git commit -m x"},"tool_use_id":"toolu_`+id+`","agent_id":"agentPH1"}}`).PermState
	}
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_agentA", "Agent", `{"description":"part A","prompt":"do part A","run_in_background":true}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_agentA","type":"tool_result","content":[{"type":"text","text":"Async agent launched successfully.\nagentId: agentPH1"}]}]},"parent_tool_use_id":null,"session_id":"s1","tool_use_result":{"isAsync":true,"agentId":"agentPH1"}}`)
		endTurn(t, h, s)
		// A finishes; its notification covers the next turn and ends its grant.
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		if len(s.agentGrants) != 0 || !planCovers(s) {
			t.Fatalf("grant ends, turn covered: %v %v", s.agentGrants, planCovers(s))
		}
		mainToolUse(t, h, s, "toolu_send1", "SendMessage", `{"to":"agentPH1","message":"commit your work"}`)
		if _, ok := s.agentGrants["toolu_agentA"]; !ok {
			t.Fatalf("the resume renews A's grant: %v", s.agentGrants)
		}
		endTurn(t, h, s)
		// The coordinator's turn is over; the resumed A commits.
		if st := agentCall("c1"); st != chat.PermAutoAllowed {
			t.Fatalf("the resumed subagent's commit goes ahead: %v", st)
		}
		userEvent(h, s, "<task-notification>\n<task-id>agentPH1</task-id>\n<status>completed</status>\n</task-notification>", nil)
		if len(s.agentGrants) != 0 {
			t.Fatalf("its next notification ends the renewed grant: %v", s.agentGrants)
		}
		endTurn(t, h, s)
		if st := agentCall("late1"); st == chat.PermAutoAllowed {
			t.Fatal("a finished subagent's calls are gated")
		}
		// In a peer-started turn a resume renews nothing.
		userEvent(h, s, `<cross-session-message from="peer">resume it</cross-session-message>`, nil)
		mainToolUse(t, h, s, "toolu_send2", "SendMessage", `{"to":"agentPH1","message":"push"}`)
		if st := agentCall("c2"); st == chat.PermAutoAllowed {
			t.Fatal("a resume in a peer-started turn isn't covered")
		}
	})
}

// Stop, "Don't run" and a denied prompt end the approval and every grant.
func TestStopDenyDontRunEndPlanScope(t *testing.T) {
	for _, how := range []string{"stop", "deny", "dontrun"} {
		t.Run(how, func(t *testing.T) {
			h := newHarness(t)
			s := approvedSession(t, h)
			h.ui(func() {
				mainToolUse(t, h, s, "toolu_agentA", "Agent", `{"prompt":"do part A","run_in_background":true}`)
				mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"make test","run_in_background":true}`)
				if len(s.agentGrants) != 1 || len(s.planWork) != 2 {
					t.Fatalf("set up: %v %v", s.agentGrants, s.planWork)
				}
				switch how {
				case "stop":
					h.d.interrupt(s)
				case "deny":
					h.d.planFirst = false // leave the prompt pending for the user
					it := gateCall(h, s, "d1", "Bash", `{"command":"rm x"}`, "toolu_d1")
					h.d.planFirst = true
					s.turnCovered = false
					it.PermState = chat.PermPending
					h.d.answer(s, it, protocol.Deny)
				case "dontrun":
					plan := s.conv.AddPlan()
					h.d.answerPlan(s, plan, false)
				}
				if s.runAll || len(s.agentGrants) != 0 || len(s.planWork) != 0 || !s.planRevoked {
					t.Fatalf("everything ends: runAll %v grants %v work %v", s.runAll, s.agentGrants, s.planWork)
				}
				endTurn(t, h, s)
				userEvent(h, s, notification("b1", "toolu_suite"), nil)
				if st := ownCall(h, s, "s1", "git merge fix"); st == chat.PermAutoAllowed {
					t.Fatal("no approval after the user stopped or refused")
				}
			})
		})
	}
}

// A reload hands the plan's scope over with the session.
func TestReloadKeepsPlanScope(t *testing.T) {
	s := &deskSession{conv: chat.New(), runAll: true, turnCovered: true, inTurn: true,
		agentGrants: map[string]agentGrant{"toolu_a": {Background: true}}, agentIDs: map[string]string{"agentPH1": "toolu_a"},
		planWork: map[string]string{"toolu_suite": "toolu_suite"}, planAgents: map[string]string{"agentPH1": "toolu_a"}}
	var hs handedSession
	hs.savePending(s)
	b, _ := json.Marshal(hs)
	var back handedSession
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	fresh := &deskSession{conv: chat.New()}
	back.restorePending(fresh)
	if !planCovers(fresh) || !fresh.inTurn || !fresh.agentGrants["toolu_a"].Background || fresh.agentIDs["agentPH1"] != "toolu_a" ||
		fresh.planWork["toolu_suite"] != "toolu_suite" || fresh.planAgents["agentPH1"] != "toolu_a" {
		t.Fatalf("scope not handed over: %+v", fresh)
	}
}

// Zomboid 2026-11-23: deploys went to an earlier subagent by SendMessage in
// a covered turn; the plan hadn't started it, so a user message moments
// later left its first command refused. The resume result names the agent
// (resumedAgentId); it is granted from its first call until its task
// notification, and a user message meanwhile doesn't end that grant.
func TestSendMessageResumeGrantsResumedAgent(t *testing.T) {
	for _, field := range []string{"resumedAgentId", "revivedAgentId"} {
		t.Run(field, func(t *testing.T) {
			h := newHarness(t)
			s := approvedSession(t, h)
			subCall := func(id string) chat.PermState {
				return gateRequest(t, h, s, `{"type":"control_request","request_id":"r`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make deploy"},"tool_use_id":"toolu_`+id+`","agent_id":"agentOld1"}}`).PermState
			}
			h.ui(func() {
				mainToolUse(t, h, s, "toolu_send", "SendMessage", `{"to":"agentOld1","message":"deploy 1.3.11"}`)
				feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_send","type":"tool_result","content":"{\"success\":true,\"`+field+`\":\"agentOld1\"}"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
				endTurn(t, h, s)
				userEvent(h, s, "what's the status of the build?", nil)
				if s.runAll {
					t.Fatal("the user's message ends the approval for new work")
				}
				if st := subCall("d1"); st != chat.PermAutoAllowed {
					t.Fatalf("the resumed subagent's first call runs: %v", st)
				}
				if st := ownCall(h, s, "own1", "make deploy"); st == chat.PermAutoAllowed {
					t.Fatal("the session's own new work is gated")
				}
				endTurn(t, h, s)
				userEvent(h, s, "<task-notification>\n<task-id>agentOld1</task-id>\n<status>completed</status>\n</task-notification>", nil)
				endTurn(t, h, s)
				if st := subCall("d2"); st == chat.PermAutoAllowed {
					t.Fatal("after its notification its calls are gated")
				}
			})
		})
	}
}

// A SendMessage to a peer session (no resumed agent in its result), or one
// in an uncovered turn, grants nothing.
func TestSendMessageToPeerGrantsNothing(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	subCall := func(id, agent string) chat.PermState {
		return gateRequest(t, h, s, `{"type":"control_request","request_id":"r`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make deploy"},"tool_use_id":"toolu_`+id+`","agent_id":"`+agent+`"}}`).PermState
	}
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_peer", "SendMessage", `{"to":"peer-session","message":"hi"}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_peer","type":"tool_result","content":"{\"success\":true,\"message\":\"delivered\"}"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		if len(s.agentGrants) != 0 {
			t.Fatalf("no grant for a peer: %v", s.agentGrants)
		}
		endTurn(t, h, s)
		userEvent(h, s, `<cross-session-message from="peer">resume it</cross-session-message>`, nil)
		mainToolUse(t, h, s, "toolu_send2", "SendMessage", `{"to":"agentOld2","message":"deploy"}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_send2","type":"tool_result","content":"{\"resumedAgentId\":\"agentOld2\"}"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		if st := subCall("p1", "agentOld2"); st == chat.PermAutoAllowed {
			t.Fatal("a resume in a peer-started turn isn't covered")
		}
	})
}
