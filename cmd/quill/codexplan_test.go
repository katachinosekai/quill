package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestCodexPlanCardApprovesScopeOnce(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	d := h.d
	d.cfg.CodexRoot = t.TempDir()
	var s *deskSession
	h.ui(func() {
		d.planFirst = true
		s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.sendTo(s, "Help me design the change")
	})
	h.eventually("idle Codex", func() bool { return s.running() && !s.conv.Busy && !s.proc.Busy() })
	h.ui(func() {
		propose := func(id, steps string) {
			content, err := json.Marshal([]protocol.Block{
				{Type: "text", Text: steps + "\n\nCan I run steps 1–3? Yes (recommended) or no?"},
			})
			if err != nil {
				t.Fatal(err)
			}
			d.handleEvent(s, &protocol.Event{Type: "assistant", Message: &protocol.Message{ID: id, Content: content}})
		}
		s.queue = []string{"Yes"}
		propose("first", "1. Gather the inputs.\n2. Update the code.\n3. Test the result.")
		d.handleEvent(s, &protocol.Event{Type: "result", Subtype: protocol.ResultContinuing})
		if livePlan(s.conv) != nil {
			t.Fatal("a continuing turn must not present a plan card")
		}
		d.handleEvent(s, &protocol.Event{Type: "result"})
		first := livePlan(s.conv)
		if first == nil || planSteps(s.conv.PlanText(first)) != 3 {
			t.Fatal("the completed proposal should yield one full plan card")
		}
		if len(s.queue) != 1 || lastUser(s) != "Help me design the change" {
			t.Fatal("an answer queued before the plan appeared must stay queued")
		}
		s.queue = nil
		propose("revised", "1. Gather the inputs.\n2. Update the docs.\n3. Test the result.")
		d.handleEvent(s, &protocol.Event{Type: "result"})
		revised := livePlan(s.conv)
		if revised == nil || revised == first || first.PermState != chat.PermCancelled {
			t.Fatal("changed steps must replace the card even when the closing question matches")
		}
		d.answerPlanMsg(s, first, true, "Approved.")
		if lastUser(s) != "Help me design the change" {
			t.Fatal("a stale card must not send approval")
		}
		d.keepNext = true
		d.answerPlanMsg(s, revised, true, "Approved.")
		d.keepNext = false
		d.answerPlanMsg(s, revised, true, "Duplicate approval")
		if revised.PermState != chat.PermAllowed || lastUser(s) != "Approved." || s.runAll || s.autoApprove {
			t.Fatal("plan approval should send only the scope decision")
		}
		d.handleEvent(s, &protocol.Event{
			Type: "control_request", RequestID: "native-permission",
			Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: "Codex command"},
		})
		if !codexNativeApprovalPending(s) {
			t.Fatal("a native tool permission must still wait for a user decision")
		}
	})
}

func TestCodexPlanFollowUpSendsOriginalText(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	d := h.d
	d.cfg.CodexRoot = t.TempDir()
	var s *deskSession
	h.ui(func() {
		d.openQuillCodex()
		a := d.assistByRole(roleCodexQuill)
		s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(s, roleCodexQuill)
		a.s, a.state = s, asLive
		a.view.show(s)
		d.sendTo(s, "Help me design the change")
	})
	h.eventually("idle operator", func() bool { return s.running() && !s.conv.Busy && !s.proc.Busy() })
	h.ui(func() {
		plan := s.conv.AddPlan()
		const text = "Also include a sign-in status for both providers."
		d.input.SetText(text)
		if !d.sendTargeted(d.input, text, false) {
			t.Fatal("the follow-up should go to the operator")
		}
		if lastUser(s) != text || s.lastSent != text || s.pendingRevision != nil {
			t.Fatal("normal Send must deliver the original text without starting an interrupt")
		}
		if plan.PermState != chat.PermCancelled || planState(s.conv, plan) != planMovedOn || s.runAll {
			t.Fatal("the follow-up should retire the old card without approving its scope")
		}
	})
}
