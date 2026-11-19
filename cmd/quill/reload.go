package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
)

type restoreState struct {
	Session string          `json:"session"`
	Draft   string          `json:"draft"`
	Width   float32         `json:"width"`
	Height  float32         `json:"height"`
	ScrollY float32         `json:"scrollY"`
	Hidden  bool            `json:"hidden"`
	Running []string        `json:"running"`
	Handed  []handedSession `json:"handed"`
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
}

func (d *desk) trySwap() {
	if d.swapTo == "" || d.swapping || d.signingIn != nil {
		return
	}
	if !d.forceSwap && time.Since(d.lastTyped) < typingQuiet {
		return
	}
	for _, s := range d.sessions {
		if s.takeover || s.running() && s.status() == stWaiting {
			return
		}
	}
	d.swapping = true
	d.flushUI()
	st := restoreState{Draft: d.input.Text, Hidden: d.hidden}
	if d.current != nil && d.current.persisted {
		st.Session = d.current.id
	}
	size := d.win.Canvas().Size()
	st.Width, st.Height = size.Width, size.Height
	st.ScrollY = d.chat.scroll.Offset.Y
	var handed []*deskSession
	for _, s := range d.runningSessions() {
		h, err := s.proc.Handoff()
		if err != nil {
			if s.persisted {
				st.Running = append(st.Running, s.id)
			}
			continue
		}
		st.Handed = append(st.Handed, handedSession{ID: s.id, Cwd: s.cwd, Model: s.model, Mode: s.mode, Title: s.title, RCOn: s.rcOn, RCURL: s.rcURL, Handle: h})
		handed = append(handed, s)
	}
	b, _ := json.Marshal(st)
	path := filepath.Join(os.TempDir(), fmt.Sprintf("quill-restore-%d.json", os.Getpid()))
	err := os.WriteFile(path, b, 0o600)
	if err == nil {
		env := append(os.Environ(), restoreEnv+"="+path)
		err = syscall.Exec(d.swapTo, os.Args, env)
	}
	d.swapping = false
	d.swapTo = ""
	d.forceSwap = false
	for i, s := range handed {
		d.reattach(s, st.Handed[i])
	}
	if d.current != nil {
		d.chat.update([]*chat.Item{d.current.conv.AddError("Could not switch to the new build: " + err.Error())})
	}
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
	return true
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
	for _, hs := range st.Handed {
		s := d.session(hs.ID)
		if s == nil {
			s = &deskSession{id: hs.ID, cwd: hs.Cwd, title: hs.Title, model: hs.Model, mode: hs.Mode, conv: chat.New(), loaded: true, persisted: true, created: time.Now()}
			d.sessions[hs.ID] = s
		}
		s.model, s.mode = hs.Model, hs.Mode
		if info, ok := d.infos[hs.ID]; ok {
			if conv, err := transcript.Load(info.Path); err == nil && conv != nil {
				s.conv, s.loaded, s.loadedMod = conv, true, info.ModTime
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
	if len(st.Running) > 0 || len(st.Handed) > 0 {
		d.refreshSidebar()
	}
	if st.Draft != "" {
		d.input.SetText(st.Draft)
	}
	if st.Session == "" {
		return
	}
	if _, ok := d.infos[st.Session]; !ok {
		return
	}
	d.open(st.Session)
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
