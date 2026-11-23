package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// boxTestRole: a stand-in for the Quill operator's role (sub E adds the real one).
func boxTestRole() *assistRole {
	return &assistRole{id: roleQuill, name: "quill-op-test", label: "Quill operator", hidden: roleQuill}
}

// testBoxView registers plain content with its own entry for role.
func testBoxView(h *harness, role, title string) *widget.Entry {
	in := widget.NewEntry()
	h.ui(func() {
		h.d.setAssistBoxView(role, &assistBoxView{title: title, content: in, input: in})
	})
	return in
}

func absRect(o fyne.CanvasObject) (fyne.Position, fyne.Size) {
	return fyne.CurrentApp().Driver().AbsolutePositionForObject(o), o.Size()
}

func TestAssistBoxOpenCloseSwitch(t *testing.T) {
	h := newHarness(t)
	kin := testBoxView(h, boxKmem, "kmem assistant")
	h.ui(func() {
		d := h.d
		b := d.box()
		if d.assistBoxRole() != "" || b.card.Visible() {
			t.Fatal("closed at start")
		}
		d.toggleAssistBox(boxQuill)
		if d.assistBoxRole() != boxQuill || !d.assistOverlayOpen() || len(b.layer.Objects) != 1 || !b.card.Visible() {
			t.Fatal("the Quill button opens the overlay")
		}
		if b.title.Text != "Quill operator" || d.quillChat == nil || b.body.Objects[0] != d.quillChat.root {
			t.Fatal("the Quill button opens the operator's chat")
		}
		d.toggleAssistBox(boxKmem)
		if d.assistBoxRole() != boxKmem || b.title.Text != "kmem assistant" || b.body.Objects[0] != kin {
			t.Fatal("the other button switches the content")
		}
		if d.win.Canvas().Focused() != d.input || d.input.PlaceHolder != "Message the kmem assistant" {
			t.Fatal("over the chat, opening focuses the main message box, which messages the assistant")
		}
		d.toggleAssistBox(boxKmem)
		if d.assistBoxRole() != "" || b.card.Visible() || len(b.layer.Objects)+len(b.panel.Objects) != 0 {
			t.Fatal("the same button closes it")
		}
		if d.win.Canvas().Focused() != d.input {
			t.Fatal("closing gives the focus back to the main message box")
		}
		d.openAssistBox(boxKmem)
		d.closeAssistBox()
		d.closeAssistBox() // twice is harmless
		if d.assistBoxRole() != "" {
			t.Fatal("× closes it")
		}
	})
}

func boxHasLabel(o fyne.CanvasObject, text string) bool {
	found := false
	walk(o, func(c fyne.CanvasObject) {
		if l, ok := c.(*widget.Label); ok && l.Text == text {
			found = true
		}
	})
	return found
}

func TestAssistBoxOverlayCoversHistoryOnly(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		d.openAssistBox(boxQuill)
		cp, cs := absRect(d.box().card)
		hp, hs := absRect(d.chatHost)
		tp, ts := absRect(d.tabRow)
		ip, _ := absRect(d.bottomBox)
		if cs.Width < hs.Width-2*boxInset-1 || cs.Height < hs.Height-2*boxInset-1 {
			t.Fatalf("the box fills the history: %v vs %v", cs, hs)
		}
		if cp.X < hp.X || cp.Y < hp.Y || cp.Y+cs.Height > hp.Y+hs.Height {
			t.Fatalf("inside the history: %v %v / %v %v", cp, cs, hp, hs)
		}
		if cp.Y < tp.Y+ts.Height || cp.Y+cs.Height > ip.Y {
			t.Fatalf("the tab row and the main message box stay visible: box %v+%v tabs %v+%v input %v", cp, cs, tp, ts, ip)
		}
	})
}

