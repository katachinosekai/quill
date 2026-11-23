package main

import (
	"fmt"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// The status line between the chat and the message box always shows the
// session's state (user 2026-11-23): Starting…, Ready, Thinking, the running
// tool, Writing…, Waiting for you, Stopped, Not running. Quill Desktop's main
// chat, every split pane and the kmem window chat share this one component
// (liveStatus + chatView.setStatus).
//
// Suffixes: "· N subagents running" / "· N background jobs" from the
// conversation's background tasks, and "· last activity 12 s ago" while a turn
// runs. The age is refreshed once a second by a ticker that runs only while a
// shown session is busy (statusKick) and stops itself when none is.
//
// "Still working" pulse: a turn that has run longer than statusPulseAfter
// makes the text fade gently in and out (a background-colour veil over the
// label, like the control fade-in) so it is clear Quill Desktop has not
// frozen. The pulse follows the text animation settings: it is off when
// "Animate new text" is off or the speed is Instant, and slower at Slow.
const (
	statusPulseAfter = 30 * time.Second
	statusAgeAfter   = 3 * time.Second // shorter ages would only flicker
)

var (
	statusNow   = time.Now
	statusEvery = time.Second
)

// statusInfo is the state function's result.
type statusInfo struct {
	text   string
	active bool // normal colour (something is going on or needs the user); else muted
	busy   bool // a turn is running: age suffix and ticker
	pulse  bool // the turn has run longer than statusPulseAfter
}

// trackTurn records when the running turn began; call from the UI thread
// whenever events arrive or the status is drawn.
func (s *deskSession) trackTurn(now time.Time) {
	if s == nil {
		return
	}
	if !s.conv.Busy {
		s.turnStart = time.Time{}
		return
	}
	if s.turnStart.IsZero() {
		s.turnStart = now
	}
}

// noteActivity records an event (for "last activity N s ago").
func (s *deskSession) noteActivity(now time.Time) {
	s.lastAct = now
	s.trackTurn(now)
}

func liveStatus(s *deskSession, now time.Time) statusInfo {
	if s == nil {
		return statusInfo{}
	}
	c := s.conv
	var st statusInfo
	running := s.running()
	if s.remote != nil {
		switch remoteStatus(s.remote) {
		case stNone, stTerminal, stError:
		default:
			running = true
		}
	}
	if !running {
		switch {
		case s.terminal != nil && s.remote == nil:
			st.text = "Open in a terminal"
		case s.failed != "" || s.remote != nil && remoteStatus(s.remote) == stError:
			st.text = "Stopped"
		case s.restarting || s.compacting || s.kmemSave != nil || s.endWait != nil:
			st.text, st.active = "Starting…", true
		case s.proc != nil:
			st.text = "Stopped" // the process ended
		default:
			st.text = "Not running"
		}
		return st
	}
	busy := c.Busy
	if s.remote != nil {
		busy = busy || remoteStatus(s.remote) == stRunning
	}
	waiting := true
	switch {
	case hasPendingPlan(s):
		st.text, st.active = "Waiting for your approval", true
	case len(c.PendingPermissions()) > 0:
		st.text, st.active = "Waiting for your answer above", true
	case s.planWait || !busy && c.AsksApproval():
		st.text, st.active = "Waiting for your approval", true
	case !busy && c.AwaitsReply():
		st.text, st.active = "Waiting for your answer above", true
	case busy:
		st.text, st.active, waiting = busyText(c), true, false
	case s.restarting:
		st.text, st.active = "Starting…", true
	case interrupted(c):
		st.text = "Stopped"
	default:
		st.text = "Ready"
	}
	var agents, jobs int
	for _, t := range c.BackgroundTasks() {
		if t.Tool == "Agent" || t.Tool == "Task" {
			agents++
		} else {
			jobs++
		}
	}
	if agents > 0 {
		st.text += " · " + plural(agents, "subagent") + " running"
	}
	if jobs > 0 {
		st.text += " · " + plural(jobs, "background job")
	}
	if busy && !waiting {
		st.busy = true
		last := s.lastAct
		if last.IsZero() {
			last = s.turnStart
		}
		if !last.IsZero() && now.Sub(last) >= statusAgeAfter {
			st.text += " · last activity " + agoText(now.Sub(last)) + " ago"
		}
		st.pulse = !s.turnStart.IsZero() && now.Sub(s.turnStart) > statusPulseAfter
	}
	return st
}

func busyText(c *chat.Conversation) string {
	if c.Thinking {
		return "✻ Thinking…"
	}
	if n := len(c.Items); n > 0 {
		last := c.Items[n-1]
		if last.Kind == chat.Assistant && last.Streaming {
			return "Writing…"
		}
		if last.Kind == chat.Tool && !last.HasResult {
			return "● " + oneLine(toolHeader(last), 90)
		}
	}
	return "✻ Working…"
}

// agoText: "12 s", "3 min".
func agoText(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return fmt.Sprintf("%d min", int(d/time.Minute))
}

// liveText is the status text of a session now.
func liveText(s *deskSession) string {
	s.trackTurn(statusNow())
	return liveStatus(s, statusNow()).text
}

// setStatus draws the session's status in the line under the chat.
func (v *chatView) setStatus(s *deskSession) {
	if v.live == nil || v.steps {
		return
	}
	now := statusNow()
	s.trackTurn(now)
	st := liveStatus(s, now)
	imp := widget.LowImportance
	if st.active {
		imp = widget.MediumImportance
	}
	if v.live.Importance != imp {
		v.live.Importance = imp
		v.live.Refresh()
	}
	v.setLive(st.text)
	v.setPulse(st.pulse)
	if st.busy && v.d != nil {
		v.d.statusKick()
	}
}

// setPulse starts or stops the still-working pulse.
func (v *chatView) setPulse(on bool) {
	if on && v.d != nil {
		on = v.d.animateText() && v.d.animateSpeed() != "instant"
	}
	if on == v.pulsing {
		return
	}
	v.pulsing = on
	if v.pulse != nil {
		v.pulse.Stop()
		v.pulse = nil
	}
	if v.veil == nil {
		return
	}
	if !on {
		v.veil.FillColor = veil(0)
		v.veil.Refresh()
		return
	}
	if fyne.CurrentApp() == nil {
		return
	}
	half := 1600 * time.Millisecond
	switch v.d.animateSpeed() {
	case "slow":
		half = 2400 * time.Millisecond
	case "fast":
		half = 1000 * time.Millisecond
	}
	a := canvas.NewColorRGBAAnimation(veil(0), veil(0.65), half, func(c color.Color) {
		v.veil.FillColor = c
		v.veil.Refresh()
	})
	a.Curve = fyne.AnimationEaseInOut
	a.AutoReverse = true
	a.RepeatCount = fyne.AnimationRepeatForever
	v.pulse = a
	a.Start()
}

// statusKick starts the one-second ticker if it is not running. It ends
// itself as soon as no shown session is busy, so an idle Quill Desktop uses
// no CPU for it.
func (d *desk) statusKick() {
	if d.statusRun {
		return
	}
	d.statusRun = true
	go func() {
		t := time.NewTicker(statusEvery)
		defer t.Stop()
		for range t.C {
			stop, ran := false, false
			d.doWait(func() {
				ran = true
				if !d.refreshStatuses() {
					d.statusRun, stop = false, true
				}
			})
			if stop || !ran {
				return
			}
		}
	}()
}

// refreshStatuses redraws every shown status line; false when none is busy.
func (d *desk) refreshStatuses() bool {
	busy := false
	for _, v := range d.allViews() {
		s := v.s
		if s == nil {
			continue
		}
		if v == d.chat && d.stt != nil && d.stt.state != sttIdle {
			continue // the recording state shows instead
		}
		now := statusNow()
		s.trackTurn(now)
		if liveStatus(s, now).busy {
			busy = true
		}
		v.setStatus(s)
	}
	return busy
}

func interrupted(c *chat.Conversation) bool {
	n := len(c.Items)
	return n > 0 && c.Items[n-1].Kind == chat.Notice && c.Items[n-1].Text == "Interrupted"
}
