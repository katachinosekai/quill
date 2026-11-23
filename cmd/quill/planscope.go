package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// Approval scope of an approved plan (t2debee, user 2026-11-22).
//
// The plan is approved by the user only: the plan card's "Approve plan & run
// all", or a typed approval of a pending plan (approvesPlan). The approval
// (s.runAll) then covers:
//
//  1. The turn the approval starts: every call of the session's own (and a
//     turn the user's retry of a blocked action starts while it lasts).
//  2. The background work started in a covered turn: Bash calls with
//     run_in_background and Agent calls (s.planWork). A subagent started in
//     a covered turn is granted (agentGrants) and its calls, and those of
//     agents it starts, go ahead until it finishes, even while the user talks
//     to the session meanwhile (track by subagent, 2026-11-22).
//  3. A later turn of the session started only by the task notification of
//     such background work: covered, so the plan's remaining steps (merge
//     after the suite, commit after the subagent) run without asking again.
//     The notification is matched by its <tool-use-id> or <task-id> against
//     the work the plan started; any other notification starts an uncovered
//     turn.
//  4. A SendMessage resume, made in a covered turn, of a subagent a plan
//     started: its grant is renewed until it finishes again.
//
// Nothing else covers a turn. A turn started by a peer (cross-session) or
// channel message or a scheduled trigger is never covered, and such a
// message arriving inside a covered turn ends the coverage for the rest of
// that turn: a peer can't ride on the user's approval. These messages never
// approve and never end the plan itself either. An idle session between
// turns is not covered (no call is the session's own then; a granted
// subagent's still is).
//
// The approval ends (the plan's later notifications no longer cover a turn):
//   - on a new message of the user that isn't an approval (subagent grants
//     live on until those subagents finish);
//   - when a turn ends asking for approval of new work (plan card);
//   - on Stop, "Don't run" on a plan card, or the user denying a permission
//     prompt: these also end every subagent grant, so later calls ask.
//   - when the Claude process exits (its subagents and jobs are gone).
//
// A reload hands the live state over with the session (handedGate).
//
// A restart (t719201, user 2026-11-22). Quitting Quill Desktop stops every
// Claude process, so a quit and start begins with fresh processes and no
// grants. After a crash the keeper (keeper_unix.go) still holds the Claude
// processes and reattaches them; it keeps the latest copy of this state with
// each session (keepGate sends it whenever it changes, in order, so a revoke
// also clears the kept copy). On such a restart (restoreRestart):
//   - The copy is used only when it belongs to the same session and the same
//     Claude process (its pid) the keeper reattaches. Otherwise, after a
//     Stop / "Don't run" / denied prompt (revoked), or when no Claude
//     process is reattached (a new one starts), nothing is restored.
//   - Only the grants of subagents and the plan's own pending background
//     work survive (with the approval, so that work's task notification
//     still covers the turn it starts, and a SendMessage resume in that turn
//     renews a grant as before). No turn is covered by the restart itself:
//     the current turn, if one was running, goes on uncovered, and no call
//     is presumed a subagent's until it ends (gateUnsure).
//   - A subagent or job that finished while Quill Desktop was down is
//     dropped: its task notification (or, for a foreground subagent, its
//     result) in the session's transcript, written after the grant was made
//     (dropFinished). Without a readable transcript every grant is dropped.
//   - Peer, channel and scheduled messages still never grant or cover.

// planCovers: the session's own call in the current turn is approved.
func planCovers(s *deskSession) bool { return s.runAll && s.turnCovered }

// startPlan: the user approved a plan; the current (or next) turn is covered.
func startPlan(s *deskSession) {
	s.runAll, s.runAllNext, s.turnCovered, s.planRevoked = true, false, true, false
}

// endPlan: the approval ends. Running subagents keep their grants.
func endPlan(s *deskSession) {
	s.runAll, s.runAllNext, s.turnCovered = false, false, false
	s.planWork = nil
}

// revokePlan: the user stopped or refused (Stop, "Don't run", a denied
// prompt): the approval and every grant under it end.
func revokePlan(s *deskSession) {
	endPlan(s)
	s.agentGrants, s.agentCalls, s.agentIDs, s.planAgents = nil, nil, nil, nil
	s.planRevoked = true
	s.grantRev++
}

// turnEnded: the session's turn is over; the next one starts uncovered.
func turnEnded(s *deskSession) {
	s.turnCovered, s.autoTurn, s.gateUnsure, s.autoPeer = false, false, false, false
}

