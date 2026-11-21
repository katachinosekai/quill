//go:build !windows

package session

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Linux and other Unix: Claude runs in its own process group, and a running
// session's pipes can be handed to a new Quill binary (live update).

const handoffSupported = true

func childAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func processExists(p *os.Process) bool { return p.Signal(syscall.Signal(0)) == nil }

func interruptProcess(p *os.Process) { _ = p.Signal(syscall.SIGINT) }

// terminateGroup signals Claude's whole process group (its tools too).
func terminateGroup(pid int, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}

func waitForeign(pid int) {
	for syscall.Kill(pid, 0) == nil {
		var ws syscall.WaitStatus
		if got, _ := syscall.Wait4(pid, &ws, syscall.WNOHANG, nil); got == pid {
			return
		}
		if zombie(pid) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func zombie(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	i := bytes.LastIndexByte(b, ')')
	return i > 0 && i+2 < len(b) && b[i+2] == 'Z'
}

func inheritFD(f *os.File) (uintptr, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, err
	}
	var fd uintptr
	var ferr error
	err = rc.Control(func(raw uintptr) {
		fd = raw
		if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, raw, syscall.F_SETFD, 0); e != 0 {
			ferr = e
		}
	})
	if err != nil {
		return 0, err
	}
	return fd, ferr
}

func pollableFile(fd uintptr, name string, inherited bool) *os.File {
	n := int(fd)
	if !inherited {
		if d, err := syscall.Dup(n); err == nil {
			n = d
		}
	}
	syscall.CloseOnExec(n)
	_ = syscall.SetNonblock(n, true)
	return os.NewFile(uintptr(n), name)
}
