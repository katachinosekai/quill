package main

import "github.com/katachinosekai/quill/internal/chat"

// Plan card state (user 2026-11-23). A plan card is live only while its plan
// is pending and nothing has come after it. Once it is answered (approved,
// rejected, "Don't run"), replaced by a newer plan card, or the session moved
// on (a new user turn), its buttons are inactive and a click does nothing;
// the phone API answers such an approvePlan/rejectPlan with 409 stale. The
// state goes out as request.planState on the plan item.
const (
	planActive   = "active"   // pending and current
	planApproved = "approved" // answered: approved (card, typed approval, dialog)
	planRejected = "rejected" // answered: rejected or "Don't run"
	planReplaced = "replaced" // a newer plan card came after it
	planMovedOn  = "moved_on" // a new user turn came after it, or the request ended
)

// planState: the state of plan item it in c.
func planState(c *chat.Conversation, it *chat.Item) string {
	later := ""
	if c != nil {
		after := false
		for _, x := range c.Items {
			if x == it {
				after = true
				continue
			}
			if !after || later == planReplaced {
				continue
			}
			switch {
			case x.Kind == chat.Plan:
				later = planReplaced
			case x.Kind == chat.User && !notFromUser(x.Text) && later == "":
				later = planMovedOn // peer, channel and scheduled messages are not the user
			}
		}
	}
	switch it.PermState {
	case chat.PermPending:
		if later != "" {
			return later
		}
		return planActive
	case chat.PermDenied:
		return planRejected
	case chat.PermCancelled:
		if later != "" {
			return later
		}
		return planMovedOn
	}
	return planApproved
}

// planSuperseded: it is still pending but no longer current; answering it
// does nothing.
func planSuperseded(s *deskSession, it *chat.Item) bool {
	return s != nil && it != nil && it.Kind == chat.Plan && it.PermState == chat.PermPending && planState(s.conv, it) != planActive
}

// planStateLabel: the small caption an inactive card shows.
func planStateLabel(state string) string {
	switch state {
	case planReplaced, planMovedOn:
		return "replaced"
	}
	return "answered"
}