// notUserMessage classifies a message the user didn't write (notFromUser).
// A system reminder changes nothing. A task notification ends the grants
// and plan work it names and, if it starts a turn, covers that turn when it
// names the approved plan's own background work. A peer, channel or
// scheduled message starts an uncovered turn, or ends the coverage of the
// turn it arrives in.
func notUserMessage(s *deskSession, text string) {
	t := strings.TrimSpace(text)
	idle := !s.inTurn && !s.autoTurn
	switch {
	case strings.HasPrefix(t, "<system-reminder"):
		return
	case strings.HasPrefix(t, "<task-notification"):
		ours := planWorkDone(s, t)
		endAgentGrants(s, t)
		if idle {
			s.autoTurn = true
			s.turnCovered = ours && s.runAll
			// Auto-approve covers it only for work it covered (autoapprove.go).
			s.autoPeer = !autoOwnNotice(s, t)
		}
	default:
		if idle {
			s.autoTurn = true
		}
		s.turnCovered = false
		s.autoPeer = true // nor auto-approve, for the rest of this turn
	}
}

var (
	taskIDRe    = regexp.MustCompile(`<task-id>([^<\s]+)</task-id>`)
	bgShellIDRe = regexp.MustCompile(`running in background with ID: ([A-Za-z0-9_-]+)`)
)

// addPlanWork records id as (part of) the background work root started.
func addPlanWork(s *deskSession, id, root string) {
	if id == "" || len(s.planWork) >= maxAgentCalls {
		return
	}
	if s.planWork == nil {
		s.planWork = map[string]string{}
	}
	s.planWork[id] = root
}

// planWorkDone: the notification names background work of the approved
// plan; that work is finished and forgotten.
func planWorkDone(s *deskSession, text string) bool {
	var roots []string
	for _, re := range []*regexp.Regexp{toolUseIDRe, taskIDRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if r, ok := s.planWork[m[1]]; ok {
				roots = append(roots, r)
			}
		}
	}
	for _, r := range roots {
		for id, root := range s.planWork {
			if root == r {
				delete(s.planWork, id)
			}
		}
	}
	// A resumed subagent's notification may name it by agent id only.
	for _, m := range taskIDRe.FindAllStringSubmatch(text, -1) {
		if g, ok := s.agentIDs[m[1]]; ok {
			if _, live := s.agentGrants[g]; live {
				endAgentGrant(s, g)
			}
		}
	}
	return len(roots) > 0
}

// planCall records a call the session makes in a covered turn: background
// Bash and Agent calls become plan work, an Agent call is granted, and a
// SendMessage to a subagent a plan started renews its grant.
func planCall(s *deskSession, name, id string, input json.RawMessage) {
	if id == "" || !planCovers(s) {
		return
	}
	var in struct {
		Background bool   `json:"run_in_background"`
		Name       string `json:"name"`
		To         string `json:"to"`
	}
	json.Unmarshal(input, &in)
	switch name {
	case "Bash":
		if in.Background {
			addPlanWork(s, id, id)
		}
	case "Agent", "Task":
		grantAgentCall(s, id, input)
		if in.Background {
			addPlanWork(s, id, id)
		}
		if in.Name != "" {
			addPlanAgent(s, in.Name, id)
		}
	case "SendMessage":
		if !resumeAgent(s, in.To, id) && len(s.planSends) < maxBatchCalls {
			if s.planSends == nil {
				s.planSends = map[string]bool{}
			}
			s.planSends[id] = true // granted if its result names a resumed subagent
		}
	}
}

// A SendMessage in a covered turn that resumes a subagent of this session
// grants it like an Agent call made then, whether or not a plan started it
// (Zomboid 2026-11-23: the ELTF 1.3.10 and 1.3.11 deploys went to an earlier
// subagent by SendMessage; it wasn't in planAgents, so a user message moments
// later ended the turn's cover and its first command was refused). The
// result names the subagent (resumedAgentId); a peer session's never does.
var resumedAgentRe = regexp.MustCompile(`\\?"(?:resumedAgentId|revivedAgentId)\\?"\s*:\s*\\?"([A-Za-z0-9_-]+)`)

// learnResumes grants the subagent a covered SendMessage resumed.
func learnResumes(s *deskSession, ev *protocol.Event) {
	if len(s.planSends) == 0 || ev.Type != "user" || ev.IsSubagent() || ev.Message == nil {
		return
	}
	for _, b := range ev.Message.Blocks() {
		if b.Type != "tool_result" || !s.planSends[b.ToolUseID] {
			continue
		}
		delete(s.planSends, b.ToolUseID)
		m := resumedAgentRe.FindSubmatch(b.Content)
		if m == nil {
			m = resumedAgentRe.FindSubmatch(ev.ToolUseResult)
		}
		if m == nil {
			continue
		}
		g, agent := b.ToolUseID, string(m[1])
		if s.agentGrants == nil {
			s.agentGrants = map[string]agentGrant{}
		}
		s.agentGrants[g] = agentGrant{Background: true, At: gateNow()}
		s.grantRev++
		addAgentID(s, agent, g)
		addPlanWork(s, agent, g)
		addPlanWork(s, g, g)
	}
}

