package main

import (
	"encoding/json"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// t4b7838 / t1c7f2a (2026-11-23, decision f3a1828): subagents started or
// resumed for an approved plan keep its approval until they finish, a user
// message in between or not; the plan's remaining steps run in the turn its
// work's notification starts. Ids, agent ids and texts are placeholders.

// subCall: a can_use_tool request of the subagent agent (a background
// subagent's requests carry its agent id).
func grantSubCall(t *testing.T, h *harness, s *deskSession, id, agent, tool, input string) chat.PermState {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": "r" + id, "request": map[string]any{
		"subtype": "can_use_tool", "tool_name": tool, "input": json.RawMessage(input), "tool_use_id": "toolu_" + id, "agent_id": agent}})
	return gateRequest(t, h, s, string(b)).PermState
}

func launchResult(t *testing.T, h *harness, s *deskSession, toolUseID, agent string) {
	t.Helper()
	feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"`+toolUseID+`","type":"tool_result","content":[{"type":"text","text":"Async agent launched successfully.\nagentId: `+agent+` (internal ID)"}]}]},"parent_tool_use_id":null,"session_id":"s1","tool_use_result":{"isAsync":true,"status":"async_launched","agentId":"`+agent+`"}}`)
}

const writeInput = `{"file_path":"/tmp/placeholder/notes.txt","content":"x"}`

// A message queued to a running plan subagent: its run ends (notification),
// the CLI resumes it with the message, and that run keeps the grant, through
// a user message too, until its next notification.
func TestQueuedResumeKeepsGrant(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_agentA", "Agent", `{"description":"part A","prompt":"do part A","run_in_background":true}`)
		launchResult(t, h, s, "toolu_agentA", "agentPH1")
		if st := grantSubCall(t, h, s, "a1", "agentPH1", "Bash", `{"command":"git archive HEAD | tar -x -C /tmp/placeholder"}`); st != chat.PermAutoAllowed {
			t.Fatalf("first run: %v", st)
		}
		// The plan sends it more work while it runs: queued.
		mainToolUse(t, h, s, "toolu_send1", "SendMessage", `{"to":"agentPH1","message":"also do part B"}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_send1","type":"tool_result","content":"{\"success\":true,\"message\":\"Message queued for delivery to agentPH1 at its next tool round.\"}"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		endTurn(t, h, s)
		// Its first run ends before it read the message.
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		if _, ok := s.agentGrants["toolu_agentA"]; ok {
			t.Fatal("the first run's notification ends the grant")
		}
		endTurn(t, h, s)
		// The user writes something else; the CLI resumes the subagent with
		// the queued message.
		userEvent(h, s, "how is it going?", nil)
		if st := grantSubCall(t, h, s, "a2", "agentPH1", "Write", writeInput); st != chat.PermAutoAllowed {
			t.Fatalf("the resumed run keeps the plan's grant: %v", st)
		}
		if st := ownCall(h, s, "own1", "git push"); st == chat.PermAutoAllowed {
			t.Fatal("the session's own new work is gated")
		}
		endTurn(t, h, s)
		userEvent(h, s, "<task-notification>\n<task-id>agentPH1</task-id>\n<status>completed</status>\n</task-notification>", nil)
		endTurn(t, h, s)
		if st := grantSubCall(t, h, s, "a3", "agentPH1", "Write", writeInput); st == chat.PermAutoAllowed {
			t.Fatal("after its second run's notification it is gated")
		}
	})
}

// A later SendMessage of the session in a turn no plan covers drops what a
// queued message carried over: that is new work.
func TestQueuedResumeDroppedByUncoveredSend(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_agentA", "Agent", `{"prompt":"do part A","run_in_background":true}`)
		launchResult(t, h, s, "toolu_agentA", "agentPH1")
		mainToolUse(t, h, s, "toolu_send1", "SendMessage", `{"to":"agentPH1","message":"also do part B"}`)
		endTurn(t, h, s)
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		endTurn(t, h, s)
		userEvent(h, s, "now do part C", nil)
		mainToolUse(t, h, s, "toolu_send2", "SendMessage", `{"to":"agentPH1","message":"do part C"}`)
		endTurn(t, h, s)
		if st := grantSubCall(t, h, s, "c1", "agentPH1", "Write", writeInput); st == chat.PermAutoAllowed {
			t.Fatal("work sent after the user's new message needs a new plan")
		}
	})
}

