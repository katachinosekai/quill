package main

import (
	"slices"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
)

func codexNativeApprovalPending(s *deskSession) bool {
	return slices.ContainsFunc(s.conv.PendingPermissions(), func(it *chat.Item) bool {
		return it.Kind == chat.Permission
	})
}

// Codex plan cards confirm the proposed scope in chat. Native permission
// requests still go through the bridge's ordinary approval flow.
func addCodexPlanCard(s *deskSession) []*chat.Item {
	text := lastTurnText(s.conv)
	if !s.conv.AsksApproval() || planSteps(text) == 0 {
		return nil
	}
	if existing := livePlan(s.conv); existing != nil {
		if reply := planReply(s.conv, existing); reply != nil && strings.TrimSpace(reply.Text) == strings.TrimSpace(text) {
			return nil
		}
	}
	items := s.conv.CancelPlans()
	items = append(items, s.conv.MarkPlanText()...)
	return append(items, s.conv.AddPlan())
}

func (d *desk) canApprovePlan(s *deskSession) bool {
	return d.canAnswer(s) && (!s.codex ||
		!s.conv.Busy && !s.proc.Busy() && !s.queueBlocked() && !codexNativeApprovalPending(s))
}

func (d *desk) answerCodexPlan(s *deskSession, it *chat.Item, ok bool, msg string) {
	if it.PermState != chat.PermPending {
		return
	}
	if !ok {
		it.PermState = chat.PermDenied
	} else {
		if !d.canApprovePlan(s) {
			return
		}
		s.cardGo = true
		if !d.sendTurn(s, msg) {
			return
		}
		it.PermState = chat.PermAllowed
	}
	d.chatFor(s, []*chat.Item{it})
	d.updateChrome()
	d.refreshSidebar()
	d.keepSession(s)
}
