package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The kmem assistant, kmem-lock-audit: kmem's developer and operator in one
// session (docs/design/kmem-window.md §5, §9, §14; user 2026-11-22, kmem fact
// fc6b73c). It is the only always-running hidden background session: it
// starts with Quill Desktop (kmemAssistBoot: kmem memory on, model not Off,
// not live in a tab) as a hidden session in kmemWindowDir() that resumes from
// its kmem handoff, and keeps running while the kmem window is closed (close
// only hides the window; claims and background jobs go on). The window's
// "Stop assistant" and Quit run the save-and-stop flow: release or transfer
// claims, save the handoff, wait up to kmemWinSaveWait, stop. A hot reload
// hands the process over (reload.go) and kmemAssistBoot adopts it. The window
// itself (left panels, sizes, keys) is kmemwindow.go's; it calls
// kmemAssistOpen/kmemAssistClose through kmemWindowOpenHook/kmemWindowCloseHook.

// kmemAssistName is the operator's kmem name (kmemWinOperator in kmemwindow.go).
const kmemAssistName = kmemWinOperator

// prefKmemWinStarted: when the last assistant session started (RFC 3339), for
// the "ended without saving" note.
const prefKmemWinStarted = "kmemWinStarted"

var (
	kmemWinSaveWait = 60 * time.Second // the close flow's cap (tests: 200 ms)
	kmemWinSavePoll = 2 * time.Second  // how often the close flow reads `kmem handoff list`
	kmemWinTick     = time.Second      // header refresh while the window is open
)

// kmemPeerName: the session name other Claude sessions see in ListAgents and
// address with SendMessage (claude --name); without it they saw the folder
// name plus a number ("kmem-window-51").
const kmemPeerName = kmemAssistName

// The kmem window's chat side (kmemwindow.go), wired at merge:
// kmemWinSetChat = (*kmemWindow).setChat and so on. No-ops until then.
var (
	kmemWinSetChat           = func(w *kmemWindow, obj fyne.CanvasObject) {}
	kmemWinSetChatReason     = func(w *kmemWindow, reason string) {}
	kmemWinSetChatReasonWith = func(w *kmemWindow, reason, button string, tap func()) {}
	kmemWinSetPrefill        = func(w *kmemWindow, f func(text string)) {}
)

// kmemAssistSignedIn: Claude Code is signed in, or its state is not known yet
// (the account check runs in the background).
var kmemAssistSignedIn = func(d *desk) bool {
	return d.acct.LoggedIn || d.acct.AuthMethod == ""
}

type assistState int

const (
	asClosed    assistState = iota
	asProbing               // checking kmem, the model and who holds the handoff
	asPanels                // no assistant: the chat side shows one reason line
	asElsewhere             // kmem-lock-audit is live in a tab
	asStarting              // the session is being started
	asLive                  // running (or FAILED when its process is gone)
	asSaving                // Stop assistant or Quit: waiting for the handoff save
	asStopping              // stopping the process
)

func (st assistState) String() string {
	return [...]string{"closed", "probing", "panels only", "elsewhere", "starting", "live", "saving", "stopping"}[st]
}

type kmemAssist struct {
	state       assistState
	w           *kmemWindow  // the open window, nil while closed (hidden)
	s           *deskSession // the assistant session (the last one, once stopped)
	gen         int          // bumped on every window open and close: stale ticks drop out
	run         int          // bumped on every probe and stop: stale lifecycle callbacks drop out
	tickGen     int
	reason      string
	button      string    // the label of the button after the reason line ("" if none)
	userStopped bool      // the user pressed Stop assistant: opening the window doesn't start it
	untilSaved  bool      // the user chose to keep it running until it saves
	rebootNext  bool      // Restart (new model setting): start again once stopped
	saveBy      time.Time // the save deadline while saving (the header's countdown)
	quit        bool      // stopping for Quit: no question on timeout
	done        []func()
	choice      dialog.Dialog
	lastSave    time.Time

	// The chat side, built once and shown again on every reopen.
	chat     fyne.CanvasObject
	view     *chatView
	input    *sendEntry
	stateLbl *widget.Label
	modelLbl *widget.Label
	note     *widget.Label
	restart  *widget.Button
	reboot   *widget.Button // Restart on the changed model setting
	stopBtn  *widget.Button
}

