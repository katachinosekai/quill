package session

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Each Claude session runs in its own systemd scope on Linux (user
// 2026-11-22). Quill itself runs as a systemd unit whose OOMPolicy is "stop":
// when the kernel killed a 10 GB game a session had started, systemd stopped
// the whole unit, Quill and every session with it. In its own scope with
// OOMPolicy=continue, an out-of-memory kill stays inside that session.
// `systemd-run --scope` moves itself into the scope and then execs the
// command, so the process stays Quill's child (same pid, same pipes) and
// hand-off, the keeper and wait4 work as before. QUILL_NO_SCOPE=1 turns it off.

var (
	scopeOnce sync.Once
	scopeOK   bool
	scopeRun  string
)

// scopeAvailable: systemd-run can start user scopes here (checked once).
var scopeAvailable = func() (string, bool) {
	scopeOnce.Do(func() {
		if runtime.GOOS != "linux" || os.Getenv("QUILL_NO_SCOPE") != "" {
			return
		}
		path, err := exec.LookPath("systemd-run")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if exec.CommandContext(ctx, path, "--user", "--scope", "--quiet", "--collect", "-p", "OOMPolicy=continue", "--", "true").Run() == nil {
			scopeRun, scopeOK = path, true
		}
	})
	return scopeRun, scopeOK
}

// scoped wraps bin+args so they run in a fresh scope (or returns them as they are).
func scoped(bin string, args []string) (string, []string) {
	run, ok := scopeAvailable()
	if !ok {
		return bin, args
	}
	// A missing binary must still fail at Start, not later inside the scope.
	if _, err := exec.LookPath(bin); err != nil {
		return bin, args
	}
	return run, append([]string{"--user", "--scope", "--quiet", "--collect", "-p", "OOMPolicy=continue", "--description=Quill: Claude session", "--", bin}, args...)
}
