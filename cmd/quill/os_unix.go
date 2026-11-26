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
// With an id it purges only that item ("kmem trash empty ID").
var terminalCmd = func(kmemPath string, id ...string) *exec.Cmd {
	args := []string{"--title", "kmem: empty the recycle bin", "-e", "bash", "-c",
		`"$@"; echo; read -r -p 'Press Enter to close this window. ' _`, "kmem", kmemPath, "trash", "empty"}
	if len(id) > 0 && validBinID(id[0]) {
		args = append(args, id[0])
	}
	return exec.Command("konsole", args...)
}

// openTerminal runs a shell script in a visible terminal window (Setup's
// installers: the user watches them and types passwords there).
var openTerminal = func(title, script string) (*exec.Cmd, error) {
	script += "; echo; read -r -p 'Press Enter to close this window. ' _"
	for _, t := range [][]string{
		{"konsole", "--title", title, "-e", "bash", "-c", script},
		{"gnome-terminal", "--title", title, "--", "bash", "-c", script},
		{"xfce4-terminal", "--title", title, "-x", "bash", "-c", script},
		{"x-terminal-emulator", "-T", title, "-e", "bash", "-c", script},
		{"xterm", "-T", title, "-e", "bash", "-c", script},
	} {
		if _, err := exec.LookPath(t[0]); err == nil {
			return exec.Command(t[0], t[1:]...), nil
		}
	}
	return nil, fmt.Errorf("no terminal program found (konsole, gnome-terminal, xfce4-terminal, xterm); run this yourself: %s", script)
}
