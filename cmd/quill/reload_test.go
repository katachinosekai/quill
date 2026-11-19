package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
)

func TestBinWatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "quill")
	old := time.Now().Add(-time.Minute)
	os.WriteFile(p, []byte("\x7fELFold"), 0o755)
	os.Chtimes(p, old, old)
	st, _ := os.Stat(p)
	b := &binWatch{path: p, mod: st.ModTime(), size: st.Size()}
	if b.changed() {
		t.Fatal("unchanged file reported")
	}
	os.WriteFile(p, []byte("\x7fELFnewer"), 0o755)
	if b.changed() {
		t.Fatal("must wait until the file settles")
	}
	os.Chtimes(p, old.Add(time.Second), old.Add(time.Second))
	os.WriteFile(p+".x", nil, 0o644)
	if !b.changed() || b.changed() {
		t.Fatal("settled new build should be reported once")
	}
	os.WriteFile(p, []byte("#!/bin/sh"), 0o755)
	os.Chtimes(p, old, old)
	if b.changed() {
		t.Fatal("non-ELF must be ignored")
	}
}

func TestTakeRestore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`{"session":"abc","draft":"hi","width":900,"height":700,"scrollY":40}`), 0o600)
	t.Setenv(restoreEnv, p)
	st := takeRestore()
	if st == nil || st.Session != "abc" || st.Draft != "hi" || st.Width != 900 || st.ScrollY != 40 {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(p); err == nil || os.Getenv(restoreEnv) != "" {
		t.Fatal("restore file and env should be consumed")
	}
}

func TestSingleInstance(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "q.sock")
	if signalRunning(sock) {
		t.Fatal("nothing is listening yet")
	}
	h := newHarness(t)
	shown := make(chan bool, 1)
	h.d.do = func(f func()) {
		uiMu.Lock()
		defer uiMu.Unlock()
		f()
		shown <- true
	}
	h.ui(func() { h.d.hidden = true })
	h.d.listenInstance(sock)
	if !signalRunning(sock) {
		t.Fatal("running instance should answer")
	}
	<-shown
	h.ui(func() {
		if h.d.hidden {
			t.Fatal("window should be shown")
		}
	})
}

func TestCloseHidesInBackground(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.background = true
		h.d.confirmClose()
		if !h.d.hidden {
			t.Fatal("closing should hide the window when running in the background")
		}
		h.d.showWindow()
		if h.d.hidden {
			t.Fatal("show should bring it back")
		}
	})
}

func TestSwapWaitsWhileTyping(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.swapTo = "/nonexistent/quill"
		h.d.input.SetText("half a thought")
		h.d.trySwap()
		if h.d.swapping {
			t.Fatal("must not swap while the user is typing")
		}
		h.d.swapTo = ""
	})
}

func TestLateSendKeepsNewTyping(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.input.SetText("typed meanwhile")
		h.d.sendTo(s, "earlier message")
		if h.d.input.Text != "typed meanwhile" {
			t.Fatalf("input wiped: %q", h.d.input.Text)
		}
	})
}

func TestRestoreResumesRunningSessions(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.applyRestore(&restoreState{Running: []string{editsID}}) })
	h.eventually("resumed after update", func() bool { s := h.d.sessions[editsID]; return s != nil && s.running() })
	h.eventually("resumed with --resume", func() bool { return strings.Contains(h.args(), "--resume "+editsID) })
}

func TestLiveIndicatorUnderChat(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("indicator", func() bool { return strings.TrimSpace(h.d.chat.live.Text) != "" && h.d.chat.live.Visible() })
	h.ui(func() { h.d.interrupt(s) })
	h.eventually("indicator clears", func() bool { return strings.TrimSpace(h.d.chat.live.Text) == "" })
}

func TestReloadCommandForcesSwap(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "q.sock")
	h := newHarness(t)
	done := make(chan bool, 4)
	h.d.do = func(f func()) {
		uiMu.Lock()
		defer uiMu.Unlock()
		f()
		done <- true
	}
	h.d.listenInstance(sock)
	h.ui(func() { h.d.lastTyped = time.Now() })
	if !sendCommand(sock, "reload") {
		t.Fatal("reload not acknowledged")
	}
	<-done
	h.ui(func() {
		if !h.d.forceSwap {
			t.Fatal("reload should skip the typing wait")
		}
	})
}

func TestTextAlignSwitch(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.alignSel.SetSelected("Left")
		if h.d.center || h.d.chat.align() != fyne.TextAlignLeading {
			t.Fatal("left alignment")
		}
		h.d.alignSel.SetSelected("Center")
		if !h.d.center || h.d.chat.align() != fyne.TextAlignCenter {
			t.Fatal("center alignment")
		}
	})
}

func TestSwapHandsOffAndReattachesOnFailure(t *testing.T) {
	h := newHarness(t)
	h.d.do = func(f func()) {
		go func() {
			uiMu.Lock()
			defer uiMu.Unlock()
			f()
		}()
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("streaming", func() bool { return s.running() && s.proc.Busy() })
	var before *session.Process
	h.ui(func() {
		before = s.proc
		h.d.swapTo = "/nonexistent/quill"
		h.d.forceSwap = true
		h.d.trySwap()
	})
	h.eventually("re-attached after a failed switch", func() bool { return !h.d.swapping && s.proc != before && s.running() })
	h.ui(func() {
		if s.proc.PID() != before.PID() {
			t.Fatal("the same Claude process must keep running")
		}
		h.d.interrupt(s)
	})
	h.eventually("turn ends on the re-attached process", func() bool { return !s.proc.Busy() && !s.conv.Busy })
	h.ui(func() { h.d.send("hello") })
	h.eventually("next reply", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "hello")
	})
}
