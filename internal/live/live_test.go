package live

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunning(t *testing.T) {
	sess, proc := t.TempDir(), t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o600)
	}
	stat := func(pid, state, start string) string {
		return pid + " (claude (x)) " + state + " 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 " + start + " 99"
	}
	write(filepath.Join(sess, "10.json"), `{"pid":10,"sessionId":"live","cwd":"/a","kind":"interactive","procStart":"500"}`)
	write(filepath.Join(proc, "10", "stat"), stat("10", "S", "500"))
	write(filepath.Join(sess, "11.json"), `{"pid":11,"sessionId":"reused","procStart":"500"}`)
	write(filepath.Join(proc, "11", "stat"), stat("11", "S", "777"))
	write(filepath.Join(sess, "12.json"), `{"pid":12,"sessionId":"gone"}`)
	write(filepath.Join(sess, "13.json"), `{"pid":13,"sessionId":"mine"}`)
	write(filepath.Join(proc, "13", "stat"), stat("13", "S", "1"))
	write(filepath.Join(sess, "14.json"), `{"pid":14,"sessionId":"zombie"}`)
	write(filepath.Join(proc, "14", "stat"), stat("14", "Z", "1"))
	f := &Finder{Dir: sess, ProcDir: proc}
	got := f.Running(map[int]bool{13: true})
	if len(got) != 1 || got["live"].Cwd != "/a" {
		t.Fatalf("%+v", got)
	}
}

func TestRelease(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	f := &Finder{ProcDir: "/proc"}
	b, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(cmd.Process.Pid), "stat"))
	s := string(b)
	start := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])[19]
	p := Process{PID: cmd.Process.Pid, SessionID: "x", Kind: "interactive", ProcStart: start}
	if !f.alive(p) {
		t.Fatal("should be alive")
	}
	if err := f.Release(p, "", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if f.alive(p) {
		t.Fatal("still alive")
	}
	if err := f.Release(Process{PID: cmd.Process.Pid, ProcStart: "1"}, "", time.Second); err != nil {
		t.Fatal(err)
	}
}
