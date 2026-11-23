package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

// Auto-approve (user 2026-11-23): an approval that lasts until the user
// turns it off. Texts and ids are placeholders.

func checkBoxes(o fyne.CanvasObject) []*widget.Check {
	var out []*widget.Check
	walk(o, func(x fyne.CanvasObject) {
		if c, ok := x.(*widget.Check); ok {
			out = append(out, c)
		}
	})
	return out
}

func keepBox(t *testing.T, o fyne.CanvasObject) *widget.Check {
	t.Helper()
	for _, c := range checkBoxes(o) {
		if c.Text == autoApproveCheck {
			return c
		}
	}
	t.Fatalf("no %q checkbox", autoApproveCheck)
	return nil
}

// autoSession: a plan card approved with "Keep approving in this tab".
func autoSession(t *testing.T, h *harness) (*deskSession, *chat.Item) {
	t.Helper()
	s, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		keepBox(t, h.d.chat.planUI(plan)).SetChecked(true)
		tapLabel(t, h, h.d.chat.planUI(plan), "Send answers")
	})
	return s, plan
}

func TestAutoApproveOnFromCard(t *testing.T) {
	h := newHarness(t)
	_, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		if keepBox(t, h.d.chat.planUI(plan)).Checked {
			t.Fatal("the checkbox starts unticked")
		}
	})
	h2 := newHarness(t)
	s2, plan2 := planCardSession(t, h2, yesNoPlan)
	h2.ui(func() {
		tapLabel(t, h2, h2.d.chat.planUI(plan2), "Send answers")
		if s2.autoApprove || strings.Contains(lastUserText(s2), "Auto-approve") {
			t.Fatal("without the checkbox an approval is per request, as before")
		}
	})
	h3 := newHarness(t)
	s3, plan3 := autoSession(t, h3)
	h3.ui(func() {
		if plan3.PermState != chat.PermAllowed || !s3.runAll || !s3.autoApprove {
			t.Fatalf("approved with auto-approve: state %v runAll %v auto %v", plan3.PermState, s3.runAll, s3.autoApprove)
		}
		got := lastUserText(s3)
		if !isPlanGo(got) || !strings.HasSuffix(got, autoApproveOnText) || !strings.HasPrefix(got, planGoMessage) {
			t.Fatalf("the approval tells Claude: %q", got)
		}
		h3.d.updateChrome()
		if h3.d.current != s3 || !h3.d.autoBtn.Visible() {
			t.Fatal("the Auto-approve on chip shows by the message box")
		}
		if len(checkBoxes(h3.d.chat.planUI(plan3))) != 0 {
			t.Fatal("an answered card has no checkbox")
		}
		if !strings.Contains(planSpecHint, "Auto-approve on") || !strings.Contains(planSpecHint, "auto-approve is off") {
			t.Fatal("the session hint explains the markers")
		}
	})
}

