package main

import (
	"fyne.io/fyne/v2/widget"
	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func TestFeatureWorkflow(t *testing.T) {
	h := newHarness(t)
	check := func(step string) {
		t.Helper()
		if h.d.current != nil && h.d.session(h.d.current.id) == nil {
			t.Fatalf("%s: current session vanished", step)
		}
		if h.d.split() && h.d.chat != h.d.focused.view {
			t.Fatalf("%s: chat is not the focused pane", step)
		}
		if !h.d.split() && h.d.chat != h.d.single {
			t.Fatalf("%s: single view lost", step)
		}
		seen := map[string]bool{}
		for _, id := range h.d.tabs {
			if seen[id] {
				t.Fatalf("%s: duplicate tab %s", step, id)
			}
			seen[id] = true
		}
	}
	var ids []string
	h.ui(func() {
		for i := 0; i < 4; i++ {
			h.d.newSessionDialog()
			ids = append(ids, h.d.current.id)
		}
		check("new sessions")
		h.d.openInSplit(ids[0])
		h.d.openInSplit(ids[1])
		check("split")
		h.d.focusPane(h.d.panes[0])
		h.d.open(ids[2])
		check("open in focused pane")
		h.d.tabChips[ids[3]].MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonTertiary})
		check("middle click")
		h.d.setLayoutMode(layoutVertical)
		h.d.setLayoutMode(layoutHorizontal)
		check("layout switch")
		h.d.setSplit(1)
		check("back to single")
		h.d.setShare("chatShare", 1)
		h.d.setShare("inputShare", minInputShare)
		h.d.setCenter(true)
		h.d.setInputCenter(true)
		h.d.setStatusCenter(true)
		h.d.setTabCenter(true)
		check("settings")
		h.d.relaunchSession(ids[2])
		check("relaunch")
		h.d.app.Preferences().SetBool("skipDeleteWarning", true)
		h.d.askDelete(ids[1])
		check("delete")
		h.d.setSplit(3)
		h.d.closePane(h.d.panes[1])
		h.d.closePane(h.d.panes[0])
		check("close panes")
		h.w.Resize(fyne.NewSize(900, 700))
		h.w.Resize(fyne.NewSize(1400, 900))
		check("resize")
	})
}

func TestCtrlTOpensNewSession(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.setupShortcuts()
		before := len(h.d.sessions)
		h.d.input.TypedShortcut(newTabShortcut)
		if len(h.d.sessions) != before+1 || len(h.d.tabs) == 0 || h.d.tabs[len(h.d.tabs)-1] != h.d.current.id {
			t.Fatal("Ctrl+T in the message box opens a new session in a new tab")
		}
	})
}

func TestPlusFollowsLastTab(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.newSessionDialog()
		h.d.newSessionDialog()
		h.d.strip.box.Refresh()
		last := h.d.tabChips[h.d.tabs[len(h.d.tabs)-1]]
		gap := h.d.strip.plus.Position().X - (last.Position().X + last.Size().Width)
		if gap < 0 || gap > tabGap+0.5 {
			t.Fatalf("+ should sit right after the last tab with tab spacing, gap %.1f", gap)
		}
	})
}

func TestCtrlNumberAndEmptySessions(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.setupShortcuts()
		var ids []string
		for i := 0; i < 3; i++ {
			h.d.newSessionDialog()
			ids = append(ids, h.d.current.id)
			h.d.current.conv.AddUser("hi")
		}
		h.d.input.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.Key1, Modifier: fyne.KeyModifierShortcutDefault})
		if h.d.current.id != h.d.tabs[0] {
			t.Fatal("Ctrl+1 opens the first tab")
		}
		h.d.input.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.Key9, Modifier: fyne.KeyModifierShortcutDefault})
		if h.d.current.id != h.d.tabs[len(h.d.tabs)-1] {
			t.Fatal("Ctrl+9 opens the last tab")
		}

		h.d.newSessionDialog()
		empty := h.d.current.id
		h.d.closeTab(empty)
		if h.d.session(empty) != nil || h.d.org.Titles[empty] != "" {
			t.Fatal("closing an empty session deletes it")
		}
		if h.d.session(ids[0]) == nil {
			t.Fatal("sessions with messages are kept")
		}

		h.d.newSessionDialog()
		drafted := h.d.current.id
		h.d.input.SetText("unsent idea")
		h.d.open(ids[0])
		h.d.closeTab(drafted)
		if h.d.session(drafted) == nil {
			t.Fatal("a session with an unsent draft is not empty")
		}

		h.d.newSessionDialog()
		e1 := h.d.current.id
		h.d.newSessionDialog()
		e2 := h.d.current.id
		h.d.open(ids[1])
		if n := h.d.deleteEmptySessions(); n < 2 || h.d.session(e1) != nil || h.d.session(e2) != nil {
			t.Fatalf("Delete empty sessions removes them all, removed %d", n)
		}
		for _, id := range h.d.tabs {
			if id == e1 || id == e2 {
				t.Fatal("their tabs go too")
			}
		}

		h.d.newSessionDialog()
		only := h.d.current.id
		h.d.tabs = []string{only}
		h.d.closeTab(only)
		if h.d.current != nil || len(h.d.tabs) != 0 {
			t.Fatal("closing the last, empty tab leaves an empty view")
		}

		h.d.newSessionDialog()
		inPane := h.d.current.id
		h.d.openInSplit(ids[0])
		h.d.closeTab(inPane)
		for _, p := range h.d.panes {
			if p.view.s != nil && p.view.s.id == inPane {
				t.Fatal("a deleted empty session leaves its split pane")
			}
		}
	})
}