var (
	kmemAssistsMu sync.Mutex
	kmemAssists   = map[*desk]*kmemAssist{}
)

func (d *desk) assist() *kmemAssist {
	kmemAssistsMu.Lock()
	defer kmemAssistsMu.Unlock()
	a := kmemAssists[d]
	if a == nil {
		a = &kmemAssist{}
		kmemAssists[d] = a
	}
	return a
}

// kmemAssistStartNote follows the resume prompt on every start.
const kmemAssistStartNote = "Quill Desktop started you as its always-running kmem assistant. After the brief and your handoff, list the open tasks titled \"kmem issue:\" (topic kmem) and propose a plan for each. " +
	"Reclaim the tasks you transferred to kmem-lock-audit when you last stopped."

// kmemAssistStopMessage asks the assistant to stop, settle its claims (rule
// 19, user 2026-11-22: release idle claims, transfer the rest with the 48 h
// lease) and save its handoff at once (Stop assistant, Quit).
func kmemAssistStopMessage() string {
	return fmt.Sprintf("Quill Desktop is stopping the kmem assistant: stop what you are doing now; this session is stopped once its kmem handoff is saved (Quill Desktop waits %s). "+
		"First release the claims you are not actively working on (`kmem --as %[2]s task release ID`) and transfer the rest to yourself with the 48 h lease (`kmem --as %[2]s task transfer ID %[2]s`). "+
		"Then run `kmem --as %[2]s handoff save %[2]s` with --pointer and --state lines for everything the next session needs, including the transferred task ids to reclaim; run it even if you saved recently, Quill Desktop checks the save time. "+
		"It refuses until your claims, background jobs and uncommitted files are recorded. Don't start other work. Reply with one line once it is saved.", waitText(), kmemAssistName)
}

func waitText() string {
	if kmemWinSaveWait >= time.Second {
		return fmt.Sprintf("%d s", int(kmemWinSaveWait/time.Second))
	}
	return kmemWinSaveWait.String()
}

// kmemAssistBoot starts the assistant with Quill Desktop (after a hot
// reload's restore, so a handed-over session is adopted, never doubled). It
// needs kmem memory on; the probe checks the model setting, sign-in and
// whether kmem-lock-audit is live in a tab (then the window offers to move it
// here and nothing starts).
func (d *desk) kmemAssistBoot() {
	a := d.assist()
	if a.state != asClosed || a.userStopped || !d.kmemMemory() {
		return
	}
	d.kmemAssistProbe(a)
}

// kmemAssistOpen: the kmem window w opened (kmemWindowOpenHook). A running
// assistant shows its chat again; otherwise it starts (unless the user
// stopped it).
func (d *desk) kmemAssistOpen(w *kmemWindow) {
	a := d.assist()
	a.w = w
	a.gen++
	switch a.state {
	case asSaving, asStopping:
		d.kmemAssistAttach(a) // the header shows the save
		return
	case asProbing:
		d.kmemAssistReason(a, "Starting kmem-lock-audit...")
		return
	}
	if a.s != nil && a.s.running() && !a.s.stopping {
		a.state = asLive
		d.kmemAssistAttach(a)
		return
	}
	if a.s != nil && (a.state == asLive || a.state == asStarting) {
		d.markEnded(a.s.id) // it exited while hidden: nothing to save
		a.state = asClosed
	}
	if a.userStopped {
		d.kmemAssistStoppedReason(a)
		return
	}
	d.kmemAssistProbe(a)
}

// kmemAssistStoppedReason: the window's line after Stop assistant.
func (d *desk) kmemAssistStoppedReason(a *kmemAssist) {
	d.kmemAssistReasonWith(a, "kmem-lock-audit is stopped; its handoff keeps its work for the next start.", "Start assistant", func() {
		if a.state == asClosed || a.state == asPanels {
			a.userStopped = false
			d.kmemAssistProbe(a)
		}
	})
}

// kmemAssistReason shows one line instead of the chat (no assistant).
func (d *desk) kmemAssistReason(a *kmemAssist, reason string) {
	a.reason, a.button = reason, ""
	if a.w == nil {
		return
	}
	kmemWinSetPrefill(a.w, nil)
	kmemWinSetChatReason(a.w, reason)
}

