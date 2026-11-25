package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func undoButtons(o fyne.CanvasObject) []*widget.Button {
	var out []*widget.Button
	walk(o, func(c fyne.CanvasObject) {
		if b, ok := c.(*widget.Button); ok && b.Text == "Undo" {
			out = append(out, b)
		}
	})
	return out
}

func TestQuillButtonOpensOperatorChat(t *testing.T) {
	h, _ := quillSetup(t)
	h.ui(func() {
		d := h.d
		d.toggleAssistBox(boxQuill)
		c := d.quillChat
		if c == nil || d.assistBoxRole() != boxQuill || d.box().body.Objects[0] != c.root {
			t.Fatal("the Quill button opens the operator's chat")
		}
		if d.win.Canvas().Focused() != d.input || c.inRow.Visible() || d.input.hint != "Message the Quill operator" {
			t.Fatal("over the chat: no message box of its own; the main one messages the operator")
		}
		if d.hostFor(roleQuill).open == nil {
			t.Fatal("the role's host is the box")
		}
	})
}

func TestQuillBadgeDotAndStatusFollowRole(t *testing.T) {
	h, _ := quillSetup(t)
	_, s := bootQuill(h)
	h.ui(func() {
		d := h.d
		bt := d.assistButtonFor(boxQuill)
		if !bt.dot.Visible() || d.assistDotState(roleQuill) != 1 {
			t.Fatal("running idle: the dot shows")
		}
		if d.assist() != nil && d.assistDotState(roleKmem) == 2 {
			t.Fatal("kmem's dot is its own")
		}
		s.inTurn = true
		if d.assistDotState(roleQuill) != 2 {
			t.Fatal("working")
		}
		d.syncAssistButtons()
		if bt.dot.FillColor == colIdle {
			t.Fatal("the dot shows working")
		}
		s.inTurn = false
		d.setKmemNeeds(parseKmemNeeds("", []kmemTask{{ID: "t1", Status: "open", Title: "quill issue: x", Topic: "quill"}}))
		if b := d.assistBubble(roleQuill); !b.box.Visible() || b.text.Text != "1" {
			t.Fatal("the Quill button's badge counts the issue")
		}
		d.openAssistBox(boxQuill)
		d.quillChat.view.setStatus(s)
		if !strings.HasPrefix(d.quillChat.view.live.Text, "Ready") { // the main chat's indicator (t9a3a6a)
			t.Fatalf("status line: %q", d.quillChat.view.live.Text)
		}
		if d.assistBubble(roleQuill).box.Visible() {
			t.Fatal("seen once the box is open")
		}
	})
}

func TestQuillSettingsChangeCardUndo(t *testing.T) {
	h, _ := quillSetup(t)
	_, op := bootQuill(h)
	h.ui(func() {
		d := h.d
		d.openAssistBox(boxQuill)
		p := d.app.Preferences()
		c1, err := d.changeSetting(op, "animateSpeed", "fast", "make it faster", byOperator)
		if err != nil {
			t.Fatal(err)
		}
		d.quillChat.view.update(op.conv.Items)
		bs := undoButtons(d.quillChat.view.root)
		if len(bs) != 1 || bs[0].Disabled() {
			t.Fatalf("a card with an enabled Undo: %d", len(bs))
		}
		var txt []string
		walk(d.quillChat.view.root, func(o fyne.CanvasObject) {
			if l, ok := o.(*widget.Label); ok {
				txt = append(txt, l.Text)
			}
		})
		all := strings.Join(txt, "|")
		for _, want := range []string{"normal → fast", "make it faster", "Change 1"} {
			if !strings.Contains(all, want) {
				t.Fatalf("card lacks %q: %s", want, all)
			}
		}
		bs[0].OnTapped()
		if p.String(prefAnimateSpeed) != "normal" || !bs[0].Disabled() {
			t.Fatal("Undo restores the value and disables the card")
		}
		// A later change disables the older card.
		c2, _ := d.changeSetting(op, "animateSpeed", "slow", "slower", byOperator)
		c3, _ := d.changeSetting(op, "animateSpeed", "fast", "faster", byOperator)
		d.quillChat.view.update(op.conv.Items)
		bs = undoButtons(d.quillChat.view.root)
		if c2.N == 0 || c3.N == 0 || len(bs) != 3 {
			t.Fatalf("cards: %d", len(bs))
		}
		if !bs[1].Disabled() || bs[2].Disabled() {
			t.Fatal("only the latest change of the setting can be undone")
		}
		_ = c1
		p.SetString(prefAnimateSpeed, "off") // the user, in Settings
		d.syncSettingCards()
		if !bs[2].Disabled() {
			t.Fatal("changed in Settings: disabled")
		}
	})
}

