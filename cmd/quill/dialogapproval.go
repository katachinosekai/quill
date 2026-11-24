package main

import (
	"encoding/json"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// Approval through Claude's AskUserQuestion dialog (user 2026-11-23). When one
// answered question is an approval question (it proposes doing something or
// asks to approve, and offers a go-ahead and a stop option), a go-ahead pick
// counts as plan approval exactly as the plan card's does (startPlan +
// keepGate), and a stop pick as "Don't run" (revokePlan + keepGate). Other
// dialog answers never approve. Only the user answering the dialog counts:
// answerQuestions is the only way in (the dialog's Send answers, or the phone
// API's answer), never a peer, channel or scheduled message, a pre-filled
// answer (the request's input already carries "answers"), or a turn such a
// message started.

// dialogApproval: what the answers to the dialog (input = its AskUserQuestion
// input) say about approval: go (a go-ahead pick) or stop. A stop wins.
func dialogApproval(input json.RawMessage, answers map[string]string) (goAhead, stop bool) {
	var pre struct {
		Answers map[string]string `json:"answers"`
	}
	if json.Unmarshal(input, &pre) == nil && len(pre.Answers) > 0 {
		return false, false // pre-filled: not the user's pick
	}
	for _, q := range protocol.ParseQuestions(input) {
		if q.MultiSelect || !chat.ApprovalQuestion(q.Question) {
			continue
		}
		hasStop, hasGo := false, false
		for _, o := range q.Options {
			if stopOption.MatchString(o.Label) {
				hasStop = true
			} else {
				hasGo = true
			}
		}
		if !hasStop || !hasGo {
			continue
		}
		pick := strings.TrimSpace(answers[q.Question])
		for _, o := range q.Options {
			if pick != o.Label {
				continue
			}
			if stopOption.MatchString(o.Label) {
				stop = true
			} else {
				goAhead = true
			}
		}
	}
	return goAhead && !stop, stop
}

// dialogGate applies the user's answers to a dialog to the plan gate.
func (d *desk) dialogGate(s *deskSession, it *chat.Item, answers map[string]string) {
	if !d.planFirst || s.autoTurn {
		return
	}
	goAhead, stop := dialogApproval(it.Input, answers)
	switch {
	case stop:
		revokePlan(s)
		d.settlePlans(s, chat.PermDenied)
	case goAhead:
		d.approveNow(s) // a pending plan card is answered too: its buttons go inactive
	default:
		return
	}
	d.keepGate(s)
}

// settlePlans marks the session's pending plan cards answered (state) so
// their buttons go inactive, on the desktop, in the mirror and on the phone.
func (d *desk) settlePlans(s *deskSession, state chat.PermState) {
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind == chat.Plan {
			it.PermState = state
			d.chatFor(s, []*chat.Item{it})
		}
	}
}
