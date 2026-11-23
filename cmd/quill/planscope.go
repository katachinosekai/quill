package main

import (
	"encoding/json"
	"regexp"
	"strings"

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
}

// turnEnded: the session's turn is over; the next one starts uncovered.
func turnEnded(s *deskSession) {
	s.turnCovered, s.autoTurn = false, false
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
		}
	default:
		if idle {
			s.autoTurn = true
		}
		s.turnCovered = false
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
		resumeAgent(s, in.To, id)
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
func resumeAgent(s *deskSession, to, id string) {
	g, ok := s.planAgents[to]
	if !ok {
		return
	}
	if s.agentGrants == nil {
		s.agentGrants = map[string]agentGrant{}
	}
	s.agentGrants[g] = agentGrant{Background: true}
	for a, root := range s.planAgents {
		if root == g {
			addAgentID(s, a, g)
			addPlanWork(s, a, g)
		}
	}
	addPlanWork(s, g, g)
	addPlanWork(s, id, g)
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

// handedGate: the plan-scope state a reload hands over with the session.
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
}

func saveGate(s *deskSession) *handedGate {
	return &handedGate{Covered: s.turnCovered, AutoTurn: s.autoTurn, InTurn: s.inTurn, Revoked: s.planRevoked,
		Grants: s.agentGrants, Calls: s.agentCalls, AgentIDs: s.agentIDs, Work: s.planWork, PlanAgents: s.planAgents}
}

func (g *handedGate) restore(s *deskSession) {
	if g == nil {
		return
	}
	s.turnCovered, s.autoTurn, s.inTurn, s.planRevoked = g.Covered, g.AutoTurn, g.InTurn, g.Revoked
	s.agentGrants, s.agentCalls, s.agentIDs, s.planWork, s.planAgents = g.Grants, g.Calls, g.AgentIDs, g.Work, g.PlanAgents
}

// isPermissionDeny: the user refused a permission prompt (not skipped a
// question).
func isPermissionDeny(it *chat.Item, dec protocol.Decision) bool {
	return dec == protocol.Deny && it.Kind == chat.Permission && !isQuestion(it)
}
