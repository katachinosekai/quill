//go:build windows

package session

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// Windows: no live hand-over of a running session's pipes (a Quill update
// resumes sessions with --resume instead), and no SIGINT (the interrupt
// control request is the only way to stop a turn).

const handoffSupported = false

var errNoHandoff = errors.New("session: live hand-over is not supported on Windows")

func childAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// processExists: FindProcess already opened the process on Windows.
func processExists(p *os.Process) bool { return p != nil }

func interruptProcess(p *os.Process) {}

// waitForeign waits for a process that is not our child; Windows can wait on
// any process handle.
func waitForeign(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_, _ = p.Wait()
	}
}

func zombie(pid int) bool { return false }

func inheritFD(f *os.File) (uintptr, error) { return 0, errNoHandoff }

func pollableFile(fd uintptr, name string, inherited bool) *os.File { return os.NewFile(fd, name) }

// terminateGroup: Windows has no process groups to signal; kill the tree
// with taskkill (/T) so Claude's tools go too.
func terminateGroup(pid int, kill bool) {
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if kill {
		args = append(args, "/F")
	}
	_ = exec.Command("taskkill", args...).Run()
}
