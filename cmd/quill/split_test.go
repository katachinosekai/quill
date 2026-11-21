package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

func TestSplitView(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		a.conv.AddUser("from a")
		b := h.d.createSession(h.proj, "default", "default")
		b.conv.AddUser("from b")
		h.d.open(a.id)
		h.d.openInSplit(b.id)
		if len(h.d.panes) != 2 || h.d.current != b || h.d.panes[0].view.s != a || h.d.panes[1].view.s != b {
			t.Fatalf("open in split should put b beside a: panes=%d", len(h.d.panes))
		}
		w0, w1 := h.d.panes[0].root.Size().Width, h.d.panes[1].root.Size().Width
		if w0 != w1 {
			t.Fatalf("panes must be the same width: %.0f vs %.0f", w0, w1)
		}
		it := a.conv.AddUser("arrived in the unfocused pane")
		h.d.chatFor(a, []*chat.Item{it})
		if _, ok := h.d.panes[0].view.views[it.Seq]; !ok {
			t.Fatal("unfocused panes update live")
		}
		h.d.focusPane(h.d.panes[0])
		if h.d.current != a || h.d.chat != h.d.panes[0].view {
			t.Fatal("clicking a pane focuses it")
		}
		h.d.setSplit(3)
		if len(h.d.panes) != 3 || h.d.panes[2].view.s != nil {
			t.Fatal("third pane starts empty")
		}
		h.d.focusPane(h.d.panes[2])
		h.d.open(b.id)
		if h.d.current != b || h.d.focused != h.d.panes[1] {
			t.Fatal("opening a session shown in another pane focuses that pane")
		}
		h.d.closePane(h.d.panes[2])
		h.d.closePane(h.d.panes[1])
		if h.d.split() || h.d.chat != h.d.single || h.d.current != a {
			t.Fatal("closing back to one pane returns to the single view")
		}
	})
}

func TestTabsAndBookmarks(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		b := h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.open(b.id)
		if len(h.d.tabs) < 2 || len(h.d.tabChips) < 2 {
			t.Fatalf("opening sessions adds tabs: %v", h.d.tabs)
		}
		h.d.closeTab(b.id)
		if h.d.current != a {
			t.Fatal("closing the active tab switches to a neighbour")
		}
		h.d.rebuildRows()
		if len(h.d.groups) == 0 || len(h.d.bookBar.Objects) != 2*len(h.d.groups) {
			t.Fatal("each folder gets a bookmark button")
		}
		h.d.folderMenu2(h.d.groups[0], fyne.NewPos(10, 10))
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("a folder opens as a drop-down overlay")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		test.TapSecondary(h.d.bookBar.Objects[0].(*tabChip))
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("right-clicking a folder opens its menu")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.openAllInSplit(h.d.groups[0].key)
		if !h.d.split() && len(h.d.groups[0].ids) > 1 {
			t.Fatal("Open in split view splits the folder's sessions")
		}
		h.d.setSplit(1)
	})
}

func TestDeleteSessionStaysInProjects(t *testing.T) {
	root := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	if _, err := sessionFiles(root, "../etc", ""); err == nil {
		t.Fatal("bad ids are refused")
	}
	if _, err := sessionFiles(root, id, "/etc/"+id+".jsonl"); err == nil {
		t.Fatal("paths outside the projects folder are refused")
	}
	if _, err := sessionFiles(root, id, root+"/p/other.jsonl"); err == nil {
		t.Fatal("a transcript must be named after its session")
	}
	files, err := sessionFiles(root, id, root+"/p/"+id+".jsonl")
	if err != nil || len(files) != 1 {
		t.Fatalf("files: %v %v", files, err)
	}
}

func TestDeleteSessionEndToEnd(t *testing.T) {
	h := newHarness(t)
	var path string
	h.ui(func() {
		path = h.d.infos[editsID].Path
		h.d.askDelete(editsID)
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("deleting asks first")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("nothing is deleted before confirming")
		}
		h.d.app.Preferences().SetBool("skipDeleteWarning", true)
		h.d.askDelete(editsID)
	})
	h.eventually("transcript removed", func() bool {
		_, err := os.Stat(path)
		return os.IsNotExist(err) && !hasRow(h.d.rows, editsID)
	})
}

func TestLayoutModes(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.layoutMode() != layoutHorizontal || !h.d.sideOff || !h.d.tabRow.Visible() || h.d.sideToggle.Visible() {
			t.Fatal("starts horizontal: tabs and folder bar shown, sidebar off")
		}
		h.d.showSettings()
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("settings open from the header in horizontal mode")
		}
		h.d.settingsPop.Hide()
		h.d.setLayoutMode(layoutVertical)
		if h.d.sideOff || h.d.tabRow.Visible() || h.d.bookRow.Visible() || len(h.d.settingsSlot.Objects) != 1 {
			t.Fatal("vertical: sidebar with settings, no tab or folder bar")
		}
		h.d.setLayoutMode(layoutHorizontal)
		if !h.d.sideOff || !h.d.tabRow.Visible() {
			t.Fatal("back to horizontal")
		}
	})
}