func (d *desk) kmemAssistReasonWith(a *kmemAssist, reason, button string, tap func()) {
	a.reason, a.button = reason, button
	if a.w == nil {
		return
	}
	kmemWinSetPrefill(a.w, nil)
	kmemWinSetChatReasonWith(a.w, reason, button, tap)
}

// kmemAssistOff: the reason line for the "Off" model setting.
const kmemAssistOffReason = "The kmem assistant is off (Settings → General → Memory (kmem) → kmem assistant)."

// kmemAssistProbe is PROBING: kmem, the model setting, sign-in, the local
// model's tool use and who holds the handoff decide between the chat and a
// reason line.
func (d *desk) kmemAssistProbe(a *kmemAssist) {
	a.state = asProbing
	a.reason, a.button = "", ""
	a.run++
	run := a.run
	model := d.kmemAssistModel()
	switch {
	case !d.kmemAvailable():
		a.state = asPanels
		d.kmemAssistReason(a, "kmem is not available, so the kmem assistant can't run.")
		return
	case model == kmemAssistOff:
		a.state = asPanels
		d.kmemAssistReason(a, kmemAssistOffReason)
		return
	case !isLocal(model) && !kmemAssistSignedIn(d):
		a.state = asPanels
		d.kmemAssistReasonWith(a, "Claude Code is not signed in.", "Sign in", d.signIn)
		return
	}
	if o := d.kmemAssistOwner(); o != nil {
		if !isHiddenSession(o) {
			d.kmemAssistElsewhere(a, o)
			return
		}
		if o.running() && !o.stopping {
			d.kmemAssistAdopt(a, o) // handed over by a hot reload
			return
		}
	}
	d.kmemAssistReason(a, "Starting kmem-lock-audit...")
	go func() {
		reason := ""
		if isLocal(model) {
			if err := d.checkOllama(model); err != nil {
				reason = "The kmem assistant can't start: " + err.Error() + "."
			} else if !localModelUsesTools(model) {
				reason = "The local model can't use tools; the assistant needs one that can."
			}
		}
		var saved time.Time
		prompt := ""
		if reason == "" {
			for _, h := range d.kmemHandoffs() {
				if h.Name == kmemAssistName {
					saved = h.Saved
				}
			}
			prompt = d.resumePrompt(kmemAssistName)
		}
		d.do(func() {
			if a.run != run || a.state != asProbing {
				return
			}
			if reason != "" {
				a.state = asPanels
				d.kmemAssistReason(a, reason)
				return
			}
			d.kmemAssistStart(a, model, prompt, saved)
		})
	}()
}

// kmemAssistOwner: a live session holding the operator's handoff, other than
// the window's own (nil if none).
func (d *desk) kmemAssistOwner() *deskSession {
	return d.handoffOwner(kmemAssistName, &deskSession{})
}

// kmemAssistStart is STARTING: a fresh hidden session resumes from the handoff.
func (d *desk) kmemAssistStart(a *kmemAssist, model, prompt string, saved time.Time) {
	if o := d.kmemAssistOwner(); o != nil {
		if isHiddenSession(o) && o.running() && !o.stopping {
			d.kmemAssistAdopt(a, o)
			return
		}
		if !isHiddenSession(o) {
			d.kmemAssistElsewhere(a, o)
			return
		}
	}
	dir := kmemWindowDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		a.state = asPanels
		d.kmemAssistReason(a, "The kmem assistant can't start: "+err.Error()+".")
		return
	}
	prefs := d.app.Preferences()
	note := kmemAssistCrashNote(prefs.String(prefKmemWinStarted), saved)
	prefs.SetString(prefKmemWinStarted, time.Now().Format(time.RFC3339))
	s := d.addSession(dir, model, "default", false)
	s.hidden = true
	s.title = kmemAssistName
	d.org.Kmem[s.id] = kmemAssistName
	if d.org.Hidden == nil {
		d.org.Hidden = map[string]bool{}
	}
	d.org.Hidden[s.id] = true
	d.saveOrg()
	a.s, a.state = s, asStarting
	a.lastSave = saved
	d.kmemAssistAttach(a)
	if note != "" {
		d.notify(s, note)
	}
	d.sendResume(s, kmemAssistName, prompt+"\n\n"+kmemAssistStartNote)
	a.state = asLive
	d.kmemAssistRefresh(a)
	d.kmemAssistDot()
}

