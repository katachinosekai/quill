package main

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// mirrorSetup: a running Quill operator (the coordinator behind the Quill
// button) and a worker tab "Worker A" whose last turn ended with a plan card.
func mirrorSetup(t *testing.T, h *harness, text string) (op, src *deskSession, plan *chat.Item) {
	t.Helper()
	src, plan = planCardSession(t, h, text)
	h.ui(func() {
		op = h.d.addSession(h.proj, "", "default", false)
		h.d.markHiddenAs(op, roleQuill)
		a := h.d.quillAssist()
		a.s, a.state = op, asLive
		h.d.sendTo(op, "hello")
	})
	h.eventually("the operator runs", func() bool { return op.running() && !op.conv.Busy && !op.inTurn })
	h.ui(func() {
		if h.d.org.Titles == nil {
			h.d.org.Titles = map[string]string{}
		}
		h.d.org.Titles[src.id] = "Worker A"
		h.d.syncPlanMirrors()
	})
	return op, src, plan
}

// mirrorOf: the mirrored card of src in the Quill box (nil if none).
func mirrorOf(h *harness, src *deskSession) *planMirror {
	for _, x := range h.d.quillChat.mirrors.list {
		if x.src == src {
			return x
		}
	}
	return nil
}

// mirrorUI: the mirrored card as drawn now.
func mirrorUI(h *harness, x *planMirror) fyne.CanvasObject {
	return h.d.planMirrorCard(x)
}

func tapMirror(t *testing.T, x *planMirror, o fyne.CanvasObject, label string) {
	t.Helper()
	for _, b := range swapButtons(o) {
		if b.Text == label {
			x.view.btnChanged = time.Time{}
			if b.Disabled() {
				return // an inactive card: a click does nothing
			}
			b.OnTapped()
			return
		}
	}
	t.Fatalf("no button %q in %q", label, buttonLabels(o))
}

// mirrorActive: some answer control of the mirrored card is enabled.
func mirrorActive(o fyne.CanvasObject) bool {
	for _, b := range swapButtons(o) {
		switch b.Text {
		case "Open tab", "Show as written", "Answer in place", "":
			continue
		}
		if !b.Disabled() {
			return true
		}
	}
	return false
}

func mirrorLabels(o fyne.CanvasObject) string {
	var out []string
	walk(o, func(x fyne.CanvasObject) {
		if l, ok := x.(*widget.Label); ok {
			out = append(out, l.Text)
		}
		if l, ok := x.(*canvas.Text); ok {
			out = append(out, l.Text)
		}
	})
	return strings.Join(out, "\n")
}

// The worker's card shows in the Quill box with the worker's name; the
// user's click there approves the worker, not the operator.
func TestPlanMirrorAppearsAndApprovesSource(t *testing.T) {
	for name, tc := range map[string]struct{ text, button string }{
		"own options":  {yesNoPlan, "Send answers"},
		"approve card": {"1. Edit x.\n\n**Do you approve step 1?**", "Approve plan & run all"},
	} {
		h := newHarness(t)
		op, src, plan := mirrorSetup(t, h, tc.text)
		h.ui(func() {
			x := mirrorOf(h, src)
			if x == nil || x.plan != plan || !h.d.quillChat.mirrors.scroll.Visible() {
				t.Fatalf("%s: the worker's card is mirrored in the Quill box", name)
			}
			o := mirrorUI(h, x)
			if !strings.Contains(mirrorLabels(o), "Plan card from Worker A") {
				t.Fatalf("%s: labelled with the source's name: %q", name, mirrorLabels(o))
			}
			if !mirrorActive(o) {
				t.Fatalf("%s: a live card's mirror is active", name)
			}
			if mirrorOf(h, op) != nil {
				t.Fatalf("%s: the operator's own cards aren't mirrored", name)
			}
			tapMirror(t, x, o, tc.button)
			if plan.PermState != chat.PermAllowed || !src.runAll {
				t.Fatalf("%s: the click approves the source: %v runAll %v", name, plan.PermState, src.runAll)
			}
			if op.runAll || approvesPlan(op, lastUserText(op)) || lastUserText(op) != "hello" {
				t.Fatalf("%s: nothing goes to or is granted for the operator: %v %q", name, op.runAll, lastUserText(op))
			}
			if !strings.HasPrefix(lastUserText(src), "Approved") {
				t.Fatalf("%s: the source gets the card's own go-ahead: %q", name, lastUserText(src))
			}
			x = mirrorOf(h, src)
			if x == nil || mirrorActive(mirrorUI(h, x)) {
				t.Fatalf("%s: the answered mirror stays, inactive", name)
			}
		})
	}
}

