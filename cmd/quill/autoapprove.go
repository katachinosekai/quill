package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// Auto-approve: an approval that lasts until the user turns it off (user
// 2026-11-23). Besides the per-request approval (planscope.go), the plan
// card's approval question has a checkbox "Keep approving in this tab until
// I turn it off". Sending a go-ahead with it ticked approves the plan as
// usual and switches the session into auto-approve:
//
//   - The gate lets every tool call of the session (its subagents' too)
//     through without a plan, except a command that runs as root
//     (privilegedRequest): those still need an approved plan, as always.
//     A batch the gate refused a call of stays refused (gatebatch.go).
//   - New user messages don't end it; Stop, "Don't run" and a denied prompt
//     don't either (they end the current plan's approval only).
//   - It ends when the user turns it off (the "Auto-approve on" chip by the
//     message box, or "Turn off auto-approve" in the session menu, or the
//     phone API), or when the session's Claude process exits or restarts.
//     It is not kept across Quill Desktop restarts or reloads.
//
// Claude learns it from the approval message (autoApproveOnText, which the
// session hint explains) and, once it is off again, from a note appended to
// the user's next message (autoApproveOffNote).

const (
	autoApproveOnText  = " Auto-approve on: no plans needed in this tab until the user turns it off; commands that run as root still need one."
	autoApproveOffNote = "\n\n(Quill Desktop: auto-approve is off in this tab now; write a plan and wait for approval again before running anything that needs one.)"
	autoApproveCheck   = "Keep approving in this tab until I turn it off"
	autoApproveLabel   = "Auto-approve on"
	autoApproveMenu    = "Turn off auto-approve"
	// autoApproveRootAsk: the refusal of a root command while auto-approve is on.
	autoApproveRootAsk = "Auto-approve is on in this tab, but commands that run as root still need an approved plan. "
)

// autoCovers: auto-approve lets r through without a plan.
func autoCovers(s *deskSession, r *protocol.ControlRequest) bool {
	return s != nil && s.autoApprove && !privilegedRequest(r)
}

// setAutoApprove turns auto-approve on or off for s and updates the chrome.
func (d *desk) setAutoApprove(s *deskSession, on bool) {
	if s == nil || s.autoApprove == on {
		return
	}
	endAutoApprove(s)
	if on {
		s.autoApprove, s.autoNote = true, ""
	}
	d.keepGate(s) // a crash restart's copy knows to tell Claude it is off
	d.syncAutoApprove()
}

// endAutoApprove: auto-approve is off; Claude is told with the next message.
func endAutoApprove(s *deskSession) {
	if s.autoApprove {
		s.autoApprove, s.autoNote = false, autoApproveOffNote
	}
}

// keepApproving: the approval msg answerPlanMsg sends turns auto-approve on
// when the card's checkbox (or the phone's keepApproving) asked for it.
func (d *desk) keepApproving(s *deskSession, msg string) string {
	if !d.keepNext || !d.planFirst || s == nil || s.remote != nil {
		return msg
	}
	d.setAutoApprove(s, true)
	return msg + autoApproveOnText
}

// withAutoNote: the user's message with the pending auto-approve-off note.
func withAutoNote(s *deskSession, text string) string {
	if s.autoNote == "" || isRetry(text) {
		return text
	}
	text += s.autoNote
	s.autoNote = ""
	return text
}

// syncAutoApprove refreshes the chips and session controls.
func (d *desk) syncAutoApprove() {
	if d.input == nil {
		return
	}
	d.updateChrome()
	for _, p := range d.panes {
		d.syncPaneControls(p)
	}
	d.refreshSidebar()
}

// autoApproveShown: the chip shows for s.
func (d *desk) autoApproveShown(s *deskSession) bool {
	return s != nil && s.autoApprove && d.planFirst
}

// syncAutoChip shows or hides c for s; true when its visibility changed.
func (d *desk) syncAutoChip(c *autoChip, s *deskSession) bool {
	if c == nil {
		return false
	}
	was := c.Visible()
	if d.autoApproveShown(s) {
		c.Show()
	} else {
		c.Hide()
	}
	return was != c.Visible()
}

// autoApproveItem: the session menu's off switch while auto-approve is on.
func (d *desk) autoApproveItem(s *deskSession) *fyne.MenuItem {
	if s == nil || !s.autoApprove {
		return nil
	}
	return fyne.NewMenuItem(autoApproveMenu, func() { d.setAutoApprove(s, false) })
}

// keepCheck: the plan card's "Keep approving in this tab" checkbox, nil when
// it doesn't apply (plan-first off, a remote session, already on, an
// inactive card).
func (v *chatView) keepCheck(owner *deskSession, plan *chat.Item, active bool) fyne.CanvasObject {
	if !active || owner == nil || owner.remote != nil || owner.autoApprove || !v.d.planFirst {
		return nil
	}
	c := widget.NewCheck(autoApproveCheck, func(on bool) {
		if v.planKeep == nil {
			v.planKeep = map[*chat.Item]bool{}
		}
		v.planKeep[plan] = on
	})
	c.Checked = v.planKeep[plan]
	return v.buttonRow(c) // follows the card's alignment
}

// sendKeeping runs send with the card's checkbox applied to its approval.
func (v *chatView) sendKeeping(plan *chat.Item, send func()) {
	v.d.keepNext = v.planKeep[plan]
	defer func() { v.d.keepNext = false }()
	send()
}

// autoChip: "Auto-approve on ×" by the message box; a tap turns it off.
type autoChip struct {
	widget.BaseWidget
	box   fyne.CanvasObject
	onTap func()
}

func newAutoChip(off func()) *autoChip {
	c := &autoChip{onTap: off}
	label := widget.NewLabelWithStyle(autoApproveLabel, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	r := canvas.NewRectangle(fade(colIdle, 0.22))
	r.CornerRadius = theme.Size(theme.SizeNameInputRadius)
	r.StrokeColor = colIdle
	r.StrokeWidth = 1
	on := widget.NewIcon(theme.ConfirmIcon())
	x := widget.NewIcon(theme.CancelIcon())
	c.box = container.NewStack(r, container.NewBorder(nil, nil, container.NewCenter(sized(16, 16, on)), container.NewCenter(sized(14, 14, x)), label))
	c.ExtendBaseWidget(c)
	c.Hide()
	return c
}

func (c *autoChip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.box)
}

func (c *autoChip) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}