// kmemAssistAdopt takes over a running hidden session (a hot reload handed
// it over, with or without the window open).
func (d *desk) kmemAssistAdopt(a *kmemAssist, s *deskSession) {
	a.s, a.state = s, asLive
	d.kmemAssistAttach(a)
	d.kmemAssistDot()
}

// kmemAssistCrashNote: the first notice when the last session ended without
// saving (its handoff is older than its start).
func kmemAssistCrashNote(started string, saved time.Time) string {
	t, err := time.Parse(time.RFC3339, started)
	if err != nil || !saved.IsZero() && !saved.Before(t) {
		return ""
	}
	if saved.IsZero() {
		return "The last session ended without saving, and kmem-lock-audit has no saved handoff yet."
	}
	return "The last session ended without saving; working from the save of " + saved.Local().Format("Jan 2 15:04") + "."
}

// kmemAssistElsewhere is ELSEWHERE: kmem-lock-audit is live in a tab. The
// window offers to move it in (save there, resume here; user 2026-11-22).
func (d *desk) kmemAssistElsewhere(a *kmemAssist, o *deskSession) {
	a.state = asElsewhere
	title, _, _ := d.rowText(o.id)
	d.kmemAssistReasonWith(a, fmt.Sprintf("kmem-lock-audit is live in the tab “%s”. Move it here: it saves its handoff there and resumes as the background kmem assistant.", title),
		"Move it here", func() { d.kmemAssistMoveIn(a, o) })
}

func (d *desk) kmemAssistMoveIn(a *kmemAssist, o *deskSession) {
	if a.state != asElsewhere {
		return
	}
	title, _, _ := d.rowText(o.id)
	d.endAndSaveTo(o.id, placeInactive)
	d.kmemAssistReason(a, fmt.Sprintf("Waiting for the tab “%s” to save its kmem handoff...", title))
	d.kmemAssistWaitOwner(a, a.run, o, title, time.Now().Add(3*kmemWinSaveWait))
}

func (d *desk) kmemAssistWaitOwner(a *kmemAssist, run int, o *deskSession, title string, deadline time.Time) {
	d.afterUI(kmemWinSavePoll, func() {
		if a.run != run || a.state != asElsewhere {
			return
		}
		if d.kmemAssistOwner() == nil {
			d.kmemAssistProbe(a)
			return
		}
		if (o.kmemSave == nil && o.running() && !o.stopping) || time.Now().After(deadline) {
			d.kmemAssistReasonWith(a, fmt.Sprintf("The tab “%s” did not save its kmem handoff, so kmem-lock-audit keeps running there.", title),
				"Try again", func() { d.kmemAssistMoveIn(a, o) })
			return
		}
		d.kmemAssistWaitOwner(a, run, o, title, deadline)
	})
}

// kmemAssistAttach shows the assistant's chat on the window's right side.
// The chat is built once and shown again on every reopen (same view, same
// unsent input); show(s) catches up on what happened while it was hidden.
func (d *desk) kmemAssistAttach(a *kmemAssist) {
	w, s := a.w, a.s
	if s != nil {
		a.reason, a.button = "", "" // also with the window closed: phones read it (kmemAssistSummary)
	}
	if w == nil || s == nil {
		return
	}
	if a.chat == nil {
		a.chat = d.kmemAssistChat(a)
	}
	w.s, w.view = s, a.view
	a.view.show(s)
	kmemWinSetChat(w, a.chat)
	kmemWinSetPrefill(w, d.kmemAssistPrefill)
	d.kmemAssistRefresh(a)
	if a.tickGen != a.gen {
		a.tickGen = a.gen
		go d.kmemAssistTick(a, a.gen, kmemWinTick)
	}
}

