package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	restoreEnv  = "QUILL_RESTORE"
	typingQuiet = 15 * time.Second
	prefsSettle = 250 * time.Millisecond
)

type restoreState struct {
	Session  string              `json:"session"`
	Draft    string              `json:"draft"`
	DraftFor string              `json:"draftFor,omitempty"`
	Width    float32             `json:"width"`
	Height   float32             `json:"height"`
	ScrollY  float32             `json:"scrollY"`
	Hidden   bool                `json:"hidden"`
	Max      bool                `json:"maximized"`
	Split    *splitState         `json:"split,omitempty"`
	X        int                 `json:"x"`
	Y        int                 `json:"y"`
	Running  []string            `json:"running"`
	Handed   []handedSession     `json:"handed"`
	Queues   map[string][]string `json:"queues,omitempty"`
	Meta     map[string]sessMeta `json:"meta,omitempty"`
	Drafts   map[string]string   `json:"drafts,omitempty"`
	Carets   map[string]caretPos `json:"carets,omitempty"`
	Paused   *pauseState         `json:"paused,omitempty"`
	KmemWin  bool                `json:"kmemWin,omitempty"` // the kmem window was open: reopen it
}

type pauseState struct {
	Until int64    `json:"until"`
	IDs   []string `json:"ids"`
	Text  string   `json:"text"`
}

type binWatch struct {
	path string
	mod  time.Time
	size int64
}

func newBinWatch() *binWatch {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	st, err := os.Stat(exe)
	if err != nil {
		return nil
	}
	return &binWatch{path: exe, mod: st.ModTime(), size: st.Size()}
}

func (b *binWatch) changed() bool { return b.changedAfter(time.Second) }

// changedAfter: the program file is a new ELF build, last written more than
// settle ago (a build still being written isn't taken).
func (b *binWatch) changedAfter(settle time.Duration) bool {
	st, err := os.Stat(b.path)
	if err != nil || st.Size() == 0 {
		return false
	}
	if st.ModTime().Equal(b.mod) && st.Size() == b.size {
		return false
	}
	if time.Since(st.ModTime()) < settle || st.Mode()&0o111 == 0 {
		return false
	}
	f, err := os.Open(b.path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := f.Read(head); err != nil || !bytes.Equal(head, []byte("\x7fELF")) {
		return false
	}
	b.mod, b.size = st.ModTime(), st.Size()
	return true
}

func (d *desk) watchBinary(ctx context.Context) {
	b := newBinWatch()
	if b == nil {
		return
	}
	d.binMu.Lock()
	d.bin = b
	d.binMu.Unlock()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.binMu.Lock()
			changed := b.changed()
			d.binMu.Unlock()
			if changed {
				d.do(func() { d.binaryChanged(b.path) })
			}
		}
	}
}

// binaryChanged: Quill's program file was replaced. Only a switch the user
// asked for (Update, Restart into it) happens by itself; otherwise the update
// line asks (user 2026-11-21: never update on its own).
func (d *desk) binaryChanged(path string) {
	if !d.swapOK {
		d.offerBuild(path)
		return
	}
	d.swapTo = path
	d.trySwap()
}

type handedSession struct {
	ID     string          `json:"id"`
	Cwd    string          `json:"cwd"`
	Model  string          `json:"model"`
	Mode   string          `json:"mode"`
	Title  string          `json:"title"`
	RCOn   bool            `json:"rcOn"`
	RCURL  string          `json:"rcUrl"`
	Handle session.Handoff `json:"handle"`
	Asks   []pendingAsk    `json:"asks"`
	Plan   bool            `json:"plan"`
	Wait   bool            `json:"planWait"`
	RunAll bool            `json:"runAll"`
	Gate   *handedGate     `json:"gate,omitempty"` // the approved plan's scope (planscope.go)
	Sugg   string          `json:"suggestion,omitempty"`
	// AutoOff: auto-approve was on; it isn't kept, but Claude is told it is
	// off with the next message (autoapprove.go).
	AutoOff bool `json:"autoOff,omitempty"`
	Hidden  bool `json:"hidden,omitempty"` // the kmem window's assistant
	// Restarted: the keeper reattaches the session after Quill Desktop
	// crashed (not a live reload); planscope.go's restart rule applies.
	Restarted bool `json:"restarted,omitempty"`
}

type pendingAsk struct {
	RequestID string                   `json:"requestId"`
	Request   *protocol.ControlRequest `json:"request"`
}

// restoreHead: the current tab and the box's unsent text, tied to that tab.
// Also a brand-new tab whose session has no transcript yet: it is restored
// from Meta and must stay the current tab.
func (d *desk) restoreHead() restoreState {
	st := restoreState{Hidden: d.hidden}
	if d.current != nil && d.current.remote != nil {
		return st // other PCs' sessions and drafts are memory only
	}
	st.Draft = d.boxDraft(d.current, d.input)
	if d.current != nil {
		st.Session = d.current.id
		if !d.split() {
			st.DraftFor = d.current.id
		}
	}
	return st
}