func TestAutoApproveGateAndUserMessages(t *testing.T) {
	h := newHarness(t)
	s, _ := autoSession(t, h)
	h.ui(func() {
		endTurn(t, h, s)
		s.inTurn = false
		h.d.sendTo(s, "now also tidy the readme") // a new request: no plan
		if !s.autoApprove {
			t.Fatal("a new user message doesn't end auto-approve")
		}
		if s.runAll {
			t.Fatal("the plan's own approval ended as before")
		}
		if got := lastUserText(s); got != "now also tidy the readme" {
			t.Fatalf("no note while on: %q", got)
		}
		if st := ownCall(h, s, "a1", "make install"); st != chat.PermAutoAllowed {
			t.Fatalf("auto-approve lets the call through without a plan: %v", st)
		}
		// A subagent this turn started.
		mainToolUse(t, h, s, "toolu_ag1", "Agent", `{"description":"tidy","prompt":"tidy the readme","run_in_background":true}`)
		feed(t, h, s, `{"type":"system","subtype":"task_started","task_id":"agent1","tool_use_id":"toolu_ag1","description":"tidy","task_type":"local_agent","is_backgrounded":true}`)
		in := `{"file_path":"/tmp/x/a.txt","content":"x"}`
		sub := &chat.Item{Kind: chat.Permission, ToolName: "Write", RequestID: "a2", Input: []byte(in),
			Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: "Write", Input: []byte(in), ToolUseID: "toolu_sub1", AgentID: "agent1"}}
		s.conv.Items = append(s.conv.Items, sub)
		h.d.planGate(s, &protocol.Event{Type: "control_request", RequestID: "a2", Request: sub.Request}, []*chat.Item{sub})
		if sub.PermState != chat.PermAutoAllowed {
			t.Fatalf("and a subagent's call: %v", sub.PermState)
		}
		userEvent(h, s, "<cross-session-message from=\"peer\">hi</cross-session-message>", nil)
		if !s.autoApprove {
			t.Fatal("peer messages don't end it")
		}
		if st := ownCall(h, s, "a4", "make install"); st != chat.PermPlanAsked {
			t.Fatalf("but the rest of the turn a peer message joined is gated: %v", st)
		}
		endTurn(t, h, s)
		s.inTurn = false
		h.d.sendTo(s, "go on")
		// Stop and a refused card end the plan's approval, not auto-approve.
		revokePlan(s)
		if !s.autoApprove {
			t.Fatal("Stop doesn't end auto-approve")
		}
		if st := ownCall(h, s, "a3", "go build ./..."); st != chat.PermAutoAllowed {
			t.Fatalf("still on: %v", st)
		}
		if items := h.d.sessionMenuItems(s.id); !hasMenuItem(items, autoApproveMenu) {
			t.Fatal("the session menu has the off switch")
		}
	})
}

