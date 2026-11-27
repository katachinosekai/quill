package main

import (
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/katachinosekai/quill/internal/chat"
)

// Pending rows belong to the view and cannot change a turn or an approval.
type pendingPeerView struct {
	msg    peerMessage
	status string
	row    fyne.CanvasObject
}

func (v *chatView) peerLabel(msg peerMessage, status string) fyne.CanvasObject {
	label := widget.NewLabel("Peer message from " + msg.From + " · " + status + "\n" + msg.Text)
	label.Selectable = true
	label.Wrapping = fyne.TextWrapWord
	label.Alignment = v.align()
	return msgText(label)
}

func (d *desk) syncPeerTimeline(s *deskSession) {
	for _, v := range d.allViews() {
		if v != nil && v.s == s && v.syncPeerTimeline() {
			v.keepUp()
			v.stickSoon()
		}
	}
}

// Interleave peers without recreating ordinary message or approval widgets.
func (v *chatView) syncPeerTimeline() bool {
	s := v.s
	if s == nil || s.conv == nil || v.steps {
		return false
	}
	type entry struct {
		at  time.Time
		row fyne.CanvasObject
	}
	var peers, history []entry
	submitted := make(map[string]bool)
	for _, it := range s.conv.Items[min(v.shownFrom, len(s.conv.Items)):] {
		iv := v.views[it.Seq]
		if iv == nil {
			continue
		}
		if it.Kind == chat.User {
			if msg, ok := readPeerEnvelope(it.Text); ok {
				if submitted[msg.ID] {
					continue
				}
				submitted[msg.ID] = true
				if !msg.ReceivedAt.IsZero() {
					peers = append(peers, entry{msg.ReceivedAt, iv.holder})
					continue
				}
			}
		}
		history = append(history, entry{it.Time, iv.holder})
	}
	store := v.d.loadPeerMessages(time.Now())
	pending := make(map[string]pendingPeerView)
	for _, msg := range store.Pending {
		if submitted[msg.ID] {
			continue
		}
		if target, _ := v.d.peerRecipient(msg.To); target != s {
			continue
		}
		status := "received · waiting for " + s.engineName()
		if store.Submitting[msg.ID] != "" {
			status = "submission unconfirmed"
		}
		pv := v.pendingPeers[msg.ID]
		if pv.row == nil || pv.msg != msg || pv.status != status {
			pv = pendingPeerView{msg, status, spaced(chat.User, v.withTime(v.peerLabel(msg, status), msg.ReceivedAt))}
		}
		pending[msg.ID] = pv
		peers = append(peers, entry{msg.ReceivedAt, pv.row})
	}
	v.pendingPeers = pending
	// Older records only have pool order until they are submitted.
	slices.SortStableFunc(peers, func(a, b entry) int {
		if a.at.IsZero() != b.at.IsZero() {
			if a.at.IsZero() {
				return 1
			}
			return -1
		}
		return a.at.Compare(b.at)
	})
	var rows []fyne.CanvasObject
	if v.moreObj != nil {
		rows = append(rows, v.moreObj)
	}
	if v.placeholder && len(v.box.Objects) > len(rows) {
		placeholder := v.box.Objects[len(rows)]
		setShown(placeholder, len(pending) == 0 || s.pickPending)
		rows = append(rows, placeholder)
	}
	for _, item := range history {
		for len(peers) > 0 && !peers[0].at.IsZero() && peers[0].at.Before(item.at) {
			rows = append(rows, peers[0].row)
			peers = peers[1:]
		}
		rows = append(rows, item.row)
	}
	for _, peer := range peers {
		rows = append(rows, peer.row)
	}
	if slices.Equal(v.box.Objects, rows) {
		return false
	}
	v.box.Objects = rows
	v.reflow()
	return true
}
