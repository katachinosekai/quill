package main

import (
	"os"
	"testing"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestInterruptDismissesIdlePlan(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		d := h.d
		d.planFirst = true
		s = d.createSession(h.proj, "default", "default")
		d.sendTo(s, "Approve this plan?")
	})
	h.eventually("idle plan", func() bool { return !s.conv.Busy && hasPendingPlan(s) })
	h.ui(func() {
		d := h.d
		plan := livePlan(s.conv)
		d.input.SetText("Change the suggestion")
		if !d.escape(s) || plan.PermState != chat.PermCancelled || hasPendingPlan(s) ||
			d.input.Text != "Change the suggestion" || s.runAll {
			t.Fatal("Escape should cancel the plan without sending or clearing the draft")
		}
	})
}

func TestCodexApprovalRevisionWaitsForInterruptedTurn(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	h.d.cfg.CodexRoot = t.TempDir()
	var s *deskSession
	var permission *chat.Item
	const text = "Use the other suggestion instead"
	h.ui(func() {
		d := h.d
		d.openQuillCodex()
		a := d.assistByRole(roleCodexQuill)
		s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(s, roleCodexQuill)
		a.s, a.state = s, asLive
		a.view.show(s)
		d.sendTo(s, "Wait for command approval")
	})
	h.eventually("native approval", func() bool { return len(s.conv.PendingPermissions()) == 1 })
	h.ui(func() {
		d := h.d
		permission = s.conv.PendingPermissions()[0]
		d.input.SetText(text)
		if !d.sendTargeted(d.input, text, false) || s.pendingRevision == nil || d.canAnswer(s) {
			t.Fatal("normal Send must start the revision without allowing the old request")
		}
		d.answer(s, permission, protocol.Allow)
		d.sendTargeted(d.input, text, false)
		if len(s.queue) != 0 {
			t.Fatal("pressing Enter again must not queue a duplicate revision")
		}
		if permission.PermState != chat.PermPending || lastUser(s) != "Wait for command approval" || d.input.Text != text {
			t.Fatal("the permission or revision was sent before interruption")
		}
	})
	h.eventually("revision and response", func() bool {
		return s.pendingRevision == nil && !s.conv.Busy && lastUser(s) == text && s.lastSent == text
	})
	h.ui(func() {
		if permission.PermState != chat.PermCancelled || h.d.input.Text != "" || s.runAll {
			t.Fatal("the old permission must cancel and the delivered draft must clear")
		}
	})
}

func TestApprovalInterruptResultWaitsForAcknowledgment(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		r := &approvalRevision{text: "Change the suggestion"}
		s := &deskSession{conv: chat.New(), pendingRevision: r}
		if !h.d.finishApprovalInterrupt(s) || !r.finished || s.pendingRevision != r {
			t.Fatal("a terminal result arriving ahead of the interrupt ACK must keep delivery on hold")
		}
		if len(s.conv.Items) != 0 {
			t.Fatal("the revision was sent before the interrupt acknowledgment")
		}
	})
}
