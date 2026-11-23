//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/katachinosekai/quill/internal/session"
)

const (
	keeperEnv      = "QUILL_KEEPER"
	keeperMaxDelay = 30 * time.Second
	keeperStable   = 2 * time.Minute
)

type keeperMsg struct {
	Op      string          `json:"op"`
	ID      string          `json:"id,omitempty"`
	Session handedSession   `json:"session"`
	View    string          `json:"view,omitempty"`
	Hidden  bool            `json:"hidden,omitempty"`
	Handle  session.Handoff `json:"handle"`
}

type keptSession struct {
	hs      handedSession
	in, out *os.File
}

type keeper struct {
	base   restoreState
	next   []*os.File
	mu     sync.Mutex
	held   map[string]*keptSession
	order  []string
	view   string
	hidden bool
}

func keeperSocket() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, fmt.Sprintf("quill-keeper-%d.sock", os.Getpid()))
}

func crashed(state *os.ProcessState) bool {
	if state == nil {
		return true
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		switch ws.Signal() {
		case syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP:
			return false
		}
		return true
	}
	return state.ExitCode() != 0
}

func (k *keeper) adopt(st *restoreState) {
	for _, hs := range st.Handed {
		in, out := int(hs.Handle.In), int(hs.Handle.Out)
		syscall.CloseOnExec(in)
		syscall.CloseOnExec(out)
		k.held[hs.ID] = &keptSession{hs: hs, in: os.NewFile(uintptr(in), "kept-in"), out: os.NewFile(uintptr(out), "kept-out")}
		k.order = append(k.order, hs.ID)
	}
	k.base = *st
	k.base.Handed = nil
	k.view, k.hidden = st.Session, st.Hidden
}

func fallbackWindow(k *keeper, initial *restoreState) int {
	defer runtime.KeepAlive(k)
	if initial != nil {
		if b, err := json.Marshal(initial); err == nil {
			if path, err := writePrivate("quill-restore-*.json", b); err == nil {
				os.Setenv(restoreEnv, path)
			}
		}
	}
	return runWindow()
}

func runKeeper(args []string, initial *restoreState) int {
	k := &keeper{held: map[string]*keptSession{}}
	fresh := false
	if initial != nil {
		k.adopt(initial)
		fresh = true
	}
	sock := keeperSocket()
	_ = os.Remove(sock)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return fallbackWindow(k, initial)
	}
	_ = os.Chmod(sock, 0o600)
	defer os.Remove(sock)
	go k.serve(l)
	go k.reap()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	exe, err := os.Executable()
	if err != nil {
		l.Close()
		return fallbackWindow(k, initial)
	}
	delay := 500 * time.Millisecond
	restore := ""
	if fresh {
		restore = k.writeRestore(true)
	}
	tail := &tailBuffer{max: crashTailBytes}
	for {
		cmd := exec.Command(exe, args...)
		cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, tail)
		cmd.Env = append(os.Environ(), keeperEnv+"="+sock)
		if restore != "" {
			cmd.Env = append(cmd.Env, restoreEnv+"="+restore)
			cmd.ExtraFiles = k.next
		}
		started := time.Now()
		if err := cmd.Start(); err != nil {
			if restore != "" {
				os.Remove(restore)
			}
			if fresh {
				l.Close()
				return fallbackWindow(k, initial)
			}
			time.Sleep(delay)
			delay = min(delay*2, keeperMaxDelay)
			restore = k.writeRestore(false)
			continue
		}
		fresh = false
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case s := <-sigs:
			_ = cmd.Process.Signal(s)
			<-done
			k.closeAll()
			return 0
		}
		if restore != "" {
			os.Remove(restore)
			restore = ""
		}
		output := tail.take()
		if !crashed(cmd.ProcessState) {
			k.closeAll()
			return 0
		}
		saveCrash(cmd.ProcessState.String(), output)
		if time.Since(started) > keeperStable {
			delay = 500 * time.Millisecond
		}
		time.Sleep(delay)
		delay = min(delay*2, keeperMaxDelay)
		restore = k.writeRestore(false)
	}
}

func (k *keeper) reap() {
	seen := map[int]bool{}
	for {
		time.Sleep(2 * time.Second)
		k.mu.Lock()
		for _, ks := range k.held {
			seen[ks.hs.Handle.PID] = true
		}
		k.mu.Unlock()
		for pid := range seen {
			var ws syscall.WaitStatus
			got, err := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil)
			if got == pid || err != nil {
				delete(seen, pid)
			}
		}
	}
}

func (k *keeper) serve(l *net.UnixListener) {
	for {
		c, err := l.AcceptUnix()
		if err != nil {
			return
		}
		go k.handle(c)
	}
}

