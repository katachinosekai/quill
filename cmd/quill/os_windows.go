//go:build windows

package main

import (
	"os"
	"os/exec"
)

// execSelf: Windows has no exec(2). Start the new binary with the restore
// file and exit; sessions that could not be handed over are resumed by the
// new process with --resume (Windows never hands over pipes).
func execSelf(target string, args, env []string) error {
	cmd := exec.Command(target, args[1:]...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

// dieWithParent: no parent-death signal on Windows; players are stopped
// explicitly when Quill stops them.
func dieWithParent(cmd *exec.Cmd) {}

// terminalCmd opens a console window running kmem's purge, where kmem asks
// the user to type the confirmation.
var terminalCmd = func(kmemPath string) *exec.Cmd {
	return exec.Command("cmd", "/c", "start", "kmem: empty the recycle bin", "cmd", "/k", kmemPath, "trash", "empty")
}

// openTerminal runs a PowerShell script in a new visible console window.
var openTerminal = func(title, script string) (*exec.Cmd, error) {
	return exec.Command("cmd", "/c", "start", title, "powershell", "-NoProfile", "-NoExit", "-Command", script), nil
}
