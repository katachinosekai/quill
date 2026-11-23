package main

import (
	"encoding/json"
	"regexp"

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
//   - The gate lets the tool calls of the turns it covers (below) through
//     without a plan, except a command that may run as root
//     (autoRootRequest, a deliberately crude check in gatebatch.go): those
//     still need an approved plan, as always. A batch the gate refused a
//     call of stays refused (gatebatch.go).
//   - New user messages don't end it; Stop, "Don't run" and a denied prompt
//     don't either (they end the current plan's approval only).
//   - It ends when the user turns it off (the "Auto-approve on" chip by the
//     message box, or "Turn off auto-approve" in the session menu, or the
//     phone API), which also ends the current plan's approval and grants;
//     when the user ends the session (End session, Switch topic), at once;
//     when the message turning it on can't be sent; or when the session's
//     Claude process exits or restarts. It is not kept across Quill Desktop
//     restarts or reloads.
//
// Claude learns it from the approval message (autoApproveOnText, which the
// session hint explains) and, once it is off again, from a note sent with
// the user's next message (autoApproveOffNote; not shown in the bubble, kept
// until a send succeeds) and from every plan-first refusal while that note
// is pending (planAsk), so a turn a peer, trigger or notification started
// learns it too.

const (
	autoApproveOnText  = " Auto-approve on: no plans needed in this tab until the user turns it off; commands that run as root still need one."
	autoApproveOffNote = chat.AutoApproveOffNote
	autoApproveCheck   = "Keep approving in this tab until I turn it off"
	autoApproveLabel   = "Auto-approve on"
	autoApproveMenu    = "Turn off auto-approve"
	// autoApproveRootAsk: a plan-first refusal while auto-approve is on.
	autoApproveRootAsk = "Auto-approve is on in this tab, but this call still needs an approved plan: it may run something as root, or a peer session's message or a scheduled trigger started this turn. "
)

// Which turns it covers (f3a1828: peer messages and scheduled jobs never
// approve anything; review 2026-11-23, pending the user's own choice): only
// turns the user started (a typed or phone message, a plan card) and turns
// started by the task notification of work the session started under
// auto-approve in such a turn. A turn a peer (cross-session) or channel
// message or a scheduled trigger started, or that such a message arrived in,
// is plan-gated as without auto-approve (s.autoPeer). A subagent's calls are
// covered when a covered turn started it (s.autoOwn, by tool_use_id and
// agent id); others (a peer turn's, one started before auto-approve, an
// unknown nested one) are gated.

// autoCovers: auto-approve lets r through without a plan.
func autoCovers(s *deskSession, r *protocol.ControlRequest) bool {
	if s == nil || !s.autoApprove || r == nil {
		return false
	}
	if r.AgentID != "" {
		if !s.autoOwn[r.AgentID] && !(r.ToolUseID != "" && s.autoOwn[r.ToolUseID]) {
			return false
		}
	} else if s.autoPeer {
		return false
	}
	return !autoRootRequest(r, s.cwd)
}

// autoRootRequest: r runs a command (Bash, or any tool with a "command"
// input) that needs a plan under auto-approve (autoNeedsPlan, gatebatch.go).
func autoRootRequest(r *protocol.ControlRequest, cwd string) bool {
	if privilegedRequest(r) {
		return true
	}
	var in map[string]any
	if json.Unmarshal(r.Input, &in) != nil {
		return true // can't tell: the safe side
	}
	cmd, ok := in["command"].(string)
	return ok && autoNeedsPlan(cmd, cwd)
}

// maxAutoOwn caps the ids remembered as auto-approve's own work.
const maxAutoOwn = 16384

func addAutoOwn(s *deskSession, id string) {
	if id == "" || len(s.autoOwn) >= maxAutoOwn {
		return
	}
	if s.autoOwn == nil {
		s.autoOwn = map[string]bool{}
	}
	s.autoOwn[id] = true
}

// trackAutoWork records the work a covered turn starts (its tool_use ids,
// the task and agent ids they get, a subagent's own calls) so that its
// notifications and its subagents' calls count as the session's own.
func trackAutoWork(s *deskSession, ev *protocol.Event) {
	if !s.autoApprove {
		return
	}
	if ev.Type == "system" && ev.Subtype == "task_started" {
		if s.autoOwn[ev.ToolUseID] {
			addAutoOwn(s, ev.TaskID)
		}
		return
	}
	if ev.Message == nil {
		return
	}
	switch ev.Type {
	case "assistant":
		sub := ev.IsSubagent()
		if sub {
			if !s.autoOwn[*ev.ParentToolUseID] {
				return
			}
			addAutoOwn(s, ev.AgentID)
		}
		for _, b := range ev.Message.Blocks() {
			if b.Type != "tool_use" {
				continue
			}
			var in struct {
				Background bool   `json:"run_in_background"`
				To         string `json:"to"`
			}
			json.Unmarshal(b.Input, &in)
			switch {
			case !sub && s.autoPeer:
				// A peer's turn writing to a subagent could hand it new
				// work: that subagent (all of them, when it can't tell
				// which) is no longer auto-approve's.
				if b.Name == "SendMessage" {
					if s.autoOwn[in.To] {
						delete(s.autoOwn, in.To)
					} else {
						s.autoOwn = nil
					}
				}
			case sub || b.Name == "Agent" || b.Name == "Task" || b.Name == "Bash" && in.Background:
				// work whose notification or calls come later; a subagent's
				// own calls (its can_use_tool requests name them)
				addAutoOwn(s, b.ID)
			}
		}
	case "user":
		var results []protocol.Block
		for _, b := range ev.Message.Blocks() {
			if b.Type == "tool_result" && s.autoOwn[b.ToolUseID] {
				results = append(results, b)
			}
		}
		for _, b := range results {
			for _, re := range []*regexp.Regexp{bgShellIDRe, agentIDRe} {
				if m := re.FindSubmatch(b.Content); m != nil {
					addAutoOwn(s, string(m[1]))
				}
			}
			if len(results) == 1 {
				var r struct {
					AgentID string `json:"agentId"`
				}
				json.Unmarshal(ev.ToolUseResult, &r)
				addAutoOwn(s, r.AgentID)
			}
		}
	}
}

// autoOwnNotice: a task notification names auto-approve's own work.
func autoOwnNotice(s *deskSession, text string) bool {
	for _, re := range []*regexp.Regexp{toolUseIDRe, taskIDRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if s.autoOwn[m[1]] {
				return true
			}
		}
	}
	return false
}

