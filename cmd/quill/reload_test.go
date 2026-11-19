package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
