package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const samplePanic = `panic: runtime error: index out of range [3] with length 3

goroutine 1 [running]:
main.(*desk).open(0xc000123456, {0xc0001, 0x24})
	/home/someone/Projects/quill/cmd/quill/desk.go:120 +0x1f
main.main()
	/home/someone/Projects/quill/cmd/quill/main.go:40 +0x2a
token=sk-ant-abc123DEF456 user someone@example.com session 11111111-2222-3333-4444-555555555555 at 192.168.1.25
https://bob:hunter2@example.com/x
`

func TestRedactRemovesSensitiveData(t *testing.T) {
	home, _ := os.UserHomeDir()
	text := strings.ReplaceAll(samplePanic, "/home/someone", home)
	out := redact(text, redactOptions{Home: true, Paths: true, Emails: true, Sessions: true, Hosts: true, Extra: []string{"quill"}})
	for _, leak := range []string{"sk-ant-", "someone@example", "11111111-2222", "192.168.1.25", "hunter2", home} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "index out of range") || !strings.Contains(out, "desk.go:120") {
		t.Fatalf("the useful crash details must stay:\n%s", out)
	}
	if strings.Contains(redact("token: abc", redactOptions{}), "abc") {
		t.Fatal("tokens are always removed")
	}
}

func TestCrashSignatureIgnoresAddresses(t *testing.T) {
	a := crashSignature(samplePanic)
	b := crashSignature(strings.ReplaceAll(samplePanic, "0xc000123456", "0xdeadbeef"))
	if a != b || a == crashSignature("panic: something else\n") {
		t.Fatal("the same crash at different addresses has one signature")
	}
}

func TestCrashLedgerLimits(t *testing.T) {
	now := time.Now()
	l := crashLedger{Sigs: map[string]bool{}}
	if l.allowed(now) != nil {
		t.Fatal("first report allowed")
	}
	l.Sent = []int64{now.Add(-time.Hour).Unix()}
	if l.allowed(now) == nil {
		t.Fatal("at most one report every 6 hours")
	}
	l.Sent = nil
	for i := 1; i <= crashWeekMax; i++ {
		l.Sent = append(l.Sent, now.Add(-time.Duration(i)*24*time.Hour+time.Hour).Unix())
	}
	if l.allowed(now) == nil {
		t.Fatal("weekly cap")
	}
	l = crashLedger{Never: true}
	if l.allowed(now) == nil {
		t.Fatal("never means never")
	}
}

func TestTailBufferAndSaveCrash(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	tb := &tailBuffer{max: 10}
	tb.Write([]byte("0123456789abcdef"))
	if got := tb.take(); got != "6789abcdef" {
		t.Fatalf("keeps only the tail: %q", got)
	}
	saveCrash("signal: aborted", samplePanic)
	if len(pendingCrashes()) != 1 {
		t.Fatal("a crash is saved for the next start")
	}
	u := issueURL("t", strings.Repeat("x", 20000))
	if len(u.String()) > 30000 {
		t.Fatal("browser links are kept short")
	}
}

func TestCrashDialogFlow(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	h := newHarness(t)
	saveCrash("signal: aborted", samplePanic)
	h.ui(func() {
		h.d.checkCrashReports()
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("the user is asked before anything happens")
		}
	})
}

func TestAgentRedactLive(t *testing.T) {
	if os.Getenv("QUILL_AGENT_TEST") == "" {
		t.Skip("set QUILL_AGENT_TEST=1 to call the real claude CLI")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude CLI")
	}
	out, err := agentRedact(context.Background(), "claude", samplePanic, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "someone@example") || !strings.Contains(out, "index out of range") {
		t.Fatalf("agent output:\n%s", out)
	}
	t.Logf("agent output:\n%s", out)
}

func TestPreviewsRefusePrivateAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:80", "192.168.1.1:443", "10.0.0.5:80", "169.254.169.254:80", "[::1]:80", "100.64.0.1:80", "0.0.0.0:80"} {
		if publicOnly("tcp", addr, nil) == nil {
			t.Errorf("%s must be refused", addr)
		}
	}
	if publicOnly("tcp", "140.82.112.3:443", nil) != nil {
		t.Error("public addresses are allowed")
	}
}

func TestPrivateFilesAreNotPredictable(t *testing.T) {
	a, err := writePrivate("quill-restore-*.json", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(a)
	b, _ := writePrivate("quill-restore-*.json", []byte("{}"))
	defer os.Remove(b)
	st, _ := os.Stat(a)
	if a == b || st.Mode().Perm() != 0o600 || strings.HasPrefix(a, "/tmp/quill-restore-") {
		t.Fatalf("restore files must be private and unguessable: %s %v", a, st.Mode())
	}
}
