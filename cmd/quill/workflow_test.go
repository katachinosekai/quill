package main

import (
	"testing"

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