// kmemAssistChat: header, chat view, input with send and stop.
func (d *desk) kmemAssistChat(a *kmemAssist) fyne.CanvasObject {
	a.view = newChatViewWith(d, func() float32 { return chatMaxWidth }, func() float32 { return 0 })
	title := boldLabel(kmemAssistName + " · kmem assistant")
	a.stateLbl = widget.NewLabel("")
	a.modelLbl = caption("")
	a.note = caption("")
	a.note.Hide()
	save := widget.NewButton("Save now", func() {
		if s := a.s; s != nil {
			d.saveHandoffNow(s)
		}
	})
	save.Importance = widget.LowImportance
	a.restart = widget.NewButtonWithIcon("Restart", theme.ViewRefreshIcon(), func() { d.kmemAssistRestart(a) })
	a.restart.Hide()
	// The model setting changed while it runs: save, stop and start again on
	// the new setting (td7337f, t9249e3).
	a.reboot = widget.NewButtonWithIcon("Restart", theme.ViewRefreshIcon(), func() { d.kmemAssistReboot(a) })
	a.reboot.Importance = widget.HighImportance
	a.reboot.Hide()
	// Stop assistant: the save-and-stop flow (closing the window only hides it).
	a.stopBtn = widget.NewButton("Stop assistant", func() { d.kmemAssistStopNow(a) })
	a.stopBtn.Importance = widget.LowImportance
	head := container.NewVBox(
		container.NewBorder(nil, nil, title, a.stateLbl),
		container.NewBorder(nil, nil, nil, container.NewHBox(a.restart, a.reboot, save, a.stopBtn), a.modelLbl),
		widget.NewSeparator())
	a.input = newSendEntry(d.kmemAssistSend)
	a.input.hint = "Ask kmem-lock-audit..."
	a.input.SetPlaceHolder(a.input.hint)
	a.input.SetMinRowsVisible(2)
	d.alignInput(a.input)
	send := widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.kmemAssistSend(a.input.Text) })
	send.Importance = widget.HighImportance
	stop := widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		if s := a.s; s != nil {
			d.interrupt(s)
		}
	})
	bottom := container.NewVBox(a.note, container.NewBorder(nil, nil, nil, container.NewVBox(send, stop), a.input))
	return container.NewBorder(head, bottom, nil, nil, a.view.root)
}

// kmemAssistRefresh updates the header: state, model, subagent note, Restart.
func (d *desk) kmemAssistRefresh(a *kmemAssist) {
	s := a.s
	if s == nil || a.stateLbl == nil {
		return
	}
	st := "● " + s.status().String()
	failed := !s.running() && (a.state == asLive || a.state == asStarting)
	switch {
	case failed && s.failed != "":
		st = "stopped: " + oneLine(s.failed, 80)
	case failed:
		st = "stopped"
	case a.state == asSaving:
		st = "● saving its handoff, then stopping"
		if left := time.Until(a.saveBy); !a.untilSaved && a.choice == nil && left > 0 {
			st += fmt.Sprintf(" · %d s", int((left+time.Second-1)/time.Second))
		}
	case a.state == asStopping:
		st = "● stopping"
	case s.status() == stNone:
		st = "● starting"
	}
	setLabel(a.stateLbl, st)
	if failed {
		a.restart.Show()
	} else {
		a.restart.Hide()
	}
	if !failed && (a.state == asLive || a.state == asStarting) {
		a.stopBtn.Show()
	} else {
		a.stopBtn.Hide()
	}
	src := "kmem assistant setting"
	if d.app.Preferences().String(prefKmemAssistModel) == "" {
		src = optAssistSame // "Online Claude (default)": the setting is unset
	}
	m := "model: " + s.model + " (" + src + ")"
	if d.kmemAssistModelChanged(a) {
		m += " · Restart to use the new setting"
		a.reboot.Show()
	} else {
		if want := d.kmemAssistModel(); want != s.model {
			m += " · the new setting applies the next time it starts"
		}
		a.reboot.Hide()
	}
	setLabel(a.modelLbl, m)
	if len(s.agentGrants) > 0 {
		setLabel(a.note, "subagents running; messages now pause them")
		a.note.Show()
	} else {
		a.note.Hide()
	}
}

func (d *desk) kmemAssistTick(a *kmemAssist, gen int, every time.Duration) {
	for {
		time.Sleep(every)
		alive := false
		d.doWait(func() { // doWait: fyne.Do returns before f runs
			if a.gen == gen && a.w != nil {
				alive = true
				d.kmemAssistRefresh(a)
			}
		})
		if !alive {
			return
		}
	}
}

// kmemAssistModelChanged: the model setting differs from the running
// assistant's model (the header's Restart).
func (d *desk) kmemAssistModelChanged(a *kmemAssist) bool {
	s := a.s
	return s != nil && s.running() && !s.stopping && (a.state == asLive || a.state == asStarting) && d.kmemAssistModel() != s.model
}

