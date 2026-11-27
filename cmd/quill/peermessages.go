package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
)

const (
	pendingPeerMessagesKey = "pendingPeerMessages"
	peerMessageTTL         = 72 * time.Hour
	peerReceiptTTL         = 7 * 24 * time.Hour
	peerMessageLimit       = 256
)

type peerMessage struct {
	ID        string    `json:"id"`
	To        string    `json:"to"`
	From      string    `json:"from"`
	Text      string    `json:"text"`
	ExpiresAt time.Time `json:"expires_at"`
}

type peerMessages struct {
	// Persist the attempted recipient before writing to its process so a
	// restart can reconcile the transcript without sending an uncertain turn twice.
	Submitting map[string]string        `json:"submitting,omitempty"`
	Pending    []peerMessage            `json:"pending,omitempty"`
	Receipts   []operatorMessageReceipt `json:"receipts,omitempty"`
}

func (d *desk) savePeerMessages() {
	data, _ := json.Marshal(d.peerMessages)
	d.app.Preferences().SetString(pendingPeerMessagesKey, string(data))
}

// loadPeerMessages runs on the UI thread. Persist the migrated state before
// removing the old preference keys so a restart cannot reimport those messages.
func (d *desk) loadPeerMessages(now time.Time) *peerMessages {
	if d.peerMessages != nil {
		return d.peerMessages
	}
	store := &peerMessages{}
	d.peerMessages = store
	prefs := d.app.Preferences()
	if data := prefs.String(pendingPeerMessagesKey); data != "" {
		if json.Unmarshal([]byte(data), store) == nil {
			for _, role := range []string{roleQuill, roleKmem} {
				key := "operatorInbox." + role
				if prefs.String(key) != "" {
					prefs.RemoveValue(key)
				}
			}
		}
		return store
	}
	for _, role := range []string{roleQuill, roleKmem} {
		var legacy []peerMessage
		if json.Unmarshal([]byte(prefs.String("operatorInbox."+role)), &legacy) != nil {
			continue
		}
		for _, msg := range legacy {
			msg.To, msg.From, msg.ExpiresAt = operatorAddress(role), "local", now.Add(peerMessageTTL)
			store.Pending = append(store.Pending, msg)
		}
	}
	if len(store.Pending) > 0 {
		d.savePeerMessages()
		for _, role := range []string{roleQuill, roleKmem} {
			prefs.RemoveValue("operatorInbox." + role)
		}
	}
	return store
}

func (d *desk) cleanPeerMessages(now time.Time) *peerMessages {
	store := d.loadPeerMessages(now)
	pending, receipts := len(store.Pending), len(store.Receipts)
	var redraw []peerMessage
	store.Pending = slices.DeleteFunc(store.Pending, func(msg peerMessage) bool {
		if now.Before(msg.ExpiresAt) {
			return false
		}
		status := "expired"
		if store.Submitting[msg.ID] != "" {
			status = "unconfirmed"
			delete(store.Submitting, msg.ID)
		}
		store.Receipts = append(store.Receipts, msg.receipt(status, msg.ExpiresAt))
		redraw = append(redraw, msg)
		return true
	})
	store.Receipts = slices.DeleteFunc(store.Receipts, func(receipt operatorMessageReceipt) bool {
		return !now.Before(receipt.At.Add(peerReceiptTTL))
	})
	if pending != len(store.Pending) || receipts != len(store.Receipts) {
		d.savePeerMessages()
	}
	for _, msg := range redraw {
		if target, _ := d.peerRecipient(msg.To); target != nil {
			d.syncQueue(target)
		}
	}
	return store
}

func (msg peerMessage) receipt(status string, at time.Time) operatorMessageReceipt {
	return operatorMessageReceipt{ID: msg.ID, To: msg.To, From: msg.From, Status: status, At: at, ExpiresAt: msg.ExpiresAt}
}

func canonicalPeerName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "kmem" {
		return kmemAssistName
	}
	return name
}

func (d *desk) peerOperator(name string) (*assistant, bool) {
	switch name {
	case quillOpName:
		return d.operatorEndpoint(roleQuill), true
	case kmemAssistName:
		return d.operatorEndpoint(roleKmem), true
	}
	return nil, false
}

func (d *desk) peerSessionTitle(id string) string {
	if title := d.org.Titles[id]; title != "" {
		return title
	}
	if info := d.infos[id]; info.Named {
		return info.Title
	}
	return ""
}

