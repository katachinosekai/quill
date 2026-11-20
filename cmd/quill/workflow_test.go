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
