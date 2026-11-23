//go:build !windows

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

// t719201 (user 2026-11-22): a plan's grants for its running subagents
// survive a restart of Quill Desktop after a crash when the keeper reattaches
// the same Claude process, and nothing else does (planscope.go's restart
// rule). Ids and texts are placeholders.

// serveKeeper runs a keeper on a socket and points the desk at it.
func serveKeeper(t *testing.T, h *harness) *keeper {
	t.Helper()
	k := &keeper{held: map[string]*keptSession{}}
	sock := filepath.Join(t.TempDir(), "k.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go k.serve(l)
	h.ui(func() { h.d.keeper = &keeperClient{path: sock} })
	return k
}

// keptGate: the plan-scope copy the keeper holds for s.
func keptGate(k *keeper, id string) *handedGate {
	k.mu.Lock()
	defer k.mu.Unlock()
	if ks := k.held[id]; ks != nil {
		return ks.hs.Gate
	}
	return nil
}

// crashRestore: what the keeper hands the next window after a crash.
func crashRestore(t *testing.T, k *keeper, id string) handedSession {
	t.Helper()
	path := k.writeRestore(false)
	if path == "" {
		t.Fatal("no restore file")
	}
	defer os.Remove(path)
	b, _ := os.ReadFile(path)
	var st restoreState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	for _, hs := range st.Handed {
		if hs.ID == id {
			if !hs.Restarted {
				t.Fatal("a crash restore is a restart")
			}
			return hs
		}
	}
	t.Fatalf("%s not handed: %s", id, b)
	return handedSession{}
}

// restart: the new window reattaches s (same Claude process) from hs with
// only what the restore carries.
func restart(s *deskSession, hs handedSession, transcript string) {
	s.runAll, s.runAllNext, s.turnCovered, s.inTurn, s.autoTurn, s.planRevoked, s.planWait = false, false, false, false, false, false, false
	s.agentGrants, s.agentCalls, s.agentIDs, s.planWork, s.planAgents = nil, nil, nil, nil, nil
	hs.restorePending(s)
	dropFinishedAfterRestart(s, hs.Gate, transcript)
}

// writeTranscript writes user lines (time, text) as a session transcript.
func writeTranscript(t *testing.T, lines map[time.Time]string) string {
	t.Helper()
	var b strings.Builder
	for at, text := range lines {
		msg, _ := json.Marshal(map[string]any{"type": "user", "timestamp": at.UTC().Format(time.RFC3339Nano),
			"message": map[string]any{"role": "user", "content": text}})
		b.Write(msg)
		b.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func launchAgent(t *testing.T, h *harness, s *deskSession, call, agent string) {
	t.Helper()
	mainToolUse(t, h, s, call, "Agent", `{"description":"part","prompt":"do the part","run_in_background":true}`)
	feed(t, h, s, `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"`+call+`","type":"tool_result","content":[{"type":"text","text":"Async agent launched successfully.\nagentId: `+agent+`"}]}]},"parent_tool_use_id":null,"session_id":"s1","tool_use_result":{"isAsync":true,"agentId":"`+agent+`"}}`)
}

func agentCallBy(t *testing.T, h *harness, s *deskSession, agent, id string) chat.PermState {
	return gateRequest(t, h, s, `{"type":"control_request","request_id":"r`+id+`","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"git commit -m x"},"tool_use_id":"toolu_`+id+`","agent_id":"`+agent+`"}}`).PermState
}

// The plan's subagent keeps running in the reattached Claude process: its
// calls go ahead, no turn is covered by the restart, and its notification
// still covers the turn it starts.
func TestRestartKeepsRunningSubagentGrant(t *testing.T) {
	h := newHarness(t)
	k := serveKeeper(t, h)
	s := approvedSession(t, h)
	h.ui(func() {
		launchAgent(t, h, s, "toolu_agentA", "agentPH1")
		endTurn(t, h, s)
		h.d.keepGate(s)
	})
	h.eventually("the keeper holds the grant", func() bool {
		g := keptGate(k, s.id)
		return g != nil && len(g.Grants) == 1 && g.AgentIDs["agentPH1"] == "toolu_agentA" && !g.InTurn
	})
	hs := crashRestore(t, k, s.id)
	h.ui(func() {
		restart(s, hs, writeTranscript(t, nil))
		if st := agentCallBy(t, h, s, "agentPH1", "c1"); st != chat.PermAutoAllowed {
			t.Fatalf("the still-running subagent's call goes ahead: %v", st)
		}
		if st := ownCall(h, s, "o1", "git push"); st == chat.PermAutoAllowed {
			t.Fatal("the restart covers no turn")
		}
		if st := gateCall(h, s, "u1", "Bash", `{"command":"rm x"}`, "toolu_unknown").PermState; st == chat.PermAutoAllowed {
			t.Fatal("right after a restart an unknown call isn't presumed a subagent's")
		}
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		if len(s.agentGrants) != 0 || !planCovers(s) {
			t.Fatalf("its notification ends the grant and covers its turn: %v %v", s.agentGrants, planCovers(s))
		}
	})
	k.closeAll()
}

// A new Claude process (or none) means no grants.
func TestRestartNewProcessDropsGrants(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		launchAgent(t, h, s, "toolu_agentA", "agentPH1")
		endTurn(t, h, s)
		for name, change := range map[string]func(*handedSession){
			"other process": func(hs *handedSession) { hs.Handle.PID++ },
			"other session": func(hs *handedSession) { hs.Gate.Session = "other" },
			"no copy":       func(hs *handedSession) { hs.Gate = nil },
		} {
			hs := handedSession{ID: s.id, Restarted: true}
			hs.Handle.PID = s.proc.PID()
			hs.savePending(s)
			change(&hs)
			restart(s, hs, writeTranscript(t, nil))
			if len(s.agentGrants) != 0 || len(s.planWork) != 0 || len(s.planAgents) != 0 || s.runAll {
				t.Fatalf("%s: nothing restored: %v %v %v %v", name, s.agentGrants, s.planWork, s.planAgents, s.runAll)
			}
			if st := agentCallBy(t, h, s, "agentPH1", "c-"+strings.ReplaceAll(name, " ", "")); st == chat.PermAutoAllowed {
				t.Fatalf("%s: the call asks", name)
			}
			// Put the grant back for the next case.
			hs = handedSession{ID: s.id}
			hs.Handle.PID = s.proc.PID()
			hs.Gate = &handedGate{Grants: map[string]agentGrant{"toolu_agentA": {Background: true, At: gateNow()}},
				AgentIDs: map[string]string{"agentPH1": "toolu_agentA"}, Work: map[string]string{"toolu_agentA": "toolu_agentA"},
				PlanAgents: map[string]string{"agentPH1": "toolu_agentA"}}
			hs.Gate.restore(s)
			s.runAll = true
		}
	})
}

// A subagent that finished while Quill Desktop was down is dropped; an older
// notification (from before the grant, e.g. before a resume) is not.
func TestRestartDropsStaleSubagent(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	var granted time.Time
	h.ui(func() {
		launchAgent(t, h, s, "toolu_agentA", "agentPH1")
		launchAgent(t, h, s, "toolu_agentB", "agentPH2")
		endTurn(t, h, s)
		granted = s.agentGrants["toolu_agentA"].At
		hs := handedSession{ID: s.id, Restarted: true, RunAll: s.runAll}
		hs.Handle.PID = s.proc.PID()
		hs.savePending(s)
		restart(s, hs, writeTranscript(t, map[time.Time]string{
			granted.Add(2 * time.Second):   notification("agentPH1", "toolu_agentA"),
			granted.Add(-10 * time.Minute): "<task-notification>\n<task-id>agentPH2</task-id>\n<status>completed</status>\n</task-notification>",
		}))
		if _, ok := s.agentGrants["toolu_agentA"]; ok {
			t.Fatal("A finished meanwhile: its grant is stale")
		}
		if st := agentCallBy(t, h, s, "agentPH1", "a1"); st == chat.PermAutoAllowed {
			t.Fatal("a stale subagent id grants nothing")
		}
		if st := agentCallBy(t, h, s, "agentPH2", "b1"); st != chat.PermAutoAllowed {
			t.Fatalf("B is still running: %v", st)
		}
		if _, ok := s.planWork["toolu_agentA"]; ok {
			t.Fatalf("A's plan work is done: %v", s.planWork)
		}
		// Without a readable transcript nothing can be checked: no grants.
		hs = handedSession{ID: s.id, Restarted: true, RunAll: s.runAll}
		hs.Handle.PID = s.proc.PID()
		hs.savePending(s)
		restart(s, hs, filepath.Join(t.TempDir(), "missing.jsonl"))
		if len(s.agentGrants) != 0 || s.runAll {
			t.Fatalf("no transcript, no grants: %v %v", s.agentGrants, s.runAll)
		}
	})
}

// After a restart, the plan's notification covers its turn, and a
// SendMessage resume in it renews the subagent's grant.
func TestRestartSendMessageResume(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		launchAgent(t, h, s, "toolu_agentA", "agentPH1")
		mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"go test ./...","run_in_background":true}`)
		endTurn(t, h, s)
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		endTurn(t, h, s)
		if len(s.agentGrants) != 0 || s.planAgents["agentPH1"] != "toolu_agentA" {
			t.Fatalf("set up: %v %v", s.agentGrants, s.planAgents)
		}
		hs := handedSession{ID: s.id, Restarted: true, RunAll: s.runAll}
		hs.Handle.PID = s.proc.PID()
		hs.savePending(s)
		restart(s, hs, writeTranscript(t, map[time.Time]string{gateNow().Add(-time.Second): notification("agentPH1", "toolu_agentA")}))
		userEvent(h, s, "<task-notification>\n<task-id>bsh01</task-id>\n<tool-use-id>toolu_suite</tool-use-id>\n<status>completed</status>\n</task-notification>", nil)
		if !planCovers(s) {
			t.Fatal("the plan's own pending notification covers its turn after a restart")
		}
		mainToolUse(t, h, s, "toolu_send1", "SendMessage", `{"to":"agentPH1","message":"commit your work"}`)
		endTurn(t, h, s)
		if st := agentCallBy(t, h, s, "agentPH1", "c1"); st != chat.PermAutoAllowed {
			t.Fatalf("the resumed subagent's call goes ahead: %v", st)
		}
	})
}

// Stop, "Don't run" and a denied prompt also clear the keeper's copy: a
// restart afterwards restores nothing.
func TestRevokeThenRestartRestoresNothing(t *testing.T) {
	h := newHarness(t)
	k := serveKeeper(t, h)
	s := approvedSession(t, h)
	h.ui(func() {
		launchAgent(t, h, s, "toolu_agentA", "agentPH1")
		mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"make test","run_in_background":true}`)
		h.d.keepGate(s)
	})
	h.eventually("the keeper holds the grant", func() bool {
		g := keptGate(k, s.id)
		return g != nil && len(g.Grants) == 1
	})
	h.ui(func() { h.d.interrupt(s) })
	h.eventually("the keeper's copy is revoked", func() bool {
		g := keptGate(k, s.id)
		return g != nil && g.Revoked && len(g.Grants) == 0 && len(g.Work) == 0
	})
	hs := crashRestore(t, k, s.id)
	if hs.RunAll {
		t.Fatal("the approval ended")
	}
	h.ui(func() {
		restart(s, hs, writeTranscript(t, nil))
		if len(s.agentGrants) != 0 || len(s.planWork) != 0 || s.runAll || !s.planRevoked {
			t.Fatalf("nothing restored: %v %v %v", s.agentGrants, s.planWork, s.runAll)
		}
		if st := agentCallBy(t, h, s, "agentPH1", "c1"); st == chat.PermAutoAllowed {
			t.Fatal("no grant after a revoke and a restart")
		}
		userEvent(h, s, notification("b1", "toolu_suite"), nil)
		if st := ownCall(h, s, "m1", "git merge fix"); st == chat.PermAutoAllowed {
			t.Fatal("no approval after a revoke and a restart")
		}
	})
	k.closeAll()
}