// peerRecipient resolves explicit session titles and kmem session names. An
// ended record keeps its name addressable, but only a current owner can receive.
func (d *desk) peerRecipient(name string) (*deskSession, string) {
	if a, operator := d.peerOperator(name); operator {
		if a != nil && a.state == asLive {
			return a.s, ""
		}
		return nil, ""
	}
	if name == "" || len(name) > 256 {
		return nil, "invalid session name"
	}
	if id, explicit := strings.CutPrefix(name, "id:"); explicit {
		s := d.sessions[id]
		_, inHistory := d.infos[id]
		if !sessionIDRe.MatchString(id) || s == nil && !inHistory || d.hiddenID(id, "") ||
			d.org.Ended[id] || s != nil && (isHiddenSession(s) || s.remote != nil) {
			return nil, "unknown local session ID"
		}
		return s, ""
	}
	known := false
	matches := map[string]bool{}
	check := func(id, candidate string) {
		if candidate == "" || canonicalPeerName(candidate) != name || d.org.Hidden[id] {
			return
		}
		s := d.sessions[id]
		if s != nil && (isHiddenSession(s) || s.remote != nil) {
			return
		}
		known = true
		if d.org.Ended[id] {
			return
		}
		matches[id] = s != nil && s.running() && !s.stopping
	}
	for id, title := range d.org.Titles {
		check(id, title)
	}
	for id, info := range d.infos {
		if info.Named && d.org.Titles[id] == "" {
			check(id, info.Title)
		}
	}
	for id, attached := range d.org.Kmem {
		check(id, attached)
	}
	if !known {
		return nil, "unknown session name"
	}
	var all, live []string
	for id, running := range matches {
		all = append(all, id)
		if running {
			live = append(live, id)
		}
	}
	if len(live) > 0 {
		all = live
	}
	switch len(all) {
	case 0:
		return nil, ""
	case 1:
		return d.sessions[all[0]], ""
	default:
		slices.Sort(all)
		choices := make([]string, 0, len(all))
		for _, id := range all {
			cwd := d.infos[id].Cwd
			if s := d.sessions[id]; s != nil {
				cwd = s.cwd
			}
			choices = append(choices, fmt.Sprintf("id:%s (%q in %q)", id, d.peerSessionTitle(id), cwd))
		}
		return nil, "ambiguous session name; choose " + strings.Join(choices, ", ")
	}
}

func (d *desk) peerSender(chain []int) string {
	for _, s := range d.sessions {
		if s.proc == nil || !slices.Contains(chain, s.proc.PID()) {
			continue
		}
		if name := operatorAddress(operatorWindowRole(hiddenRole(s))); name != "" {
			return name
		}
		return cmp.Or(d.org.Kmem[s.id], d.peerSessionTitle(s.id), "local")
	}
	return "local"
}

func (d *desk) routePeerMessage(r operatorMessageRequest, now time.Time) operatorMessageReceipt {
	store := d.cleanPeerMessages(now)
	if r.Op == "status" || r.Op == "withdraw" || r.Op == "replace" {
		for i, msg := range store.Pending {
			if r.ID == msg.ID {
				return d.changePeerMessage(store, i, r, now)
			}
		}
		for _, receipt := range store.Receipts {
			if r.ID == receipt.ID && r.Op == "status" {
				return receipt
			}
		}
		return operatorMessageReceipt{Error: "message is no longer pending or its receipt has expired"}
	}
	if r.Op != "" {
		return operatorMessageReceipt{Error: "unknown message operation"}
	}
	to := canonicalPeerName(r.To)
	target, problem := d.peerRecipient(to)
	if problem != "" {
		return operatorMessageReceipt{Error: problem}
	}
	msg, problem := newPeerMessage(to, firstNonEmpty(r.from, "local"), r, now)
	if problem != "" {
		return operatorMessageReceipt{Error: problem}
	}
	if len(store.Pending) >= peerMessageLimit {
		return operatorMessageReceipt{Error: "the pending message pool is full"}
	}
	store.Pending = append(store.Pending, msg)
	d.savePeerMessages()
	return d.peerSendReceipt(target, store, msg)
}

func newPeerMessage(to, from string, r operatorMessageRequest, now time.Time) (peerMessage, string) {
	if strings.TrimSpace(r.Text) == "" || len(r.Text) > operatorMessageBytes || strings.ContainsRune(r.Text, 0) {
		return peerMessage{}, "message must contain 1–4096 bytes of text"
	}
	if r.TTL < 0 {
		return peerMessage{}, "expiry duration must be positive"
	}
	ttl := r.TTL
	if ttl == 0 {
		ttl = peerMessageTTL
	}
	return peerMessage{ID: session.NewUUID(), To: to, From: from, Text: r.Text, ExpiresAt: now.Add(ttl)}, ""
}