func TestAssistBoxOwnMessageBox(t *testing.T) {
	h := newHarness(t)
	r := boxTestRole()
	var c *assistChat
	var s *deskSession
	h.ui(func() {
		d := h.d
		c = d.attachAssistBox(r, "Quill operator", "Message the Quill operator")
		a := d.assistFor(r)
		s = d.addSession(h.proj, "", "default", false)
		d.markHiddenAs(s, roleQuill)
		a.s, a.state = s, asLive
		d.sendTo(s, "hello")
	})
	h.eventually("the role's session runs", func() bool { return s.running() && !s.conv.Busy && !s.inTurn })
	h.ui(func() {
		d := h.d
		d.input.SetText("main draft")
		d.openAssistBox(boxQuill)
		if d.box().body.Objects[0] != c.root || c.view.s != s || d.hostView(s) != c.view || !d.hostShown(s) {
			t.Fatal("the role's chat shows in the box and is its hidden host")
		}
		// Over the chat: no message box of its own; the main one messages it.
		if c.inRow.Visible() || d.input.PlaceHolder != "Message the Quill operator" || d.win.Canvas().Focused() != d.input || d.input.Text != "" {
			t.Fatalf("over the chat, the main message box messages the operator: %v %q %v %q", c.inRow.Visible(), d.input.PlaceHolder, d.win.Canvas().Focused() == d.input, d.input.Text)
		}
		if !d.targetLbl.Visible() || !strings.Contains(d.targetLbl.Text, "Quill operator") {
			t.Fatal("the main box says who it messages")
		}
		d.input.SetText("overlay message")
		d.input.onSend(d.input.Text)
		if lastUser(s) != "overlay message" || d.input.Text != "" || d.current == s {
			t.Fatalf("sent to the role's session: %q", lastUser(s))
		}
	})
	h.eventually("the overlay message's reply", func() bool { return !s.conv.Busy && !s.inTurn })
	h.ui(func() {
		d := h.d
		// Docked: its own message box, the main one stays the tab's.
		d.setAssistDock(boxQuill, true)
		if !d.assistBoxDocked() || !c.inRow.Visible() || d.input.Text != "main draft" || d.input.PlaceHolder != "Message Claude" || d.targetLbl.Visible() {
			t.Fatal("docked: its own message box; the main one has the tab's draft back")
		}
		if c.input.PlaceHolder != "Message the Quill operator" || d.win.Canvas().Focused() != c.input {
			t.Fatal("its own message box, focused")
		}
		c.input.SetText("box message")
		c.input.onSend(c.input.Text)
		if lastUser(s) != "box message" || c.input.Text != "" {
			t.Fatalf("sent to the role's session: %q", lastUser(s))
		}
		if d.input.Text != "main draft" || d.current == s {
			t.Fatal("the main message box keeps its draft and tab")
		}
		d.setAssistDock(boxQuill, false)
		if d.box().status.Text == "" {
			t.Fatal("the header shows the status")
		}
	})
	h.eventually("the reply", func() bool { return !s.conv.Busy && !s.inTurn })
	h.ui(func() {
		d := h.d
		// Dot and badge from the role's state.
		if d.assistDotState(roleQuill) != 1 {
			t.Fatal("idle dot while it runs")
		}
		bt := d.box().btns[boxQuill]
		d.syncAssistButtons()
		if !bt.dot.Visible() || !bt.sideDot.Visible() || bt.dot.FillColor != colIdle {
			t.Fatal("the idle dot on both buttons")
		}
		s.inTurn = true
		d.syncAssistButtons()
		if d.assistDotState(roleQuill) != 2 || (bt.dot.FillColor != colRunning && bt.dot.FillColor != fade(colRunning, 0.45)) {
			t.Fatal("the working dot (pulsing)")
		}
		s.inTurn = false
		d.assistBubble(roleQuill).set(3)
		d.syncAssistButtons()
		if !bt.sideBub.box.Visible() || bt.sideBub.text.Text != "3" {
			t.Fatal("the portrait button mirrors the badge")
		}
		d.assistBubble(roleQuill).set(0)
		d.syncAssistButtons()
		if bt.sideBub.box.Visible() {
			t.Fatal("the badge clears")
		}
		d.closeAssistBox()
		if d.hostShown(s) || !s.running() {
			t.Fatal("closing hides; the assistant keeps running")
		}
	})
}

