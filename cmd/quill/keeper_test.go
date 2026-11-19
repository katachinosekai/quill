package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
)

func TestCrashedClassification(t *testing.T) {
	exit := func(code int) *os.ProcessState {
		c := []string{"sh", "-c", "exit " + string(rune('0'+code))}
		p, _ := os.StartProcess("/bin/sh", c, &os.ProcAttr{})
		st, _ := p.Wait()
		return st
	}
	if crashed(exit(0)) {
		t.Fatal("normal quit is not a crash")
	}
	if !crashed(exit(2)) {
		t.Fatal("panic exit code is a crash")
	}
}

func TestKeeperKeepsSessionThroughCrash(t *testing.T) {
	h := newHarness(t)
	k := &keeper{held: map[string]*keptSession{}}
	sock := filepath.Join(t.TempDir(), "k.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go k.serve(l)
	h.d.keeper = &keeperClient{path: sock}

	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("first reply", func() bool {
		return s.running() && !s.conv.Busy && lastItem(s) != nil && lastItem(s).Kind == chat.Assistant
	})
	h.eventually("held by keeper", func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.held[s.id] != nil
	})
	var pid int
	h.ui(func() {
		pid = s.proc.PID()
		in, out, _ := s.proc.Keep()
		h.d.keeper = nil
		in.Close()
		out.Close()
	})
	time.Sleep(500 * time.Millisecond)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("claude must survive the window crashing")
	}

	path := k.writeRestore(false)
	if path == "" {
		t.Fatal("no restore file")
	}
	defer os.Remove(path)
	var st restoreState
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &st) != nil || len(st.Handed) != 1 || st.Handed[0].ID != s.id {
		t.Fatalf("restore: %s", b)
	}
	files := k.next
	hs := st.Handed[0]
	hs.Handle.In, hs.Handle.Out = files[0].Fd(), files[1].Fd()
	got := make(chan *protocol.Event, 64)
	p, err := session.Attach(hs.Handle, func(ev *protocol.Event) { got <- ev })
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Send("hello again"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-got:
			if ev.Type == "result" {
				k.closeAll()
				return
			}
		case <-deadline:
			t.Fatal("reconnected session did not answer")
		}
	}
}

func TestKeeperAdoptsHandover(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	st := &restoreState{Session: "s1", Draft: "half typed", Handed: []handedSession{{ID: "s1", Handle: session.Handoff{PID: os.Getpid(), In: w.Fd(), Out: r.Fd(), Leftover: []byte("{\"type\""), Busy: true, Seq: 7}}}}
	k := &keeper{held: map[string]*keptSession{}}
	k.adopt(st)
	path := k.writeRestore(true)
	defer os.Remove(path)
	var got restoreState
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &got) != nil || got.Draft != "half typed" || len(got.Handed) != 1 || len(k.next) != 2 {
		t.Fatalf("handover restore: %s", b)
	}
	h := got.Handed[0].Handle
	if h.In != 3 || h.Out != 4 || string(h.Leftover) != "{\"type\"" || !h.Busy || h.Seq != 7 {
		t.Fatalf("a live update must keep the in-flight state: %+v", h)
	}
}