// setAutoApprove turns auto-approve on or off for s and updates the chrome.
// Off also ends the current plan approval and its grants (revokePlan), so
// the calls after the click are gated at once, the approval turn's too.
func (d *desk) setAutoApprove(s *deskSession, on bool) {
	if s == nil || s.autoApprove == on {
		return
	}
	endAutoApprove(s)
	if on {
		s.autoApprove, s.autoNote = true, ""
	} else {
		revokePlan(s)
	}
	d.keepGate(s) // a crash restart's copy knows to tell Claude it is off
	d.syncAutoApprove()
}

// endAutoApprove: auto-approve is off; Claude is told with the next message.
func endAutoApprove(s *deskSession) {
	if s.autoApprove {
		s.autoApprove, s.autoNote = false, autoApproveOffNote
	}
	s.autoOwn, s.autoPeer, s.autoStarting = nil, false, false
}

// autoOffForEnd: ending the session (End session, Switch topic, a kmem
// save-and-end) turns auto-approve off at once, before any save turn runs.
func (d *desk) autoOffForEnd(id string) {
	if s := d.sessions[id]; s != nil && s.autoApprove {
		d.setAutoApprove(s, false)
	}
}

// keepApproving: the approval msg answerPlanMsg sends turns auto-approve on
// when the card's checkbox (or the phone's keepApproving) asked for it.
// sendTo turns it off again if the message can't be sent (autoStartFailed).
func (d *desk) keepApproving(s *deskSession, msg string) string {
	if !d.keepNext || !d.planFirst || s == nil || s.remote != nil {
		return msg
	}
	starting, prev := !s.autoApprove, s.autoNote
	d.setAutoApprove(s, true)
	if starting {
		s.autoStarting, s.autoPrevNote = true, prev
	}
	return msg + autoApproveOnText
}

// autoStartFailed: the message that turned auto-approve on never reached
// Claude (no process, or the send failed): it is off again, as it was.
func (d *desk) autoStartFailed(s *deskSession) {
	if !s.autoStarting {
		return
	}
	s.autoStarting = false
	s.autoApprove, s.autoNote, s.autoOwn, s.autoPeer = false, s.autoPrevNote, nil, false
	d.keepGate(s)
	d.syncAutoApprove()
}

// autoNoteFor: the pending auto-approve-off note to send with the user's
// message (not shown in its bubble); sendTo clears it once sent.
func autoNoteFor(s *deskSession, text string) string {
	if isRetry(text) {
		return ""
	}
	return s.autoNote
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