func TestAssistBoxDockRemembered(t *testing.T) {
	h := newHarness(t)
	testBoxView(h, boxKmem, "kmem assistant")
	h.ui(func() {
		d := h.d
		b := d.box()
		d.openAssistBox(boxQuill)
		d.setAssistDock(boxQuill, true)
		if !d.assistBoxDocked() || len(b.panel.Objects) != 1 || len(b.layer.Objects) != 0 || !b.panel.Visible() {
			t.Fatal("Dock moves the box to the side panel")
		}
		if mw, rw := d.mainPane.Size().Width, d.mainRoot.Size().Width; mw >= rw || rw-mw < dockMinW {
			t.Fatalf("the chat column narrows: %v of %v", mw, rw)
		}
		if b.dockBtn.Text != "Undock" {
			t.Fatal("the button undocks")
		}
		d.closeAssistBox()
		if d.mainPane.Size().Width != d.mainRoot.Size().Width || b.panel.Visible() {
			t.Fatal("closing gives the chat its width back")
		}
		d.openAssistBox(boxQuill)
		if !d.assistBoxDocked() {
			t.Fatal("Quill opens docked again (remembered)")
		}
		d.openAssistBox(boxKmem)
		if d.assistBoxDocked() || len(b.layer.Objects) != 1 {
			t.Fatal("kmem keeps its own state: overlay")
		}
		d.setAssistDock(boxKmem, true)
		d.setAssistDock(boxKmem, false)
		if d.assistBoxDocked() || d.app.Preferences().Bool(prefAssistDock+boxKmem) || !d.app.Preferences().Bool(prefAssistDock+boxQuill) {
			t.Fatal("undock is remembered per assistant")
		}
		// A docked panel stays open across a tab switch.
		d.openAssistBox(boxQuill)
		d.tabNumberKey(1)
		if d.assistBoxRole() != boxQuill {
			t.Fatal("docked: stays open on a tab switch")
		}
	})
	// Too narrow: the docked choice falls back to the overlay.
	h.ui(func() { h.w.Resize(fyne.NewSize(760, 700)) })
	h.ui(func() {
		d := h.d
		d.layoutRoot()
		if d.assistBoxDocked() || len(d.box().layer.Objects) != 1 || !d.box().note.Visible() {
			t.Fatal("too narrow: shown over the chat, with a note")
		}
		if !d.app.Preferences().Bool(prefAssistDock + boxQuill) {
			t.Fatal("the choice stays")
		}
	})
	h.ui(func() { h.w.Resize(fyne.NewSize(1400, 860)) })
	h.ui(func() {
		h.d.layoutRoot()
		if !h.d.assistBoxDocked() {
			t.Fatal("wider again: docked")
		}
	})
}

func TestAssistBoxSplitOneBoxOverAllPanes(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		d.setSplit(2)
		d.openAssistBox(boxQuill)
		cp, cs := absRect(d.box().card)
		p0, s0 := absRect(d.panes[0].center)
		p1, s1 := absRect(d.panes[1].center)
		in0, _ := absRect(d.panes[0].input)
		if cp.X > p0.X+1 || cp.X+cs.Width < p1.X+s1.Width-1 {
			t.Fatalf("one box over both panes: box %v+%v panes %v / %v+%v", cp, cs, p0, p1, s1)
		}
		if cp.Y+cs.Height > p0.Y+s0.Height+1 || cp.Y+cs.Height > in0.Y {
			t.Fatal("the panes' message boxes stay visible")
		}
		d.setAssistDock(boxQuill, true)
		if !d.assistBoxDocked() {
			t.Fatal("two panes fit next to the panel at this width")
		}
		d.setSplit(3)
		if d.assistBoxDocked() {
			t.Fatal("three panes don't: the overlay")
		}
	})
}

