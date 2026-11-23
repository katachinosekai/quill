package main

import (
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// Linux: the settings command also checks who is on the other end of
// quill.sock (SO_PEERCRED): the same user, and a process under the Quill
// operator's Claude process (docs/design/quill-operator.md §3.8).

const peerCredsSupported = true

// peerChain: the peer's pid and its ancestors' (nil: unknown, or another
// user).
func peerChain(c net.Conn) []int {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return nil
	}
	var cred *syscall.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || cerr != nil || cred == nil {
		return nil
	}
	if int(cred.Uid) != os.Getuid() {
		return nil
	}
	return pidChain(int(cred.Pid))
}

// pidChain: pid, its parent, and so on up to init (at most 64).
func pidChain(pid int) []int {
	var out []int
	for i := 0; i < 64 && pid > 1; i++ {
		out = append(out, pid)
		pid = parentPID(pid)
	}
	return out
}

// parentPID reads the ppid from /proc/PID/stat (after the command name,
// which may hold spaces and parentheses).
func parentPID(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}
