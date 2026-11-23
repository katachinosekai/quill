package main

import (
	"fmt"
	"strings"
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

// kmemRole is the kmem assistant's configuration: kmem-lock-audit, kmem's
// developer and operator in one session, working in kmemWindowDir(). Its UI is
// the kmem window's chat side (this file).
var kmemRole *assistRole

func init() { kmemRole = newKmemRole() } // not a var initializer: the hooks reach assistFor, which reads kmemRole

func newKmemRole() *assistRole {
	return &assistRole{
		id:          "kmem",
		name:        kmemAssistName,
		label:       "kmem assistant",
		hidden:      roleKmem,
		hint:        kmemAssistantHint,
		dir:         kmemWindowDir,
		startedPref: prefKmemWinStarted,
		startNote:   kmemAssistStartNote,
		stopMessage: kmemAssistStopMessage,
		bootOK:      func(d *desk) bool { return d.kmemMemory() },
		available:   func(d *desk) bool { return d.kmemAvailable() },
		model:       func(d *desk) string { return d.kmemAssistModel() },
		unavailable: "kmem is not available, so the kmem assistant can't run.",
		offReason:   kmemAssistOffReason,
		items:       func(d *desk) []badgeItem { return badgeItems(d.kmemNeedsNow, "", "") },
		ready:       func(d *desk) bool { return d.kmemGot },
		badgeOn:     func(d *desk) bool { return d.kmemBadgeOn() },
		seenPref:    prefKmemSeen,
		bubble:      func(d *desk) *bubble { return d.kmemBubble },
		reason:      kmemWinReason,
		attach:      kmemWinAttach,
		refresh:     kmemWinRefresh,
		dot:         func(d *desk, on bool) { d.setKmemRunDot(on) },
		ask:         kmemWinAsk,
		stopped:     kmemWinStopped,
	}
}

// The kmem role's names for the generic lifecycle (assist.go); the window,
// the phone API and the tests call these.

func (d *desk) kmemAssistBoot()                           { d.assistBoot(d.assist()) }
func (d *desk) kmemAssistProbe(a *kmemAssist)             { d.assistProbe(a) }
func (d *desk) kmemAssistRestartNow(a *kmemAssist) bool   { return d.assistRestartNow(a) }
func (d *desk) kmemAssistReboot(a *kmemAssist)            { d.assistReboot(a) }
func (d *desk) kmemAssistModelChanged(a *kmemAssist) bool { return d.assistModelChanged(a) }
func (d *desk) kmemAssistChoose(a *kmemAssist, n int)     { d.assistChoose(a, n) }
func (d *desk) kmemAssistExited(s *deskSession) {
	if a := d.assist(); a.s == s {
		d.assistExited(a, s)
	}
}
func (d *desk) kmemAssistRunning() bool                       { return d.assistRunning(d.assist()) }
func (d *desk) kmemAssistDot()                                { d.assistDot(d.assist()) }
func (d *desk) kmemAssistOwner() *deskSession                 { return d.assistOwner(d.assist()) }
func (d *desk) kmemAssistFlush(a *kmemAssist, s *deskSession) { d.assistFlush(a, s) }

// kmemAssistQuit runs the save-and-stop flow for Quit and calls done once the
// assistant is stopped.
func (d *desk) kmemAssistQuit(done func()) { d.assistSaveStop(d.assist(), true, done) }

func kmemAssistCrashNote(started string, saved time.Time) string {
	return assistCrashNote(kmemAssistName, started, saved)
}

// kmemAssistRestart (FAILED → Restart) starts a fresh session.
func (d *desk) kmemAssistRestart(a *kmemAssist) {
	if a.w == nil {
		return
	}
	d.assistRestartNow(a)
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

// kmemAssistOpen: the kmem window w opened (kmemWindowOpenHook). A running
// assistant shows its chat again; otherwise it starts (unless the user
// stopped it).
func (d *desk) kmemAssistOpen(w *kmemWindow) {
	a := d.assist()
	a.w = w
	a.gen++
	switch a.state {
	case asSaving, asStopping:
		d.assistAttach(a) // the header shows the save
		return
	case asProbing:
		d.assistReason(a, "Starting "+a.role.name+"...")
		return
	}
	if a.s != nil && a.s.running() && !a.s.stopping {
		a.state = asLive
		d.assistAttach(a)
		return
	}
	if a.s != nil && (a.state == asLive || a.state == asStarting) {
		d.markEnded(a.s.id) // it exited while hidden: nothing to save
		a.state = asClosed
	}
	if a.userStopped {
		d.assistStoppedReason(a)
		return
	}
	d.assistProbe(a)
}

// kmemWinStopped: the window's line after Stop assistant.
func kmemWinStopped(d *desk, a *assistant) {
	d.assistReasonWith(a, "kmem-lock-audit is stopped; its handoff keeps its work for the next start.", "Start assistant", func() {
		if a.state == asClosed || a.state == asPanels {
			a.userStopped = false
			d.assistProbe(a)
		}
	})
}

// kmemAssistReason shows one line instead of the chat (no assistant).
func (d *desk) kmemAssistReason(a *kmemAssist, reason string) { d.assistReason(a, reason) }

func (d *desk) kmemAssistReasonWith(a *kmemAssist, reason, button string, tap func()) {
	d.assistReasonWith(a, reason, button, tap)
}

// kmemWinReason is the kmem role's reason hook: the line goes to the window's
// chat side (nothing while the window is closed).
func kmemWinReason(d *desk, a *assistant, reason, button string, tap func()) {
	if a.w == nil {
		return
	}
	kmemWinSetPrefill(a.w, nil)
	if button == "" && tap == nil {
		kmemWinSetChatReason(a.w, reason)
		return
	}
	kmemWinSetChatReasonWith(a.w, reason, button, tap)
}

// kmemAssistOff: the reason line for the "Off" model setting.
const kmemAssistOffReason = "The kmem assistant is off (Settings → General → Memory (kmem) → kmem assistant)."

// kmemWinAttach (the kmem role's attach hook) shows the assistant's chat on the window's right side.
// The chat is built once and shown again on every reopen (same view, same
// unsent input); show(s) catches up on what happened while it was hidden.
func kmemWinAttach(d *desk, a *assistant) {
	w, s := a.w, a.s
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
	a.reboot = widget.NewButtonWithIcon("Restart", theme.ViewRefreshIcon(), func() { d.assistReboot(a) })
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
func (d *desk) kmemAssistRefresh(a *kmemAssist) { d.assistRefresh(a) }

// kmemWinRefresh is the kmem role's refresh hook (after the badge).
func kmemWinRefresh(d *desk, a *assistant) {
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
	if a.view != nil && a.view.s == s {
		a.view.setStatus(s)
	}
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
	if d.assistModelChanged(a) {
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
	d.assistSaveStop(a, false, nil)
}

// kmemWinAsk (the kmem role's ask hook): the save missed the deadline; the user picks what happens.
// Quill Android shows the same question as a card; the first answer on either
// side wins (kmemAssistChoose hides the other).
func kmemWinAsk(d *desk, a *assistant) {
	msg := widget.NewLabel(d.kmemAssistAskText(a))
	msg.Wrapping = fyne.TextWrapWord
	var btns []fyne.CanvasObject
	for i, label := range kmemAssistChoices {
		n := i
		btns = append(btns, widget.NewButton(label, func() { d.assistChoose(a, n) }))
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
		"kmem refuses the save until its claims, jobs and files are recorded, so it may still be working on that.", waitText(), d.assistLastSave(a))
}