func addPlanAgent(s *deskSession, agent, g string) {
	if agent == "" || len(s.planAgents) >= maxAgentCalls {
		return
	}
	if s.planAgents == nil {
		s.planAgents = map[string]string{}
	}
	s.planAgents[agent] = g
}

// resumeAgent: a covered SendMessage to a subagent a plan started (by agent
// id or name) grants it again until its next notification.
func resumeAgent(s *deskSession, to, id string) bool {
	g, ok := s.planAgents[to]
	if !ok {
		return false
	}
	if s.agentGrants == nil {
		s.agentGrants = map[string]agentGrant{}
	}
	s.agentGrants[g] = agentGrant{Background: true, At: gateNow()}
	s.grantRev++
	for a, root := range s.planAgents {
		if root == g {
			addAgentID(s, a, g)
			addPlanWork(s, a, g)
		}
	}
	addPlanWork(s, g, g)
	addPlanWork(s, id, g)
	return true
}

// learnPlanWork: a launch result or task_started event names the task id of
// plan work (a background shell's id, an agent's id).
func learnPlanWork(s *deskSession, ev *protocol.Event) {
	if len(s.planWork) == 0 {
		return
	}
	if ev.Type == "system" && ev.Subtype == "task_started" {
		if r, ok := s.planWork[ev.ToolUseID]; ok {
			addPlanWork(s, ev.TaskID, r)
		}
		return
	}
	if ev.Type != "user" || ev.IsSubagent() || ev.Message == nil {
		return
	}
	for _, b := range ev.Message.Blocks() {
		r, ok := s.planWork[b.ToolUseID]
		if b.Type != "tool_result" || !ok {
			continue
		}
		for _, re := range []*regexp.Regexp{bgShellIDRe, agentIDRe} {
			if m := re.FindSubmatch(b.Content); m != nil {
				addPlanWork(s, string(m[1]), r)
			}
		}
	}
}

// handedGate: the plan-scope state a reload hands over with the session,
// and the copy the keeper holds for a restart after a crash.
type handedGate struct {
	Covered    bool                  `json:"covered,omitempty"`
	AutoTurn   bool                  `json:"autoTurn,omitempty"`
	InTurn     bool                  `json:"inTurn,omitempty"`
	Revoked    bool                  `json:"revoked,omitempty"`
	Grants     map[string]agentGrant `json:"grants,omitempty"`
	Calls      map[string]string     `json:"calls,omitempty"`
	AgentIDs   map[string]string     `json:"agentIds,omitempty"`
	Work       map[string]string     `json:"work,omitempty"`
	PlanAgents map[string]string     `json:"planAgents,omitempty"`
	Session    string                `json:"session,omitempty"` // whose state it is
	PID        int                   `json:"pid,omitempty"`     // the Claude process it belongs to
	At         time.Time             `json:"at,omitzero"`       // when it was taken
}

// gateNow is the clock for grants and snapshots (tests set it).
var gateNow = time.Now

func saveGate(s *deskSession) *handedGate {
	g := &handedGate{Covered: s.turnCovered, AutoTurn: s.autoTurn, InTurn: s.inTurn, Revoked: s.planRevoked,
		Grants: s.agentGrants, Calls: s.agentCalls, AgentIDs: s.agentIDs, Work: s.planWork, PlanAgents: s.planAgents,
		Session: s.id, At: gateNow()}
	if s.proc != nil {
		g.PID = s.proc.PID()
	}
	return g
}

func (g *handedGate) restore(s *deskSession) {
	if g == nil {
		return
	}
	s.turnCovered, s.autoTurn, s.inTurn, s.planRevoked = g.Covered, g.AutoTurn, g.InTurn, g.Revoked
	s.agentGrants, s.agentCalls, s.agentIDs, s.planWork, s.planAgents = g.Grants, g.Calls, g.AgentIDs, g.Work, g.PlanAgents
}

// restoreRestart: the restart rule above. id and pid are the session and
// Claude process the keeper reattached.
func (g *handedGate) restoreRestart(s *deskSession, id string, pid int) {
	s.turnCovered, s.inTurn, s.autoTurn, s.gateUnsure = false, false, false, false
	s.agentGrants, s.agentCalls, s.agentIDs, s.planWork, s.planAgents = nil, nil, nil, nil, nil
	if g == nil || g.Revoked || g.Session != id || g.PID == 0 || g.PID != pid {
		s.runAll, s.runAllNext = false, false
		s.planRevoked = g != nil && g.Revoked
		return
	}
	s.agentGrants, s.agentCalls, s.agentIDs, s.planWork, s.planAgents = g.Grants, g.Calls, g.AgentIDs, g.Work, g.PlanAgents
	if len(s.planWork) == 0 {
		s.runAll, s.runAllNext = false, false // nothing left the approval could cover
	}
	// A turn that was running goes on, uncovered.
	s.autoTurn = g.InTurn || g.AutoTurn
	s.gateUnsure = true
	s.grantRev++
}