// kmemAssistReboot (Restart after a model setting change) runs the
// save-and-stop flow, then starts the assistant again on the new setting.
func (d *desk) kmemAssistReboot(a *kmemAssist) {
	if !d.kmemAssistModelChanged(a) {
		return
	}
	a.rebootNext = true
	d.kmemAssistSaveStop(false, nil)
}

// kmemAssistExited: the assistant's process ended (handleExit). While it was
// saving, the stop flow ends at once (the window shows "Start assistant");
// otherwise the header shows it stopped.
func (d *desk) kmemAssistExited(s *deskSession) {
	a := d.assist()
	if a.s != s {
		return
	}
	if a.state == asSaving {
		d.kmemAssistStop(a, false)
		return
	}
	d.kmemAssistRefresh(a)
}

// kmemAssistRestart (FAILED → Restart) starts a fresh session.
func (d *desk) kmemAssistRestart(a *kmemAssist) {
	if a.w == nil {
		return
	}
	d.kmemAssistRestartNow(a)
}

// kmemAssistRestartNow: Restart without the window (Quill Android's
// /phone/kmem/assistant/restart and start); false when the state offers no
// restart.
func (d *desk) kmemAssistRestartNow(a *kmemAssist) bool {
	if a.s != nil && a.s.running() || a.state == asSaving || a.state == asStopping || a.state == asProbing {
		return false
	}
	if a.s != nil && (a.state == asLive || a.state == asStarting) {
		d.markEnded(a.s.id) // FAILED: nothing to save
	}
	a.userStopped = false
	d.kmemAssistProbe(a)
	return true
}

// kmemAssistChatOn: the window's chat takes input (the left side's Explain,
// Suggest split and Ask the operator buttons show only then).
func (d *desk) kmemAssistChatOn() bool {
	a := d.assist()
	return a.w != nil && a.input != nil && a.s != nil && a.reason == "" && (a.state == asLive || a.state == asStarting)
}

// kmemAssistPrefill puts text in the window's input; it never sends.
func (d *desk) kmemAssistPrefill(text string) {
	a := d.assist()
	if !d.kmemAssistChatOn() {
		return
	}
	a.input.SetText(text)
	a.input.SetCursorTextOffset(len([]rune(text)))
}

func (d *desk) kmemAssistSend(text string) {
	a := d.assist()
	s := a.s
	if s == nil || strings.TrimSpace(text) == "" || a.state != asLive && a.state != asStarting {
		return
	}
	if a.input != nil {
		a.input.SetText("")
	}
	d.sendTo(s, text)
	d.kmemAssistRefresh(a)
}

// kmemAssistClose: the kmem window closed (X, Escape; kmemWindowCloseHook).
// Closing only hides it: the assistant keeps running with its claims and
// background jobs, nothing is saved or stopped, and reopening shows the same
// chat.
func (d *desk) kmemAssistClose(w *kmemWindow) {
	a := d.assist()
	if a.w != w {
		return
	}
	a.w = nil
	a.gen++ // the header tick ends
}

// kmemAssistStopNow: the window's "Stop assistant" (the save-and-stop flow;
// opening the window again doesn't restart it, "Start assistant" does).
func (d *desk) kmemAssistStopNow(a *kmemAssist) {
	if a.state != asLive && a.state != asStarting {
		return
	}
	a.userStopped = true
	d.kmemAssistSaveStop(false, nil)
}

// kmemAssistQuit runs the save-and-stop flow for Quit (bounded by
// kmemWinSaveWait, no question on timeout) and calls done once the assistant
// is stopped.
func (d *desk) kmemAssistQuit(done func()) {
	d.kmemAssistSaveStop(true, done)
}