func TestRelaunchTakesOverNameAndJob(t *testing.T) {
	h := newHarness(t)
	var old, ns *deskSession
	h.ui(func() {
		old = h.d.createSession(h.proj, "sonnet", "default")
		old.conv.AddUser("build the launcher")
		old.conv.Items = append(old.conv.Items, &chat.Item{Seq: 500, Kind: chat.Assistant, Text: "Step 1 done, next is packaging"})
		h.d.org.Titles[old.id] = "PZ Launcher"
		h.d.relaunchSession(old.id)
		ns = h.d.current
		if ns == old || h.d.org.Titles[ns.id] != "PZ Launcher" || ns.model != "sonnet" || h.d.org.Place[old.id] != placeArchive {
			t.Fatalf("new session takes the name, keeps the model, old one is archived: same=%v title=%q model=%q place=%q", ns == old, h.d.org.Titles[ns.id], ns.model, h.d.org.Place[old.id])
		}
	})
	h.eventually("handoff sent", func() bool {
		for _, it := range ns.conv.Items {
			if it.Kind == chat.User && strings.Contains(it.Text, "taking over") && strings.Contains(it.Text, "next is packaging") {
				return true
			}
		}
		return false
	})
}

func TestTabDragMenusAndCentering(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		var ids []string
		for i := 0; i < 3; i++ {
			s := h.d.createSession(h.proj, "default", "default")
			h.d.open(s.id)
			ids = append(ids, s.id)
		}
		first := ids[0]
		chip := h.d.tabChips[first]
		h.d.strip.held, h.d.strip.heldID = chip, first
		h.d.strip.grab = 0
		h.d.strip.ptr = 10000
		h.d.strip.drop()
		if h.d.tabs[len(h.d.tabs)-1] != first {
			var dbg []string
			for _, id := range h.d.tabs {
				c := h.d.tabChips[id]
				p := fyne.CurrentApp().Driver().AbsolutePositionForObject(c)
				dbg = append(dbg, fmt.Sprintf("%s@%.0f+%.0f", id[:4], p.X, c.Size().Width))
			}
			t.Fatalf("dragging a tab to the end moves it there: first=%s dragX=%.0f %v", first[:4], h.d.tabDragX, dbg)
		}
		h.d.tabMenu(ids[1], fyne.NewPos(5, 5))
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("right-clicking a tab opens a menu")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.setTabCenter(true)
		if _, ok := h.d.tabCenter.Layout.(interface {
			Layout([]fyne.CanvasObject, fyne.Size)
		}); !ok || !h.d.app.Preferences().Bool("centerTabs") {
			t.Fatal("tab centering toggles")
		}
	})
}

func TestMiddleClickClosesTab(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		b := h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.open(b.id)
		chip := h.d.tabChips[a.id]
		chip.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonTertiary})
		for _, id := range h.d.tabs {
			if id == a.id {
				t.Fatal("middle click closes the tab")
			}
		}
	})
}

func TestSplitPanesHaveOwnInputsPickerAndDrop(t *testing.T) {
	h := newHarness(t)
	var a, b, c, e *deskSession
	h.ui(func() {
		a = h.d.createSession(h.proj, "default", "default")
		b = h.d.createSession(h.proj, "default", "default")
		c = h.d.createSession(h.proj, "default", "default")
		e = h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.setSplit(3)
		if h.d.bottomBox.Visible() {
			t.Fatal("the shared message box hides in split view")
		}
		empty := h.d.panes[2]
		if !empty.picker.Visible() || len(empty.choices.Objects) == 0 {
			t.Fatal("an empty pane offers the tabs that are not shown")
		}
		test.Tap(empty.choices.Objects[0].(*widget.Button))
		if empty.view.s == nil || empty.picker.Visible() {
			t.Fatal("picking a tab fills the pane")
		}
		h.d.focusPane(h.d.panes[0])
		h.d.panes[2].input.SetText("hello from pane three")
		h.d.sendFromPane(h.d.panes[2], "hello from pane three")
		if h.d.focused != h.d.panes[2] || h.d.panes[2].input.Text != "" {
			t.Fatal("sending from a pane focuses it and clears its own box")
		}
		target := h.d.panes[0]
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(target.root)
		h.d.strip.held, h.d.strip.heldID = h.d.tabChips[e.id], e.id
		h.d.strip.abs = pos.Add(fyne.NewPos(20, 200))
		h.d.strip.drop()
		if target.view.s != e {
			t.Fatal("dropping a tab on a pane shows it there")
		}
		_, _ = b, c
		h.d.setSplit(1)
		if !h.d.bottomBox.Visible() {
			t.Fatal("the shared message box returns with a single view")
		}
	})
}

func TestSplitSurvivesUpdate(t *testing.T) {
	h := newHarness(t)
	var saved *splitState
	var a, b *deskSession
	h.ui(func() {
		a = h.d.createSession(h.proj, "opus", "default")
		b = h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.openInSplit(b.id)
		h.d.panes[0].input.SetText("half typed in pane one")
		h.d.focusPane(h.d.panes[0])
		if h.d.panes[0].model.Selected != "opus" {
			t.Fatal("each pane shows its session's model")
		}
		saved = h.d.saveSplit()
		h.d.setSplit(1)
		h.d.panes = nil
		h.d.restoreSplit(saved)
		if len(h.d.panes) != 2 || h.d.panes[0].view.s != a || h.d.panes[1].view.s != b {
			t.Fatal("split layout comes back after an update")
		}
		if h.d.panes[0].input.Text != "half typed in pane one" || h.d.focused != h.d.panes[0] {
			t.Fatal("pane drafts and focus come back after an update")
		}
	})
}

func TestDraftsFollowTheirSession(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		b := h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.input.SetText("draft for a")
		h.d.open(b.id)
		if h.d.input.Text != "" {
			t.Fatal("switching sessions should not carry a's draft to b")
		}
		h.d.open(a.id)
		if h.d.input.Text != "draft for a" {
			t.Fatalf("a's draft should come back, got %q", h.d.input.Text)
		}
		h.d.openInSplit(b.id)
		if h.d.panes[0].input.Text != "draft for a" {
			t.Fatal("entering split moves a's draft into a's pane")
		}
		h.d.panes[1].input.SetText("draft for b")
		h.d.focusPane(h.d.panes[1])
		h.d.setSplit(1)
		if h.d.current != b || h.d.input.Text != "draft for b" {
			t.Fatalf("back to single shows the focused session's draft, got %q", h.d.input.Text)
		}
		h.d.open(a.id)
		if h.d.input.Text != "draft for a" {
			t.Fatal("the other pane's draft stayed with its session")
		}
	})
}