func TestCtrlEnterSendsNowViaShortcut(t *testing.T) {
	var sentNow string
	e := newSendEntry(func(string) { t.Fatal("Ctrl+Enter must use send-now") })
	e.onSendNow = func(text string) { sentNow = text }
	e.SetText("stop and do this")
	e.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyReturn, Modifier: fyne.KeyModifierControl})
	if sentNow != "stop and do this" {
		t.Fatal("Fyne delivers Ctrl+Enter as a shortcut; it must send right away")
	}
	before := e.Text
	e.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEnter, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift})
	if e.Text != before {
		t.Fatal("other Enter combinations must not change the text")
	}
}

func TestKeyCombinationsLikeTheRealWindow(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.setupShortcuts()
		var sent, sentNow []string
		e := newSendEntry(func(s string) { sent = append(sent, s) })
		e.onSendNow = func(s string) { sentNow = append(sentNow, s) }
		e.onShortcut = h.d.handleShortcut
		e.SetText("line one")
		e.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
		e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		e.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
		if len(sent) != 0 || !strings.Contains(e.Text, "\n") {
			t.Fatalf("Shift+Enter adds a new line, text %q", e.Text)
		}
		e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		if len(sent) != 1 {
			t.Fatal("Enter sends")
		}
		e.SetText("urgent")
		e.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyEnter, Modifier: fyne.KeyModifierControl})
		if len(sentNow) != 1 {
			t.Fatal("Ctrl+Enter (keypad) sends now")
		}
		before := len(h.d.sessions)
		e.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyT, Modifier: fyne.KeyModifierControl})
		if len(h.d.sessions) != before+1 {
			t.Fatal("Ctrl+T works from any message box")
		}
		e.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyReturn, Modifier: fyne.KeyModifierAlt})
		if len(sent) != 1 || len(sentNow) != 1 {
			t.Fatal("Alt+Enter does nothing")
		}
	})
}

func TestLimitBannerAndBlockedCard(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "auto")
		h.d.setSplit(2)
		h.d.setRateLimit(&protocol.RateLimit{Status: "rejected", Type: "five_hour", Utilization: 1, ResetsAt: time.Now().Add(time.Hour).Unix()})
		for _, p := range h.d.panes {
			if !p.view.limit.Visible() {
				t.Fatal("every split pane shows the limit banner")
			}
		}
		h.d.setSplit(1)
		if !h.d.single.limit.Visible() || !strings.Contains(h.d.single.limitText.Text, "Usage limit reached") {
			t.Fatal("the single view shows the limit banner too")
		}
		h.d.setRateLimit(&protocol.RateLimit{Status: "allowed", Type: "five_hour", Utilization: 0.2, ResetsAt: time.Now().Add(time.Hour).Unix()})
		if h.d.single.limit.Visible() {
			t.Fatal("the banner goes away when usage is allowed again")
		}

		blocked := &chat.Item{Seq: 777, Kind: chat.Tool, ToolName: "Bash", HasResult: true, ResultError: true,
			Input:  []byte(`{"command":"git push origin master"}`),
			Result: "Permission for this action was denied by the Claude Code auto mode classifier. Reason: [Destructive or Unwanted Actions]."}
		if !autoBlocked(blocked) || blockedCommand(blocked) != "git push origin master" {
			t.Fatal("auto-mode denials are recognized with their command")
		}
		h.d.chat.show(s)
		card := h.d.chat.blockedCard(blocked)
		found := false
		walk(card, func(o fyne.CanvasObject) {
			if l, ok := o.(*widget.Label); ok && strings.Contains(l.Text, "Destructive or Unwanted Actions") {
				found = true
			}
		})
		if !found {
			t.Fatal("the card names the reason")
		}
		h.d.retryBlocked(s, blocked)
		if s.mode != "default" || s.restoreMode != "auto" {
			t.Fatalf("retry runs with normal approval prompts, mode %q restore %q", s.mode, s.restoreMode)
		}
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		if s.mode != "auto" || s.restoreMode != "" {
			t.Fatal("auto mode comes back after the retry finishes")
		}
	})
}