func (d *desk) kmemAssistSaveStop(quit bool, done func()) {
	a := d.assist()
	if done != nil {
		a.done = append(a.done, done)
	}
	a.quit = a.quit || quit
	s := a.s
	switch {
	case a.state == asSaving || a.state == asStopping:
		if quit && a.state == asSaving && (a.choice != nil || a.untilSaved) {
			d.kmemAssistStop(a, false)
		}
		return
	case s == nil || !s.running() || s.stopping || a.state != asLive && a.state != asStarting:
		if s != nil && !s.running() && (a.state == asLive || a.state == asStarting) {
			d.markEnded(s.id) // FAILED: nothing to save
		}
		a.run++ // a probe in flight starts nothing
		a.state, a.quit = asClosed, false
		d.kmemAssistDot()
		d.kmemAssistFinished(a)
		if a.w != nil && a.userStopped {
			d.kmemAssistStoppedReason(a)
		}
		return
	}
	a.state = asSaving
	a.untilSaved = false
	a.saveBy = time.Now().Add(kmemWinSaveWait)
	asked := time.Now().Add(-2 * time.Second)
	// prompt set: kmemAfterTurn leaves this save to the close flow below;
	// kmemSaveAllowed still lets `kmem handoff save` through plan-first.
	ks := &kmemSave{name: kmemAssistName, asked: asked, end: true, prompt: "kmem assistant stop"}
	s.kmemSave = ks
	d.chatFor(s, s.conv.CancelPlans())
	d.flashHint(d.folder, "kmem assistant: saving handoff...")
	msg := kmemAssistStopMessage()
	d.kmemAssistRefresh(a)
	if p := s.proc; s.inTurn || s.conv.Busy || p.Busy() {
		// Mid-turn: stop at once, then ask for the save (user 2026-11-22).
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.Interrupt(ctx)
			d.do(func() {
				if s.kmemSave == ks {
					d.sendTo(s, msg)
				}
			})
		}()
	} else {
		d.sendTo(s, msg)
	}
	go d.kmemAssistWaitSave(a, s, ks, a.saveBy, kmemWinSavePoll)
}

// kmemSavedSince: the same save-time check as kmemAfterTurn (kmem.go): saved
// at or after asked, or a recent save that names this session.
func (d *desk) kmemSavedSince(name, id string, asked time.Time) (bool, time.Time) {
	var last time.Time
	saved, recent := false, false
	for _, h := range d.kmemHandoffs() {
		if h.Name != name {
			continue
		}
		last = h.Saved
		if !h.Saved.Before(asked.Truncate(time.Second)) {
			saved = true
		} else if asked.Sub(h.Saved) <= kmemRecentSave {
			recent = true
		}
	}
	if !saved && recent {
		saved = strings.Contains(d.kmemHandoffText(name), "session cs:"+id+")")
	}
	return saved, last
}

func (d *desk) kmemAssistWaitSave(a *kmemAssist, s *deskSession, ks *kmemSave, deadline time.Time, poll time.Duration) {
	for {
		saved, last := d.kmemSavedSince(ks.name, s.id, ks.asked)
		stop, ran := false, false
		// doWait, not do: fyne.Do returns before f runs, so ran was still
		// false and the loop ended after one poll (the window stayed at
		// "saving its handoff" after the process died; t9249e3).
		d.doWait(func() {
			ran = true
			if s.kmemSave != ks || a.s != s || a.state != asSaving {
				stop = true // reopened, or already stopped
				return
			}
			if !last.IsZero() {
				a.lastSave = last
			}
			switch {
			case saved:
				d.kmemAssistStop(a, true)
				stop = true
			case !s.running():
				d.kmemAssistStop(a, false)
				stop = true
			case !a.untilSaved && a.choice == nil && time.Now().After(deadline):
				if a.quit {
					d.kmemAssistStop(a, false)
					stop = true
					return
				}
				d.kmemAssistAsk(a)
			}
		})
		if stop || !ran {
			return
		}
		time.Sleep(poll)
	}
}

func (d *desk) kmemAssistLastSave(a *kmemAssist) string {
	if a.lastSave.IsZero() {
		return "There is no earlier save."
	}
	return "The last save is from " + a.lastSave.Local().Format("Jan 2 15:04") + "."
}

// kmemAssistAsk: the save missed the deadline; the user picks what happens.
// Quill Android shows the same question as a card; the first answer on either
// side wins (kmemAssistChoose hides the other).
func (d *desk) kmemAssistAsk(a *kmemAssist) {
	msg := widget.NewLabel(d.kmemAssistAskText(a))
	msg.Wrapping = fyne.TextWrapWord
	var btns []fyne.CanvasObject
	for i, label := range kmemAssistChoices {
		n := i
		btns = append(btns, widget.NewButton(label, func() { d.kmemAssistChoose(a, n) }))
	}
	dlg := dialog.NewCustomWithoutButtons("kmem assistant", container.NewVBox(msg, container.NewHBox(btns...)), d.win)
	dlg.Resize(fyne.NewSize(560, 200))
	a.choice = dlg
	dlg.Show()
	d.kmemAssistRefresh(a) // the countdown ends
	if s := a.s; s != nil && !d.seen(s) {
		d.kmemPhoneEvent("did not save its kmem handoff in time: choose what happens.")
	}
}