func TestRestoredDraftSurvivesFirstOpen(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		h.d.strip.lastCur = ""
		h.d.strip.drafts = nil
		h.d.current = nil
		h.d.input.SetText("typed before the update")
		h.d.open(a.id)
		if h.d.input.Text != "typed before the update" {
			t.Fatalf("the draft restored after an update must stay, got %q", h.d.input.Text)
		}
	})
}

func TestAutoSplitFillsActive(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		var ids []string
		for i := 0; i < 3; i++ {
			s := h.d.createSession(h.proj, "default", "default")
			ids = append(ids, s.id)
		}
		h.d.autoChk.SetChecked(true)
		h.d.rebuildRows()
		if !h.d.split() {
			t.Fatal("auto split turns on with several active sessions")
		}
		shown := map[string]bool{}
		for _, p := range h.d.panes {
			if p.view.s != nil {
				shown[p.view.s.id] = true
			}
		}
		for _, id := range ids {
			if !shown[id] {
				t.Fatalf("active session %s should be in a pane", id[:8])
			}
		}
		h.d.rebuildRows()
		if len(h.d.panes) > maxPanes {
			t.Fatal("never more than three panes")
		}
	})
}

func TestCenteringAppliesToEveryPane(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		b := h.d.createSession(h.proj, "default", "default")
		h.d.open(a.id)
		h.d.openInSplit(b.id)
		h.d.setStatusCenter(true)
		for _, p := range h.d.panes {
			if p.view.live.Alignment != fyne.TextAlignCenter {
				t.Fatal("status centering reaches every pane")
			}
		}
		h.d.setCenter(!h.d.center)
		for _, p := range h.d.panes {
			if p.view.s == nil {
				t.Fatal("re-centering keeps each pane's session")
			}
		}
	})
}

func TestUncheckAutoSplitReturnsToSingle(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		var ids []string
		for i := 0; i < 3; i++ {
			ids = append(ids, h.d.createSession(h.proj, "default", "default").id)
		}
		h.d.open(ids[0])
		h.d.autoChk.SetChecked(true)
		h.d.rebuildRows()
		if !h.d.split() {
			t.Fatal("auto split splits")
		}
		h.d.focusPane(h.d.panes[len(h.d.panes)-1])
		h.d.autoChk.SetChecked(false)
		if h.d.split() || h.d.current == nil || h.d.current.id != ids[0] {
			t.Fatal("unchecking returns to the single view of the session open before")
		}
	})
}

func TestRelaunchCompactsAndTrims(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	var gotModel, gotSrc string
	h.d.compactor = func(ctx context.Context, model, text string) (string, error) {
		gotModel, gotSrc = model, text
		<-release
		return "Goal: ship the launcher\nNext steps: packaging", nil
	}
	var old, ns *deskSession
	h.ui(func() {
		old = h.d.createSession(h.proj, "sonnet", "default")
		old.conv.AddUser("build the launcher")
		old.conv.Items = append(old.conv.Items,
			&chat.Item{Seq: 500, Kind: chat.Tool, ToolName: "Bash", Summary: "go build ./...", Result: strings.Repeat("noise ", 5000), HasResult: true},
			&chat.Item{Seq: 501, Kind: chat.Assistant, Text: "Step 1 done, " + strings.Repeat("x", 3000)})
		h.d.relaunchSession(old.id)
		ns = h.d.current
		if !ns.compacting {
			t.Fatal("the new session waits for the compacted brief")
		}
		h.d.send("also add tests")
		if len(ns.queue) != 1 {
			t.Fatalf("messages sent while compacting are queued: %v", ns.queue)
		}
	})
	close(release)
	h.eventually("handoff sent", func() bool {
		for _, it := range ns.conv.Items {
			if it.Kind == chat.User && strings.Contains(it.Text, "Compacted brief") {
				if !strings.Contains(it.Text, "Next steps: packaging") || !strings.Contains(it.Text, "Step 1 done") || strings.Contains(it.Text, strings.Repeat("x", 900)) {
					t.Fatalf("brief plus trimmed recent messages: %q", it.Text)
				}
				return true
			}
		}
		return false
	})
	if gotModel != "sonnet" || !strings.Contains(gotSrc, "[Bash] go build ./...") || strings.Contains(gotSrc, "noise") {
		t.Fatalf("compactor gets the model and a trimmed conversation without tool output: %q %q", gotModel, gotSrc)
	}
}

func TestRelaunchFallsBackWhenCompactFails(t *testing.T) {
	h := newHarness(t)
	h.d.compactor = func(context.Context, string, string) (string, error) { return "", errors.New("offline") }
	var ns *deskSession
	h.ui(func() {
		old := h.d.createSession(h.proj, "default", "default")
		old.conv.AddUser("build the launcher")
		h.d.relaunchSession(old.id)
		ns = h.d.current
	})
	h.eventually("fallback handoff", func() bool {
		for _, it := range ns.conv.Items {
			if it.Kind == chat.User && strings.Contains(it.Text, "Its most recent conversation") && strings.Contains(it.Text, "build the launcher") {
				return !ns.compacting
			}
		}
		return false
	})
}

