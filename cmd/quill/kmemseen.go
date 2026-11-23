package main

import (
	"fmt"
	"time"
)

// The kmem button's badge (user 2026-11-23, kmem task t491363) counts only
// what needs the user, not everything kmem lists:
//   - a question or plan the kmem assistant waits for an answer to (sticky:
//     it stays badged until answered, being seen does not clear it);
//   - a paused staged rollout;
//   - maintenance warnings that differ from the last seen report (a new report
//     with the same warnings is not news);
//   - open tasks titled "kmem issue:".
//
// Every item has a seen record (signature and time, in the preferences). An
// item is unread when it has no record or its signature changed since it was
// seen. Showing it in the kmem window marks it seen; "Mark all read"
// only marks the other items read: the assistant's question or plan stays badged until answered. The recycle bin, audit problems and user
// tasks stay on the window's Overview ("Waiting for you") but do not badge.
// Settings → General → Memory (kmem) can turn the badge off.
const (
	prefKmemBadge = "kmemBadge"     // bool, default on
	prefKmemSeen  = "kmemBadgeSeen" // JSON map: item key -> seenRec
)

type badgeItem struct {
	Key    string
	Sig    string // changes when the item changes
	Sticky bool   // badged until it is gone, not just seen
}

type seenRec struct {
	Sig       string    `json:"sig"`
	At        time.Time `json:"at"`
	Dismissed bool      `json:"dismissed,omitempty"` // unused since 2026-11-23 (questions stay until answered); kept so old prefs parse
}

// badgeItems lists what needs the user. waitKind/waitSig: the assistant's
// pending question or plan ("" when it waits for nothing).
func badgeItems(n kmemNeeds, waitKind, waitSig string) []badgeItem {
	var items []badgeItem
	if waitKind != "" {
		items = append(items, badgeItem{Key: "assist:" + waitKind, Sig: waitSig, Sticky: true})
	}
	if n.RolloutWait != "" {
		items = append(items, badgeItem{Key: "rollout", Sig: n.RolloutWait})
	}
	if n.Maint > 0 {
		items = append(items, badgeItem{Key: "maint", Sig: firstNonEmpty(n.MaintSig, fmt.Sprintf("n:%d", n.Maint))})
	}
	for _, t := range n.Issues {
		if t.Status == "open" {
			items = append(items, badgeItem{Key: "issue:" + t.ID, Sig: t.Title})
		}
	}
	return items
}

func unreadBadgeItems(items []badgeItem, seen map[string]seenRec) []badgeItem {
	var out []badgeItem
	for _, it := range items {
		r, ok := seen[it.Key]
		// A sticky item (the assistant's question or plan) badges until it is
		// answered (gone), even after Mark all read (user 2026-11-23).
		if !ok || r.Sig != it.Sig || it.Sticky {
			out = append(out, it)
		}
	}
	return out
}

func (d *desk) kmemBadgeOn() bool {
	return d.app.Preferences().BoolWithFallback(prefKmemBadge, true)
}

func (d *desk) loadKmemSeen() map[string]seenRec { return d.loadSeen(prefKmemSeen) }

func (d *desk) saveKmemSeen(m map[string]seenRec) { d.saveSeen(prefKmemSeen, m) }

// The kmem role's names for the generic badge (assist.go: assistNeeds,
// updateAssistBadge, markAssistSeen).

func (d *desk) kmemAssistWait() (kind, sig string) { return d.assistWait(d.assist()) }

func (d *desk) kmemBadgeNow() []badgeItem { return d.assistNeeds(d.assist()) }

// updateKmemBadge recomputes the bubble from the needs, the assistant and the
// seen records (UI thread).
func (d *desk) updateKmemBadge() { d.updateAssistBadge(d.assist()) }

// markKmemSeen records that the items were shown in the kmem window (UI
// thread). all (Mark all read) marks every shown item; a pending question
// still badges until answered.
func (d *desk) markKmemSeen(all bool) { d.markAssistSeen(d.assist(), all) }

// kmemUnread: how many items would badge now.
func (d *desk) kmemUnread() int { return d.assistUnread(d.assist()) }

// kmemAssistIs: s is the kmem assistant's session.
func (d *desk) kmemAssistIs(s *deskSession) bool { return d.assistIs(d.assist(), s) }
