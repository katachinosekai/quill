package session

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A process in its own scope can be OOM-killed (here: SIGKILL) without
// touching anything else, and it stays our child with the same pid.
func TestScopedChild(t *testing.T) {
	run, ok := scopeAvailable()
	if !ok {
		t.Skip("no systemd user scopes here")
	}
	bin, args := scoped("sleep", []string{"30"})
	if bin != run || args[len(args)-2] != "sleep" || !strings.Contains(strings.Join(args, " "), "OOMPolicy=continue") {
		t.Fatalf("wrapped command: %s %v", bin, args)
	}
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// systemd-run execs in place: the same pid becomes sleep.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile("/proc/" + itoa(cmd.Process.Pid) + "/comm"); strings.TrimSpace(string(b)) == "sleep" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cg, _ := os.ReadFile("/proc/" + itoa(cmd.Process.Pid) + "/cgroup")
	if !strings.Contains(string(cg), "run-") || !strings.Contains(string(cg), ".scope") {
		t.Fatalf("the child is in its own scope: %s", cg)
	}
	mine, _ := os.ReadFile("/proc/self/cgroup")
	if string(mine) == string(cg) {
		t.Fatal("the child left our cgroup")
	}
	cmd.Process.Kill()
	if err := cmd.Wait(); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("we still wait for our child: %v", err)
	}
}

func itoa(n int) string {
	s := ""
	for {
		s = string(rune('0'+n%10)) + s
		n /= 10
		if n == 0 {
			return s
		}
	}
}