func TestKmemNamesAndParsing(t *testing.T) {
	hs := parseHandoffList("pz-lock-audit  8 items, saved 2026-11-20T12:45:55-08:00\nquill  4 items, saved 2026-11-20T12:16:10-08:00\nnoise line\n")
	if len(hs) != 2 || hs[1].Name != "quill" || hs[1].Saved.Hour() != 12 {
		t.Fatalf("handoff list parses: %+v", hs)
	}
	taken := func(n string) bool { return n == "pz-launcher" || n == "pz-launcher-2" }
	if got := kmemSlug("PZ Launcher!", taken); got != "pz-launcher-3" {
		t.Fatalf("slug avoids names in use: %q", got)
	}
	if got := kmemSlug("  ", func(string) bool { return false }); got != "session" {
		t.Fatalf("empty title slug: %q", got)
	}
	if choiceHandoff(kmemChoices(hs)[2]) != "quill" || choiceHandoff(kmemNewChoice) != "" {
		t.Fatal("resume choices map back to handoff names")
	}
}

func TestKmemNewSessionAndResume(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch {
		case prog == "kmem" && len(args) > 1 && args[1] == "list":
			return "quill  4 items, saved 2026-11-20T12:16:10-08:00\n", nil
		case prog == "kmem-resume":
			return "RESUME PROMPT for " + args[0], nil
		}
		return "", errors.New("unexpected")
	}
	var fresh, resumed *deskSession
	h.ui(func() {
		fresh = h.d.createSession(h.proj, "default", "default")
		h.d.applyKmemChoice(fresh, kmemNewChoice, "Quill")
		if h.d.org.Kmem[fresh.id] != "quill" || !fresh.kmemNew {
			t.Fatalf("new kmem takes the session title as name: %q", h.d.org.Kmem[fresh.id])
		}
	})
	h.eventually("a new kmem name never reuses an existing handoff", func() bool {
		return h.d.org.Kmem[fresh.id] == "quill-2"
	})
	h.ui(func() {
		h.d.sendTo(fresh, "hello")
		if it := fresh.conv.Items[len(fresh.conv.Items)-1]; !strings.Contains(it.Text, `kmem name is "quill-2"`) || !strings.HasSuffix(it.Text, "hello") {
			t.Fatalf("the first message tells Claude its kmem name: %q", it.Text)
		}
		h.d.sendTo(fresh, "again")
		if it := fresh.conv.Items[len(fresh.conv.Items)-1]; it.Text != "again" {
			t.Fatal("only the first message carries the intro")
		}
		if _, meta, _ := h.d.rowText(fresh.id); !strings.Contains(meta, "kmem quill-2") {
			t.Fatalf("the sidebar shows the kmem name: %q", meta)
		}
		resumed = h.d.createSession(h.proj, "default", "default")
		h.d.applyKmemChoice(resumed, kmemChoices(h.d.kmemHandoffs())[1], "quill")
	})
	h.eventually("resume sends the kmem-resume prompt", func() bool {
		for _, it := range resumed.conv.Items {
			if it.Kind == chat.User && it.Text == "RESUME PROMPT for quill" {
				return true
			}
		}
		return false
	})
	h.ui(func() {
		if h.d.org.Kmem[resumed.id] != "quill" {
			t.Fatal("a resumed session takes over the handoff's name")
		}
	})
}

func TestKmemRestartSavesThenDeletes(t *testing.T) {
	h := newHarness(t)
	saved, savedBy := "", ""
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch {
		case prog == "kmem" && len(args) > 1 && args[1] == "list":
			return saved, nil
		case prog == "kmem" && len(args) > 1 && args[1] == "get":
			return savedBy, nil
		case prog == "kmem-resume":
			return "", errors.New("unknown name")
		}
		return "", errors.New("unexpected")
	}
	var old, ns *deskSession
	var plan *chat.Item
	h.ui(func() {
		old = h.d.createSession(h.proj, "sonnet", "default")
		h.d.org.Titles[old.id] = "PZ Launcher"
		h.d.org.Kmem[old.id] = "launcher"
		h.d.relaunchViaKmem(old.id, "launcher")
		if old.kmemSave == nil || !strings.Contains(old.conv.Items[len(old.conv.Items)-1].Text, "kmem handoff save launcher") {
			t.Fatal("the old session is asked to save its handoff")
		}
		perm := &chat.Item{Kind: chat.Permission, ToolName: "Bash", RequestID: "k1", Input: []byte(`{"command":"kmem handoff save launcher --state 'x'"}`),
			Request: &protocol.ControlRequest{Subtype: "can_use_tool", ToolName: "Bash", Input: []byte(`{"command":"kmem handoff save launcher --state 'x'"}`)}}
		old.conv.Items = append(old.conv.Items, perm)
		h.d.planFirst = true
		h.d.planGate(old, &protocol.Event{Type: "control_request", RequestID: "k1", Request: perm.Request}, []*chat.Item{perm})
		if perm.PermState != chat.PermAutoAllowed {
			t.Fatal("kmem commands run without a plan round while saving the handoff")
		}
		plan = old.conv.AddPlan()
		h.d.kmemAfterTurn(old)
	})
	h.eventually("a turn that ends asking for plan approval keeps the restart waiting", func() bool {
		return old.kmemSave != nil && old.kmemSave.prompt == "" && strings.Contains(old.conv.Items[len(old.conv.Items)-1].Text, "approve the plan")
	})
	h.ui(func() {
		plan.PermState = chat.PermCancelled
		h.d.kmemAfterTurn(old)
	})
	h.eventually("an unsaved handoff cancels the restart", func() bool {
		return old.kmemSave == nil
	})
	h.ui(func() {
		if h.d.current != old || h.d.sessions[old.id] == nil {
			t.Fatal("nothing is deleted when the handoff isn't saved")
		}
		h.d.relaunchViaKmem(old.id, "launcher")
		saved = "launcher  3 items, saved " + time.Now().Add(-5*time.Minute).Format(time.RFC3339) + "\n"
		savedBy = "[f1] history: Handoff artifact launcher: saved by pid1 (session cs:" + old.id + ") at ..."
		h.d.kmemAfterTurn(old)
	})
	h.eventually("a handoff this session saved minutes before the request counts, and the restart finishes", func() bool {
		return h.d.sessions[old.id] == nil && h.d.current != old
	})
	h.ui(func() {
		ns = h.d.current
		if h.d.org.Titles[ns.id] != "PZ Launcher" || h.d.org.Kmem[ns.id] != "launcher" || h.d.org.Kmem[old.id] != "" || ns.model != "sonnet" {
			t.Fatalf("the new session takes the name, kmem name and model: %q %q", h.d.org.Titles[ns.id], h.d.org.Kmem[ns.id])
		}
		found := false
		for _, it := range ns.conv.Items {
			found = found || it.Kind == chat.User && strings.Contains(it.Text, "kmem handoff get launcher")
		}
		if !found {
			t.Fatal("without kmem-resume for the name, the built-in resume prompt is sent")
		}
	})
}

