package main

import (
	"os"
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestCodexApprovalRefreshesOwningBox(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	h.d.cfg.CodexRoot = t.TempDir()
	h.ui(func() {
		d := h.d
		d.openQuillCodex()
		a := d.assistByRole(roleCodexQuill)
		s := d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.markHiddenAs(s, roleCodexQuill)
		if err := d.ensureProcess(s); err != nil {
			t.Fatal(err)
		}
		a.s, a.state = s, asLive
		ev := &protocol.Event{Type: "control_request", RequestID: "1", Request: &protocol.ControlRequest{
			Subtype: "can_use_tool", ToolName: "Codex command",
		}}
		created := s.conv.Apply(ev)
		if len(created) != 1 || len(s.conv.Apply(ev)) != 0 || len(s.conv.PendingPermissions()) != 1 {
			t.Fatal("one request ID should render only one approval")
		}
		it := created[0]
		a.view.show(s)
		buttons := func() (out []string) {
			deepWalk(a.view.views[it.Seq].holder, func(o fyne.CanvasObject) {
				if button, ok := o.(*widget.Button); ok {
					out = append(out, button.Text)
				}
			})
			return out
		}
		if !slices.Contains(buttons(), "Allow") {
			t.Fatal("the pending approval should be actionable")
		}
		d.answer(s, it, protocol.Allow)
		if it.PermState != chat.PermAllowed || slices.Contains(buttons(), "Allow") {
			t.Fatal("the already answered approval still looks pending in the operator box")
		}
	})
}

func TestCodexNativeQuestionSupersedesTextChoice(t *testing.T) {
	s := &deskSession{codex: true, conv: chat.New()}
	s.conv.AddUser("Help me choose")
	reply := s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{
		ID: "reply", Content: []byte(`[{"type":"text","text":"Pick one: Now or Later?"}]`),
	}})[0]
	if openSessionChoiceItem(s) != reply {
		t.Fatal("the text-only Codex choice should be available")
	}
	track := newItemTrack(trackClock())
	track.update(s)
	rev := track.rev
	s.conv.Apply(&protocol.Event{Type: "control_request", RequestID: "native", Request: &protocol.ControlRequest{
		Subtype: "can_use_tool", ToolName: "AskUserQuestion",
		Input: []byte(`{"questions":[{"question":"Pick one","options":[{"label":"Now"},{"label":"Later"}]}]}`),
	}})
	track.update(s)
	if openSessionChoiceItem(s) != nil || track.irev[reply.Seq] <= rev {
		t.Fatal("the native choice must supersede the inferred one, including incremental phone updates")
	}
	s.conv.Permission("native").PermState = chat.PermAllowed
	if openSessionChoiceItem(s) != nil {
		t.Fatal("answering the native choice should not reactivate its text duplicate")
	}
	native := s.conv.Permission("native")
	native.Input = []byte(`{"questions":[{"question":"Which branch?"}]}`)
	if openSessionChoiceItem(s) != reply {
		t.Fatal("an answered unrelated native question should not suppress a text choice")
	}
	native.PermState = chat.PermPending
	s.conv.AddUser("A follow-up while the native request is still open")
	newReply := s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{
		ID: "follow-up", Content: []byte(`[{"type":"text","text":"Pick one: Red or Blue?"}]`),
	}})[0]
	if !codexQuestionSupersedes(s, newReply) {
		t.Fatal("steering must not make another choice actionable while a native question is pending")
	}
	s.codex = false
	if openSessionChoiceItem(s) != newReply {
		t.Fatal("Claude's existing choice behavior should be unchanged")
	}
}

func TestPlanMirrorsFollowQuillOperatorEngine(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		s := d.createSession(h.proj, "default", "default")
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{
			ID: "plan", Content: []byte(`[{"type":"text","text":"1. Do the work.\n\nApprove this plan?"}]`),
		}})
		s.conv.MarkPlanText()
		plan := s.conv.AddPlan()
		d.syncPlanMirrors()
		m := d.quillChat.mirrors
		if len(m.list) != 1 || m.list[0].plan != plan || !d.mirrorAlerting(boxQuill) {
			t.Fatal("a pending plan should be mirrored before the operator box opens")
		}
		d.openQuillCodex()
		if m.host != d.codexQuillChat || !slices.Contains(m.host.top.Objects, fyne.CanvasObject(m.wrap)) ||
			slices.Contains(d.quillChat.top.Objects, fyne.CanvasObject(m.wrap)) || d.mirrorAlerting(boxQuill) {
			t.Fatal("the active Codex box should show the original plan mirror and clear the hidden alert")
		}
		d.openAssistBox(boxQuill)
		if m.host != d.quillChat || len(m.list) != 1 || m.list[0].plan != plan {
			t.Fatal("switching back must preserve the same plan and mirror")
		}
	})
}
