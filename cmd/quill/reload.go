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
)

const restoreEnv = "QUILL_RESTORE"

type restoreState struct {
	Session string  `json:"session"`
	Draft   string  `json:"draft"`
	Width   float32 `json:"width"`
	Height  float32 `json:"height"`
	ScrollY float32 `json:"scrollY"`
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

func (d *desk) trySwap() {
	if d.swapTo == "" || d.swapping {
		return
	}
	for _, s := range d.runningSessions() {
		if st := s.status(); st == stRunning || st == stWaiting {
			return
		}
	}
	for _, s := range d.sessions {
		if s.takeover {
			return
		}
	}
	d.swapping = true
	st := restoreState{Draft: d.input.Text}
	if d.current != nil && d.current.persisted {
		st.Session = d.current.id
	}
	size := d.win.Canvas().Size()
	st.Width, st.Height = size.Width, size.Height
	st.ScrollY = d.chat.scroll.Offset.Y
	b, _ := json.Marshal(st)
	path := filepath.Join(os.TempDir(), fmt.Sprintf("quill-restore-%d.json", os.Getpid()))
	if os.WriteFile(path, b, 0o600) != nil {
		d.swapping = false
		return
	}
	running := d.runningSessions()
	target := d.swapTo
	go func() {
		d.stopAll(running)
		env := append(os.Environ(), restoreEnv+"="+path)
		err := syscall.Exec(target, os.Args, env)
		d.do(func() {
			d.swapping = false
			d.swapTo = ""
			if err != nil && d.current != nil {
				d.chat.update([]*chat.Item{d.current.conv.AddError("Could not switch to the new build: " + err.Error())})
			}
		})
	}()
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
