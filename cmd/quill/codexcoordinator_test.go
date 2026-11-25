package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/codex"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
)

func TestCodexCoordinatorFollowsQuillPreference(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	h.d.cfg.CodexRoot = t.TempDir()
	h.ui(func() {
		d := h.d
		d.app.Preferences().SetString(prefQuillSource, h.proj)
		d.app.Preferences().SetBool(prefBuildGate, true)
		for _, role := range []string{"", "topic", roleCodexQuill, roleCodexKmem} {
			s := d.addSessionWithBackend(h.proj, "default", "default", false, true)
			if role == "topic" {
				d.org.Kmem[s.id] = "fixture-topic"
			} else if role != "" {
				d.markHiddenAs(s, role)
			}
			for _, enabled := range []bool{true, false} {
				d.app.Preferences().SetBool(prefCoordinator, enabled)
				opts, err := d.codexProcessOptions(s)
				if err != nil {
					t.Fatal(err)
				}
				var bridge codex.Options
				if err := json.Unmarshal([]byte(opts.BridgeArgs[1]), &bridge); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(bridge.Instructions, screenshotReplyHint) {
					t.Fatalf("role %q: missing shared screenshot guidance", role)
				}
				if bridge.Coordinate != (enabled && role != roleCodexKmem) {
					t.Fatalf("role %q: unexpected native coordination switch %v", role, bridge.Coordinate)
				}
				if got, want := strings.Contains(bridge.Instructions, codexCoordinatorHint), enabled && role != roleCodexKmem; got != want {
					t.Fatalf("role %q enabled=%v: coordinator got %v want %v", role, enabled, got, want)
				}
				if got, want := strings.Contains(bridge.Instructions, codexBuildGateHint), role != roleCodexKmem; got != want {
					t.Fatalf("role %q: unexpected build gate %v", role, got)
				}
			}
		}
	})
}

func TestCodexOperatorSendsWhileWorkingAndQueuesDuringApproval(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	h.d.cfg.CodexRoot = t.TempDir()
	var s *deskSession
	h.ui(func() {
		d := h.d
		d.openQuillCodex()
		a := d.assistByRole(roleCodexQuill)
		s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(s, roleCodexQuill)
		a.s, a.state = s, asLive
		d.sendTo(s, "Initial turn")
	})
	h.eventually("idle parent", func() bool { return s.running() && !s.conv.Busy && !s.proc.Busy() })
	h.ui(func() {
		d := h.d
		s.agentGrants = map[string]agentGrant{"worker": {Background: true}}
		if s.shouldQueue() {
			t.Fatal("a detached worker alone must not queue a parent message")
		}
		d.handleEvent(s, &protocol.Event{Type: "system", Subtype: protocol.CodexParentTurnStarted})
		if !s.steerCodexMessage() {
			t.Fatal("the working operator should use bridge steering")
		}
		d.sendTargeted(d.input, "Design while the coordinator works", false)
		if len(s.queue) != 0 || lastUser(s) != "Design while the coordinator works" {
			t.Fatal("normal Send should reach the working operator without a queued row")
		}
		d.handleEvent(s, &protocol.Event{
			Type: "control_request", RequestID: "ask",
			Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: "Codex command"},
		})
		d.sendOrQueue(s, "Send once approval clears")
		d.sendQueuedNow(s, 0)
		if len(s.queue) != 1 || lastUser(s) != "Design while the coordinator works" {
			t.Fatal("messages should stay queued while a native approval is open")
		}
		d.handleEvent(s, &protocol.Event{Type: "control_cancel_request", RequestID: "ask"})
		if len(s.queue) != 0 || lastUser(s) != "Send once approval clears" {
			t.Fatal("clearing a native approval should deliver to the working operator")
		}
	})
}

