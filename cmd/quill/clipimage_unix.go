//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// runClipTool feeds the PNG to wl-copy / xclip. Both fork a background
// child that keeps serving the selection after the command returns, so the
// tool runs in its own session (no Pdeathsig: the copied image must outlive
// Quill Desktop) with no output pipes we would wait on.
func runClipTool(argv []string, data []byte) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(data)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s: %w", argv[0], err)
		}
		return nil
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		return fmt.Errorf("%s did not finish", argv[0])
	}
}