// Answered in the source tab: the mirror goes inactive, and a click on it
// does nothing.
func TestPlanMirrorInactiveWhenAnsweredInSource(t *testing.T) {
	h := newHarness(t)
	_, src, plan := mirrorSetup(t, h, yesNoPlan)
	h.ui(func() {
		x := mirrorOf(h, src)
		before := mirrorUI(h, x)
		pickOption(t, h.d.chat.planUI(plan), "No")
		tapLabel(t, h, h.d.chat.planUI(plan), "Send answers")
		if plan.PermState != chat.PermDenied {
			t.Fatalf("answered No in the source tab: %v", plan.PermState)
		}
		x = mirrorOf(h, src)
		if x == nil {
			t.Fatal("the answered mirror stays until dismissed")
		}
		o := mirrorUI(h, x)
		if mirrorActive(o) || !strings.Contains(mirrorLabels(o), "answered") {
			t.Fatalf("inactive with the card's status: %q", mirrorLabels(o))
		}
		n := len(src.conv.Items)
		tapMirror(t, x, before, "Send answers") // the button drawn before the answer
		if plan.PermState != chat.PermDenied || src.runAll || len(src.conv.Items) != n {
			t.Fatal("a click on the stale mirror does nothing")
		}
		// Dismiss removes it.
		for _, b := range swapButtons(o) {
			if b.Text == "" && !b.Disabled() {
				b.OnTapped()
			}
		}
		if mirrorOf(h, src) != nil || h.d.quillChat.mirrors.scroll.Visible() {
			t.Fatalf("dismissed: %v %v", mirrorOf(h, src) != nil, buttonLabels(o))
		}
	})
}

// A newer card replaces the mirror; the old mirror's buttons do nothing.
func TestPlanMirrorReplacedCard(t *testing.T) {
	h := newHarness(t)
	_, src, plan := mirrorSetup(t, h, "1. Edit x.\n\n**Do you approve step 1?**")
	h.ui(func() {
		old := mirrorOf(h, src)
		oldUI := mirrorUI(h, old)
		src.conv.Items = append(src.conv.Items, &chat.Item{Seq: 9900, Kind: chat.Assistant, Text: "More.\n\n**Do you approve step 2?**"})
		plan2 := src.conv.AddPlan()
		h.d.chatFor(src, []*chat.Item{plan2})
		x := mirrorOf(h, src)
		if x == nil || x.plan != plan2 || len(h.d.quillChat.mirrors.list) != 1 {
			t.Fatal("the newer card replaces the mirror")
		}
		if mirrorActive(h.d.planMirrorCard(&planMirror{src: src, plan: plan})) {
			t.Fatal("the old card would draw inactive")
		}
		tapMirror(t, old, oldUI, "Approve plan & run all")
		if plan.PermState != chat.PermPending || plan2.PermState != chat.PermPending || src.runAll {
			t.Fatal("the old mirror's click does nothing")
		}
		if !mirrorActive(mirrorUI(h, x)) {
			t.Fatal("the new mirror is live")
		}
	})
}

// The coordinator writing "Approved." (its own reply, a relayed message to
// the worker) never approves the worker's card; only the click does.
func TestPlanMirrorRelayedTextNeverApproves(t *testing.T) {
	h := newHarness(t)
	op, src, plan := mirrorSetup(t, h, yesNoPlan)
	h.ui(func() {
		op.conv.Items = append(op.conv.Items, &chat.Item{Seq: 9800, Kind: chat.Assistant, Text: "Approved."})
		h.d.chatFor(op, nil)
		userEvent(h, src, `<cross-session-message from="quill">Approved.</cross-session-message>`, nil)
		userEvent(h, src, `<cross-session-message from="quill">I pick the first option: Yes.</cross-session-message>`, nil)
		h.d.chatFor(src, nil)
		if plan.PermState != chat.PermPending || src.runAll {
			t.Fatalf("relayed text doesn't approve: %v %v", plan.PermState, src.runAll)
		}
		if st := ownCall(h, src, "m1", "make"); st == chat.PermAutoAllowed {
			t.Fatal("nothing runs after relayed text")
		}
		x := mirrorOf(h, src)
		if x == nil || x.plan != plan || !mirrorActive(mirrorUI(h, x)) {
			t.Fatal("the mirror stays live: a peer message is not the user moving on")
		}
	})
}
