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