func (k *keeper) handle(c *net.UnixConn) {
	defer c.Close()
	buf := make([]byte, 1<<20)
	oob := make([]byte, syscall.CmsgSpace(2*4))
	n, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
	if err != nil {
		return
	}
	var files []*os.File
	if oobn > 0 {
		if msgs, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil {
			for _, m := range msgs {
				if fds, err := syscall.ParseUnixRights(&m); err == nil {
					for _, fd := range fds {
						syscall.CloseOnExec(fd)
						files = append(files, os.NewFile(uintptr(fd), "kept"))
					}
				}
			}
		}
	}
	var m keeperMsg
	if json.Unmarshal(buf[:n], &m) != nil {
		for _, f := range files {
			f.Close()
		}
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	switch m.Op {
	case "hold":
		if len(files) != 2 {
			for _, f := range files {
				f.Close()
			}
			return
		}
		k.dropLocked(m.Session.ID)
		hs := m.Session
		hs.Handle = m.Handle
		k.held[hs.ID] = &keptSession{hs: hs, in: files[0], out: files[1]}
		k.order = append(k.order, hs.ID)
	case "drop":
		k.dropLocked(m.ID)
	case "view":
		k.view, k.hidden = m.View, m.Hidden
	}
	_, _ = c.Write([]byte("ok"))
}

func (k *keeper) dropLocked(id string) {
	ks, ok := k.held[id]
	if !ok {
		return
	}
	ks.in.Close()
	ks.out.Close()
	delete(k.held, id)
	for i, x := range k.order {
		if x == id {
			k.order = append(k.order[:i], k.order[i+1:]...)
			break
		}
	}
}

func (k *keeper) alive() []*keptSession {
	var out []*keptSession
	for _, id := range append([]string(nil), k.order...) {
		ks := k.held[id]
		if syscall.Kill(ks.hs.Handle.PID, 0) != nil || zombiePID(ks.hs.Handle.PID) {
			k.dropLocked(id)
			continue
		}
		out = append(out, ks)
	}
	return out
}

func zombiePID(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	for i := len(b) - 1; i > 0; i-- {
		if b[i] == ')' {
			return i+2 < len(b) && b[i+2] == 'Z'
		}
	}
	return false
}

func (k *keeper) files() []*os.File {
	k.mu.Lock()
	defer k.mu.Unlock()
	var fs []*os.File
	for _, ks := range k.alive() {
		fs = append(fs, ks.in, ks.out)
	}
	return fs
}

func (k *keeper) writeRestore(handover bool) string {
	k.mu.Lock()
	st := restoreState{Session: k.view, Hidden: k.hidden}
	if handover {
		st = k.base
	}
	k.next = nil
	for i, ks := range k.alive() {
		k.next = append(k.next, ks.in, ks.out)
		hs := ks.hs
		hs.Handle.In = uintptr(3 + 2*i)
		hs.Handle.Out = uintptr(4 + 2*i)
		if !handover {
			hs.Restarted = true // planscope.go: only the subagents' grants survive
			hs.Handle.Leftover = nil
			hs.Handle.Busy = false
			hs.Handle.Seq += 1000
		}
		st.Handed = append(st.Handed, hs)
	}
	k.mu.Unlock()
	b, _ := json.Marshal(st)
	path, err := writePrivate("quill-crash-restore-*.json", b)
	if err != nil {
		return ""
	}
	return path
}

func (k *keeper) closeAll() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id := range k.held {
		k.dropLocked(id)
	}
}

// keeperClient talks to the keeper. Hold and drop messages go out in order
// (async): an older snapshot of a session must never overwrite a newer one,
// e.g. the plan's grants after a Stop revoked them.
type keeperClient struct {
	path    string
	mu      sync.Mutex
	pending []func()
	running bool
}

func (kc *keeperClient) async(f func()) {
	kc.mu.Lock()
	kc.pending = append(kc.pending, f)
	if kc.running {
		kc.mu.Unlock()
		return
	}
	kc.running = true
	kc.mu.Unlock()
	go func() {
		for {
			kc.mu.Lock()
			if len(kc.pending) == 0 {
				kc.running = false
				kc.mu.Unlock()
				return
			}
			next := kc.pending[0]
			kc.pending = kc.pending[1:]
			kc.mu.Unlock()
			next()
		}
	}()
}

func newKeeperClient() *keeperClient {
	p := os.Getenv(keeperEnv)
	if p == "" {
		return nil
	}
	return &keeperClient{path: p}
}

func (kc *keeperClient) send(m keeperMsg, files ...*os.File) error {
	if kc == nil {
		return nil
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: kc.path, Net: "unix"})
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	b, _ := json.Marshal(m)
	var oob []byte
	if len(files) > 0 {
		var fds []int
		for _, f := range files {
			rc, err := f.SyscallConn()
			if err != nil {
				return err
			}
			_ = rc.Control(func(fd uintptr) { fds = append(fds, int(fd)) })
		}
		oob = syscall.UnixRights(fds...)
	}
	if _, _, err := c.WriteMsgUnix(b, oob, nil); err != nil {
		return err
	}
	ack := make([]byte, 2)
	if _, err := c.Read(ack); err != nil {
		return errors.New("keeper did not answer")
	}
	return nil
}

func (d *desk) keepSession(s *deskSession) {
	if d.keeper == nil || s.proc == nil {
		return
	}
	in, out, h := s.proc.Keep()
	hs := handedSession{ID: s.id, Cwd: s.cwd, Model: s.model, Mode: s.mode, Title: s.title, RCOn: s.rcOn, RCURL: s.rcURL}
	hs.Hidden, hs.Role = handedRole(s)
	hs.savePending(s)
	s.keptGate = markGate(s)
	kc := d.keeper
	kc.async(func() { _ = kc.send(keeperMsg{Op: "hold", Session: hs, Handle: h}, in, out) })
}

func (d *desk) unkeepSession(id string) {
	if d.keeper == nil {
		return
	}
	kc := d.keeper
	kc.async(func() { _ = kc.send(keeperMsg{Op: "drop", ID: id}) })
}

func (d *desk) keepView() {
	if d.keeper == nil {
		return
	}
	view := ""
	if d.current != nil && d.current.persisted {
		view = d.current.id
	}
	if view == d.keptView && d.hidden == d.keptHidden {
		return
	}
	d.keptView, d.keptHidden = view, d.hidden
	kc, hidden := d.keeper, d.hidden
	go func() { _ = kc.send(keeperMsg{Op: "view", View: view, Hidden: hidden}) }()
}