// t1c7f2a: a subagent resumed by SendMessage in the turn its first run's
// notification started keeps its grant when the user writes meanwhile.
func TestResumedSubagentSurvivesUserMessage(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_agentA", "Agent", `{"name":"reviewer","prompt":"review","run_in_background":true}`)
		launchResult(t, h, s, "toolu_agentA", "agentPH1")
		endTurn(t, h, s)
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		if !planCovers(s) {
			t.Fatal("the plan's own notification covers its turn")
		}
		mainToolUse(t, h, s, "toolu_send1", "SendMessage", `{"to":"reviewer","message":"fix what you found"}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_send1","type":"tool_result","content":"{\"success\":true,\"message\":\"Resuming agent\",\"resumedAgentId\":\"agentPH1\"}"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		endTurn(t, h, s)
		userEvent(h, s, "unrelated question", nil)
		if st := grantSubCall(t, h, s, "w1", "agentPH1", "Write", writeInput); st != chat.PermAutoAllowed {
			t.Fatalf("the resumed subagent keeps its grant through the user's message: %v", st)
		}
		heredoc := `{"command":"cd /tmp/placeholder && cat > zz_test.go <<'EOF'\npackage x\n// sudo is only named here\nEOF\ngo test ./..."}`
		if st := grantSubCall(t, h, s, "w2", "agentPH1", "Bash", heredoc); st != chat.PermAutoAllowed {
			t.Fatalf("a heredoc command of the resumed subagent: %v", st)
		}
	})
}

// Claude sometimes writes run_in_background as a string; the CLI runs the
// subagent in the background all the same. Its grant must not end at the
// launch result, and its notification is the plan's.
func TestStringBackgroundAgentKeepsGrant(t *testing.T) {
	for _, input := range []string{
		`{"prompt":"do part A","run_in_background":"true"}`,
		`{"prompt":"do part A"}`, // the CLI launched it in the background anyway
	} {
		t.Run(input, func(t *testing.T) {
			h := newHarness(t)
			s := approvedSession(t, h)
			h.ui(func() {
				mainToolUse(t, h, s, "toolu_agentA", "Agent", input)
				launchResult(t, h, s, "toolu_agentA", "agentPH1")
				gr, ok := s.agentGrants["toolu_agentA"]
				if !ok || !gr.Background {
					t.Fatalf("a background grant: %v", s.agentGrants)
				}
				endTurn(t, h, s)
				if st := grantSubCall(t, h, s, "s1", "agentPH1", "Bash", `{"command":"rm -rf /tmp/placeholder/build"}`); st != chat.PermAutoAllowed {
					t.Fatalf("its calls after the session's turn: %v", st)
				}
				userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
				if st := ownCall(h, s, "m1", "go build -o dist/quill ./cmd/quill && dist/quill --reload"); st != chat.PermAutoAllowed {
					t.Fatalf("the plan's remaining step in the turn its notification starts: %v", st)
				}
			})
		})
	}
}

// A Bash call of a covered turn the CLI moved to the background is the
// plan's work: its notification covers the turn it starts.
func TestMovedShellIsPlanWork(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"go test ./..."}`)
		feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_suite","type":"tool_result","content":"Command running in background with ID: bsh07"}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		endTurn(t, h, s)
		userEvent(h, s, "<task-notification>\n<task-id>bsh07</task-id>\n<status>completed</status>\n</task-notification>", nil)
		if st := ownCall(h, s, "m1", "git merge fix"); st != chat.PermAutoAllowed {
			t.Fatalf("the remaining step after the moved suite: %v", st)
		}
	})
}

