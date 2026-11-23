package main

import (
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
)

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

// questionSig: the pending questions a reply ends with, normalised (case,
// whitespace and bold markers dropped): its plan questions with options, and
// its closing line.
func questionSig(text string) string {
	text = strings.TrimSpace(text)
	var parts []string
	for _, q := range planQuestions(text) {
		parts = append(parts, normSpace(q.Text))
		for _, o := range q.Options {
			parts = append(parts, normSpace(o))
		}
	}
	last := text[strings.LastIndex(text, "\n")+1:]
	return strings.Join(parts, "|") + "||" + normSpace(last)
}

func normSpace(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(mdNoise.Replace(s))), " ")
}

// keptPlan: the card a reply (text, after the plan's turn) leaves active
// because it ends with the same pending questions as the still-unanswered
// card (user 2026-11-23: a turn a task notification started repeated the
// question; the card stays, picks and all, instead of "replaced").
func keptPlan(c *chat.Conversation, text string) *chat.Item {
	if c == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind != chat.Plan {
			continue
		}
		if it.PermState == chat.PermPending && planState(c, it) == planActive {
			if pt := c.PlanText(it); strings.TrimSpace(pt) != "" && questionSig(pt) == questionSig(text) {
				return it
			}
		}
		return nil
	}
	return nil
}

// lastTurnText: the last assistant text of the current turn.
func lastTurnText(c *chat.Conversation) string {
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == chat.User || it.Kind == chat.Plan {
			return ""
		}
		if it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" {
			return it.Text
		}
	}
	return ""
}