func TestKmemLinkFromFirstPrompt(t *testing.T) {
	for prompt, want := range map[string]string{
		"You are the Quill for Android session; use kmem name \"Quill Android\" (kmem --as ...). Start by running `kmem brief` and `kmem handoff get quill-android`, read": "quill-android",
		"kmem-resume-quill": "quill",
		"fix the tabs":      "",
	} {
		if got := transcript.KmemName(prompt); got != want {
			t.Errorf("KmemName(%q) = %q, want %q", prompt, got, want)
		}
	}
	h := newHarness(t)
	h.ui(func() {
		h.d.org.Kmem["c"] = ""
		h.d.org.Kmem["d"] = "viewpoint"
		h.d.linkKmem([]transcript.Info{{ID: "a", Kmem: "quill"}, {ID: "b"}, {ID: "c", Kmem: "fps-ab"}, {ID: "e", Kmem: "viewpoint"}})
		if h.d.org.Kmem["a"] != "quill" || h.d.org.Kmem["b"] != "" || h.d.org.Kmem["c"] != "" || h.d.org.Kmem["e"] != "" {
			t.Fatalf("auto-link fills only unset sessions and never takes a name in use: %v", h.d.org.Kmem)
		}
	})
}

func TestPlanApprovedPromptsAreHidden(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(s)
		texts := func(it *chat.Item) []string {
			var out []string
			walk(h.d.chat.render(it, &itemView{}), func(o fyne.CanvasObject) {
				if l, ok := o.(*widget.Label); ok && l.Text != "" {
					out = append(out, l.Text)
				}
			})
			return out
		}
		auto := &chat.Item{Kind: chat.Permission, ToolName: "Bash", PermState: chat.PermAutoAllowed}
		if got := texts(auto); len(got) != 0 {
			t.Fatalf("prompts approved with the plan show nothing in the chat: %v", got)
		}
		held := &chat.Item{Kind: chat.Permission, ToolName: "Bash", PermState: chat.PermPlanAsked}
		if got := texts(held); len(got) != 1 || !strings.Contains(got[0], "Held") {
			t.Fatalf("held requests still show their line: %v", got)
		}
	})
}

func TestAutoHandoffPolicy(t *testing.T) {
	for out, want := range map[string]handoffPolicy{
		"soft=40 hard=80 mode=ask": {40, 80, "ask"},
		"mode=off":                 {50, 75, "off"},
		"soft=90 hard=80":          defaultPolicy,
		"usage: kmem handoff ...":  defaultPolicy,
		"soft=0 hard=75":           defaultPolicy,
	} {
		if got, _ := parsePolicy(out); got != want {
			t.Errorf("parsePolicy(%q) = %+v, want %+v", out, got, want)
		}
	}
}

func TestAutoHandoffAsksThenRotates(t *testing.T) {
	h := newHarness(t)
	policy := "soft=50 hard=75 mode=auto"
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if prog == "kmem" && len(args) > 1 && args[1] == "policy" {
			return policy, nil
		}
		return "", errors.New("unexpected")
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hello")
	})
	h.eventually("the turn ends", func() bool { return !s.conv.Busy && s.running() })
	h.eventually("policy loaded", func() bool { h.d.handoffPolicy(); return !h.d.policyAt.IsZero() })
	cards := func() (n int, pending *chat.Item) {
		for _, it := range s.conv.Items {
			if it.Kind == chat.Rotate {
				n++
				if it.PermState == chat.PermPending {
					pending = it
				}
			}
		}
		return
	}
	h.ui(func() {
		s.conv.Stats.ContextMax = 200000
		s.conv.Stats.ContextUsed = 60000
		h.d.checkAutoHandoff(s)
		if n, _ := cards(); n != 0 {
			t.Fatal("below the soft threshold nothing happens")
		}
		s.conv.Stats.ContextUsed = 110000
		h.d.checkAutoHandoff(s)
		h.d.checkAutoHandoff(s)
		n, card := cards()
		if n != 1 || card == nil || !strings.Contains(card.Text, "55%") {
			t.Fatalf("the soft threshold asks once: %d cards", n)
		}
		h.d.answerRotate(s, card, false)
		s.conv.Stats.ContextUsed = 160000
		h.d.checkAutoHandoff(s)
		if n, card := cards(); n != 2 || card == nil {
			t.Fatal("without a kmem name the hard threshold asks again instead of rotating")
		}
		h.d.org.Kmem[s.id] = "quill"
		plan := s.conv.AddPlan()
		h.d.checkAutoHandoff(s)
		if s.kmemSave != nil {
			t.Fatal("never rotates while something waits for the user")
		}
		plan.PermState = chat.PermCancelled
		h.d.checkAutoHandoff(s)
		if s.kmemSave == nil || s.kmemSave.name != "quill" || s.rotateTried != 80 {
			t.Fatal("the hard threshold rotates automatically through the kmem restart")
		}
		if _, card := cards(); card != nil {
			t.Fatal("open rotate cards close when the automatic rotation starts")
		}
		s.kmemSave = nil
		s.conv.Stats.ContextUsed = 164000
		h.d.checkAutoHandoff(s)
		if s.kmemSave != nil {
			t.Fatal("after a cancelled rotation it waits for 5 more points before trying again")
		}
		s.conv.Stats.ContextUsed = 172000
		h.d.policy.Mode = "off"
		h.d.checkAutoHandoff(s)
		if s.kmemSave != nil {
			t.Fatal("mode off never rotates")
		}
	})
}

