package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/katachinosekai/quill/internal/session"
)

func TestSessionRestartKeepsTabTranscriptAndDraft(t *testing.T) {
	for _, codex := range []bool{false, true} {
		t.Run(fmt.Sprintf("codex=%t", codex), func(t *testing.T) {
			h := newHarness(t)
			d := h.d
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("QUILL_CODEX_BIN", self)
			t.Setenv("QUILL_HANDOFF_FAKE", "1")
			t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
			d.cfg.CodexRoot = filepath.Join(t.TempDir(), "codex")
			d.kmemOff = false
			var saved atomic.Bool
			d.kmemRun = func(_ string, args ...string) (string, error) {
				switch {
				case len(args) >= 3 && args[0] == "handoff" && args[1] == "get":
					if saved.Load() {
						return "Fresh fixture handoff", nil
					}
					return "Previous handoff", nil
				case len(args) == 8 && args[1] == "continuation" && args[2] == "handoff" && args[3] == "save":
					if strings.Contains(args[7], "verify the result") {
						saved.Store(true)
						return "saved", nil
					}
				}
				return "", errors.New("unexpected kmem call")
			}
			var s *deskSession
			var old *session.Process
			h.ui(func() {
				s = d.addSessionWithBackend(h.proj, "default", "default", true, codex)
				d.org.Kmem[s.id] = "continuation"
				d.sendTo(s, "Keep this conversation")
			})
			h.eventually("first turn settled", func() bool {
				return s.running() && sessionRestartBlocker(s) == ""
			})
			h.ui(func() {
				old = s.proc
				d.input.SetText("Unsent draft")
				if codex {
					reply := d.requestSessionRestart(s, sessionRestartRequest{})
					if !strings.Contains(reply.Error, "--from-handoff") {
						t.Fatalf("restart without handoff consent: %+v", reply)
					}
				}
				reply := d.requestSessionRestart(s, sessionRestartRequest{FromHandoff: true})
				if reply.Status != "Restart requested." {
					t.Fatalf("restart: %+v", reply)
				}
			})
			h.eventually("replacement process started", func() bool {
				return s.running() && s.proc != old && (!codex || strings.Contains(lastUser(s), "Fresh fixture handoff"))
			})
			h.ui(func() {
				if !old.Exited() || d.current != s || len(d.tabs) != 1 || d.tabs[0] != s.id || s.codex != codex {
					t.Fatal("restart changed the tab or backend, or left the old process running")
				}
				if d.input.Text != "Unsent draft" || d.org.Kmem[s.id] != "continuation" {
					t.Fatal("restart lost the draft or topic")
				}
				for _, item := range s.conv.Items {
					if item.Text == "Keep this conversation" {
						return
					}
				}
				t.Fatal("restart lost the transcript")
			})
		})
	}
}

func TestSessionRestartCanCancelWhileApprovalWaits(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		s := d.createSession(h.proj, "default", "default")
		if err := d.ensureProcess(s); err != nil {
			t.Fatal(err)
		}
		old := s.proc
		s.planWait = true
		if r := d.requestSessionRestart(s, sessionRestartRequest{}); !s.restartPending || !strings.Contains(r.Status, "approval") {
			t.Fatalf("restart did not wait: %+v", r)
		}
		d.pollSessionRestarts()
		if r := d.requestSessionRestart(s, sessionRestartRequest{Cancel: true}); r.Error != "" || s.restartPending || s.proc != old {
			t.Fatalf("cancel waiting restart: %+v", r)
		}
		s.planWait = false
		d.pollSessionRestarts()
		if s.proc != old || s.restartActive {
			t.Fatal("cancelled restart still ran")
		}
	})
}
