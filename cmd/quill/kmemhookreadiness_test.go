package main

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestKmemHookProbeUsesNativeSessionOnce(t *testing.T) {
	h := newHarness(t)
	d := h.d
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	t.Setenv("QUILL_TEST_KMEM_HOOKS", "1")
	t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
	d.cfg.CodexRoot = t.TempDir()
	d.kmemOff = false

	var eligible, evidence atomic.Bool
	var providerThread atomic.Value
	d.kmemRun = func(_ string, args ...string) (string, error) {
		line := strings.Join(args, " ")
		switch {
		case line == "version --json":
			return `{"api":1,"kmem":"1.15.3","features":["rollout-session-guard"]}`, nil
		case line == "rollout status --json":
			return `{"api":1,"rollout":{"candidate":"1.15.3","stages":[{"name":"zomboid"}]}}`, nil
		case len(args) == 7 && line == "--as zomboid rollout status --session "+args[5]+" --json":
			providerThread.Store(args[5])
			stage := "different"
			if eligible.Load() {
				stage = "zomboid"
			}
			guards := 0
			if evidence.Load() {
				guards = 1
			}
			return fmt.Sprintf(`{"api":1,"rollout":{"session_guard":{"stage":%q,"candidate":"1.15.3","since":99,"guard":%d}}}`, stage, guards), nil
		}
		return "", nil
	}

	var s *deskSession
	p := &kmemPanel{d: d}
	h.ui(func() {
		s = d.addSessionWithBackend(h.proj, "default", "default", true, true)
		d.org.Kmem[s.id] = "zomboid"
		d.sendTo(s, "Start probe fixture")
	})
	h.eventually("Codex is idle", func() bool { return s.running() && !s.conv.Busy && !s.proc.Busy() })
	h.ui(func() { p.refreshHookReadiness() })
	h.eventually("trust status loaded", func() bool { return !p.hookReading && strings.Contains(p.hookStatus, "kmem-guard ready") })
	h.ui(func() {
		if strings.Contains(lastUser(s), hookProbeText) {
			t.Fatal("an out-of-stage session received a probe")
		}
		if raw := providerThread.Load(); raw == nil || raw == s.id {
			t.Fatalf("rollout read used Quill tab identity rather than the native provider thread")
		}
		eligible.Store(true)
		p.refreshHookReadiness()
	})
	h.eventually("eligible Codex session receives a peer probe", func() bool {
		return lastUser(s) == hookProbeText && notFromUser(lastUser(s))
	})
	evidence.Store(true)
	h.eventually("the staged rollout reports evidence", func() bool {
		return strings.Contains(p.hookProbeStatus, "recorded a successful guard evaluation")
	})
	h.ui(func() {
		evidence.Store(false) // a new refresh may observe the same scope before evidence appears
		p.refreshHookReadiness()
	})
	h.eventually("repeat hook refresh completes", func() bool { return !p.hookReading })
	h.ui(func() {
		n := 0
		for _, item := range s.conv.Items {
			if item.Text == hookProbeText {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("got %d probe turns, want 1", n)
		}
	})
}