func (d *desk) trySwap() {
	if d.swapTo == "" || d.swapping {
		return
	}
	if why := d.swapBlockers(); len(why) > 0 {
		d.swapWaits(why)
		return
	}
	d.swapNow = false
	noticed := d.swapNotice
	if noticed {
		d.swapNotice = false
		d.setUpdLine("Switching to the new Quill Desktop build…")
	}
	d.swapping = true
	d.stopMedia()
	d.flushUI()
	// Other PCs' sessions are memory only: never handed over or restored.
	d.stopRemotes()
	st := d.restoreHead()
	if d.current != nil && !d.split() {
		d.strip.keepBox(d.current.id, d.input)
		d.strip.keepDraft(d.current.id, st.Draft)
	}
	for _, p := range d.panes {
		if p.view.s != nil {
			d.strip.keepBox(p.view.s.id, p.input)
			d.strip.keepDraft(p.view.s.id, d.boxDraft(p.view.s, p.input))
		}
	}
	d.saveQueues(&st)
	d.saveSessions(&st)
	if d.usagePaused {
		st.Paused = &pauseState{Until: d.pauseUntil.Unix(), IDs: d.pausedIDs, Text: d.pauseText.Text}
	}
	size := d.win.Canvas().Size()
	st.Width, st.Height = size.Width, size.Height
	st.Max, st.X, st.Y = windowPlacement(d.win)
	st.Split = d.saveSplit()
	st.ScrollY = d.chat.scroll.Offset.Y
	st.KmemWin = d.kmemWin != nil // hot reload keeps the assistant and reopens the window (§14 #8)
	running := d.runningSessions()
	type meta struct {
		s  *deskSession
		hs handedSession
	}
	var metas []meta
	for _, s := range running {
		hs := handedSession{ID: s.id, Cwd: s.cwd, Model: s.model, Mode: s.mode, Title: s.title, RCOn: s.rcOn, RCURL: s.rcURL, Hidden: isHiddenSession(s)}
		hs.savePending(s)
		metas = append(metas, meta{s, hs})
	}
	target := d.swapTo
	var lan *lanMem
	if a := d.phone().api; a != nil {
		lan = a.lan
	}
	idle := d.pcIdle()
	go func() {
		var handed []meta
		for _, m := range metas {
			h, err := m.s.proc.Handoff()
			if err != nil {
				if m.s.persisted {
					st.Running = append(st.Running, m.s.id)
				}
				continue
			}
			m.hs.Handle = h
			st.Handed = append(st.Handed, m.hs)
			handed = append(handed, m)
		}
		time.Sleep(prefsSettle)
		b, _ := json.Marshal(st)
		path, err := writePrivate("quill-restore-*.json", b)
		if err == nil {
			env := append(os.Environ(), restoreEnv+"="+path)
			// Device-lock tokens: memory only, in a pipe (pclock.go).
			unlocks, _ := json.Marshal(d.pcLock.handOverWith(lan, idle))
			kv, undo, ok := passUnlocks(unlocks)
			if ok {
				env = append(env, kv)
			}
			err = execSelf(target, os.Args, env)
			if ok {
				undo()
			}
			os.Remove(path)
		}
		d.do(func() {
			d.swapping = false
			d.swapTo = ""
			d.forceSwap = false
			for _, m := range handed {
				d.reattach(m.s, m.hs)
			}
			if noticed {
				d.setUpdLine("")
			}
			if d.current != nil {
				d.chat.update([]*chat.Item{d.current.conv.AddError("Could not switch to the new build: " + err.Error())})
			}
		})
	}()
}

func (d *desk) reattach(s *deskSession, hs handedSession) bool {
	p, err := session.Attach(hs.Handle, func(ev *protocol.Event) {
		d.do(func() { d.handleEvent(s, ev) })
	})
	if err != nil {
		return false
	}
	d.adopt(s, p)
	s.rcOn, s.rcURL = hs.RCOn, hs.RCURL
	s.conv.Busy = hs.Handle.Busy
	hs.restorePending(s)
	if hs.Restarted {
		path := ""
		if info, ok := d.infos[s.id]; ok {
			path = info.Path
		}
		dropFinishedAfterRestart(s, hs.Gate, path)
	}
	d.reclaimRC(s)
	return true
}

func (d *desk) reclaimRC(s *deskSession) {
	if s.rcURL != "" {
		d.rememberRC(s.id, s.rcURL)
		return
	}
	d.autoRemote(s)
}