func TestAssistBoxShortcutsEscapeAndTabs(t *testing.T) {
	h := newHarness(t)
	testBoxView(h, boxKmem, "kmem assistant")
	h.ui(func() {
		d := h.d
		if !d.handleShortcut(quillBoxShortcut) || d.assistBoxRole() != boxQuill {
			t.Fatal("Ctrl+Shift+O opens Quill")
		}
		d.handleShortcut(kmemBoxShortcut)
		if d.assistBoxRole() != boxKmem {
			t.Fatal("Ctrl+Shift+K switches to kmem")
		}
		d.handleShortcut(kmemBoxShortcut)
		if d.assistBoxRole() != "" {
			t.Fatal("Ctrl+Shift+K closes kmem")
		}
		// Escape in the box closes it, never interrupting.
		d.openAssistBox(boxKmem)
		if !d.boxFocused() || !d.escape(d.current) || d.assistBoxRole() != "" {
			t.Fatal("Escape with the box focused closes the box")
		}
		d.openAssistBox(boxQuill)
		d.win.Canvas().Unfocus()
		if !d.canvasEscape() || d.assistBoxRole() != "" {
			t.Fatal("Escape over the overlay closes it")
		}
		// A tab click closes the overlay, then switches.
		d.openAssistBox(boxQuill)
		d.open(editsID)
		if d.assistBoxRole() != "" || d.current == nil || d.current.id != editsID {
			t.Fatal("a tab closes the box, then switches")
		}
		d.openAssistBox(boxQuill)
		d.tabNumberKey(1)
		if d.assistBoxRole() != "" {
			t.Fatal("Ctrl+1-9 closes the box")
		}
	})
}

func TestAssistBoxAnimationLength(t *testing.T) {
	h := newHarness(t)
	old := systemReduceMotion
	sys := false
	systemReduceMotion = func() bool { return sys }
	t.Cleanup(func() { systemReduceMotion = old })
	h.ui(func() {
		d := h.d
		p := d.app.Preferences()
		if d.assistBoxDuration() != 0 {
			t.Fatal("no animation without d.animate")
		}
		d.animate = true
		defer func() { d.animate = false }()
		if d.assistBoxDuration() != 220*time.Millisecond {
			t.Fatal("Normal by default")
		}
		p.SetString(prefAnimateSpeed, "slow") // Text animation speed: not this setting
		p.SetString(prefBoxAnim, "fast")
		if d.assistBoxDuration() != 140*time.Millisecond {
			t.Fatal("its own setting")
		}
		p.SetString(prefBoxAnim, "slow")
		if d.assistBoxDuration() != 320*time.Millisecond {
			t.Fatal("Slow")
		}
		p.SetString(prefBoxAnim, "off")
		if d.assistBoxDuration() != 0 {
			t.Fatal("Off")
		}
		p.SetString(prefBoxAnim, "normal")
		sys = true
		if !d.reduceMotion() || d.assistBoxDuration() != 0 {
			t.Fatal("Reduce motion follows the system")
		}
		setUserPref(p, prefReduceMotion, false)
		if d.reduceMotion() || d.assistBoxDuration() != 220*time.Millisecond {
			t.Fatal("the user's choice wins over the system")
		}
		setUserPref(p, prefReduceMotion, true)
		d.openAssistBox(boxQuill)
		if d.box().lastDur != 0 || !d.box().card.Visible() {
			t.Fatal("Reduce motion: the box appears at once")
		}
		d.closeAssistBox()
		if d.box().lastDur != 0 || d.box().card.Visible() {
			t.Fatal("and goes at once")
		}
	})
}

func TestSystemReduceMotionKDE(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_CURRENT_DESKTOP", "KDE")
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "kdeglobals"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[General]\nAnimationDurationFactor=0\n[KDE]\nAnimationDurationFactor=0\n")
	if !readSystemReduceMotion() {
		t.Fatal("KDE animation speed instant")
	}
	write("[KDE]\nAnimationDurationFactor=0.5\n")
	if readSystemReduceMotion() {
		t.Fatal("KDE animations on")
	}
}

func TestAssistBoxShots(t *testing.T) {
	dir := os.Getenv("QUILL_BOX_SHOTS")
	if dir == "" {
		t.Skip("set QUILL_BOX_SHOTS=dir")
	}
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	r := boxTestRole()
	h.ui(func() {
		h.d.attachAssistBox(r, "Quill operator", "Message the Quill operator")
		h.d.open(editsID)
	})
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { h.d.openAssistBox(boxQuill) })
	h.ui(func() { shot(t, h.w, dir, "overlay.png") })
	h.ui(func() { h.d.setAssistDock(boxQuill, true) })
	h.ui(func() { shot(t, h.w, dir, "docked.png") })
	h.ui(func() {
		h.d.setAssistDock(boxQuill, false)
		h.d.closeAssistBox()
		h.d.setSplit(2)
		h.d.openAssistBox(boxQuill)
	})
	h.ui(func() { shot(t, h.w, dir, "split.png") })
}
