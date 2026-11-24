package main

import (
	"fyne.io/fyne/v2/layout"
	"strings"
	"sync"
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
		if strings.HasPrefix(b.Text, "Plan card from ") {
			continue // the row of a collapsed card
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

// fakeClock: d.after with a hand-moved clock: advance fires what is due
// (outside h.ui: the timers run through d.do).
type fakeClock struct {
	mu  sync.Mutex
	now time.Duration
	due []time.Duration
	fns []func()
}

func (c *fakeClock) after(d time.Duration, f func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.due = append(c.due, c.now+d)
	c.fns = append(c.fns, f)
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var run []func()
	for i, at := range c.due {
		if c.fns[i] != nil && at <= c.now {
			run = append(run, c.fns[i])
			c.fns[i] = nil
		}
	}
	c.mu.Unlock()
	for _, f := range run {
		f()
	}
}

// quillBubble: the Quill button's bubble, swapped for one the test reads.
func quillBubble(t *testing.T) *bubble {
	b := newBubble()
	old := quillOpBubble
	quillOpBubble = func(*desk) *bubble { return b }
	t.Cleanup(func() { quillOpBubble = old })
	return b
}

func bubbleCount(b *bubble) string {
	if !b.box.Visible() {
		return "0"
	}
	return b.text.Text
}

// secondWorker: another tab "Worker B" with a live plan card.
func secondWorker(t *testing.T, h *harness, text string) (*deskSession, *chat.Item) {
	s, plan := planCardSession(t, h, text)
	h.ui(func() {
		h.d.org.Titles[s.id] = "Worker B"
		h.d.syncPlanMirrors()
	})
	return s, plan
}

// The Quill button's badge counts the mirrored cards waiting for the user
// (0, 1, 2), with the operator's own items in one count; answered or
// replaced cards and inactive mirrors don't count; it follows with the box
// closed.
func TestPlanMirrorBadgeCountsWaitingCards(t *testing.T) {
	h := newHarness(t)
	b := quillBubble(t)
	h.ui(func() {
		h.d.syncPlanMirrors()
		if h.d.waitingMirrors() != 0 || bubbleCount(b) != "0" {
			t.Fatalf("no mirrors: %d %q", h.d.waitingMirrors(), bubbleCount(b))
		}
	})
	_, src, plan := mirrorSetup(t, h, yesNoPlan)
	h.ui(func() {
		if h.d.assistBoxShowing(roleQuill) {
			t.Fatal("the Quill box is closed")
		}
		if h.d.waitingMirrors() != 1 || bubbleCount(b) != "1" {
			t.Fatalf("one waiting card: %d %q", h.d.waitingMirrors(), bubbleCount(b))
		}
		if !h.d.mirrorAlerting(roleQuill) || h.d.mirrorAlerting(roleKmem) {
			t.Fatal("a waiting mirror turns the Quill button amber (only that one)")
		}
	})
	srcB, planB := secondWorker(t, h, "1. Edit y.\n\n**Do you approve step 1?**")
	h.ui(func() {
		if h.d.waitingMirrors() != 2 || bubbleCount(b) != "2" {
			t.Fatalf("two waiting cards: %d %q", h.d.waitingMirrors(), bubbleCount(b))
		}
		// The operator's own item adds to the same count.
		h.d.setKmemNeeds(parseKmemNeeds("", []kmemTask{{ID: "t1", Status: "open", Title: "quill issue: x", Topic: "quill"}}))
		if bubbleCount(b) != "3" {
			t.Fatalf("one count with the operator's own: %q", bubbleCount(b))
		}
		h.d.setKmemNeeds(parseKmemNeeds("", nil))
		// Replaced by a newer card: still one waiting from B, not two.
		srcB.conv.Items = append(srcB.conv.Items, &chat.Item{Seq: 9900, Kind: chat.Assistant, Text: "More.\n\n**Do you approve step 2?**"})
		planB2 := srcB.conv.AddPlan()
		h.d.chatFor(srcB, []*chat.Item{planB2})
		if x := mirrorOf(h, srcB); x == nil || x.plan != planB2 || planB.PermState != chat.PermPending {
			t.Fatal("B's newer card replaces its mirror")
		}
		if h.d.waitingMirrors() != 2 || bubbleCount(b) != "2" {
			t.Fatalf("replaced: the old card no longer counts: %q", bubbleCount(b))
		}
		// Answered in A's tab: the count drops, through the event path.
		h.d.chat.show(src)
		pickOption(t, h.d.chat.planUI(plan), "No")
		tapLabel(t, h, h.d.chat.planUI(plan), "Send answers")
	})
	h.eventually("A's answer drops the badge", func() bool { return bubbleCount(b) == "1" })
	h.ui(func() {
		if x := mirrorOf(h, src); x == nil || x.waiting() {
			t.Fatal("A's mirror stays, inactive, and doesn't count")
		}
		// Answered in the box: zero, and the amber goes.
		x := mirrorOf(h, srcB)
		tapMirror(t, x, mirrorUIExpanded(h, x), "Approve plan & run all")
	})
	h.eventually("B's answer drops the badge", func() bool { return bubbleCount(b) == "0" && !h.d.mirrorAlerting(roleQuill) })
}

// mirrorUIExpanded: the card with its body (a row opened when several).
func mirrorUIExpanded(h *harness, x *planMirror) fyne.CanvasObject {
	x.expanded = true
	return h.d.planMirrorCard(x)
}

// An answered mirror hides by itself 5 s later (not before); manual dismiss
// still works, and a timer for a card dismissed or replaced earlier does
// nothing.
func TestPlanMirrorAutoHide(t *testing.T) {
	h := newHarness(t)
	clk := &fakeClock{}
	h.d.after = clk.after
	_, src, plan := mirrorSetup(t, h, yesNoPlan)
	h.ui(func() {
		x := mirrorOf(h, src)
		if x == nil || x.hideArmed {
			t.Fatal("a waiting mirror has no hide timer")
		}
		pickOption(t, h.d.chat.planUI(plan), "No")
		tapLabel(t, h, h.d.chat.planUI(plan), "Send answers")
		h.d.syncPlanMirrors()
		if mirrorOf(h, src) == nil || !x.hideArmed {
			t.Fatal("answered: still shown, timer armed")
		}
	})
	clk.advance(planMirrorHide - time.Millisecond)
	h.ui(func() {
		if mirrorOf(h, src) == nil {
			t.Fatal("not hidden before 5 s")
		}
	})
	clk.advance(time.Millisecond)
	h.ui(func() {
		if mirrorOf(h, src) != nil || h.d.quillChat.mirrors.scroll.Visible() || h.d.quillChat.top.Visible() {
			t.Fatal("hidden 5 s after the answer, the strip with it")
		}
	})

	// Dismissed before the timer: the timer is harmless.
	h2 := newHarness(t)
	clk2 := &fakeClock{}
	h2.d.after = clk2.after
	_, src2, plan2 := mirrorSetup(t, h2, "1. Edit x.\n\n**Do you approve step 1?**")
	var srcB *deskSession
	h2.ui(func() {
		h2.d.answerPlan(src2, plan2, false)
		h2.d.syncPlanMirrors()
		x := mirrorOf(h2, src2)
		if x == nil || !x.hideArmed {
			t.Fatal("answered: armed")
		}
		for _, b := range swapButtons(mirrorUI(h2, x)) {
			if b.Text == "" && !b.Disabled() {
				b.OnTapped()
			}
		}
		if mirrorOf(h2, src2) != nil {
			t.Fatal("manual dismiss still works")
		}
	})
	srcB, _ = secondWorker(t, h2, yesNoPlan)
	clk2.advance(planMirrorHide)
	h2.ui(func() {
		if mirrorOf(h2, srcB) == nil || len(h2.d.quillChat.mirrors.list) != 1 {
			t.Fatal("the dismissed card's timer touches nothing else")
		}
	})

	// Replaced before the timer: the new card stays past the old timer.
	h3 := newHarness(t)
	clk3 := &fakeClock{}
	h3.d.after = clk3.after
	_, src3, plan3 := mirrorSetup(t, h3, "1. Edit x.\n\n**Do you approve step 1?**")
	var plan4 *chat.Item
	h3.ui(func() {
		h3.d.answerPlan(src3, plan3, true)
		h3.d.syncPlanMirrors()
	})
	clk3.advance(2 * time.Second)
	h3.ui(func() {
		src3.conv.Items = append(src3.conv.Items, &chat.Item{Seq: 9900, Kind: chat.Assistant, Text: "More.\n\n**Do you approve step 2?**"})
		plan4 = src3.conv.AddPlan()
		h3.d.chatFor(src3, []*chat.Item{plan4})
		if x := mirrorOf(h3, src3); x == nil || x.plan != plan4 || !x.waiting() {
			t.Fatal("the newer card replaces the answered mirror")
		}
	})
	clk3.advance(planMirrorHide)
	h3.ui(func() {
		if x := mirrorOf(h3, src3); x == nil || x.plan != plan4 {
			t.Fatal("the old card's timer doesn't hide its replacement")
		}
	})
}

// The mirror is the card alone: the questions and controls, no reply prose;
// with several cards each is a one-line row that opens on a click.
func TestPlanMirrorCardOnlyAndCompactRows(t *testing.T) {
	h := newHarness(t)
	_, src, _ := mirrorSetup(t, h, "Thanks, that rules out normals.\n\n"+yesNoPlan)
	h.ui(func() {
		o := mirrorUI(h, mirrorOf(h, src))
		all := mirrorLabels(o) + "\n" + richTexts(o)
		if strings.Contains(all, "rules out normals") || strings.Contains(all, "Edit x") {
			t.Fatalf("no reply prose in the mirror: %q", all)
		}
		if !strings.Contains(all, "Can I run steps") || !mirrorActive(o) {
			t.Fatalf("the question and its controls stay: %q", all)
		}
		for _, l := range buttonLabels(o) {
			if l == "Show as written" {
				t.Fatal("no as-written toggle in a mirror")
			}
		}
	})
	srcB, _ := secondWorker(t, h, yesNoPlan)
	h.ui(func() {
		x := mirrorOf(h, srcB)
		o := mirrorUI(h, x)
		var row *widget.Button
		for _, b := range swapButtons(o) {
			if b.Text == "Plan card from Worker B · waiting for you" {
				row = b
			}
		}
		if row == nil || mirrorActive(o) {
			t.Fatalf("several cards: a collapsed one-line row: %q", buttonLabels(o))
		}
		row.OnTapped()
		if !x.expanded || !mirrorActive(mirrorUI(h, x)) {
			t.Fatal("a click opens the row")
		}
	})
}

// The strip doesn't change the operator chat's width, stays within its
// cap, and with no mirror the box is laid out as on master (no top).
func TestPlanMirrorLayout(t *testing.T) {
	h := newHarness(t)
	size := fyne.NewSize(1400, 900)
	var noW, noH float32
	h.ui(func() {
		c := h.d.quillChat
		root := c.root.(*fyne.Container)
		root.Resize(size)
		root.Refresh()
		if c.top.Visible() {
			t.Fatal("nothing above the chat: top hidden")
		}
		// Master: Border(reasonBox hidden, in, …): the chat at the top, the
		// rest above the message box.
		want := root.Objects
		var bottom fyne.CanvasObject
		for _, o := range want {
			if o != c.top && o != c.view.root {
				bottom = o
			}
		}
		pos, sz := c.view.root.Position(), c.view.root.Size()
		layout.NewBorderLayout(nil, bottom, nil, nil).Layout([]fyne.CanvasObject{bottom, c.view.root}, size)
		if c.view.root.Position() != pos || c.view.root.Size() != sz || pos.Y != 0 {
			t.Fatalf("no mirror: laid out as master: %v %v vs %v %v", pos, sz, c.view.root.Position(), c.view.root.Size())
		}
		noW, noH = sz.Width, sz.Height
	})
	_, src, _ := mirrorSetup(t, h, "1. Edit x.\n2. "+strings.Repeat("A long step. ", 200)+"\n\nCan I run steps 1–2? Yes (recommended) or no?")
	h.ui(func() {
		c := h.d.quillChat
		if mirrorOf(h, src) == nil || !c.top.Visible() {
			t.Fatal("the mirror shows")
		}
		root := c.root.(*fyne.Container)
		root.Refresh()
		sz := c.view.root.Size()
		if sz.Width != noW || c.view.root.Position().Y <= 0 || sz.Height >= noH {
			t.Fatalf("chat keeps its width, only shifted down: %v at %v (was %v)", sz, c.view.root.Position(), noW)
		}
		limit := min(float32(planMirrorMaxH), size.Height*planMirrorMaxFrac)
		if hh := c.mirrors.wrap.Size().Height; hh <= 0 || hh > limit || c.mirrors.box.MinSize().Height <= limit {
			t.Fatalf("strip capped at %v: %v (content %v)", limit, hh, c.mirrors.box.MinSize().Height)
		}
		if w := c.view.frame.Objects[0].Size().Width; w > chatMaxWidth {
			t.Fatalf("the chat column stays at most %v: %v", chatMaxWidth, w)
		}
	})
}
