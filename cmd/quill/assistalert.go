package main

import (
	"image/color"

	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// Approval waiting on an assistant's button (t98af12, user 2026-11-23): the
// Quill operator and the kmem assistant run hidden, so a permission prompt,
// AskUserQuestion, plan card or approval question of theirs must reach the
// user while their box is closed or shows the other assistant: the button
// turns amber (button, dot and badge), stronger than the working dot; the
// desktop notification and the phone event come from alert (notify.go).
// The state ends once the approval is answered or replaced.

// assistApprovalWait: a waits for an approval: a pending permission (an
// AskUserQuestion too) or plan card, or a finished turn that ends asking
// for approval. UI thread.
func assistApprovalWait(a *assistant) bool {
	if a == nil || a.s == nil || a.s.conv == nil || !a.s.running() {
		return false
	}
	c := a.s.conv
	return len(c.PendingPermissions()) > 0 || !c.Busy && !a.s.inTurn && c.AsksApproval()
}

// assistAlerting: role's assistant waits for an approval the user can't see
// (its box is closed or shows the other assistant).
func (d *desk) assistAlerting(role string) bool {
	a := d.assistByRole(role)
	return a != nil && d.assistRunning(a) && !d.assistBoxShowing(role) && assistApprovalWait(a)
}

// syncAssistAlert puts role's button in the approval-waiting state or takes
// it out (syncAssistButtons, after the dots).
func (d *desk) syncAssistAlert(bt *assistButton) {
	on := d.assistAlerting(bt.role)
	for _, dot := range []*canvas.Circle{bt.dot, bt.sideDot} {
		if dot != nil && on && (dot.FillColor != colWaiting || !dot.Visible()) {
			dot.FillColor = colWaiting
			dot.Show()
			dot.Refresh()
		}
	}
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
	if bt.btn != nil {
		imp := widget.MediumImportance
		if on {
			imp = widget.WarningImportance
		}
		if bt.btn.Importance != imp {
			bt.btn.Importance = imp
			bt.btn.Refresh()
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
}
