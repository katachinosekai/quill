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

// The kmem window's assistant, kmem-lock-audit, the kmem operator
// (docs/design/kmem-window.md §5, §9, §14). It runs only while the kmem
// window is open: each open starts a fresh hidden session in kmemWindowDir()
// that resumes from the operator's kmem handoff; closing asks it to release
// or transfer its claims and save the handoff, waits up to kmemWinSaveWait
// and then stops it. The window itself (left panels, sizes, keys) is
// kmemwindow.go's; it calls kmemAssistOpen/kmemAssistClose through
// kmemWindowOpenHook/kmemWindowCloseHook.

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
	asSaving                // the window closed: waiting for the handoff save
	asStopping              // stopping the process
)

func (st assistState) String() string {
	return [...]string{"closed", "probing", "panels only", "elsewhere", "starting", "live", "saving", "stopping"}[st]
}

type kmemAssist struct {
	state       assistState
	w           *kmemWindow  // the open window, nil while closed
	s           *deskSession // the assistant session (the last one, once stopped)
	gen         int          // bumped on every open and close: stale callbacks drop out
	tickGen     int
	reason      string
	pendingOpen bool // the window was reopened while saving: start again after the stop
	untilSaved  bool // the user chose to keep it running hidden until it saves
	quit        bool // closing for Quit: no question on timeout
	done        []func()
	choice      dialog.Dialog
	lastSave    time.Time

	view     *chatView
	input    *sendEntry
	stateLbl *widget.Label
	modelLbl *widget.Label
	note     *widget.Label
	restart  *widget.Button
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

// kmemAssistStartNote follows the resume prompt on every open.
const kmemAssistStartNote = "Quill Desktop opened the kmem window. After the brief and your handoff, list the open tasks titled \"kmem issue:\" (topic kmem) and propose a plan for each. " +
	"Reclaim the tasks you transferred to kmem-lock-audit when the window last closed."

// kmemWindowCloseMessage asks the operator to stop, settle its claims (rule 19,
// user 2026-11-22: release idle claims, transfer the rest with the 48 h lease)
// and save its handoff at once.
func kmemWindowCloseMessage() string {
	return fmt.Sprintf("Quill Desktop is closing the kmem window: stop what you are doing now; this session is stopped once its kmem handoff is saved (Quill Desktop waits %s). "+
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

// kmemAssistOpen: the kmem window w opened (kmemWindowOpenHook).
func (d *desk) kmemAssistOpen(w *kmemWindow) {
	a := d.assist()
	a.w = w
	a.gen++
	switch a.state {
	case asSaving, asStopping:
		if a.state == asSaving && (a.choice != nil || a.untilSaved) {
			// It missed the save deadline and still runs: reopening keeps it.
			d.kmemAssistKeep(a)
			return
		}
		a.pendingOpen = true
		d.kmemAssistReason(a, "kmem-lock-audit is still saving its handoff from the last time; it starts here once that is done.")
		return
	}
	if a.s != nil && a.s.running() && !a.s.stopping {
		a.state = asLive
		d.kmemAssistAttach(a)
		return
	}
	d.kmemAssistProbe(a)
}

// kmemAssistReason shows one line instead of the chat (no assistant).
func (d *desk) kmemAssistReason(a *kmemAssist, reason string) {
	a.reason = reason
	if a.w == nil {
		return
	}
	kmemWinSetPrefill(a.w, nil)
	kmemWinSetChatReason(a.w, reason)
}

func (d *desk) kmemAssistReasonWith(a *kmemAssist, reason, button string, tap func()) {
	a.reason = reason
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
	a.reason = ""
	gen, w := a.gen, a.w
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
	if o := d.kmemAssistOwner(); o != nil && !isHiddenSession(o) {
		d.kmemAssistElsewhere(a, o)
		return
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
			if a.gen != gen || a.w != w || a.state != asProbing {
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
			// Handed over by a hot reload: the window shows it again.
			a.s, a.state = o, asLive
			d.kmemAssistAttach(a)
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
	d.kmemAssistReasonWith(a, fmt.Sprintf("kmem-lock-audit is live in the tab “%s”. Move it here: it saves its handoff there and resumes in this window.", title),
		"Move it here", func() { d.kmemAssistMoveIn(a, o) })
}

func (d *desk) kmemAssistMoveIn(a *kmemAssist, o *deskSession) {
	if a.state != asElsewhere {
		return
	}
	title, _, _ := d.rowText(o.id)
	d.endAndSaveTo(o.id, placeInactive)
	d.kmemAssistReason(a, fmt.Sprintf("Waiting for the tab “%s” to save its kmem handoff...", title))
	d.kmemAssistWaitOwner(a, a.gen, o, title, time.Now().Add(3*kmemWinSaveWait))
}

func (d *desk) kmemAssistWaitOwner(a *kmemAssist, gen int, o *deskSession, title string, deadline time.Time) {
	d.afterUI(kmemWinSavePoll, func() {
		if a.gen != gen || a.state != asElsewhere {
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
		d.kmemAssistWaitOwner(a, gen, o, title, deadline)
	})
}

// kmemAssistAttach shows the assistant's chat on the window's right side.
func (d *desk) kmemAssistAttach(a *kmemAssist) {
	w, s := a.w, a.s
	if w == nil || s == nil {
		return
	}
	a.reason = ""
	obj := d.kmemAssistChat(a)
	w.s, w.view = s, a.view
	a.view.show(s)
	kmemWinSetChat(w, obj)
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
	title := boldLabel(kmemAssistName + " · operator")
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
	head := container.NewVBox(
		container.NewBorder(nil, nil, title, a.stateLbl),
		container.NewBorder(nil, nil, nil, container.NewHBox(a.restart, save), a.modelLbl),
		widget.NewSeparator())
	a.input = newSendEntry(d.kmemAssistSend)
	a.input.hint = "Ask kmem-lock-audit..."
	a.input.SetPlaceHolder(a.input.hint)
	a.input.SetMinRowsVisible(2)
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
	case s.status() == stNone:
		st = "● starting"
	}
	setLabel(a.stateLbl, st)
	if failed {
		a.restart.Show()
	} else {
		a.restart.Hide()
	}
	src := "kmem assistant setting"
	if d.app.Preferences().String(prefKmemAssistModel) == "" {
		src = "New sessions"
	}
	m := "model: " + s.model + " (" + src + ")"
	if want := d.kmemAssistModel(); want != s.model {
		m += " · the new setting applies the next time the window opens"
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
		d.do(func() {
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

// kmemAssistRestart (FAILED → Restart) starts a fresh session.
func (d *desk) kmemAssistRestart(a *kmemAssist) {
	if a.w == nil || a.s != nil && a.s.running() {
		return
	}
	d.kmemAssistProbe(a)
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
// The window is gone at once; the save runs in the background.
func (d *desk) kmemAssistClose(w *kmemWindow) {
	d.kmemAssistCloseThen(false, nil)
}

// kmemAssistQuit runs the close flow for Quit (bounded by kmemWinSaveWait, no
// question on timeout) and calls done once the assistant is stopped.
func (d *desk) kmemAssistQuit(done func()) {
	d.kmemAssistCloseThen(true, done)
}

func (d *desk) kmemAssistCloseThen(quit bool, done func()) {
	a := d.assist()
	a.w = nil
	a.gen++
	a.pendingOpen = false
	a.view, a.input, a.stateLbl = nil, nil, nil
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
	case s == nil || !s.running() || s.stopping:
		if s != nil && !s.running() && (a.state == asLive || a.state == asStarting) {
			d.markEnded(s.id) // FAILED: nothing to save
		}
		a.state, a.quit = asClosed, false
		d.kmemAssistFinished(a)
		return
	}
	a.state = asSaving
	a.untilSaved = false
	asked := time.Now().Add(-2 * time.Second)
	// prompt set: kmemAfterTurn leaves this save to the close flow below;
	// kmemSaveAllowed still lets `kmem handoff save` through plan-first.
	ks := &kmemSave{name: kmemAssistName, asked: asked, end: true, prompt: "kmem window close"}
	s.kmemSave = ks
	d.chatFor(s, s.conv.CancelPlans())
	d.flashHint(d.folder, "kmem assistant: saving handoff...")
	msg := kmemWindowCloseMessage()
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
	go d.kmemAssistWaitSave(a, s, ks, time.Now().Add(kmemWinSaveWait), kmemWinSavePoll)
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
		d.do(func() {
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
func (d *desk) kmemAssistAsk(a *kmemAssist) {
	msg := widget.NewLabel(fmt.Sprintf("kmem-lock-audit did not save its kmem handoff within %s. %s\n"+
		"kmem refuses the save until its claims, jobs and files are recorded, so it may still be working on that.", waitText(), d.kmemAssistLastSave(a)))
	msg.Wrapping = fyne.TextWrapWord
	var btns []fyne.CanvasObject
	for i, label := range []string{"Stop without saving", "Keep running hidden until saved", "Reopen the kmem window"} {
		n := i
		btns = append(btns, widget.NewButton(label, func() { d.kmemAssistChoose(a, n) }))
	}
	dlg := dialog.NewCustomWithoutButtons("kmem assistant", container.NewVBox(msg, container.NewHBox(btns...)), d.win)
	dlg.Resize(fyne.NewSize(560, 200))
	a.choice = dlg
	dlg.Show()
}

// kmemAssistChoose: 0 stop without saving, 1 keep running hidden until
// saved, 2 reopen the kmem window.
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
		d.flashHint(d.folder, "kmem assistant: still running hidden until it saves its handoff.")
	case 2:
		d.kmemAssistKeep(a)
	}
}

// kmemAssistKeep cancels the close: the session goes on in the window.
func (d *desk) kmemAssistKeep(a *kmemAssist) {
	if a.choice != nil {
		a.choice.Hide()
		a.choice = nil
	}
	s := a.s
	if s == nil || !s.running() {
		a.state = asClosed
		if a.w != nil {
			d.kmemAssistProbe(a)
		}
		return
	}
	s.kmemSave = nil
	a.state, a.untilSaved, a.quit = asLive, false, false
	d.notify(s, "The kmem window is open again, so Quill Desktop no longer stops this session.")
	if a.w != nil {
		d.kmemAssistAttach(a)
		return
	}
	d.openKmemWindow()
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
	a.state, a.untilSaved, a.quit = asClosed, false, false
	d.kmemAssistFinished(a)
	if a.pendingOpen && a.w != nil {
		a.pendingOpen = false
		d.kmemAssistProbe(a)
	}
}

func (d *desk) kmemAssistFinished(a *kmemAssist) {
	done := a.done
	a.done = nil
	for _, f := range done {
		f()
	}
}
