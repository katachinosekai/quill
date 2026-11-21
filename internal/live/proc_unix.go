//go:build !windows

package live

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func (f *Finder) alive(p Process) bool {
	stat, err := os.ReadFile(filepath.Join(f.ProcDir, strconv.Itoa(p.PID), "stat"))
	if err != nil {
		return false
	}
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return false
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return false
	}
	if fields[0] == "Z" {
		return false
	}
	if p.ProcStart != "" && fields[19] != p.ProcStart {
		return false
	}
	return true
}

func signalPID(pid int, kill bool) {
	sig := syscall.SIGTERM
	if kill {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(pid, sig)
}
