package main

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Approval waiting on an assistant's button (t98af12, user 2026-11-23): the
// Quill operator and the kmem assistant run hidden, so a permission prompt,
// AskUserQuestion, plan card or approval question of theirs must reach the
// user while their box is closed or shows the other assistant: the button
// turns amber (button, dot and badge), stronger than the working dot; the
// desktop notification and the phone event come from alert (notify.go).
// The state ends once the approval is answered or replaced.

type assistRequest struct {
	kind, sig string
	alert     bool
}

// Use the active plan and native permission state. A superseded plan can
// remain pending in history, along with the assistant text that introduced it.
func actionableAssistRequest(c *chat.Conversation, inTurn bool) assistRequest {
	if it := livePlan(c); it != nil {
		return assistRequest{"plan", fmt.Sprint(it.Seq, it.RequestID), true}
	}
	for _, it := range c.PendingPermissions() {
		if it.Kind == chat.Permission {
			return assistRequest{"question", fmt.Sprint(it.Seq, it.RequestID), true}
		}
	}
	if c.Busy || inTurn {
		return assistRequest{}
	}
	approval := c.AsksApproval()
	if !approval && !c.AwaitsReply() {
		return assistRequest{}
	}
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == chat.Plan {
			break
		}
		if it.Kind == chat.Assistant && !it.Streaming && strings.TrimSpace(it.Text) != "" {
			return assistRequest{"question", fmt.Sprint(it.Seq, it.UUID), approval}
		}
	}
	return assistRequest{}
}

func assistCompanion(role string) string {
	switch role {
	case boxQuill:
		return roleCodexQuill
	case boxKmem:
		return roleCodexKmem
	}
	return ""
}

// Both provider variants share their operator button. A visible request
// can be answered in its window, so only a hidden request alerts.
func (d *desk) assistAlerting(role string) bool {
	for _, id := range []string{role, assistCompanion(role)} {
		a := d.assistByRole(id)
		if !d.assistantRequest(a).alert {
			continue
		}
		boxShowsRole := d.assistBoxShowing(id)
		if id == boxKmem && (!d.kmemWin.assistantSelected() || d.kmemWin.codexShown) {
			boxShowsRole = false
		}
		if d.hidden || !(boxShowsRole || d.hostShown(a.s)) {
			return true
		}
	}
	return false
}

func (d *desk) syncAssistAlert(bt *assistButton, on bool) {
	bubbleColor := color.Color(colAccent)
	if on {
		bubbleColor = colWaiting
	}
	for _, b := range []*bubble{bt.bubble, bt.sideBub} {
		if b != nil && b.dot.FillColor != bubbleColor {
			b.dot.FillColor = bubbleColor
			b.dot.Refresh()
		}
	}
	for _, btn := range []*widget.Button{bt.btn, bt.side} {
		if btn == nil {
			continue
		}
		imp := widget.MediumImportance
		if btn == bt.side {
			imp = widget.LowImportance
		}
		if on {
			imp = widget.WarningImportance
		} else if operatorButtonRole(d.assistBoxRole()) == bt.role {
			imp = widget.HighImportance
		}
		if btn.Importance != imp {
			btn.Importance = imp
			btn.Refresh()
		}
	}
}

// assistEvent: an event of s came; when s is an assistant's session, its
// badge and button follow (a question or plan came or was answered).
func (d *desk) assistEvent(s *deskSession) {
	a := d.assistOf(s)
	if a == nil {
		return
	}
	d.updateAssistBadge(a)
	d.syncAssistButtons()
	d.syncMainOperatorControls()
	if p := d.focused; p != nil && d.targetSession(d.targeted(p.input)) == s {
		d.syncPaneControls(p)
	}
}