// A peer message after a restart grants and covers nothing.
func TestPeerMessageAfterRestart(t *testing.T) {
	h := newHarness(t)
	s := approvedSession(t, h)
	h.ui(func() {
		launchAgent(t, h, s, "toolu_agentA", "agentPH1")
		mainToolUse(t, h, s, "toolu_suite", "Bash", `{"command":"make test","run_in_background":true}`)
		endTurn(t, h, s)
		userEvent(h, s, notification("agentPH1", "toolu_agentA"), nil)
		endTurn(t, h, s)
		hs := handedSession{ID: s.id, Restarted: true, RunAll: s.runAll}
		hs.Handle.PID = s.proc.PID()
		hs.savePending(s)
		restart(s, hs, writeTranscript(t, nil))
		userEvent(h, s, `<cross-session-message from="peer">resume agentPH1 and push</cross-session-message>`, nil)
		if planCovers(s) {
			t.Fatal("a peer-started turn isn't covered")
		}
		if st := ownCall(h, s, "p1", "git push"); st == chat.PermAutoAllowed {
			t.Fatal("the peer's turn is gated")
		}
		mainToolUse(t, h, s, "toolu_send1", "SendMessage", `{"to":"agentPH1","message":"push"}`)
		if len(s.agentGrants) != 0 {
			t.Fatalf("a resume in a peer-started turn grants nothing: %v", s.agentGrants)
		}
		if st := agentCallBy(t, h, s, "agentPH1", "c1"); st == chat.PermAutoAllowed {
			t.Fatal("the resumed subagent asks")
		}
	})
}