// transcriptNote: a task notification, or the tool result of one of the
// calls asked about, in a session's transcript.
type transcriptNote struct {
	At     time.Time
	Text   string // the notification
	Result string // the tool_use_id of a main-session tool result
}

// transcriptNotes reads path's task notifications and the main session's
// tool results for the calls in results. ok is false when it can't be read.
func transcriptNotes(path string, results map[string]bool) (notes []transcriptNote, ok bool) {
	if path == "" {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		notify := bytes.Contains(line, []byte("<task-notification>")) || bytes.Contains(line, []byte(`\u003ctask-notification`))
		if notify || len(results) > 0 && bytes.Contains(line, []byte("tool_result")) {
			if ev, perr := protocol.ParseLine(line); perr == nil {
				at, _ := time.Parse(time.RFC3339Nano, ev.Timestamp)
				if notify {
					text := string(line) // the notification may sit outside the message too
					if ev.Message != nil {
						text += "\n" + protocol.ContentText(ev.Message.Content)
					}
					notes = append(notes, transcriptNote{At: at, Text: text})
				}
				if ev.Type == "user" && !ev.IsSubagent() && ev.Message != nil {
					for _, b := range ev.Message.Blocks() {
						if b.Type == "tool_result" && results[b.ToolUseID] {
							notes = append(notes, transcriptNote{At: at, Result: b.ToolUseID})
						}
					}
				}
			}
		}
		if err == io.EOF {
			return notes, true
		}
		if err != nil {
			return nil, false
		}
	}
}

// restartSlack: transcript and Quill Desktop times may differ a little (the
// stream is read after the transcript is written); a notification this close
// before a grant counts as after it (dropping is the safe side).
const restartSlack = time.Minute

// dropFinishedAfterRestart ends the restored grants and plan work whose
// subagent or job finished while Quill Desktop was down (restart rule).
func dropFinishedAfterRestart(s *deskSession, g *handedGate, path string) {
	if g == nil || len(s.agentGrants) == 0 && len(s.planWork) == 0 {
		return
	}
	fg := map[string]bool{}
	for id, gr := range s.agentGrants {
		if !gr.Background {
			fg[id] = true
		}
	}
	notes, ok := transcriptNotes(path, fg)
	dropFinished(s, g.At, notes, ok)
}

func dropFinished(s *deskSession, since time.Time, notes []transcriptNote, ok bool) {
	if !ok {
		for id := range s.agentGrants {
			endAgentGrant(s, id)
		}
		s.planWork = nil
		s.runAll, s.runAllNext = false, false
		return
	}
	after := func(n transcriptNote, t time.Time) bool { return !n.At.Before(t.Add(-restartSlack)) }
	for _, n := range notes {
		var named []string
		if n.Result != "" {
			named = append(named, n.Result)
		}
		for _, m := range toolUseIDRe.FindAllStringSubmatch(n.Text, -1) {
			named = append(named, m[1])
		}
		for _, m := range taskIDRe.FindAllStringSubmatch(n.Text, -1) {
			if gid, ok := s.agentIDs[m[1]]; ok {
				named = append(named, gid)
			}
		}
		for _, id := range named {
			if gr, live := s.agentGrants[id]; live && after(n, gr.At) {
				endAgentGrant(s, id)
			}
		}
		if n.Text != "" && after(n, since) {
			planWorkDone(s, n.Text)
		}
	}
	if len(s.planWork) == 0 {
		s.runAll, s.runAllNext = false, false
	}
}

// gateMark: what keepGate compares to know the plan-scope state changed.
type gateMark struct {
	rev, grants, calls, ids, work, agents      int
	runAll, covered, inTurn, autoTurn, revoked bool
	auto                                       bool // auto-approve (autoapprove.go): a restart tells Claude it is off
}

func markGate(s *deskSession) gateMark {
	return gateMark{s.grantRev, len(s.agentGrants), len(s.agentCalls), len(s.agentIDs), len(s.planWork), len(s.planAgents),
		s.runAll, s.turnCovered, s.inTurn, s.autoTurn, s.planRevoked, s.autoApprove}
}

// keepGate sends the session's state to the keeper again when its plan
// scope changed, so a restart after a crash finds the latest copy.
func (d *desk) keepGate(s *deskSession) {
	if s == nil || s.proc == nil || markGate(s) == s.keptGate {
		return
	}
	d.keepSession(s)
}

// isPermissionDeny: the user refused a permission prompt (not skipped a
// question).
func isPermissionDeny(it *chat.Item, dec protocol.Decision) bool {
	return dec == protocol.Deny && it.Kind == chat.Permission && !isQuestion(it)
}