func hasMenuItem(items []*fyne.MenuItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

// Root commands need an approved plan with auto-approve on too, and a batch
// with a refused root command is refused as a whole.
func TestAutoApproveKeepsRootRule(t *testing.T) {
	h := newHarness(t)
	s, _ := autoSession(t, h)
	h.ui(func() {
		endTurn(t, h, s)
		s.inTurn = false
		h.d.sendTo(s, "set up swap")
		for i, cmd := range []string{"sudo systemctl daemon-reload", "pkexec bash /tmp/x/setup.sh", "swapon /swap/file", "env FOO=1 doas reboot"} {
			if st := ownCall(h, s, "r"+string(rune('a'+i)), cmd); st != chat.PermPlanAsked {
				t.Fatalf("%q needs a plan: %v", cmd, st)
			}
		}
		if !strings.HasPrefix(planAsk(s), autoApproveRootAsk) {
			t.Fatalf("the refusal says why: %q", planAsk(s))
		}
		endTurn(t, h, s)
		s.inTurn = false
		h.d.sendTo(s, "set it up")
		feed(t, h, s, batchBash)
		feed(t, h, s, batchWrite)
		if e := gateRequest(t, h, s, reqPkexec); e.PermState != chat.PermPlanAsked {
			t.Fatalf("pkexec refused: %v", e.PermState)
		}
		if e := gateRequest(t, h, s, reqWrite); e.PermState != chat.PermPlanAsked {
			t.Fatalf("the Write of the refused batch is refused too: %v", e.PermState)
		}
		if !s.autoApprove {
			t.Fatal("a refusal doesn't end auto-approve")
		}
	})
}

func TestAutoApproveOffRestoresPlanFirst(t *testing.T) {
	h := newHarness(t)
	s, _ := autoSession(t, h)
	h.ui(func() {
		endTurn(t, h, s)
		s.inTurn = false
		h.d.updateChrome()
		h.d.autoBtn.Tapped(nil) // the chip's ×
		if s.autoApprove || h.d.autoBtn.Visible() {
			t.Fatal("the chip turns it off and goes away")
		}
		if hasMenuItem(h.d.sessionMenuItems(s.id), autoApproveMenu) {
			t.Fatal("no off switch while off")
		}
		h.d.sendTo(s, "next change")
		if got := s.lastSent; got != "next change"+autoApproveOffNote {
			t.Fatalf("Claude is told with the next message: %q", got)
		}
		if got := lastUserText(s); got != "next change" {
			t.Fatalf("the bubble shows the user's words only: %q", got)
		}
		if st := ownCall(h, s, "o1", "make install"); st != chat.PermPlanAsked {
			t.Fatalf("plan-first again: %v", st)
		}
		endTurn(t, h, s)
		s.inTurn = false
		h.d.sendTo(s, "and another")
		if got := lastUserText(s); got != "and another" {
			t.Fatalf("the note goes once: %q", got)
		}
	})
	// The session menu's switch.
	h2 := newHarness(t)
	s2, _ := autoSession(t, h2)
	h2.ui(func() {
		for _, it := range h2.d.sessionMenuItems(s2.id) {
			if it.Label == autoApproveMenu {
				it.Action()
			}
		}
		if s2.autoApprove || s2.autoNote != autoApproveOffNote {
			t.Fatal("the menu turns it off")
		}
	})
}

func TestAutoApproveEndsWithSession(t *testing.T) {
	h := newHarness(t)
	s, _ := autoSession(t, h)
	h.ui(func() {
		endTurn(t, h, s)
		h.d.handleExit(s, s.proc, nil)
		if s.autoApprove || s.autoNote != autoApproveOffNote {
			t.Fatalf("the process exit ends it: %v %q", s.autoApprove, s.autoNote)
		}
	})
	h2 := newHarness(t)
	s2, _ := autoSession(t, h2)
	h2.ui(func() {
		endTurn(t, h2, s2)
		if !h2.d.restartOnNext(s2, "Restarting.") || s2.autoApprove {
			t.Fatal("a restart ends it")
		}
		var hs handedSession
		s2.autoApprove = true
		hs.savePending(s2)
		fresh := &deskSession{conv: chat.New()}
		hs.restorePending(fresh)
		if fresh.autoApprove || fresh.autoNote != autoApproveOffNote {
			t.Fatal("a reload doesn't keep it, and tells Claude it is off")
		}
	})
}

func TestPhoneAutoApprove(t *testing.T) {
	h := newHarness(t)
	s, plan := planCardSession(t, h, yesNoPlan)
	a := &phoneAPI{d: h.d, tracks: map[string]*itemTrack{}, lastSess: map[string]string{}, lastCtl: map[string]bool{}, wake: make(chan struct{})}
	h.ui(func() {
		zero := 0
		if _, err := a.answer(s, writeBody{Action: "approvePlan", RequestID: plan.RequestID, Seq: plan.Seq, ApprovalChoice: &zero, KeepApproving: true}); err != nil {
			t.Fatal(err.msg)
		}
		if !s.autoApprove || h.d.keepNext || !strings.HasSuffix(lastUserText(s), autoApproveOnText) {
			t.Fatalf("keepApproving turns it on: %v %q", s.autoApprove, lastUserText(s))
		}
		if !a.session(s.id, "").AutoApprove {
			t.Fatal("the session shows autoApprove")
		}
		if _, err := a.sessionWrite(s, "auto-approve-off", "", writeBody{}); err != nil || s.autoApprove {
			t.Fatal("auto-approve-off turns it off")
		}
		if a.session(s.id, "").AutoApprove {
			t.Fatal("and the session shows it")
		}
	})
}

// TestAutoApproveShot renders the chip and the card's checkbox to
// $QUILL_SHOT_DIR, for a human to look at.
func TestAutoApproveShot(t *testing.T) {
	dir := os.Getenv("QUILL_SHOT_DIR")
	if dir == "" {
		t.Skip("set QUILL_SHOT_DIR to write screenshots")
	}
	shotTo := func(h *harness, name string) {
		h.w.Canvas().Refresh(h.w.Content())
		img := h.w.Canvas().Capture()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, img)
		f.Close()
	}
	h := newHarness(t)
	_, plan := planCardSession(t, h, yesNoPlan)
	h.ui(func() {
		h.w.Resize(fyne.NewSize(1100, 1000))
		keepBox(t, h.d.chat.planUI(plan)).SetChecked(true)
		h.d.chat.show(h.d.current)
		shotTo(h, "card-checkbox")
		tapLabel(t, h, h.d.chat.planUI(plan), "Send answers")
		h.d.updateChrome()
		shotTo(h, "chip-on")
	})
}
