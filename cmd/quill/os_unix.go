//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

// execSelf replaces this process with the new Quill binary (live update; the
// Claude processes and their pipes carry over).
func execSelf(target string, args, env []string) error { return syscall.Exec(target, args, env) }

// dieWithParent: helper players (mpv, ffmpeg) end when Quill ends.
func dieWithParent(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }

// terminalCmd opens a terminal running kmem's purge, where kmem itself asks
// the user to type the confirmation.
var terminalCmd = func(kmemPath string) *exec.Cmd {
	script := fmt.Sprintf("%q trash empty; echo; read -r -p 'Press Enter to close this window. ' _", kmemPath)
	return exec.Command("konsole", "--title", "kmem: empty the recycle bin", "-e", "bash", "-c", script)
}
