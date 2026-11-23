package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
)

// gateTranscript makes the session's transcript end with the human message
// text (what approvedInTranscript reads).
func gateTranscript(t *testing.T, h *harness, s *deskSession, text string) {
	t.Helper()
	p := h.d.transcriptPath(s)
	if p == "" {
		p = filepath.Join(h.d.cfg.ProjectsRoot, "gate-batch", s.id+".jsonl")
		os.MkdirAll(filepath.Dir(p), 0o700)
	}
	line := `{"type":"user","message":{"role":"user","content":"` + text + `"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if h.d.transcriptPath(s) != p {
		t.Fatalf("transcript not found at %s", p)
	}
}

// gatedSession: a running session with plan-first on, idle between turns.
func gatedSession(t *testing.T, h *harness) *deskSession {
	t.Helper()
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("a running, idle session", func() bool { return s.proc != nil && !s.conv.Busy && s.id != "" })
	h.ui(func() {
		h.d.planFirst = true
		s.inTurn = false
	})
	return s
}

const (
	batchWrite  = `{"type":"assistant","message":{"id":"msg_batch","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_write","name":"Write","input":{"file_path":"/tmp/x/setup.sh","content":"#!/bin/bash\necho hi\n"}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	batchBash   = `{"type":"assistant","message":{"id":"msg_batch","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_pkexec","name":"Bash","input":{"command":"pkexec bash /tmp/x/setup.sh","run_in_background":true}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	batchRead   = `{"type":"assistant","message":{"id":"msg_batch","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_read","name":"Read","input":{"file_path":"/tmp/x/notes.txt"}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	reqWrite    = `{"type":"control_request","request_id":"rw","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/tmp/x/setup.sh","content":"#!/bin/bash\necho hi\n"},"tool_use_id":"toolu_write"}}`
	reqPkexec   = `{"type":"control_request","request_id":"rb","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"pkexec bash /tmp/x/setup.sh","run_in_background":true},"tool_use_id":"toolu_pkexec"}}`
	reqReadCall = `{"type":"control_request","request_id":"rr","request":{"subtype":"can_use_tool","tool_name":"Read","input":{"file_path":"/tmp/x/notes.txt"},"tool_use_id":"toolu_read"}}`
)

// ta43580 (2026-11-23): the user's plain "yes" to a plan that got no card
// started a turn whose one assistant message called Write (a root script)
// and Bash `pkexec bash <that script>` in parallel. Plan-first refused the
// Write, then let the pkexec call run: the refusal set planWait, and with it
// the "yes" read back from the transcript counted as approval.
func TestRefusedBatchRefusesTheRest(t *testing.T) {
	h := newHarness(t)
	s := gatedSession(t, h)
	gateTranscript(t, h, s, "yes")
	h.ui(func() {
		userEvent(h, s, "yes", nil)
		if s.runAll {
			t.Fatal("a yes with no plan asked for approves nothing")
		}
		feed(t, h, s, batchWrite)
		if e := gateRequest(t, h, s, reqWrite); e.PermState != chat.PermPlanAsked {
			t.Fatalf("the Write is refused: %v", e.PermState)
		}
		feed(t, h, s, batchBash)
		if e := gateRequest(t, h, s, reqPkexec); e.PermState != chat.PermPlanAsked {
			t.Fatalf("the pkexec call of the same batch is refused too: %v", e.PermState)
		}
		if s.runAll {
			t.Fatal("no approval came out of the refusal")
		}
		feed(t, h, s, batchRead)
		if e := gateRequest(t, h, s, reqReadCall); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("a read-only call isn't gated, in that batch too: %v", e.PermState)
		}
		// The next message of the same turn: the transcript's "yes" came
		// before the refusal, so it still doesn't count.
		feed(t, h, s, `{"type":"assistant","message":{"id":"msg_next","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_make","name":"Bash","input":{"command":"make install"}}]},"parent_tool_use_id":null,"session_id":"s1"}`)
		if e := gateRequest(t, h, s, `{"type":"control_request","request_id":"rm","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make install"},"tool_use_id":"toolu_make"}}`); e.PermState != chat.PermPlanAsked {
			t.Fatalf("no transcript approval after a refusal in this turn: %v", e.PermState)
		}
		feed(t, h, s, `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}`)
		if s.batchOf != nil || s.batchRefused != nil || s.planWait {
			t.Fatal("the turn's batches are forgotten when it ends")
		}
	})
}

// The same batch in the other order, and a batch the user did approve.
func TestRefusedBatchOrderAndApproval(t *testing.T) {
	h := newHarness(t)
	s := gatedSession(t, h)
	h.ui(func() {
		userEvent(h, s, "set up swap", nil)
		feed(t, h, s, batchBash)
		feed(t, h, s, batchWrite)
		if e := gateRequest(t, h, s, reqPkexec); e.PermState != chat.PermPlanAsked {
			t.Fatalf("pkexec first: refused: %v", e.PermState)
		}
		if e := gateRequest(t, h, s, reqWrite); e.PermState != chat.PermPlanAsked {
			t.Fatalf("then the Write: refused: %v", e.PermState)
		}
		feed(t, h, s, `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s1"}`)
		s.inTurn = false
		s.planWait = true // a plan waits for the approval
		userEvent(h, s, planGoMessage, nil)
		if !s.runAll {
			t.Fatal("the card's approval approves")
		}
		feed(t, h, s, batchWrite)
		feed(t, h, s, batchBash)
		if e := gateRequest(t, h, s, reqWrite); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("approved: the Write runs: %v", e.PermState)
		}
		if e := gateRequest(t, h, s, reqPkexec); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("approved: the pkexec call runs: %v", e.PermState)
		}
	})
}

// Root commands never take the idle-subagent fallback, nor the transcript
// approval fallback.
func TestPrivilegedSkipsFallbacks(t *testing.T) {
	h := newHarness(t)
	s := gatedSession(t, h)
	h.ui(func() {
		s.agentGrants = map[string]agentGrant{"toolu_agentA": {Background: true}}
		if e := gateRequest(t, h, s, `{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"go test ./..."},"tool_use_id":"toolu_unknown1"}}`); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("idle session, live grant: an unknown call is a subagent's: %v", e.PermState)
		}
		for i, cmd := range []string{"pkexec bash /tmp/x/setup.sh", "sudo systemctl daemon-reload", "env FOO=1 doas reboot", "systemctl restart sshd", "swapon /swap/file"} {
			id := string(rune('a' + i))
			e := gateRequest(t, h, s, `{"type":"control_request","request_id":"p`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"`+cmd+`"},"tool_use_id":"toolu_priv`+id+`"}}`)
			if e.PermState != chat.PermPlanAsked {
				t.Fatalf("%q never takes the idle fallback: %v", cmd, e.PermState)
			}
		}
		s.agentGrants = nil
		s.planWait = false
		// A typed yes to a pending plan, which Quill didn't see go out.
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. Write the script.\n2. Run it with pkexec.\n\nDo you approve steps 1–2?"})
		s.conv.AddPlan()
		gateTranscript(t, h, s, "yes")
		if e := gateRequest(t, h, s, reqPkexec); e.PermState != chat.PermPlanAsked {
			t.Fatalf("a root command doesn't run on the transcript fallback: %v", e.PermState)
		}
	})
}

func TestPrivilegedNeverReadOnly(t *testing.T) {
	extra := parseReadOnlyExtra("pkexec\nsudo\ndoas\nsu\nrun0\nenv\nbash\nsystemctl\nmount\npzlock status")
	for cmd, want := range map[string]bool{
		"pkexec bash /tmp/x/setup.sh":       false,
		"sudo cat /etc/shadow":              false,
		"/usr/bin/sudo ls":                  false,
		"doas ls":                           false,
		"su -c 'id'":                        false,
		"run0 ls":                           false,
		"env sudo ls":                       false,
		"bash -c 'sudo ls'":                 false,
		"systemctl daemon-reload":           false,
		"mount /dev/sda1 /mnt":              false,
		"FOO=1 pkexec ls":                   false,
		"ls; pkexec ls":                     false,
		"systemctl status sshd":             true,
		"systemctl --user restart pipewire": true,
		"pzlock status":                     true,
		"kmem find pkexec":                  true,
		"grep -r sudo /tmp/x":               true,
	} {
		if got := readOnlyCommand(cmd, extra); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
	for cmd, want := range map[string]bool{
		"pkexec bash /tmp/x/setup.sh": true,
		"echo $(sudo id)":             true,
		"x=`doas id`":                 true,
		"nohup sudo make install &":   true,
		"timeout 5 pkexec ls":         true,
		"ssh host sudo reboot":        true,
		"mkfs.ext4 /dev/sdz1":         true,
		"go test ./...":               false,
		"kmem find pkexec":            false,
		"systemctl status sshd":       false,
		"git commit -m 'sudo docs'":   false,
	} {
		if got := privilegedCommand(cmd); got != want {
			t.Errorf("privilegedCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// fakeStops records the subagents the gate stops instead of asking the CLI.
func fakeStops(t *testing.T) *[]string {
	t.Helper()
	var stopped []string
	old := stopSubagent
	stopSubagent = func(_ *deskSession, task string) { stopped = append(stopped, task) }
	t.Cleanup(func() { stopSubagent = old })
	return &stopped
}

const (
	mixAgent = `{"type":"assistant","message":{"id":"msg_mix","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_mixagent","name":"Agent","input":{"description":"install","prompt":"run make install","run_in_background":true}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	mixBash  = `{"type":"assistant","message":{"id":"msg_mix","type":"message","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_mixbash","name":"Bash","input":{"command":"make install"}}]},"parent_tool_use_id":null,"session_id":"s1"}`
	reqMix   = `{"type":"control_request","request_id":"rmix","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make install"},"tool_use_id":"toolu_mixbash"}}`
	mixStart = `{"type":"system","subtype":"task_started","task_id":"agentMix1","tool_use_id":"toolu_mixagent","description":"install","task_type":"local_agent","is_backgrounded":true}`
)

func mixSubCall(t *testing.T, h *harness, s *deskSession, id string) chat.PermState {
	t.Helper()
	return gateRequest(t, h, s, `{"type":"control_request","request_id":"rs`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make install"},"tool_use_id":"toolu_s`+id+`","agent_id":"agentMix1"}}`).PermState
}

// Zomboid 2026-11-23: in one batch of an Agent call and a Bash call, the
// gate refused the Bash call but the subagent started anyway (the CLI asks
// nothing for an Agent call) and did the refused work. It is stopped once its
// task id is known, whichever comes first, and its calls are refused.
func TestRefusedBatchStopsItsAgent(t *testing.T) {
	for _, order := range []string{"refusal first", "task id first"} {
		t.Run(order, func(t *testing.T) {
			h := newHarness(t)
			stopped := fakeStops(t)
			s := gatedSession(t, h)
			h.ui(func() {
				userEvent(h, s, "install it", nil)
				feed(t, h, s, mixAgent)
				feed(t, h, s, mixBash)
				if order == "task id first" {
					feed(t, h, s, mixStart)
					if len(*stopped) != 0 {
						t.Fatal("nothing is stopped before a refusal")
					}
				}
				if e := gateRequest(t, h, s, reqMix); e.PermState != chat.PermPlanAsked {
					t.Fatalf("the Bash call is refused: %v", e.PermState)
				}
				if order == "refusal first" {
					if len(*stopped) != 0 {
						t.Fatal("no task id yet: nothing to stop")
					}
					feed(t, h, s, mixStart)
				}
				if len(*stopped) != 1 || (*stopped)[0] != "agentMix1" {
					t.Fatalf("the batch's subagent is stopped: %v", *stopped)
				}
				if st := mixSubCall(t, h, s, "1"); st != chat.PermDenied {
					t.Fatalf("the stopped subagent's call is refused: %v", st)
				}
				endTurn(t, h, s)
				// Even with a live grant and the idle fallback, after the turn.
				s.agentGrants = map[string]agentGrant{"toolu_other": {Background: true}}
				if st := mixSubCall(t, h, s, "2"); st != chat.PermDenied {
					t.Fatalf("its later calls stay refused: %v", st)
				}
			})
		})
	}
}

// An Agent call in a batch with no refusal starts; nothing is stopped.
func TestUnrefusedBatchKeepsItsAgent(t *testing.T) {
	h := newHarness(t)
	stopped := fakeStops(t)
	s := approvedSession(t, h)
	h.ui(func() {
		feed(t, h, s, mixAgent)
		feed(t, h, s, mixBash)
		feed(t, h, s, mixStart)
		if e := gateRequest(t, h, s, reqMix); e.PermState != chat.PermAutoAllowed {
			t.Fatalf("approved: %v", e.PermState)
		}
		if len(*stopped) != 0 || len(s.refusedAgents) != 0 {
			t.Fatalf("nothing stopped: %v %v", *stopped, s.refusedAgents)
		}
	})
}

// A subagent's own batch is decided as a whole too: its root call is refused
// (no fallback for root), so the Write next to it doesn't run on the idle
// fallback. Streamed subagents are known from the stream, background ones
// from their transcript.
func TestSubagentRefusedBatch(t *testing.T) {
	const (
		subPkexec = `{"type":"tool_use","id":"toolu_subpk","name":"Bash","input":{"command":"pkexec bash /tmp/x/setup.sh"}}`
		subWrite  = `{"type":"tool_use","id":"toolu_subw","name":"Write","input":{"file_path":"/tmp/x/setup.sh","content":"x"}}`
		reqSubPk  = `{"type":"control_request","request_id":"rsp","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"pkexec bash /tmp/x/setup.sh"},"tool_use_id":"toolu_subpk","agent_id":"agentSub1"}}`
		reqSubW   = `{"type":"control_request","request_id":"rsw","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/tmp/x/setup.sh","content":"x"},"tool_use_id":"toolu_subw","agent_id":"agentSub1"}}`
		reqSubW2  = `{"type":"control_request","request_id":"rsw2","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/tmp/x/other.sh","content":"x"},"tool_use_id":"toolu_subw2","agent_id":"agentSub1"}}`
	)
	for _, how := range []string{"streamed", "background"} {
		t.Run(how, func(t *testing.T) {
			h := newHarness(t)
			s := gatedSession(t, h)
			h.ui(func() {
				gateTranscript(t, h, s, "fix the tabs")
				switch how {
				case "streamed":
					for _, b := range []string{subPkexec, subWrite} {
						feed(t, h, s, `{"type":"assistant","message":{"id":"msg_sub","type":"message","role":"assistant","model":"m","content":[`+b+`]},"parent_tool_use_id":"toolu_parent","session_id":"s1"}`)
					}
				case "background":
					p := h.d.subagentTranscript(s, "agentSub1")
					os.MkdirAll(filepath.Dir(p), 0o700)
					line := `{"type":"assistant","message":{"id":"msg_sub","type":"message","role":"assistant","model":"m","content":[` + subPkexec + `,` + subWrite + `]}}` + "\n"
					if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				// The idle fallback is on: a live grant, the session idle.
				s.agentGrants = map[string]agentGrant{"toolu_other": {Background: true}}
				if e := gateRequest(t, h, s, reqSubPk); e.PermState != chat.PermPlanAsked {
					t.Fatalf("the subagent's root call is refused: %v", e.PermState)
				}
				if e := gateRequest(t, h, s, reqSubW); e.PermState != chat.PermPlanAsked {
					t.Fatalf("the Write of the same batch is refused too: %v", e.PermState)
				}
				if e := gateRequest(t, h, s, reqSubW2); e.PermState != chat.PermAutoAllowed {
					t.Fatalf("a call of another batch takes the idle fallback as before: %v", e.PermState)
				}
			})
		})
	}
}

// Zomboid 2026-11-23: a "Yes" typed in Quill that approved nothing was read
// back from the transcript later and approved the plan card the next turn
// ended with. A message Quill already judged never approves again; one Quill
// didn't see (typed elsewhere) still can.
func TestJudgedMessageDoesntReapprove(t *testing.T) {
	h := newHarness(t)
	s := gatedSession(t, h)
	subWrite := func(id string) chat.PermState {
		return gateRequest(t, h, s, `{"type":"control_request","request_id":"r`+id+`","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"/tmp/x/a.go","content":"x"},"tool_use_id":"toolu_`+id+`","agent_id":"agentJ1"}}`).PermState
	}
	h.ui(func() {
		userEvent(h, s, "Yes", nil)
		if s.runAll {
			t.Fatal("no plan asked for: the yes approves nothing")
		}
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. Edit a.go.\n2. Run the tests.\n\n**Can I run steps 1–2? Yes (recommended) or no?**"})
		endTurn(t, h, s)
		if !hasPendingPlan(s) {
			t.Fatal("the turn ends with a card")
		}
		gateTranscript(t, h, s, "Yes")
		if st := subWrite("j1"); st != chat.PermPlanAsked {
			t.Fatalf("the judged yes doesn't approve the newer card: %v", st)
		}
		if s.runAll {
			t.Fatal("no approval")
		}
		// A yes Quill never saw (typed in the terminal) is still the fallback.
		s.planWait = false
		gateTranscript(t, h, s, "yes, go ahead")
		if st := subWrite("j2"); st != chat.PermAutoAllowed {
			t.Fatalf("an unseen approval still counts: %v", st)
		}
	})
}

// Bare approvals answer a pending card; with no card and no ask they approve
// nothing; peer messages never approve.
func TestBareApprovalsWithPendingCard(t *testing.T) {
	h := newHarness(t)
	s := gatedSession(t, h)
	h.ui(func() {
		variants := []string{"yes", "Yes", "Yes.", "yes!", "YES", "ok", "OK", "Ok.", "okay", "approved", "Approved.", "approve", "Approve", "yes approved", "yes, approve"}
		for _, v := range variants {
			if v == planGoMessage {
				continue // the card's own approval text always counts
			}
			if approvesPlan(s, v) {
				t.Errorf("%q with no card and no ask approves nothing", v)
			}
		}
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. Edit a.go.\n\n**Can I run step 1? Yes (recommended) or no?**"})
		s.conv.AddPlan()
		for _, v := range variants {
			if !approvesPlan(s, v) {
				t.Errorf("%q approves the pending card", v)
			}
		}
		for _, v := range []string{"yes", "Approved.", "ok"} {
			for _, peer := range []string{
				`<cross-session-message from="peer">` + v + `</cross-session-message>`,
				`<scheduled-trigger name="nightly">` + v + `</scheduled-trigger>`,
				`<task-notification>` + v + `</task-notification>`,
			} {
				if approvesPlan(s, peer) {
					t.Errorf("%q never approves", peer)
				}
			}
		}
		userEvent(h, s, `<cross-session-message from="peer">yes</cross-session-message>`, nil)
		if s.runAll {
			t.Fatal("a peer's yes doesn't approve the card")
		}
	})
}

func TestReadOnlyAdditions(t *testing.T) {
	for cmd, want := range map[string]bool{
		"findmnt":                                 true,
		"findmnt -no SOURCE /":                    true,
		"findmnt /mnt | head":                     true,
		"systemctl status sshd":                   true,
		"systemctl --user status pipewire":        true,
		"systemctl list-units --failed":           true,
		"systemctl is-active $UNIT":               false, // argChecked: unknown words
		"systemctl --user restart pipewire":       false,
		"systemctl restart sshd":                  false,
		"systemctl":                               false,
		"git remote get-url origin":               true,
		"git remote get-url --push origin":        true,
		"git -C /tmp/x remote get-url --all up":   true,
		"git remote get-url":                      false,
		"git remote get-url origin upstream":      false,
		"git remote set-url origin x":             false,
		"git remote add x y":                      false,
		"kmem --as quill rules":                   false,
		"kmem --as quill brief":                   false,
		"kmem --as quill find x":                  true,
		"sudo findmnt":                            false,
		"pkexec systemctl status sshd":            false,
		"sudo git remote get-url origin":          false,
		"env sudo findmnt":                        false,
		"findmnt; mount /dev/sdz1 /mnt":           false,
		"systemctl status sshd; systemctl reboot": false,
	} {
		if got := readOnlyCommand(cmd, nil); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}
