package main

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestRemoteActivityKeepsConnection(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		conf := remotePCConf{ID: "a1b2c3d4", Name: "Office PC", Hosts: []string{"192.0.2.10"}, Port: 1, Key: "k", FP: strings.Repeat("ab", 32)}
		l := newRemoteLink(h.d, conf) // not connected: touches fail quietly
		s := &deskSession{id: remoteLocalID(conf.ID, "x"), conv: chat.New(), loaded: true, remote: &remoteSess{link: l, rid: "x"}}
		h.d.sessions[s.id] = s
		h.d.addTab(s.id)
		h.d.open(s.id)
		old := func() {
			l.mu.Lock()
			l.lastUser, l.lastTouch = time.Now().Add(-time.Hour), time.Time{}
			l.mu.Unlock()
		}
		fresh := func(what string) {
			t.Helper()
			if l.idleFor() > time.Minute {
				t.Errorf("%s counts as activity", what)
			}
		}
		old()
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
		fresh("an arrow key in the message box")
		old()
		h.d.chat.scroll.OnScrolled(fyne.NewPos(0, 10))
		fresh("scrolling the chat")
		old()
		h.d.chat.touched()
		fresh("a click in the chat")
		old()
		h.d.chat.autoScrolling = true
		h.d.chat.scroll.OnScrolled(fyne.NewPos(0, 20))
		h.d.chat.autoScrolling = false
		if l.idleFor() < time.Minute {
			t.Error("Quill's own scrolling (new output) doesn't count")
		}
		h.d.closeTab(s.id)
		delete(h.d.sessions, s.id)
	})
}