func TestHiddenIDOpensItsOwnBox(t *testing.T) {
	h, _ := quillSetup(t)
	_, op := bootQuill(h)
	h.ui(func() {
		d := h.d
		d.open(op.id)
		if d.assistBoxRole() != boxQuill {
			t.Fatalf("the operator's id opens the Quill box, not %q", d.assistBoxRole())
		}
		d.closeAssistBox()
		k := d.createSession(h.proj, "default", "default")
		d.markHiddenAs(k, roleKmem)
		d.open(k.id)
		if d.assistBoxRole() == boxQuill {
			t.Fatal("a kmem id doesn't open the Quill box")
		}
		d.openHiddenRole(roleQuill)
		if d.assistBoxRole() != boxQuill {
			t.Fatal("openHiddenRole(quill)")
		}
		if got := d.processOptions(op).Name; got != quillOpName {
			t.Fatalf("peer name %q", got)
		}
		d.refuseSecondSession("quill", "test", op)
		if d.assistBoxRole() != boxQuill {
			t.Fatal("a refused second session brings the operator's box forward")
		}
	})
}

func TestQuillMoveItHereOnlyForRunningTab(t *testing.T) {
	h, _ := quillSetup(t)
	o := topicSession(h, "quill")
	h.ui(func() {
		d := h.d
		a := d.quillAssist()
		d.assistBoot(a)
		d.openAssistBox(boxQuill)
		c := d.quillChat
		if a.state != asElsewhere || !c.reasonBox.Visible() || !c.reasonBtn.Visible() || c.reasonBtn.Text != "Move it here" {
			t.Fatalf("a running tab: Move it here in the box (%v %q)", a.state, c.reasonBtn.Text)
		}
		if !strings.Contains(c.reasonLbl.Text, "open in the tab") {
			t.Fatal("the reason line")
		}
		c.reasonBtn.OnTapped()
		if a.s != o || a.state != asLive || c.reasonBox.Visible() || hiddenRole(o) != roleQuill {
			t.Fatal("moved in; the strip is gone")
		}
		if c.view.s != o {
			t.Fatal("the box shows the moved session")
		}
	})
	// Not running: no "Move it here".
	h2, _ := quillSetup(t)
	h2.ui(func() {
		d := h2.d
		s := d.createSession(h2.proj, "default", "default")
		d.org.Kmem[s.id] = "quill"
		a := d.quillAssist()
		quillOpElsewhere(d, a, s)
		if a.button == "Move it here" || a.button == "" {
			t.Fatalf("not running: %q", a.button)
		}
	})
}

func TestQuillBoxShots(t *testing.T) {
	dir := os.Getenv("QUILL_BOX_SHOTS")
	if dir == "" {
		t.Skip("set QUILL_BOX_SHOTS=dir")
	}
	os.MkdirAll(dir, 0o755)
	h, _ := quillSetup(t)
	_, op := bootQuill(h)
	h.ui(func() {
		h.d.open(editsID)
		h.d.openAssistBox(boxQuill)
		h.d.changeSetting(op, "animateSpeed", "fast", "make the animation faster", byOperator)
		h.d.changeSetting(op, "showMsgTimes", "off", "hide the message times", byOperator)
		h.d.quillChat.view.update(op.conv.Items)
	})
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { shot(t, h.w, dir, "quill-change-card.png") })
}