func TestOnlineLocalSwitch(t *testing.T) {
	var shows []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[{"name":"llama9:8b"},{"name":"nomic-embed:latest"},{"name":"qwen3.8:27b-80k"},{"name":"old:1b"}]}`))
		case "/api/show":
			var req struct {
				Model string `json:"model"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			shows = append(shows, req.Model)
			switch req.Model {
			case "nomic-embed:latest":
				w.Write([]byte(`{"capabilities":["embedding"]}`))
			case "qwen3.8:27b-80k":
				w.Write([]byte(`{"capabilities":["completion","tools"],"parameters":"temperature 0.6\nnum_ctx                        81920\nstop \"x\""}`))
			case "old:1b":
				w.Write([]byte(`{"parameters":"num_ctx 4096"}`))
			default:
				w.Write([]byte(`{"capabilities":["completion","tools"]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	got := fetchOllamaModels(srv.URL)
	want := []localInfo{{"ollama:llama9:8b", 0}, {"ollama:qwen3.8:27b-80k", 81920}, {"ollama:old:1b", 4096}}
	if !reflect.DeepEqual(got, want) || len(shows) != 4 {
		t.Fatalf("live Ollama list keeps tool models (or ones without capabilities) with their num_ctx: %+v (show calls %v)", got, shows)
	}
	if fetchOllamaModels("http://127.0.0.1:1") != nil {
		t.Fatal("Ollama down gives no list")
	}
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "opus", "auto")
		h.d.open(s.id)
		if h.d.backendBtn.Text != backendOnline || contains(h.d.modelSel.Options, localFallback[0]) || !contains(h.d.modeSel.Options, "auto") {
			t.Fatalf("Online shows only Claude models and every mode: %v %v", h.d.modelSel.Options, h.d.modeSel.Options)
		}
		test.Tap(h.d.backendBtn)
		if h.d.backendBtn.Text != backendLocal || h.d.backendBtn.Importance != widget.WarningImportance {
			t.Fatal("the button flips to Local and stands out")
		}
		if s.model != localFallback[0] || s.mode != "default" {
			t.Fatalf("Local picks the first local model and leaves auto mode: %q %q", s.model, s.mode)
		}
		if last := s.conv.Items[len(s.conv.Items)-1]; !strings.Contains(last.Text, "auto to default") {
			t.Fatalf("the mode change is explained: %q", last.Text)
		}
		if !reflect.DeepEqual(h.d.modelSel.Options, localFallback) || contains(h.d.modeSel.Options, "auto") {
			t.Fatalf("Local shows only local models (fallback list) and no auto mode: %v %v", h.d.modelSel.Options, h.d.modeSel.Options)
		}
		if _, meta, _ := h.d.rowText(s.id); !strings.Contains(meta, "local qwen3.8:27b-80k") || !strings.Contains(h.d.subtitle.Text, "Local: qwen3.8:27b-80k") {
			t.Fatalf("a local session is marked in the sidebar and header: %q / %q", meta, h.d.subtitle.Text)
		}
		if h.d.app.Preferences().String("newModel") != localFallback[0] {
			t.Fatal("new sessions remember the choice")
		}
		test.Tap(h.d.backendBtn)
		if h.d.backendBtn.Text != backendOnline {
			t.Fatal("clicking again flips back to Online")
		}
		if s.model != "opus" || backendOf(s.model) != backendOnline {
			t.Fatalf("Online goes back to the last Claude model: %q", s.model)
		}
		n := h.d.createSession(h.proj, "ollama:qwen3-coder:30b-48k", "auto")
		if n.mode != "default" {
			t.Fatal("a new local session never starts in auto mode")
		}
		h.d.localModels = []string{"ollama:llama9:8b", "ollama:qwen3.8:27b-80k"}
		if h.d.defaultLocal() != "ollama:qwen3.8:27b-80k" {
			t.Fatal("Local prefers the known Qwen tag over whatever Ollama lists first")
		}
		h.d.localWindows["ollama:qwen3.8:27b-80k"] = 81920
		if env := strings.Join(h.d.localEnv("ollama:qwen3.8:27b-80k"), " "); env != "CLAUDE_CODE_MAX_CONTEXT_TOKENS=81920 CLAUDE_CODE_AUTO_COMPACT_WINDOW=81920" {
			t.Fatalf("the CLI is told the real window: %q", env)
		}
		if h.d.localEnv("ollama:llama9:8b") != nil || h.d.localEnv("opus") != nil {
			t.Fatal("unknown windows and Claude models get no override")
		}
		r := h.d.createSession(h.proj, "opus", "default")
		h.d.sendTo(r, "hi")
	})
	var r *deskSession
	h.ui(func() { r = h.d.current })
	h.eventually("turn over", func() bool { return r.running() && !r.conv.Busy })
	h.ui(func() {
		r.rcOn, r.rcURL, r.rcName = true, "https://claude.ai/x", "rc"
		h.d.setModel(r, "ollama:qwen3.8:27b-80k")
		if r.rcOn || r.rcURL != "" || r.rcName != "" || r.proc != nil {
			t.Fatal("switching Claude <-> local drops the process and its Remote Control state")
		}
		h.d.app.Preferences().SetBool("rcLocal", false)
		h.d.autoRC = true
		h.d.autoRemote(r)
		if r.rcOn {
			t.Fatal("with the setting off, local sessions never start Remote Control")
		}
		h.d.setRemoteControl(r, true)
		if r.rcOn || r.proc != nil {
			t.Fatal("with the setting off, the manual Remote Control toggle is refused for local sessions")
		}
	})
}

func TestSuggestionSurvivesReload(t *testing.T) {
	s := &deskSession{conv: chat.New()}
	s.conv.Suggestion = "run the tests"
	var hs handedSession
	hs.savePending(s)
	fresh := &deskSession{conv: chat.New()}
	hs.restorePending(fresh)
	if fresh.conv.Suggestion != "run the tests" {
		t.Fatalf("the suggestion comes back after a reload: %q", fresh.conv.Suggestion)
	}
	busy := &deskSession{conv: chat.New()}
	busy.conv.Busy = true
	hs.restorePending(busy)
	if busy.conv.Suggestion != "" {
		t.Fatal("a session that is already busy again gets no stale suggestion")
	}
}

func TestApprovalCardsFollowCentering(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(s)
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. build", InPlan: true})
		plan := s.conv.AddPlan()
		done := &chat.Item{Kind: chat.Permission, ToolName: "Bash", PermState: chat.PermAllowed}
		ask := &chat.Item{Kind: chat.Permission, ToolName: "AskUserQuestion", PermState: chat.PermPending, Input: []byte(`{"questions":[{"question":"Which one?","header":"Pick","multiSelect":false,"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}`)}
		blocked := &chat.Item{Kind: chat.Tool, ToolName: "Bash", ResultError: true, Input: []byte(`{"command":"rm x"}`), Result: "denied by the Claude Code auto mode classifier. Reason: [Destructive]"}
		check := func(center bool) {
			h.d.setCenter(center)
			want := fyne.TextAlignLeading
			if center {
				want = fyne.TextAlignCenter
			}
			for _, o := range []fyne.CanvasObject{h.d.chat.render(plan, &itemView{}), h.d.chat.render(done, &itemView{}), h.d.chat.render(ask, &itemView{}), h.d.chat.blockedCard(blocked)} {
				walk(o, func(c fyne.CanvasObject) {
					switch x := c.(type) {
					case *widget.Label:
						if x.Text != "" && x.Alignment != want {
							t.Errorf("center=%v: label %q is aligned %v", center, x.Text, x.Alignment)
						}
					case *canvas.Text:
						if x.Text != "" && x.Alignment != want {
							t.Errorf("center=%v: text %q is aligned %v", center, x.Text, x.Alignment)
						}
					}
				})
			}
		}
		check(true)
		check(false)
	})
}

func TestLocalSessionNeedsOllama(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		h.d.pingLocal = func(string) bool { return false }
		s = h.d.createSession(h.proj, "ollama:qwen3.8:27b-80k", "default")
		h.d.sendTo(s, "hi")
		if s.running() || !strings.Contains(s.failed, "Ollama isn't running") {
			t.Fatalf("a local session with Ollama down fails fast with a clear message: %q", s.failed)
		}
		h.d.pingLocal = func(string) bool { return true }
		h.d.sendTo(s, "hi")
	})
	h.eventually("local session starts once Ollama is up", func() bool { return s.running() })
	if ollamaReachable("http://127.0.0.1:1") || ollamaReachable("not a url") {
		t.Fatal("reachability check")
	}
}

func TestTabsGlowLikeSidebar(t *testing.T) {
	h := newHarness(t)
	var a, b, c, e *deskSession
	h.ui(func() {
		a = h.d.createSession(h.proj, "default", "default")
		b = h.d.createSession(h.proj, "default", "default")
		c = h.d.createSession(h.proj, "default", "default")
		e = h.d.createSession(h.proj, "default", "default")
		for _, s := range []*deskSession{a, b, c} {
			h.d.sendTo(s, "hi")
		}
	})
	h.eventually("all three idle", func() bool {
		return a.status() == stIdle && b.status() == stIdle && c.status() == stIdle
	})
	h.ui(func() {
		h.d.open(a.id)
		check := func(s *deskSession, want status, glows bool) {
			h.d.tabSig = ""
			h.d.syncTabs()
			if got := s.status(); got != want {
				t.Fatalf("fixture: status %v, want %v", got, want)
			}
			g, on := h.d.glowBoxes[h.d.tabChips[s.id].glow]
			if !glows {
				if on {
					t.Errorf("%v tab should not glow, has %+v", want, g)
				}
				return
			}
			if !on || g.base != want.color() || g.strength != glowStrength(want) || g.pulse != (glowStrength(want) >= 1) {
				t.Errorf("%v tab should glow like its sidebar row, got %+v on=%v", want, g, on)
			}
		}
		b.conv.AddPlan()
		check(b, stWaiting, true)
		check(c, stIdle, true)
		c.conv.Busy = true
		check(c, stRunning, false)
		c.conv.Busy = false
		c.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"bg1","name":"Bash","input":{"command":"./test.sh","run_in_background":true}}]`)}})
		c.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"bg1","content":"Command running in background with ID: tst123abc. Output is being written to: /tmp/o"}]`)}})
		check(c, stRunning, false)
		c.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"text","text":"<task-notification>\n<task-id>tst123abc</task-id>\n<status>completed</status>\n</task-notification>"}]`)}})
		check(c, stIdle, true)
		e.failed = "boom"
		check(e, stError, true)
		a.conv.AddPlan()
		check(a, stWaiting, false)
		chip := h.d.tabChips[b.id]
		h.d.closeTab(b.id)
		h.d.tabSig = ""
		h.d.syncTabs()
		if _, on := h.d.glowBoxes[chip.glow]; on {
			t.Fatal("a closed tab's glow is removed")
		}
	})
}

func TestQueueSurvivesReload(t *testing.T) {
	h := newHarness(t)
	var busy, idle *deskSession
	h.ui(func() {
		busy = h.d.createSession(h.proj, "default", "default")
		idle = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(busy, "hi")
		h.d.sendTo(idle, "hi")
	})
	h.eventually("both idle", func() bool { return busy.status() == stIdle && idle.status() == stIdle })
	h.ui(func() {
		busy.queue = []string{"first", "second"}
		idle.queue = []string{"later"}
		var st restoreState
		h.d.saveQueues(&st)
		b, _ := json.Marshal(st)
		var back restoreState
		json.Unmarshal(b, &back)
		busy.queue, idle.queue = nil, nil
		busy.conv.Busy = true
		h.d.restoreQueues(&back)
		if !reflect.DeepEqual(busy.queue, []string{"first", "second"}) {
			t.Fatalf("a busy session gets its queue back in order: %v", busy.queue)
		}
		if len(idle.queue) != 0 {
			t.Fatal("an idle session sends its restored queue right away")
		}
		last := idle.conv.Items[len(idle.conv.Items)-1]
		if last.Kind != chat.User || last.Text != "later" {
			t.Fatalf("the queued text was sent: %+v", last)
		}
		busy.conv.Busy = false
	})
}

func TestReloadKeepsSessionState(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	h.d.compactor = func(context.Context, string, string) (string, error) {
		<-release
		return "Goal: resumed brief", nil
	}
	var fresh, ns *deskSession
	var st restoreState
	h.ui(func() {
		old := h.d.session(editsID)
		if old == nil {
			t.Fatal("fixture session missing")
		}
		old.model, old.mode = "ollama:qwen3.8:27b-80k", "plan"
		old.restoreMode, old.retryCmd, old.retryDest = "auto", "rm x", "session"
		old.retryRules = []protocol.Rule{{Tool: "Bash", Content: "rm *"}}
		old.kmemNew, old.rotateAsk, old.rotateTried, old.lastClaude = true, 2, 80, "opus"
		old.kmemSave = &kmemSave{name: "quill", asked: time.Now().Truncate(time.Second), spare: 1}
		fresh = h.d.createSession(h.proj, "sonnet", "acceptEdits")
		ns = h.d.createSession(h.proj, "haiku", "default")
		h.d.strip.keepDraft(fresh.id, "an improvement idea")
		h.d.strip.keepDraft(editsID, "another idea")
		h.d.compactInto(ns, editsID, "", "Edits", chat.New())
		h.d.saveSessions(&st)
	})
	close(release)
	h.eventually("first compaction done", func() bool { return !ns.compacting })
	h.ui(func() {
		b, _ := json.Marshal(st)
		var back restoreState
		json.Unmarshal(b, &back)
		if back.Meta[ns.id].CompactFrom != editsID {
			t.Fatalf("an interrupted compaction is saved: %+v", back.Meta[ns.id])
		}
		ns.conv = chat.New()
		for id, s := range h.d.sessions {
			if !s.running() {
				delete(h.d.sessions, id)
			}
		}
		h.d.strip.drafts = nil
		h.d.compactor = func(context.Context, string, string) (string, error) { return "Goal: resumed brief", nil }
		h.d.restoreSessions(&back)
		old := h.d.session(editsID)
		if old.model != "ollama:qwen3.8:27b-80k" || old.mode != "plan" || old.restoreMode != "auto" || old.retryCmd != "rm x" || len(old.retryRules) != 1 || old.retryDest != "session" {
			t.Fatalf("a stopped session keeps its model, mode and retry state: %+v", old)
		}
		if !old.kmemNew || old.rotateAsk != 2 || old.rotateTried != 80 || old.lastClaude != "opus" || old.kmemSave == nil || old.kmemSave.name != "quill" || old.kmemSave.spare != 1 {
			t.Fatalf("kmem and rotate state survive: %+v", old)
		}
		f := h.d.sessions[fresh.id]
		if f == nil || f.model != "sonnet" || f.mode != "acceptEdits" || f.cwd != h.proj {
			t.Fatalf("a new tab with nothing sent yet comes back: %+v", f)
		}
		if h.d.strip.draft(fresh.id) != "an improvement idea" || h.d.strip.draft(editsID) != "another idea" {
			t.Fatal("drafts typed into other tabs survive")
		}
		ns = h.d.sessions[ns.id]
		h.d.resumeInterrupted(&back)
	})
	h.eventually("the interrupted compaction is re-run and handed over", func() bool {
		for _, it := range ns.conv.Items {
			if it.Kind == chat.User && strings.Contains(it.Text, "resumed brief") {
				return true
			}
		}
		return false
	})
}
