//go:build windows

package live

import (
	"os"
	"os/exec"
	"strconv"
)

// alive: on Windows FindProcess opens the process and fails when it is gone.
// The start-time check of the Linux version (/proc stat) is not available.
func (f *Finder) alive(p Process) bool {
	proc, err := os.FindProcess(p.PID)
	if err != nil {
		return false
	}
	_ = proc.Release()
	return true
}

func signalPID(pid int, kill bool) {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if kill {
		args = append(args, "/F")
	}
	_ = exec.Command("taskkill", args...).Run()
}