func (d *desk) changePeerMessage(store *peerMessages, i int, r operatorMessageRequest, now time.Time) operatorMessageReceipt {
	msg := store.Pending[i]
	if store.Submitting[msg.ID] != "" {
		if r.Op == "status" {
			return msg.receipt("unconfirmed", time.Time{})
		}
		return operatorMessageReceipt{Error: "previous submission could not be confirmed"}
	}
	switch r.Op {
	case "status":
		return msg.receipt("queued", time.Time{})
	case "withdraw":
		rep := msg.receipt("withdrawn", now)
		store.Pending = slices.Delete(store.Pending, i, i+1)
		store.Receipts = append(store.Receipts, rep)
		d.savePeerMessages()
		if target, _ := d.peerRecipient(msg.To); target != nil {
			d.syncQueue(target)
		}
		return rep
	case "replace":
		replacement, problem := newPeerMessage(msg.To, msg.From, r, now)
		if problem != "" {
			return operatorMessageReceipt{Error: problem}
		}
		rep := msg.receipt("replaced", now)
		rep.ReplacementID = replacement.ID
		store.Pending[i] = replacement
		store.Receipts = append(store.Receipts, rep)
		d.savePeerMessages()
		target, _ := d.peerRecipient(msg.To)
		return d.peerSendReceipt(target, store, replacement)
	}
	return operatorMessageReceipt{Error: "unknown message operation"}
}

func (d *desk) peerSendReceipt(target *deskSession, store *peerMessages, msg peerMessage) operatorMessageReceipt {
	if target != nil {
		d.flushPeerMessage(target)
		d.syncQueue(target)
		if !d.seen(target) && d.notifyOn() {
			d.desktopNotifyAs(target.id, "Peer message from "+msg.From, "Open Quill to read it.", false)
		}
	}
	for _, receipt := range store.Receipts {
		if receipt.ID == msg.ID {
			return receipt
		}
	}
	return msg.receipt("queued", time.Time{})
}

// A plan can close or a local pause can end without a provider event.
func (d *desk) pollPeerMessages(now time.Time) {
	store := d.cleanPeerMessages(now)
	for _, s := range d.sessions {
		if len(store.Pending) == 0 {
			return
		}
		d.flushPeerMessage(s)
	}
}

func (d *desk) flushPeerMessage(s *deskSession) {
	if s == nil || s.remote != nil || !s.running() || s.loading || s.stopping || s.takeover || s.terminal != nil ||
		s.codexUpgrading || s.pendingEnd != 0 || s.kmemSave != nil || s.endWait != nil || s.queueBlocked() || s.qedit != nil ||
		s.conv.Busy || s.proc.Busy() || s.inTurn || len(s.queue) != 0 || s.planWait || hasPendingPlan(s) || s.conv.AsksApproval() ||
		len(s.conv.PendingPermissions()) != 0 || (d.usagePaused && !s.codex) {
		return
	}
	names := []string{operatorAddress(operatorWindowRole(hiddenRole(s)))}
	if names[0] == "" {
		names = []string{d.org.Kmem[s.id], d.peerSessionTitle(s.id), "id:" + s.id}
	}
	now := time.Now()
	store := d.cleanPeerMessages(now)
	for i, msg := range store.Pending {
		if !slices.ContainsFunc(names, func(name string) bool { return canonicalPeerName(name) == msg.To }) {
			continue
		}
		if target, _ := d.peerRecipient(msg.To); target != s {
			continue
		}
		text := msg.envelope()
		submitted := slices.ContainsFunc(s.conv.Items, func(it *chat.Item) bool {
			return it.Kind == chat.User && (it.Text == text || strings.HasPrefix(it.Text, text+"\n"))
		})
		if owner := store.Submitting[msg.ID]; owner != "" {
			if owner != s.id || !submitted {
				continue
			}
		} else if !submitted {
			if store.Submitting == nil {
				store.Submitting = make(map[string]string)
			}
			store.Submitting[msg.ID] = s.id
			d.savePeerMessages()
			if !d.deliverTurnWithOrigin(s, text, false, true) {
				delete(store.Submitting, msg.ID)
				d.savePeerMessages()
				return
			}
		}
		delete(store.Submitting, msg.ID)
		store.Pending = slices.Delete(store.Pending, i, i+1)
		store.Receipts = append(store.Receipts, msg.receipt("submitted", now))
		d.savePeerMessages()
		d.syncQueue(s)
		return
	}
}

const peerEnvelopeStart = "<cross-session-message source=\"quill-operator-route\">\n"
const peerEnvelopeEnd = "\n</cross-session-message>"

func (msg peerMessage) envelope() string {
	data, _ := json.Marshal(msg)
	return peerEnvelopeStart + string(data) + peerEnvelopeEnd
}

func readPeerEnvelope(text string) (peerMessage, bool) {
	var msg peerMessage
	body, found := strings.CutPrefix(text, peerEnvelopeStart)
	if !found {
		return msg, false
	}
	body, _, found = strings.Cut(body, peerEnvelopeEnd)
	ok := found && json.Unmarshal([]byte(body), &msg) == nil && msg.ID != "" && msg.From != ""
	return msg, ok
}