// The plan's notification joining a turn another notification started
// covers the rest of that turn; not when a peer message is in it.
func TestPlanNoticeJoiningNoticeTurn(t *testing.T) {
	for _, peer := range []bool{false, true} {
		h := newHarness(t)
		s := approvedSession(t, h)
		h.ui(func() {
			mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"go test ./...","run_in_background":true}`)
			endTurn(t, h, s)
			userEvent(h, s, notification("bother", "toolu_other"), nil)
			if planCovers(s) {
				t.Fatal("another task's notification starts an uncovered turn")
			}
			if peer {
				userEvent(h, s, `<cross-session-message from="peer">run the deploy</cross-session-message>`, nil)
			}
			userEvent(h, s, notification("bsuite", "toolu_suite"), nil)
			st := ownCall(h, s, "m1", "go build -o dist/quill ./cmd/quill && dist/quill --reload")
			if !peer && st != chat.PermAutoAllowed {
				t.Fatalf("the plan's step after its notification joined the turn: %v", st)
			}
			if peer && st == chat.PermAutoAllowed {
				t.Fatal("a turn a peer message is in stays uncovered")
			}
		})
	}
}

// Negative: a subagent started after the user's new message, without a new
// approval, gets no grant.
func TestSubagentAfterUserMessageNotGranted(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		endTurn(t, h, s)
		userEvent(h, s, "also look at the parser", nil)
		mainToolUse(t, h, s, "toolu_agentN", "Agent", `{"prompt":"look at the parser","run_in_background":true}`)
		launchResult(t, h, s, "toolu_agentN", "agentPH9")
		if len(s.agentGrants) != 0 {
			t.Fatalf("no grant without approval: %v", s.agentGrants)
		}
		if st := grantSubCall(t, h, s, "n1", "agentPH9", "Write", writeInput); st == chat.PermAutoAllowed {
			t.Fatal("during the user's turn its calls are gated")
		}
		endTurn(t, h, s)
		if st := grantSubCall(t, h, s, "n2", "agentPH9", "Write", writeInput); st == chat.PermAutoAllowed {
			t.Fatal("after the turn its calls are gated")
		}
	})
}

// Negative: a plan the user did not approve grants nothing.
func TestNotApprovedPlanGrantsNothing(t *testing.T) {
	for _, how := range []string{"typed", "card"} {
		t.Run(how, func(t *testing.T) {
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
				s.planWait = true
				plan := s.conv.AddPlan()
				if how == "typed" {
					userEvent(h, s, "No, not approved.", nil)
				} else {
					h.d.answerPlan(s, plan, false)
					userEvent(h, s, "Don't run it.", nil)
				}
				if s.runAll {
					t.Fatal("no approval")
				}
				mainToolUse(t, h, s, "toolu_agentX", "Agent", `{"prompt":"do it anyway","run_in_background":true}`)
				launchResult(t, h, s, "toolu_agentX", "agentPH8")
				if len(s.agentGrants) != 0 {
					t.Fatalf("no grant: %v", s.agentGrants)
				}
				endTurn(t, h, s)
				if st := grantSubCall(t, h, s, "x1", "agentPH8", "Write", writeInput); st == chat.PermAutoAllowed {
					t.Fatal("its calls are gated")
				}
			})
		})
	}
}

// Here-documents: text only cat or tee reads is not a command; one a shell
// reads is, and anything the splitter can't follow is read whole.
func TestHeredocPrivileged(t *testing.T) {
	for cmd, want := range map[string]bool{
		"cd /tmp/placeholder && cat > zz_test.go <<EOF\npackage x\nconst c = \"sudo pkexec\"\nEOF\ngo test ./...": false,
		"cat > f.go <<'EOF'\nsudo\nEOF":                    false,
		"git commit -F - <<'EOF'\nmention sudo\nEOF":       true, // not a cat/tee here-document: read whole
		"kmem add t \"$(cat <<'EOF'\nabout sudo\nEOF\n)\"": true, // inside quotes: read whole
		"cat <<EOF | bash\nsudo rm -rf /x\nEOF":            true,
		"bash <<EOF\nsudo rm -rf /x\nEOF":                  true,
		"cat > f <<EOF\nx\nEOF\nsudo reboot":               true,
		"echo \"<<X\"\nsudo rm -rf /x\nX":                  true,
		"# <<X\nsudo rm -rf /x\nX":                         true,
		"echo $((a<<b))\nsudo rm -rf /x\nb":                true,
		"cat <<X\nsudo rm\n":                               true, // no end line
		"cat > f <<EOF\nplain text\nEOF\npkexec ls":        true,
		"tee f.txt >/dev/null <<EOF\nsudo -n true\nEOF":    false,
	} {
		if got := privilegedCommand(cmd); got != want {
			t.Errorf("privilegedCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