// kmemAssistChoices: the save-timeout question's answers, by kmemAssistChoose's index.
var kmemAssistChoices = []string{"Stop without saving", "Keep running until saved", "Keep it running"}

func (d *desk) kmemAssistAskText(a *kmemAssist) string {
	return fmt.Sprintf("kmem-lock-audit did not save its kmem handoff within %s. %s\n"+
		"kmem refuses the save until its claims, jobs and files are recorded, so it may still be working on that.", waitText(), d.kmemAssistLastSave(a))
}

// kmemAssistChoose: 0 stop without saving, 1 keep running until saved, 2 keep
// it running (cancel the stop).
func (d *desk) kmemAssistChoose(a *kmemAssist, n int) {
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	if a.state != asSaving {
		return
	}
	switch n {
	case 0:
		d.kmemAssistStop(a, false)
	case 1:
		a.untilSaved = true
		d.flashHint(d.folder, "kmem assistant: still running until it saves its handoff.")
	case 2:
		d.kmemAssistKeep(a)
	}
}

// kmemAssistKeep cancels the stop: the session goes on in the background.
func (d *desk) kmemAssistKeep(a *kmemAssist) {
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	a.userStopped, a.rebootNext = false, false
	s := a.s
	if s == nil || !s.running() {
		a.state = asClosed
		d.kmemAssistProbe(a)
		return
	}
	s.kmemSave = nil
	a.state, a.untilSaved, a.quit = asLive, false, false
	d.notify(s, "Quill Desktop no longer stops kmem-lock-audit; it keeps running in the background.")
	d.kmemAssistAttach(a)
	d.kmemAssistDot()
}

// kmemAssistStop is STOPPING: stop the process and mark the session ended
// (the hidden session has no tab and no place; docs §4 #23).
func (d *desk) kmemAssistStop(a *kmemAssist, saved bool) {
	s := a.s
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	s.kmemSave = nil
	a.state = asStopping
	a.run++
	d.kmemAssistRefresh(a)
	note := "Saved kmem handoff “" + kmemAssistName + "”."
	if !saved {
		note = fmt.Sprintf("kmem-lock-audit did not save its handoff within %s; it was stopped. %s", waitText(), d.kmemAssistLastSave(a))
		if !s.running() {
			note = "kmem-lock-audit stopped before it saved its handoff. " + d.kmemAssistLastSave(a)
		}
	}
	d.notify(s, note)
	d.flashHint(d.folder, note)
	d.markEnded(s.id)
	if !s.running() {
		d.kmemAssistStopped(a, s)
		return
	}
	s.stopping = true
	p := s.proc
	go func() {
		_ = p.Stop(5 * time.Second)
		d.do(func() { d.kmemAssistStopped(a, s) })
	}()
}

func (d *desk) kmemAssistStopped(a *kmemAssist, s *deskSession) {
	if a.s != s || a.state != asStopping {
		return
	}
	quit, reboot := a.quit, a.rebootNext && !a.quit
	a.state, a.untilSaved, a.quit, a.rebootNext = asClosed, false, false, false
	d.kmemAssistDot()
	d.kmemAssistFinished(a)
	if reboot {
		a.userStopped = false
		d.kmemAssistProbe(a) // on the new model setting
		return
	}
	if a.w != nil && !quit {
		d.kmemAssistStoppedReason(a)
	}
}

// kmemAssistRunning: the assistant's process runs (the kmem button's dot).
func (d *desk) kmemAssistRunning() bool {
	a := d.assist()
	return a.s != nil && a.s.running() && !a.s.stopping && a.state != asClosed && a.state != asStopping
}

// kmemAssistDot shows the kmem button's dot while the assistant runs.
func (d *desk) kmemAssistDot() {
	d.setKmemRunDot(d.kmemAssistRunning())
}

func (d *desk) kmemAssistFinished(a *kmemAssist) {
	done := a.done
	a.done = nil
	for _, f := range done {
		f()
	}
}
