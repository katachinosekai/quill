package main

import (
	"context"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
)

type approvalRevision struct {
	proc                   *session.Process
	input                  *sendEntry
	text                   string
	acknowledged, finished bool
}

func (d *desk) dismissApprovalProposal(s *deskSession) {
	revokePlan(s)
	s.planWait, s.cardGo = false, false
	s.retryCmd, s.retryRules, s.retryDest = "", nil, ""
	if items := s.conv.CancelPlans(); len(items) > 0 {
		d.chatFor(s, items)
	}
	d.keepGate(s)
	if d.current == s {
		d.updateChrome()
	}
	d.assistEvent(s)
}

func (d *desk) reviseTypedApproval(s *deskSession, input *sendEntry, text string) bool {
	if s == nil || (!s.codex && (len(s.conv.PendingPermissions()) == 0 || approvesPlan(s, text))) {
		return false
	}
	return d.reviseApproval(s, input, text)
}

func (d *desk) reviseApproval(s *deskSession, input *sendEntry, text string) bool {
	if s == nil || s.remote != nil || !s.running() || strings.TrimSpace(text) == "" {
		return false
	}
	if s.pendingRevision != nil {
		return true
	}
	if len(s.conv.PendingPermissions()) == 0 && !planPending(s) {
		return false
	}
	if s.codex && !codexNativeApprovalPending(s) {
		if plan := livePlan(s.conv); plan != nil && approvesPlan(s, text) {
			d.answerPlanMsg(s, plan, true, strings.TrimSpace(text))
			if plan.PermState == chat.PermAllowed && input != nil && strings.TrimSpace(input.Text) == strings.TrimSpace(text) {
				input.SetText("")
			}
			return true
		}
		return false // a plan card does not block an ordinary Codex follow-up
	}
	if s.agentHandoff != nil || s.stopping {
		d.notify(s, "Wait for the assistant to finish switching or stopping before sending a revision.")
		return true
	}
	r := &approvalRevision{proc: s.proc, input: input, text: strings.TrimSpace(text)}
	s.pendingRevision = r
	if !s.codex {
		d.dismissApprovalProposal(s)
	}
	if !s.conv.Busy && !r.proc.Busy() && len(s.conv.PendingPermissions()) == 0 {
		d.deliverApprovalRevision(s)
		return true
	}
	d.requestApprovalInterrupt(s, r)
	return true
}

func (d *desk) requestApprovalInterrupt(s *deskSession, r *approvalRevision) {
	d.chatFor(s, s.conv.PendingPermissions())
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := r.proc.Interrupt(ctx)
		d.do(func() {
			if s.pendingRevision != r {
				return
			}
			if err != nil {
				d.failApprovalRevision(s, r)
				return
			}
			r.acknowledged = true
			if r.finished {
				if !d.deliverApprovalRevision(s) {
					d.flushQueue(s)
				}
				d.trySwap()
			}
		})
	}()
	d.afterUI(8*time.Second, func() { d.failApprovalRevision(s, r) })
}

func (d *desk) finishApprovalInterrupt(s *deskSession) bool {
	r := s.pendingRevision
	if r == nil {
		return false
	}
	r.finished = true
	return !r.acknowledged || d.deliverApprovalRevision(s)
}

func pendingRevisionMatches(s *deskSession, text string) bool {
	return s != nil && s.pendingRevision != nil && s.pendingRevision.text != "" &&
		s.pendingRevision.text == strings.TrimSpace(text)
}

func (d *desk) failApprovalRevision(s *deskSession, r *approvalRevision) {
	if s.pendingRevision != r {
		return
	}
	s.pendingRevision = nil
	if r.text != "" {
		d.notify(s, "Could not stop the turn. Your revision was not sent.")
	} else {
		d.notify(s, "Could not stop the turn.")
	}
	d.chatFor(s, s.conv.PendingPermissions())
	d.assistEvent(s)
	d.trySwap()
}

func (d *desk) deliverApprovalRevision(s *deskSession) bool {
	r := s.pendingRevision
	if r == nil {
		return false
	}
	if r.text == "" {
		s.pendingRevision = nil
		return false
	}
	if s.proc != r.proc || !s.running() || s.agentHandoff != nil {
		d.failApprovalRevision(s, r)
		return false
	}
	s.pendingRevision = nil
	if !d.sendTurn(s, r.text) {
		return false
	}
	if in := r.input; in != nil && strings.TrimSpace(in.Text) == r.text {
		a := d.assistOf(s)
		if a != nil && a.input == in || d.messageSession(in, d.entrySession(in)) == s {
			in.SetText("")
		}
	}
	return true
}