func (hs *handedSession) savePending(s *deskSession) {
	hs.Wait, hs.RunAll, hs.Sugg = s.planWait, s.runAll, s.conv.Suggestion
	hs.AutoOff = s.autoApprove || s.autoNote != ""
	hs.Gate = saveGate(s)
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind == chat.Plan {
			hs.Plan = true
		} else if it.Request != nil {
			hs.Asks = append(hs.Asks, pendingAsk{RequestID: it.RequestID, Request: it.Request})
		}
	}
}

func (hs handedSession) restorePending(s *deskSession) {
	s.planWait, s.runAll = hs.Wait, hs.RunAll
	if hs.AutoOff {
		s.autoNote = autoApproveOffNote
	}
	if hs.Restarted {
		hs.Gate.restoreRestart(s, hs.ID, hs.Handle.PID)
	} else {
		hs.Gate.restore(s)
	}
	if hs.Sugg != "" && s.conv.Suggestion == "" && !s.conv.Busy {
		s.conv.Suggestion = hs.Sugg
	}
	if len(s.conv.PendingPermissions()) > 0 {
		return
	}
	for _, a := range hs.Asks {
		s.conv.Apply(&protocol.Event{Type: "control_request", RequestID: a.RequestID, Request: a.Request})
	}
	if hs.Plan {
		s.conv.MarkPlanText()
		s.conv.AddPlan()
	}
}

func (d *desk) saveQueues(st *restoreState) {
	for id, s := range d.sessions {
		q := append([]string(nil), s.queue...)
		if e := s.qedit; e != nil {
			idx := min(e.idx, len(q))
			q = append(append(append([]string{}, q[:idx]...), e.text), q[idx:]...)
		}
		if len(q) == 0 {
			continue
		}
		if st.Queues == nil {
			st.Queues = map[string][]string{}
		}
		st.Queues[id] = q
	}
}

func (d *desk) restoreQueues(st *restoreState) {
	for id, q := range st.Queues {
		s := d.session(id)
		if s == nil || len(q) == 0 {
			continue
		}
		s.queue = append(append([]string(nil), q...), s.queue...)
		d.syncQueue(s)
		if s.running() && !s.conv.Busy {
			d.flushQueue(s)
		}
	}
}

func takeRestore() *restoreState {
	path := os.Getenv(restoreEnv)
	if path == "" {
		return nil
	}
	os.Unsetenv(restoreEnv)
	b, err := os.ReadFile(path)
	os.Remove(path)
	if err != nil {
		return nil
	}
	var st restoreState
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}

func (d *desk) applyRestore(st *restoreState) {
	d.restoreSessions(st)
	if p := st.Paused; p != nil {
		d.usagePaused, d.pauseUntil, d.pausedIDs = true, time.Unix(p.Until, 0), p.IDs
		d.showPauseBanner(p.Text)
	}
	for _, hs := range st.Handed {
		hs.Handle.FromExec = true
		s := d.session(hs.ID)
		if s == nil {
			s = &deskSession{id: hs.ID, cwd: hs.Cwd, title: hs.Title, model: hs.Model, mode: hs.Mode, conv: chat.New(), loaded: true, persisted: true, created: time.Now()}
			d.sessions[hs.ID] = s
		}
		s.model, s.mode = hs.Model, hs.Mode
		if hs.Hidden {
			d.markHidden(s) // before reattach: no tab, no Remote Control
		}
		if info, ok := d.infos[hs.ID]; ok {
			if conv, err := transcript.Load(info.Path); err == nil && conv != nil {
				s.conv, s.loaded, s.loadedMod = conv, true, info.ModTime
				d.noteWindow(s)
				if conv.Cwd != "" {
					s.cwd = conv.Cwd
				}
			}
		}
		if !d.reattach(s, hs) {
			st.Running = append(st.Running, hs.ID)
		}
	}
	for _, id := range st.Running {
		if s := d.session(id); s != nil && !s.running() && s.terminal == nil {
			if err := d.ensureProcess(s); err != nil {
				d.notify(s, "Could not resume after the update: "+err.Error())
			}
		}
	}
	d.restoreQueues(st)
	d.resumeInterrupted(st)
	if len(st.Running) > 0 || len(st.Handed) > 0 || len(st.Meta) > 0 {
		d.refreshSidebar()
	}
	if st.KmemWin {
		d.openKmemWindow()
	}
	defer d.restoreSplit(st.Split)
	if st.Session == "" {
		return
	}
	if _, ok := d.infos[st.Session]; !ok && d.sessions[st.Session] == nil {
		return
	}
	d.open(st.Session)
	// The unsent text goes back only into the box of the tab it was typed
	// in; otherwise it stays that tab's draft (kept in strip.drafts).
	draftFor := firstNonEmpty(st.DraftFor, st.Session)
	if st.Draft != "" && d.current != nil && d.current.id == draftFor && d.input.Text == "" {
		d.input.SetText(st.Draft)
		d.strip.placeCaret(d.current.id, d.input)
	}
	if st.ScrollY > 0 {
		go func() {
			time.Sleep(400 * time.Millisecond)
			d.do(func() {
				d.chat.scroll.Offset = fyne.NewPos(0, st.ScrollY)
				d.chat.scroll.Refresh()
			})
		}()
	}
}