func TestCodexOperatorUpgradesIdleBridgeInPlace(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	h.d.cfg.CodexRoot = t.TempDir()
	var s *deskSession
	h.ui(func() {
		d := h.d
		d.openQuillCodex()
		a := d.assistByRole(roleCodexQuill)
		s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(s, roleCodexQuill)
		a.s, a.state = s, asLive

		opts, err := d.codexProcessOptions(s)
		if err != nil {
			t.Fatal(err)
		}
		var bridge codex.Options
		if err := json.Unmarshal([]byte(opts.BridgeArgs[1]), &bridge); err != nil {
			t.Fatal(err)
		}
		bridge.ForceSteer = false
		encoded, err := json.Marshal(bridge)
		if err != nil {
			t.Fatal(err)
		}
		opts.BridgeArgs[1] = string(encoded)
		old, err := session.Start(opts, func(*protocol.Event) {})
		if err != nil {
			t.Fatal(err)
		}
		d.adopt(s, old)
		previous := s.conv.AddUser("Earlier message")
		s.queue = []string{"Queued message"}
		d.strip.keepDraft(assistDraftKey(roleCodexQuill), "Unsent draft")

		s.conv.Busy = true
		d.refreshLegacyCodexBridge(s)
		if s.proc != old {
			t.Fatal("an active turn must keep its bridge")
		}
		s.conv.Busy = false
		s.conv.AddPlan()
		d.refreshLegacyCodexBridge(s)
		if s.proc != old {
			t.Fatal("a pending approval must keep its bridge")
		}

		s.conv.CancelPlans()
		d.refreshLegacyCodexBridge(s)
		var updated codex.Options
		if s.proc == old || !old.Exited() || !s.running() ||
			json.Unmarshal([]byte(s.proc.Options().BridgeArgs[1]), &updated) != nil || !updated.ForceSteer {
			t.Fatal("the idle operator should switch to the new bridge")
		}
		d.flushQueue(s)
		if a.s != s || d.kmemTopicFor(s) != quillOpName || !s.codexUpgrading ||
			s.conv.Items[0] != previous || len(s.queue) != 1 || s.queue[0] != "Queued message" ||
			d.strip.draft(assistDraftKey(roleCodexQuill)) != "Unsent draft" {
			t.Fatal("upgrading should preserve the operator, conversation, queue, and draft")
		}
	})
	h.eventually("fresh bridge has started and accepted the queue", func() bool {
		return !s.codexUpgrading && len(s.queue) == 0 && lastUser(s) == "Queued message"
	})
}

func TestCodexRegularAndTopicBridgesWaitForWorkersBeforeRefresh(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	h.d.cfg.CodexRoot = t.TempDir()
	for _, topic := range []string{"", "fixture-topic"} {
		h.ui(func() {
			d := h.d
			s := d.addSessionWithBackend(h.proj, "default", "default", false, true)
			if topic != "" {
				d.org.Kmem[s.id] = topic
			}
			opts, err := d.codexProcessOptions(s)
			if err != nil {
				t.Fatal(err)
			}
			var old codex.Options
			if err := json.Unmarshal([]byte(opts.BridgeArgs[1]), &old); err != nil {
				t.Fatal(err)
			}
			old.ComposerSettings = false
			payload, err := json.Marshal(old)
			if err != nil {
				t.Fatal(err)
			}
			opts.BridgeArgs[1] = string(payload)
			proc, err := session.Start(opts, func(*protocol.Event) {})
			if err != nil {
				t.Fatal(err)
			}
			d.adopt(s, proc)
			d.handleEvent(s, &protocol.Event{Type: "system", Subtype: protocol.CodexWorkersStarted})
			if d.codexBridgeNeedsRefresh(s) {
				t.Fatal("a worker must keep its bridge alive")
			}
			var handed handedSession
			handed.savePending(s)
			if !handed.CodexWorkers {
				t.Fatal("live reload must preserve the worker flag")
			}
			d.handleEvent(s, &protocol.Event{Type: "system", Subtype: protocol.CodexWorkersEnded})
			if !d.codexBridgeNeedsRefresh(s) {
				t.Fatal("an idle regular or topic tab should refresh a legacy bridge")
			}
		})
	}
}