func TestChipsHiddenUntilOpened(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		tool := &chat.Item{Seq: 900, Kind: chat.Tool, ToolName: "Bash", HasResult: true, Input: []byte(`{"command":"ls"}`)}
		s.conv.Items = append(s.conv.Items, tool)
		h.d.chat.show(s)
		count := func() int {
			n := 0
			walk(h.d.chat.stepRun(tool), func(o fyne.CanvasObject) {
				if _, ok := o.(*tapRow); ok {
					n++
				}
			})
			return n
		}
		if !h.d.hideChips || count() != 0 {
			t.Fatal("chips are hidden by default")
		}
		h.d.setAllChips(true)
		if h.d.hideChips || count() == 0 {
			t.Fatal("Open chips shows them")
		}
		h.d.setAllChips(false)
		if !h.d.hideChips || count() != 0 {
			t.Fatal("Collapse chips hides them again")
		}
	})
}

func TestQueueAndHistory(t *testing.T) {
	h := newHarness(t)
	h.d.do = func(f func()) {
		go func() {
			uiMu.Lock()
			defer uiMu.Unlock()
			f()
		}()
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("busy", func() bool { return s.running() && (s.conv.Busy || s.proc.Busy()) })
	h.ui(func() {
		h.d.input.SetText("first follow-up")
		h.d.send("first follow-up")
		h.d.input.SetText("second follow-up")
		h.d.send("second follow-up")
		if len(s.queue) != 2 || h.d.input.Text != "" || len(h.d.chat.queueBox.Objects) != 3 {
			t.Fatalf("messages sent while busy are queued and shown: %d %q", len(s.queue), h.d.input.Text)
		}
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
		if h.d.input.Text != "second follow-up" || len(s.queue) != 1 {
			t.Fatal("Up in an empty box pulls the last queued message back for editing")
		}
		h.d.input.SetText("second, edited")
		h.d.send("second, edited")
		if len(s.queue) != 2 {
			t.Fatal("the edited message goes back in the queue")
		}
		h.d.interrupt(s)
	})
	h.eventually("queue flushed after the turn", func() bool {
		for _, it := range s.conv.Items {
			if it.Kind == chat.User && strings.Contains(it.Text, "first follow-up") && strings.Contains(it.Text, "second, edited") {
				return len(s.queue) == 0
			}
		}
		return false
	})
	h.eventually("idle", func() bool { return !s.conv.Busy })
	h.ui(func() {
		h.d.input.SetText("draft")
		h.d.input.CursorRow = 0
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
		if !strings.Contains(h.d.input.Text, "second, edited") {
			t.Fatalf("Up recalls the last sent message, got %q", h.d.input.Text)
		}
		h.d.input.CursorRow = 0
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
		if h.d.input.Text != "slow" {
			t.Fatalf("Up again goes further back, got %q", h.d.input.Text)
		}
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
		h.d.input.CursorRow = strings.Count(h.d.input.Text, "\n")
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
		if h.d.input.Text != "draft" {
			t.Fatalf("Down past the newest brings the draft back, got %q", h.d.input.Text)
		}
	})
}

func TestCtrlEnterSendsQueueNow(t *testing.T) {
	h := newHarness(t)
	h.d.do = func(f func()) {
		go func() {
			uiMu.Lock()
			defer uiMu.Unlock()
			f()
		}()
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("busy", func() bool { return s.running() && (s.conv.Busy || s.proc.Busy()) })
	h.ui(func() {
		h.d.send("queued one")
		h.d.input.SetText("and this")
		h.d.input.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyReturn, Modifier: fyne.KeyModifierControl})
		if len(s.queue) != 0 {
			t.Fatal("Ctrl+Enter takes the queue right away")
		}
	})
	h.eventually("queue and box sent together", func() bool {
		for _, it := range s.conv.Items {
			if it.Kind == chat.User && strings.Contains(it.Text, "queued one") && strings.Contains(it.Text, "and this") {
				return true
			}
		}
		return false
	})
}

func TestCtrlEnterEmptyBoxSendsQueue(t *testing.T) {
	h := newHarness(t)
	h.d.do = func(f func()) {
		go func() {
			uiMu.Lock()
			defer uiMu.Unlock()
			f()
		}()
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("busy", func() bool { return s.running() && (s.conv.Busy || s.proc.Busy()) })
	h.ui(func() {
		h.d.send("queued one")
		h.d.input.SetText("")
		h.d.input.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyReturn, Modifier: fyne.KeyModifierControl})
		if len(s.queue) != 0 {
			t.Fatal("Ctrl+Enter in an empty box sends the queued message right away")
		}
	})
	h.eventually("queued message sent", func() bool {
		for _, it := range s.conv.Items {
			if it.Kind == chat.User && it.Text == "queued one" {
				return true
			}
		}
		return false
	})
}