type placedWindow interface {
	Maximized() bool
	Maximize()
	Position() (int, int)
	RequestPosition(x, y int)
}

func windowPlacement(w fyne.Window) (bool, int, int) {
	if p, ok := w.(placedWindow); ok {
		x, y := p.Position()
		return p.Maximized(), x, y
	}
	return false, 0, 0
}

func placeWindow(w fyne.Window, st *restoreState) {
	p, ok := w.(placedWindow)
	if !ok {
		return
	}
	if st.Max {
		p.Maximize()
		return
	}
	if os.Getenv("WAYLAND_DISPLAY") == "" && (st.X != 0 || st.Y != 0) {
		p.RequestPosition(st.X, st.Y)
	}
}

// swapBlockers: what holds a switch the user asked for, in words. Busy
// turns and running subagents don't: their sessions are handed over to the
// new build and keep running.
func (d *desk) swapBlockers() []string {
	var why []string
	if d.signingIn != nil {
		why = append(why, "signing in to Claude is in progress")
	}
	// Never swap while the user is typing, not even for `quill --reload`
	// (user 2026-11-21: a forced reload mid-prompt moved the draft), unless
	// they press "Reload now".
	if !d.swapNow && time.Since(d.lastTyped) < typingQuiet {
		why = append(why, "you typed in the last 15 seconds")
	}
	var taken []string
	for _, s := range d.sessions {
		if s.takeover {
			taken = append(taken, strconv.Quote(firstNonEmpty(s.title, s.id)))
		}
	}
	sort.Strings(taken)
	for _, t := range taken {
		why = append(why, t+" is being taken over from a terminal")
	}
	return why
}

// typingHolds: only typing holds the switch, which "Reload now" overrides.
// A takeover or sign-in in progress can't be handed over.
func typingHolds(why []string) bool {
	return len(why) == 1 && strings.HasPrefix(why[0], "you typed")
}

// swapWaits shows why a switch waits (t2debee: `quill --reload` waited
// without a word) and retries it when typing pauses; a takeover or sign-in
// retries it when it ends (handleEvent, refresh).
func (d *desk) swapWaits(why []string) {
	d.swapNotice = true
	text := "Quill Desktop switches to the new build when this clears: " + strings.Join(why, "; ") + "."
	if typingHolds(why) {
		d.setUpdLine(text, lineBtn("Reload now", func() {
			d.swapNow = true
			d.trySwap()
		}))
	} else {
		d.setUpdLine(text)
	}
	if left := typingQuiet - time.Since(d.lastTyped); left > 0 && d.swapRetry == nil {
		d.swapRetry = time.AfterFunc(left+100*time.Millisecond, func() {
			d.do(func() {
				d.swapRetry = nil
				d.trySwap()
			})
		})
	}
}

// Replies to `quill --reload` after "ok" (reloadStatus, reloadCLI).
const (
	reloadSwitching = "switching"
	reloadWaiting   = "waiting"
	reloadNoBuild   = "nobuild"
)

// reloadRequest: `quill --reload`. It also takes a build that waits behind
// the update line, or one the watcher hasn't seen yet (the command can come
// within its one-second settle). It returns the status line for the CLI.
func (d *desk) reloadRequest() string {
	if d.swapTo == "" && d.pendingBuild == "" {
		d.binMu.Lock()
		if b := d.bin; b != nil && b.changedAfter(0) {
			d.pendingBuild = b.path
		}
		d.binMu.Unlock()
	}
	if d.swapTo == "" && d.pendingBuild != "" {
		d.swapOK, d.swapTo = true, d.pendingBuild
	}
	d.forceSwap = true
	d.trySwap()
	return d.reloadStatus()
}

func (d *desk) reloadStatus() string {
	switch {
	case d.swapping:
		return reloadSwitching
	case d.swapTo == "":
		return reloadNoBuild + "\t" + runningExe()
	default:
		return reloadWaiting + "\t" + strings.Join(d.swapBlockers(), "; ")
	}
}

// runningExe: the program file this process was started from.
func runningExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return exe
}

// buildID identifies a program file by size and time; the running image is
// read through /proc/self/exe where there is one (it stays readable after a
// rebuild replaced the file).
func buildID(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(st.Size(), 10) + ":" + strconv.FormatInt(st.ModTime().UnixNano(), 10)
}

func runningBuildID() string {
	if id := buildID("/proc/self/exe"); id != "" {
		return id
	}
	return buildID(runningExe())
}
