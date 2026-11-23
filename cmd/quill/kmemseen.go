package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
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

func (d *desk) loadKmemSeen() map[string]seenRec {
	m := map[string]seenRec{}
	if raw := d.app.Preferences().String(prefKmemSeen); raw != "" {
		if json.Unmarshal([]byte(raw), &m) != nil || m == nil {
			m = map[string]seenRec{}
		}
	}
	return m
}

func (d *desk) saveKmemSeen(m map[string]seenRec) {
	b, _ := json.Marshal(m)
	d.app.Preferences().SetString(prefKmemSeen, string(b))
}

// kmemAssistWait: the kmem assistant waits for the user: a plan to approve or
// a question (a permission or a reply to its question), with a signature that
// changes with each new one. UI thread.
func (d *desk) kmemAssistWait() (kind, sig string) {
	s := d.assist().s
	if s == nil || s.conv == nil || !s.running() {
		return "", ""
	}
	c := s.conv
	for _, it := range c.PendingPermissions() {
		if it.Kind == chat.Plan {
			return "plan", fmt.Sprint(it.Seq, it.RequestID)
		}
	}
	if p := c.PendingPermissions(); len(p) > 0 {
		return "question", fmt.Sprint(p[0].Seq, p[0].RequestID)
	}
	if !c.Busy && (c.AsksApproval() || c.AwaitsReply()) {
		for i := len(c.Items) - 1; i >= 0; i-- {
			if it := c.Items[i]; it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" {
				return "question", fmt.Sprint(it.Seq, it.UUID)
			}
		}
		return "question", "text"
	}
	return "", ""
}

func (d *desk) kmemBadgeNow() []badgeItem {
	k, sig := d.kmemAssistWait()
	return badgeItems(d.kmemNeedsNow, k, sig)
}

// updateKmemBadge recomputes the bubble from the needs, the assistant and the
// seen records (UI thread). Records of items that are gone are dropped, so an
// item that comes back is new again; only once kmem has answered.
func (d *desk) updateKmemBadge() {
	if d.kmemBubble == nil {
		return
	}
	items := d.kmemBadgeNow()
	seen := d.loadKmemSeen()
	if d.kmemGot {
		live := map[string]bool{}
		for _, it := range items {
			live[it.Key] = true
		}
		drop := false
		for k := range seen {
			if !live[k] {
				delete(seen, k)
				drop = true
			}
		}
		if drop {
			d.saveKmemSeen(seen)
		}
	}
	if !d.kmemBadgeOn() {
		d.kmemBubble.set(0)
		return
	}
	d.kmemBubble.set(len(unreadBadgeItems(items, seen)))
}

// markKmemSeen records that the items were shown in the kmem window (UI
// thread). all (Mark all read) marks every shown item; a pending question still badges until answered.
func (d *desk) markKmemSeen(all bool) {
	items := d.kmemBadgeNow()
	seen := d.loadKmemSeen()
	now := kmemNow()
	changed := false
	for _, it := range items {
		old, ok := seen[it.Key]
		if ok && old.Sig == it.Sig {
			continue // already recorded: keep its first seen time
		}
		seen[it.Key] = seenRec{Sig: it.Sig, At: now}
		changed = true
	}
	if changed {
		d.saveKmemSeen(seen)
	}
	d.updateKmemBadge()
}

// kmemUnread: how many items would badge now.
func (d *desk) kmemUnread() int {
	return len(unreadBadgeItems(d.kmemBadgeNow(), d.loadKmemSeen()))
}

// kmemAssistIs: s is the kmem assistant's session.
func (d *desk) kmemAssistIs(s *deskSession) bool {
	return s != nil && d.assist().s == s
}
