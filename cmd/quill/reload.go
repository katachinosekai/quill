package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	Paused   *pauseState         `json:"paused,omitempty"`
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

func (b *binWatch) changed() bool {
	st, err := os.Stat(b.path)
	if err != nil || st.Size() == 0 {
		return false
	}
	if st.ModTime().Equal(b.mod) && st.Size() == b.size {
		return false
	}
	if time.Since(st.ModTime()) < time.Second || st.Mode()&0o111 == 0 {
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
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if b.changed() {
				d.do(func() {
					d.swapTo = b.path
					d.trySwap()
				})
			}
		}
	}
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
	Sugg   string          `json:"suggestion,omitempty"`
}

type pendingAsk struct {
	RequestID string                   `json:"requestId"`
	Request   *protocol.ControlRequest `json:"request"`
}

// restoreHead: the current tab and the box's unsent text, tied to that tab.
// Also a brand-new tab whose session has no transcript yet: it is restored
// from Meta and must stay the current tab.
func (d *desk) restoreHead() restoreState {
	st := restoreState{Draft: d.boxDraft(d.current, d.input), Hidden: d.hidden}
	if d.current != nil {
		st.Session = d.current.id
		if !d.split() {
			st.DraftFor = d.current.id
		}
	}
	return st
}

func (d *desk) trySwap() {
	if d.swapTo == "" || d.swapping || d.signingIn != nil {
		return
	}
	// Never swap while the user is typing, not even for `quill --reload`
	// (user 2026-11-21: a forced reload mid-prompt moved the draft).
	if time.Since(d.lastTyped) < typingQuiet {
		return
	}
	for _, s := range d.sessions {
		if s.takeover {
			return
		}
	}
	d.swapping = true
	d.stopMedia()
	d.flushUI()
	st := d.restoreHead()
	if d.current != nil && !d.split() {
		d.strip.keepDraft(d.current.id, st.Draft)
	}
	for _, p := range d.panes {
		if p.view.s != nil {
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
	running := d.runningSessions()
	type meta struct {
		s  *deskSession
		hs handedSession
	}
	var metas []meta
	for _, s := range running {
		hs := handedSession{ID: s.id, Cwd: s.cwd, Model: s.model, Mode: s.mode, Title: s.title, RCOn: s.rcOn, RCURL: s.rcURL}
		hs.savePending(s)
		metas = append(metas, meta{s, hs})
	}
	target := d.swapTo
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
			err = execSelf(target, os.Args, env)
			os.Remove(path)
		}
		d.do(func() {
			d.swapping = false
			d.swapTo = ""
			d.forceSwap = false
			for _, m := range handed {
				d.reattach(m.s, m.hs)
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