func TestQuillOperatorFollowsAndJumpsToLatest(t *testing.T) {
	for _, role := range []string{roleQuill, roleCodexQuill} {
		t.Run(role, func(t *testing.T) {
			h := newDeskHarness(t, false)
			h.ui(func() {
				d := h.d
				d.animate = false
				if role == roleCodexQuill {
					d.codexAssistant(codexQuillRole)
				}
				a := d.assistByRole(role)
				session := d.addSessionWithBackend(h.proj, "default", "default", false, role == roleCodexQuill)
				d.markHiddenAs(session, role)
				a.s = session
				for range 35 {
					session.conv.AddUser("A message from earlier in the operator chat.")
				}
				d.openAssistBox(role)
				view := d.hostFor(role).view()
				d.syncVisibleChatFollow()
				if !view.pinned() || view.jump.Visible() {
					t.Fatal("the open operator should start at the latest message")
				}

				item := session.conv.AddNotice("Replying")
				item.Kind, item.Streaming = chat.Assistant, true
				for _, text := range []string{"Replying", strings.Repeat("The assistant's reply has several lines.\n", 35)} {
					item.Text = text
					d.chatFor(session, []*chat.Item{item})
				}
				late := canvas.NewRectangle(colCard)
				late.SetMinSize(fyne.NewSize(1, 500))
				view.box.Add(late)
				d.syncVisibleChatFollow()
				if !view.pinned() || view.jump.Visible() {
					t.Fatal("the operator follow guard should recover when streaming content grows after layout")
				}

				view.scroll.Offset = fyne.NewPos(0, 0)
				view.scroll.OnScrolled(view.scroll.Offset)
				d.chatFor(session, []*chat.Item{session.conv.AddUser("Another new message")})
				if view.follow || !view.jump.Visible() {
					t.Fatalf("history mode after scrolling: follow=%v arrow=%v offset=%v lastY=%v viewport=%v", view.follow, view.jump.Visible(), view.scroll.Offset, view.lastY, view.scroll.Size())
				}
				d.closeAssistBox()
				d.openAssistBox(role)
				d.syncVisibleChatFollow()
				if view.scroll.Offset.Y != 0 || !view.jump.Visible() || view.follow {
					t.Fatalf("reopening should preserve history: follow=%v arrow=%v offset=%v lastY=%v viewport=%v", view.follow, view.jump.Visible(), view.scroll.Offset, view.lastY, view.scroll.Size())
				}
				var tap *jumpTap
				deepWalk(view.jump, func(o fyne.CanvasObject) {
					if button, ok := o.(*jumpTap); ok {
						tap = button
					}
				})
				if tap == nil {
					t.Fatal("the visible arrow should have a tap target")
				}
				tap.Tapped(nil)
				if !view.follow || !view.pinned() || view.jump.Visible() {
					t.Fatal("the arrow should jump to the bottom and resume following")
				}
			})
		})
	}
}

func TestPortraitComposerUsesSidebarActionButtons(t *testing.T) {
	h := newDeskHarness(t, false)
	h.ui(func() {
		d := h.d
		for _, split := range []int{1, 2} {
			d.setSplit(split)
			for _, mode := range []string{layoutVertical, layoutHorizontal} {
				d.setLayoutMode(mode)
				wantToolbar := mode == layoutHorizontal
				for _, o := range []fyne.CanvasObject{d.settingsBtn, d.quillBtnBox, d.kmemBtnBox} {
					if o.Visible() != wantToolbar {
						t.Fatalf("split %d, %s: incorrect composer toolbar visibility for %T", split, mode, o)
					}
				}
				if !wantToolbar && (d.sideOff || len(d.settingsSlot.Objects) != 1 ||
					!d.assistButtonFor(boxQuill).side.Visible() || !d.assistButtonFor(boxKmem).side.Visible()) {
					t.Fatalf("split %d: portrait actions should be accessible from the sidebar", split)
				}
			}
		}
	})
}
