package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
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
		h.d.closeSettings()
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
		h.d.open(old.id)
		h.d.send("/clear")
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
		"soft=40 hard=80 mode=ask": {Soft: 40, Hard: 80, Mode: "ask"},
		"mode=off":                 {Soft: 50, Hard: 75, Mode: "off"},
		"ttl=5":                    {Soft: 50, Hard: 75, Mode: "auto", TTL: 5 * time.Minute},
		"soft=50 ttl=1h":           {Soft: 50, Hard: 75, Mode: "auto", TTL: time.Hour},
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

func TestChatTextSelection(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Seq: 901, Kind: chat.Assistant, Text: "Alpha beta gamma"},
			&chat.Item{Seq: 902, Kind: chat.Assistant, Text: "Second **bold** line"})
		s.conv.AddPlan()
		h.d.chat.show(s)
		h.d.chat.relayoutInner()
	})
	var runs []textRun
	h.eventually("chat laid out", func() bool {
		runs = collectRuns(h.d.chat.box)
		for _, r := range runs {
			if strings.Contains(r.t.Text, "Second") {
				return true
			}
		}
		return false
	})
	find := func(sub string) textRun {
		for _, r := range runs {
			if strings.Contains(r.t.Text, sub) {
				return r
			}
		}
		t.Fatalf("no drawn text with %q", sub)
		return textRun{}
	}
	at := func(r textRun, sub string, after bool) fyne.Position {
		i := strings.Index(r.t.Text, sub)
		n := len([]rune(r.t.Text[:i]))
		if after {
			n += len([]rune(sub))
		}
		return fyne.NewPos(charX(r, n), r.pos.Y+r.t.Size().Height/2)
	}
	drag := func(from, to fyne.Position) {
		sel := h.d.chat.sel
		sel.dragging = false
		sel.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: to}, Dragged: fyne.NewDelta(to.X-from.X, to.Y-from.Y)})
		sel.DragEnd()
	}
	h.ui(func() {
		for _, r := range runs {
			if strings.Contains(r.t.Text, "Approve plan") || strings.Contains(r.t.Text, "Don't run") {
				t.Fatalf("button text is not selectable: %q", r.t.Text)
			}
		}
		alpha := find("Alpha")
		drag(at(alpha, "Alpha", false), at(alpha, "beta", true))
		if got := h.d.chat.sel.text(); got != "Alpha beta" {
			t.Fatalf("a drag inside one message selects exactly that text: %q", got)
		}
		if len(h.d.chat.sel.CreateRenderer().Objects()) != 0 {
			t.Log("renderer starts empty until refreshed")
		}
		line := find("line")
		drag(at(alpha, "gamma", false), at(line, "line", true))
		if got := h.d.chat.sel.text(); got != "gamma\nSecond bold line" {
			t.Fatalf("a drag across messages keeps the source text, bold included: %q", got)
		}
		mid := func(r textRun, sub string, n int) fyne.Position {
			i := len([]rune(r.t.Text[:strings.Index(r.t.Text, sub)])) + n
			return fyne.NewPos(charX(r, i), r.pos.Y+r.t.Size().Height/2)
		}
		drag(fyne.NewPos(0, alpha.pos.Y+3), mid(alpha, "beta", 2))
		if got := h.d.chat.sel.text(); got != "Alpha beta" {
			t.Fatalf("a sloppy drag from the margin into the middle of a word selects whole words: %q", got)
		}
		drag(mid(alpha, "Alpha", 1), mid(alpha, "Alpha", 3))
		if got := h.d.chat.sel.text(); got != "lp" {
			t.Fatalf("inside one word the selection stays exact: %q", got)
		}
		sec := find("Second")
		drag(fyne.NewPos(charX(sec, 0)+2, sec.pos.Y-4), fyne.NewPos(9999, line.pos.Y+line.t.Size().Height/2))
		if got := h.d.chat.sel.text(); got != "Second bold line" {
			t.Fatalf("starting just above a line and ending past its right edge selects that whole line: %q", got)
		}
		drag(at(alpha, "gamma", false), at(line, "line", true))
		if !h.d.copySelection() || h.d.app.Clipboard().Content() != "gamma\nSecond bold line" || h.d.chat.sel.has {
			t.Fatal("Ctrl+C copies the selection and clears it")
		}
		drag(at(alpha, "Alpha", false), at(alpha, "beta", true))
		h.d.clearSelections()
		if h.d.chat.sel.has || h.d.copySelection() {
			t.Fatal("Escape clears the selection")
		}
	})
}

func TestStreamingMarkdownAndPlanCard(t *testing.T) {
	for in, want := range map[string]string{
		"Here:\n```go\nfunc x() {":      "Here:\n```go\nfunc x() {\n```",
		"Done.\n```\nok\n```\nMore":     "Done.\n```\nok\n```\nMore",
		"This is **bol":                 "This is **bol**",
		"This is **":                    "This is ",
		"Use `go te":                    "Use `go te`",
		"**bold `cod":                   "**bold `cod`**",
		"a **b** and `c` done":          "a **b** and `c` done",
		"First para **x**\n\nSecond `y": "First para **x**\n\nSecond `y`",
	} {
		if got := closeOpenMarkdown(in); got != want {
			t.Errorf("closeOpenMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
	c := chat.New()
	c.AddUser("do it")
	c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. build\n\n**Approve 1–6, and should auto-handoff also rotate when the cache has gone cold?**"})
	c.MarkPlanText()
	plan := c.AddPlan()
	if got := planQuestion(c, plan); got != "Approve 1–6, and should auto-handoff also rotate when the cache has gone cold?" {
		t.Fatalf("the plan card asks Claude's own question: %q", got)
	}
	c3 := chat.New()
	c3.Items = append(c3.Items, &chat.Item{Kind: chat.Assistant, Text: "1. Login\n2. Token\n\n**Approve?** Once you do, I'll start the login and send you the link right away."})
	if got := planQuestion(c3, c3.AddPlan()); got != "Approve? Once you do, I'll start the login and send you the link right away." {
		t.Fatalf("a question at the start of the last line is shown with its whole line: %q", got)
	}
	c2 := chat.New()
	c2.Items = append(c2.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. build\n2. test"})
	if got := planQuestion(c2, c2.AddPlan()); got != "Approve this plan?" {
		t.Fatalf("without a question the card keeps the generic title: %q", got)
	}
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		msg := &chat.Item{Seq: 950, Kind: chat.Assistant, Text: "Plan:\n1. build\n\n**Approve?**", InPlan: true}
		s.conv.Items = append(s.conv.Items, msg)
		found := false
		var look func(o fyne.CanvasObject)
		look = func(o fyne.CanvasObject) {
			walk(o, func(x fyne.CanvasObject) {
				switch y := x.(type) {
				case *widget.RichText:
					found = found || strings.Contains(y.String(), "Plan:")
				case *container.ThemeOverride:
					look(y.Content)
				case *msgMenu:
					look(y.content)
				}
			})
		}
		look(h.d.chat.render(msg, &itemView{}))
		if !found {
			t.Fatal("a plan message stays where it streamed instead of moving into the card")
		}
	})
	for text, want := range map[string]bool{
		"yes approve all, add the cold cache rotation too, for approval prompts can you change approval message to match the prompted approval message?": true,
		"yes, approve. also look at the tabs": true,
		"I approve, and add a test for it?":   true,
		"yes approve but skip step 2":         false,
		"yes approve, don't push":             false,
		"approve all except the release":      false,
		"yes, can you change it first?":       false,
	} {
		if isApproval(text) != want {
			t.Errorf("isApproval(%q) = %v", text, !want)
		}
	}
}

func TestQueueEditSendNowAndReorder(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		s.compacting = true
		for _, m := range []string{"one", "two", "three"} {
			h.d.send(m)
		}
		key := func(name fyne.KeyName) bool { return h.d.historyKey(h.d.input, s, &fyne.KeyEvent{Name: name}) }
		if !reflect.DeepEqual(s.queue, []string{"one", "two", "three"}) {
			t.Fatalf("queued: %v", s.queue)
		}
		key(fyne.KeyUp)
		if h.d.input.Text != "three" || s.qedit == nil || len(s.queue) != 2 {
			t.Fatal("up opens the newest queued message for editing")
		}
		key(fyne.KeyDown)
		if h.d.input.Text != "" || s.qedit != nil || !reflect.DeepEqual(s.queue, []string{"one", "two", "three"}) {
			t.Fatalf("down puts it back unchanged: %v", s.queue)
		}
		h.d.editQueued(s, 0)
		h.d.input.SetText("one, edited")
		key(fyne.KeyEscape)
		if !reflect.DeepEqual(s.queue, []string{"one", "two", "three"}) {
			t.Fatalf("Escape discards the edit and restores the original in place: %v", s.queue)
		}
		h.d.editQueued(s, 1)
		h.d.send("two, edited")
		if !reflect.DeepEqual(s.queue, []string{"one", "two, edited", "three"}) || s.qedit != nil {
			t.Fatalf("Enter saves the edit in its own position: %v", s.queue)
		}
		h.d.editQueued(s, 2)
		other := h.d.createSession(h.proj, "default", "default")
		h.d.open(other.id)
		if !reflect.DeepEqual(s.queue, []string{"one", "two, edited", "three"}) {
			t.Fatalf("switching sessions puts an unsaved edit back: %v", s.queue)
		}
		h.d.open(s.id)
		h.d.moveQueued(s, 2, 0)
		if !reflect.DeepEqual(s.queue, []string{"three", "one", "two, edited"}) {
			t.Fatalf("reorder: %v", s.queue)
		}
		h.d.syncQueue(s)
		var rows []*queueRow
		for _, o := range h.d.chat.queueBox.Objects {
			if r, ok := o.(*queueRow); ok {
				rows = append(rows, r)
			}
		}
		if len(rows) != 3 {
			t.Fatalf("one draggable row per queued message: %d", len(rows))
		}
		rows[0].Resize(fyne.NewSize(300, 40))
		rows[0].Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(0, 2*(40+theme.Padding()))})
		rows[0].DragEnd()
		if !reflect.DeepEqual(s.queue, []string{"one", "two, edited", "three"}) {
			t.Fatalf("dragging a row two rows down moves it there: %v", s.queue)
		}
		s.compacting = false
		h.d.sendQueuedNow(s, 1)
		if !reflect.DeepEqual(s.queue, []string{"one", "three"}) {
			t.Fatalf("send now takes just that message out of the queue: %v", s.queue)
		}
		last := s.conv.Items[len(s.conv.Items)-1]
		if last.Kind != chat.User || last.Text != "two, edited" {
			t.Fatalf("and sends it right away: %+v", last)
		}
	})
}

func TestColdCacheRotation(t *testing.T) {
	h := newHarness(t)
	var fire func()
	h.d.after = func(_ time.Duration, f func()) { fire = f }
	h.d.kmemOff = false
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if prog == "kmem" && len(args) > 1 && args[1] == "policy" {
			return "soft=50 hard=75 mode=auto", nil
		}
		return "", errors.New("unexpected")
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.eventually("policy", func() bool { h.d.handoffPolicy(); return !h.d.policyAt.IsZero() })
	h.ui(func() {
		h.d.org.Kmem[s.id] = "quill"
		s.conv.Stats.ContextMax, s.conv.Stats.ContextUsed = 1000, 600
		s.conv.CacheTTL = 5 * time.Minute
		if got := cacheTTL(s, h.d.handoffPolicy()); got != 5*time.Minute {
			t.Fatalf("the session's own cache lifetime is used: %v", got)
		}
		h.d.checkAutoHandoff(s)
		if fire == nil || s.kmemSave != nil {
			t.Fatal("above the soft threshold a cold-cache check is scheduled, nothing rotates yet")
		}
	})
	saved := fire
	h.ui(func() { h.d.sendTo(s, "more work") })
	h.eventually("turn over", func() bool { return !s.conv.Busy })
	saved()
	h.ui(func() {
		if s.kmemSave != nil {
			t.Fatal("a new turn since then cancels the cold rotation")
		}
		s.conv.Stats.ContextMax, s.conv.Stats.ContextUsed = 1000, 600
		h.d.checkAutoHandoff(s)
	})
	fire()
	h.ui(func() {
		if s.kmemSave == nil || s.kmemSave.name != "quill" {
			t.Fatal("after the cache lifetime with no new turn, the session rotates through kmem")
		}
	})
}

func TestChoiceCard(t *testing.T) {
	for line, want := range map[string][]string{
		"**Push `4bc346e` now, or wait until you've seen it rotate a session?**": {"Push 4bc346e now", "Wait until you've seen it rotate a session"},
		"Should I reload now or wait until you've sent the queued messages?":     {"Reload now", "Wait until you've sent the queued messages"},
		"Should I fix this now or later?":                                        {"Fix this now", "Later"},
		"**Approve A–E, and which of 3–12 should I include?**":                   nil,
		"Done. The suite passes.":                                                nil,
	} {
		_, got, ok := parseChoices(line)
		if !reflect.DeepEqual(got, want) || ok != (want != nil) {
			t.Errorf("parseChoices(%q) = %v %v", line, got, ok)
		}
	}
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		msg := &chat.Item{Seq: 990, Kind: chat.Assistant, Text: "Built it.\n\n**Push now, or wait for the release?**"}
		s.conv.Items = append(s.conv.Items, msg)
		h.d.chat.show(s)
		body, card := h.d.chat.choiceCard(msg)
		if card == nil || body != "Built it.\n" {
			t.Fatalf("the question line becomes a card under the rest of the message: %q", body)
		}
		var labels []string
		walk(card, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok {
				labels = append(labels, b.Text)
			}
		})
		if !reflect.DeepEqual(labels, []string{"Push now", "Wait for the release"}) {
			t.Fatalf("one button per option: %v", labels)
		}
		h.d.pickChoice(s, msg, "Wait for the release")
		last := s.conv.Items[len(s.conv.Items)-1]
		if last.Kind != chat.User || last.Text != "Wait for the release" {
			t.Fatalf("a button sends its option as the reply: %+v", last)
		}
		n := len(s.conv.Items)
		h.d.pickChoice(s, msg, "Push now")
		if len(s.conv.Items) != n {
			t.Fatal("once answered, the old card can't send again")
		}
		_, card = h.d.chat.choiceCard(msg)
		labels = nil
		walk(card, func(o fyne.CanvasObject) {
			if b, ok := o.(*widget.Button); ok {
				labels = append(labels, b.Text)
			}
		})
		if len(labels) != 0 {
			t.Fatal("an answered card keeps the question but has no buttons")
		}
		withPlan := &chat.Item{Seq: 991, Kind: chat.Assistant, Text: "Plan.\n\n**Approve now, or change it first?**"}
		s.conv.Items = append(s.conv.Items, withPlan)
		s.conv.AddPlan()
		if _, c := h.d.chat.choiceCard(withPlan); c != nil {
			t.Fatal("no choice card when a plan card already asks")
		}
	})
}

func TestSlashCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	h := newHarness(t)
	work := t.TempDir()
	write := func(p, body string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(work, ".claude", "settings.json"), `{"model":"opus","permissions":{"allow":["Bash(ls *)","Bash(kill *)"],"deny":["Bash(rm -rf *)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"guard"}]}]}}`)
	write(filepath.Join(work, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\ndescription: Reviews diffs\n---\nBody")
	rules := permissionRules(work)
	if len(rules) != 3 || rules[0].scope != "Project" {
		t.Fatalf("rules from the project settings: %+v", rules)
	}
	if err := removeRule(rules[1]); err != nil {
		t.Fatal(err)
	}
	m, _ := readSettings(filepath.Join(work, ".claude", "settings.json"))
	if m["model"] != "opus" || len(permissionRules(work)) != 2 || strings.Contains(fmt.Sprint(m["permissions"]), "kill") {
		t.Fatalf("Remove drops just that rule and keeps the rest of the file: %v", m)
	}
	if ht := hooksText(work); !strings.Contains(ht, "PreToolUse") || !strings.Contains(ht, "guard") {
		t.Fatalf("hooks: %s", ht)
	}
	if a := agentFiles(work); len(a) != 1 || a[0].name != "reviewer" || a[0].desc != "Reviews diffs" {
		t.Fatalf("agents: %+v", a)
	}
	if mf := memoryFiles(work); mf[4] != filepath.Join(home, ".claude", "projects", transcript.Sanitize(work), "memory", "MEMORY.md") {
		t.Fatalf("memory files: %v", mf)
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", work, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-q")
	write(filepath.Join(work, "a.txt"), "old\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "x")
	write(filepath.Join(work, "a.txt"), "new\n")
	if d, err := gitDiff(work); err != nil || !strings.Contains(d, "M a.txt") || !strings.Contains(d, "+new") {
		t.Fatalf("diff: %v %s", err, d)
	}
	if md := exportMarkdown("T", func() *chat.Conversation {
		c := chat.New()
		c.AddUser("hello")
		c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: "hi **there**"})
		return c
	}()); md != "# T\n\n**You:**\n\nhello\n\n**Claude:**\n\nhi **there**\n\n" {
		t.Fatalf("export: %q", md)
	}
	var reqs []string
	var reqMu sync.Mutex
	h.d.controlFn = func(s *deskSession, req map[string]any) (json.RawMessage, error) {
		reqMu.Lock()
		reqs = append(reqs, fmt.Sprint(req["subtype"], " ", req["serverName"], " ", req["enabled"]))
		reqMu.Unlock()
		return json.RawMessage(`{"mcpServers":[{"name":"docs","status":"disabled","scope":"claudeai"},{"name":"gh","status":"needs-auth"}]}`), nil
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "last answer"})
		h.d.cmdCopy(s)
		if h.d.app.Clipboard().Content() != "last answer" {
			t.Fatal("/copy copies the last reply")
		}
		path := filepath.Join(work, "out.md")
		h.d.cmdExport(s, path)
		if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "last answer") {
			t.Fatalf("/export writes the file: %v", err)
		}
		h.d.cmdMCP(s)
	})
	h.eventually("mcp list", func() bool {
		found := false
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if l, ok := x.(*widget.Label); ok && strings.HasPrefix(l.Text, "gh  ·  needs-auth") {
					found = true
				}
			})
		}
		return found
	})
	h.ui(func() {
		var signIn, enable *widget.Button
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if b, ok := x.(*widget.Button); ok {
					switch b.Text {
					case "Sign in":
						signIn = b
					case "Enable":
						enable = b
					}
				}
			})
		}
		if signIn == nil || enable == nil {
			t.Fatal("needs-auth servers get Sign in, disabled ones Enable")
		}
		test.Tap(enable)
	})
	h.eventually("toggle sent", func() bool {
		reqMu.Lock()
		defer reqMu.Unlock()
		return len(reqs) >= 3 && strings.HasPrefix(reqs[1], "mcp_toggle docs true")
	})
	for _, o := range h.w.Canvas().Overlays().List() {
		h.ui(func() { h.w.Canvas().Overlays().Remove(o) })
	}
	h.ui(func() {
		h.d.sendTo(s, "start")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.setChrome(s, true)
		h.d.addDir(s, work)
		if s.proc != nil || s.chrome != "on" || len(s.addDirs) != 1 {
			t.Fatal("chrome and add-dir restart the process on the next message")
		}
		h.d.sendTo(s, "again")
	})
	h.eventually("restarted with flags", func() bool {
		a := h.args()
		return strings.Contains(a, "--chrome") && strings.Contains(a, "--add-dir "+work)
	})
	h.ui(func() {
		before := len(h.w.Canvas().Overlays().List())
		h.d.cmdTasks(s)
		h.d.cmdMemory(s)
		h.d.cmdAgents(s)
		h.d.cmdPermissions(s)
		h.d.cmdChrome(s, "")
		h.d.textDialog("Hooks", hooksText(work))
		if n := len(h.w.Canvas().Overlays().List()) - before; n != 6 {
			t.Fatalf("each dialog command opens a dialog: %d", n)
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
	})
	h.ui(func() {
		old := h.d.session(editsID)
		ns, err := h.d.forkSession(old)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(filepath.Dir(h.d.infos[editsID].Path), ns.id+".jsonl"))
		if !strings.Contains(string(b), ns.id) || strings.Contains(string(b), editsID) || len(ns.conv.Items) == 0 || !strings.HasSuffix(h.d.org.Titles[ns.id], "(fork)") {
			t.Fatal("/fork copies the transcript under a new id and opens it with the history")
		}
	})
}

func deepWalk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	driver.WalkVisibleObjectTree(o, func(x fyne.CanvasObject, _ fyne.Position, _ fyne.Position, _ fyne.Size) bool {
		fn(x)
		return false
	}, nil)
}

func TestRewind(t *testing.T) {
	h := newHarness(t)
	var reqs []map[string]any
	var mu sync.Mutex
	h.d.controlFn = func(_ *deskSession, req map[string]any) (json.RawMessage, error) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		if req["dry_run"] == true {
			return json.RawMessage(`{"canRewind":true,"filesChanged":["/w/a.go","/w/b.go"],"insertions":3,"deletions":7}`), nil
		}
		return json.RawMessage(`{"canRewind":true,"skippedLinks":0}`), nil
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy && s.persisted })
	var u2 *chat.Item
	h.ui(func() {
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Kind: chat.User, Text: "first", UUID: "U1"},
			&chat.Item{Kind: chat.Assistant, Text: "one", UUID: "A1"},
			&chat.Item{Kind: chat.User, Text: "second, to redo", UUID: "U2"},
			&chat.Item{Kind: chat.Assistant, Text: "two", UUID: "A2"})
		u2 = s.conv.Items[len(s.conv.Items)-2]
		h.d.open(s.id)
		h.d.rewindTo(s, u2, true)
	})
	var confirm *widget.Button
	h.eventually("confirm dialog", func() bool {
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if b, ok := x.(*widget.Button); ok && b.Text == "Restore files" {
					confirm = b
				}
			})
		}
		return confirm != nil
	})
	h.ui(func() {
		if s.resumeAt != "" {
			t.Fatal("nothing changes before the restore is confirmed")
		}
		test.Tap(confirm)
	})
	h.eventually("rewound", func() bool { return s.resumeAt == "A1" })
	h.ui(func() {
		var last *chat.Item
		for _, it := range s.conv.Items {
			if it.Kind != chat.Notice {
				last = it
			}
		}
		if last.UUID != "A1" || s.proc != nil || h.d.input.Text != "second, to redo" {
			t.Fatalf("the conversation is cut before that message and its text is back in the box: %+v", last)
		}
		mu.Lock()
		if len(reqs) != 2 || reqs[0]["dry_run"] != true || reqs[1]["dry_run"] != nil || reqs[1]["user_message_id"] != "U2" {
			t.Fatalf("a dry run, then the real rewind of that message's files: %v", reqs)
		}
		mu.Unlock()
		found := false
		for _, it := range s.conv.Items {
			found = found || it.Kind == chat.Notice && strings.Contains(it.Text, "Restored 2 files to how they were at that message (+3 −7)")
		}
		if !found {
			t.Fatal("the note reports the file count from the dry run")
		}
		h.d.sendTo(s, "second, rewritten")
	})
	h.eventually("resumed at A1", func() bool { return strings.Contains(h.args(), "--resume-session-at A1") })
	h.ui(func() {
		if s.resumeAt != "" {
			t.Fatal("the resume point is used once")
		}
	})
}

func TestUIPassMenusSettingsTasks(t *testing.T) {
	h := newHarness(t)
	h.d.controlFn = func(_ *deskSession, req map[string]any) (json.RawMessage, error) {
		if req["subtype"] == "rewind_files" {
			return json.RawMessage(`{"canRewind":false,"skippedLinks":0}`), nil
		}
		return json.RawMessage(`{"mcpServers":[]}`), nil
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy && s.persisted })
	h.ui(func() {
		user := &chat.Item{Kind: chat.User, Text: "redo me", UUID: "U9"}
		reply := &chat.Item{Kind: chat.Assistant, Text: "answer", UUID: "A9"}
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "before", UUID: "A8"}, user, reply)
		h.d.open(s.id)
		labels := func(items []*fyne.MenuItem) string {
			var out []string
			for _, m := range items {
				if !m.IsSeparator {
					out = append(out, m.Label)
				}
			}
			return strings.Join(out, ",")
		}
		if got := labels(h.d.messageMenuItems(s, user)); got != "Copy message,Rewind to here,Rewind to here and undo file changes,Fork from here" {
			t.Fatalf("user message menu: %s", got)
		}
		if got := labels(h.d.messageMenuItems(s, reply)); got != "Copy message,Fork from here" {
			t.Fatalf("reply menu: %s", got)
		}
		if _, ok := h.d.chat.render(reply, &itemView{}).(*msgMenu); !ok {
			t.Fatal("messages are wrapped so a right-click opens the menu")
		}
		tools := h.d.sessionToolsItem(s.id)
		if got := labels(tools.ChildMenu.Items); got != "Rewind...,Fork,Export...,Show changes (git diff),Background tasks,MCP servers,Claude in Chrome...,Add working folder..." {
			t.Fatalf("session tools: %s", got)
		}
		before := len(h.w.Canvas().Overlays().List())
		h.d.showSettings()
		if len(h.w.Canvas().Overlays().List()) != before+1 || h.d.settingsDlg == nil {
			t.Fatal("Settings opens as one window")
		}
		var tabs *container.AppTabs
		deepWalk(h.w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
			if a, ok := o.(*container.AppTabs); ok {
				tabs = a
			}
		})
		var names []string
		for _, it := range tabs.Items {
			names = append(names, it.Text)
		}
		if strings.Join(names, ",") != "General,Sessions,Permissions,Tools,Memory" {
			t.Fatalf("settings tabs: %v", names)
		}
		h.d.closeSettings()
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"bgx","name":"Bash","input":{"command":"./test.sh","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"bgx","content":"Command running in background with ID: tsk1. Output is being written to: /tmp/o"}]`)}})
		h.d.updateChrome()
		if !h.d.tasksBtn.Visible() || h.d.tasksBtn.Text() != "Background Task" {
			t.Fatalf("the message bar shows this session's background task: %q %v", h.d.tasksBtn.Text(), h.d.tasksBtn.Visible())
		}
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"bgy","name":"Bash","input":{"command":"./other.sh","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"bgy","content":"Command running in background with ID: tsk2. Output is being written to: /tmp/o2"}]`)}})
		h.d.updateChrome()
		if h.d.tasksBtn.Text() != "Background Tasks" {
			t.Fatalf("two tasks read as plural, without names: %q", h.d.tasksBtn.Text())
		}
		relayout(h.d.mainPane)
		if w := h.d.tasksBtn.Size().Width; w > h.d.tasksBtn.natural()+1 || w < 60 {
			t.Fatalf("the tasks box is as wide as its text, not the row: %v (natural %v)", w, h.d.tasksBtn.natural())
		}
		var row *fyne.Container
		deepWalk(h.d.mainPane, func(o fyne.CanvasObject) {
			if c, ok := o.(*fyne.Container); ok {
				for i, x := range c.Objects {
					if x == fyne.CanvasObject(h.d.tasksBtn) && i > 0 && c.Objects[i-1] == fyne.CanvasObject(h.d.stt.langBtn) {
						row = c
					}
				}
			}
		})
		if row == nil {
			t.Fatal("the tasks box sits right after Voice in the options row")
		}
		if h.d.tasksBtn.Position().X <= h.d.stt.langBtn.Position().X {
			t.Fatal("to its right")
		}
		h.d.rewindTo(s, user, true)
	})
	h.eventually("rewound", func() bool { return s.resumeAt == "A8" })
	h.ui(func() {
		found := false
		for _, it := range s.conv.Items {
			found = found || it.Kind == chat.Notice && strings.Contains(it.Text, "no file changes to undo")
		}
		if !found {
			t.Fatal("canRewind:false is reported as nothing to undo, and the conversation still rewinds")
		}
	})
}

func TestKmemSafetyAndSelectionClicks(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		if !h.d.safePoint(s) {
			t.Fatal("an idle session is a safe point")
		}
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"bgz","name":"Bash","input":{"command":"./test.sh","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"bgz","content":"Command running in background with ID: tz1abcdef. Output is being written to: /tmp/o"}]`)}})
		if h.d.safePoint(s) {
			last := s.conv.Items[len(s.conv.Items)-1]
			t.Fatalf("never rotate a session while its background tasks run (bg=%d last=%+v)", s.conv.Background(), last)
		}
		old := h.d.session(editsID)
		h.d.org.Kmem[editsID] = "quill"
		ns, err := h.d.forkSession(old)
		if err != nil {
			t.Fatal(err)
		}
		if ns.forkOf != "quill" || h.d.org.Kmem[ns.id] != "" {
			t.Fatal("a fork of a kmem session is not linked to that kmem name")
		}
		h.d.sendTo(ns, "go on")
		var first string
		for _, it := range ns.conv.Items {
			if it.Kind == chat.User {
				first = it.Text
			}
		}
		if !strings.Contains(first, `fork of the session with kmem name "quill"`) || !strings.HasSuffix(first, "go on") || ns.forkOf != "" {
			t.Fatalf("the fork's first message tells Claude not to use the original's kmem identity: %q", first)
		}
	})
	h.ui(func() {
		s2 := h.d.createSession(h.proj, "default", "default")
		s2.conv.Items = append(s2.conv.Items,
			&chat.Item{Seq: 801, Kind: chat.User, Text: "my own words"},
			&chat.Item{Seq: 802, Kind: chat.Assistant, Text: "Alpha beta gamma"})
		h.d.open(s2.id)
		h.d.chat.relayoutInner()
		var userLabel *widget.Label
		deepWalk(h.d.chat.render(s2.conv.Items[0], &itemView{}), func(o fyne.CanvasObject) {
			if l, ok := o.(*widget.Label); ok && l.Text == "my own words" {
				userLabel = l
			}
		})
		if userLabel == nil || userLabel.Selectable {
			t.Fatal("user bubbles leave selection and right-click to Quill's own layer and menu")
		}
	})
	var runs []textRun
	h.eventually("laid out", func() bool { runs = collectRuns(h.d.chat.box); return len(runs) >= 2 })
	h.ui(func() {
		sel := h.d.chat.sel
		var alpha textRun
		for _, r := range runs {
			if strings.Contains(r.t.Text, "beta") {
				alpha = r
			}
		}
		abs := fyne.CurrentApp().Driver().AbsolutePositionForObject(sel)
		i := strings.Index(alpha.t.Text, "beta") + 1
		sel.selectWordAt(abs.Add(fyne.NewPos(charX(alpha, i), alpha.pos.Y+alpha.t.Size().Height/2)))
		if got := sel.text(); got != "beta" {
			t.Fatalf("double-click selects the word: %q", got)
		}
		var mm *msgMenu
		deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
			if m, ok := o.(*msgMenu); ok && mm == nil {
				mm = m
			}
		})
		mm.Tapped(&fyne.PointEvent{})
		if sel.has {
			t.Fatal("a click on a message clears the selection")
		}
		sel.selectAll()
		if got := sel.text(); !strings.Contains(got, "my own words") || !strings.Contains(got, "Alpha beta gamma") {
			t.Fatalf("Ctrl+A selects the whole chat: %q", got)
		}
		h.d.input.FocusGained()
		if sel.has {
			t.Fatal("focusing the message box clears the selection")
		}
		sel.selectAll()
		newBgTap(h.d.clearSelections).Tapped(&fyne.PointEvent{})
		if sel.has {
			t.Fatal("a click on empty space clears the selection")
		}
	})
}

func TestOneViewButton(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		for i := 0; i < 3; i++ {
			s := h.d.createSession(h.proj, "default", "default")
			h.d.open(s.id)
		}
		state := func() string {
			v := splitLabel(max(1, len(h.d.panes)))
			if h.d.autoChk.Checked {
				v = "Auto split"
			}
			icon := "single"
			switch h.d.viewBtn.Icon {
			case theme.GridIcon():
				icon = "grid"
			case theme.ViewRefreshIcon():
				icon = "auto"
			}
			return v + "/" + icon
		}
		if h.d.viewBtn.Text != "" {
			t.Fatal("the view button has no text")
		}
		pick := func(label string) {
			test.Tap(h.d.viewBtn)
			for _, it := range h.d.viewItems {
				if it.Label == label {
					h.d.viewMenu.Hide()
					it.Action()
					return
				}
			}
			t.Fatalf("no %q in the view menu", label)
		}
		test.Tap(h.d.viewBtn)
		var labels, checked []string
		for _, it := range h.d.viewItems {
			labels = append(labels, it.Label)
			if it.Checked {
				checked = append(checked, it.Label)
			}
		}
		if strings.Join(labels, ",") != "Single,Split 2,Split 3,Auto split" || strings.Join(checked, ",") != "Single" || !h.d.viewMenu.Visible() || state() != "Single/single" {
			t.Fatalf("the view button opens a menu and changes nothing yet: %v %v %s", labels, checked, state())
		}
		h.d.viewMenu.Hide()
		var seen []string
		for _, l := range []string{"Split 2", "Split 3", "Auto split", "Single"} {
			pick(l)
			seen = append(seen, state())
		}
		if strings.Join(seen, ",") != "Split 2/grid,Split 3/grid,Auto split/auto,Single/single" {
			t.Fatalf("picking a view applies it: %v", seen)
		}
		test.Tap(h.d.viewBtn)
		for _, it := range h.d.viewItems {
			if it.Checked != (it.Label == "Single") {
				t.Fatalf("the current view is ticked: %s %v", it.Label, it.Checked)
			}
		}
		h.d.viewMenu.Hide()
		if h.d.autoChk.Checked {
			t.Fatal("leaving Auto split turns it off")
		}
		if h.d.split() {
			t.Fatal("and goes back to a single view")
		}
	})
}

func TestReviewFixesChatQueue(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.open(s.id)
		s.compacting = true
		for _, m := range []string{"a", "b", "c"} {
			h.d.send(m)
		}
		h.d.input.SetText("half-written idea")
		h.d.editQueued(s, 2)
		if h.d.input.Text != "c" {
			t.Fatal("edit opens the queued text")
		}
		var st restoreState
		h.d.saveQueues(&st)
		if h.d.boxDraft(s, h.d.input) != "half-written idea" || !reflect.DeepEqual(st.Queues[s.id], []string{"a", "b", "c"}) {
			t.Fatal("a reload during an edit keeps the original queued and the real draft, never the unsaved edit")
		}
		h.d.historyKey(h.d.input, s, &fyne.KeyEvent{Name: fyne.KeyEscape})
		if h.d.input.Text != "half-written idea" {
			t.Fatalf("cancelling an edit gives the draft back: %q", h.d.input.Text)
		}
		h.d.editQueued(s, 2)
		h.d.sendQueuedNow(s, 0)
		if !reflect.DeepEqual(s.queue, []string{"a", "b"}) {
			t.Fatal("send now is refused while compacting")
		}
		s.queue = append([]string{}, s.queue[1:]...)
		s.editRemoved(0)
		h.d.historyKey(h.d.input, s, &fyne.KeyEvent{Name: fyne.KeyEscape})
		if !reflect.DeepEqual(s.queue, []string{"b", "c"}) {
			t.Fatalf("the edit goes back to its own place after rows before it were removed: %v", s.queue)
		}
		h.d.editQueued(s, 1)
		h.d.moveQueued(s, 0, 0)
		s.queue = append(s.queue, "z")
		h.d.moveQueued(s, 1, 0)
		h.d.historyKey(h.d.input, s, &fyne.KeyEvent{Name: fyne.KeyEscape})
		if !reflect.DeepEqual(s.queue, []string{"z", "b", "c"}) {
			t.Fatalf("moving rows keeps the edit slot after its neighbour: %v", s.queue)
		}
		s.compacting = false
		h.d.editQueued(s, 2)
		s.queue = nil
		h.d.historyKey(h.d.input, s, &fyne.KeyEvent{Name: fyne.KeyEscape})
		if len(s.queue) != 0 {
			t.Fatalf("an edit cancelled while the session is idle is sent, not stranded: %v", s.queue)
		}
		last := s.conv.Items[len(s.conv.Items)-1]
		if last.Kind != chat.User || last.Text != "c" {
			t.Fatalf("sent: %+v", last)
		}
	})
	h.eventually("idle again", func() bool { return !s.conv.Busy })
	h.ui(func() {
		other := h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.input.SetText("newest text")
		h.d.open(other.id)
		h.d.open(s.id)
		h.d.input.SetText("even newer")
		var st restoreState
		if !h.d.split() {
			h.d.strip.keepDraft(s.id, h.d.boxDraft(s, h.d.input))
		}
		h.d.saveSessions(&st)
		if st.Drafts[s.id] != "even newer" {
			t.Fatalf("the saved tab draft is the box's current text, not an older copy: %q", st.Drafts[s.id])
		}
	})
}

func TestDragPassThroughAndChoiceRefresh(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	var ask *chat.Item
	h.ui(func() {
		ask = &chat.Item{Seq: 970, Kind: chat.Permission, ToolName: "AskUserQuestion", PermState: chat.PermPending, RequestID: "q",
			Input: []byte(`{"questions":[{"question":"Which?","header":"Pick","multiSelect":false,"options":[{"label":"A","description":"x"}]}]}`)}
		s.conv.Items = append(s.conv.Items, ask)
		h.d.open(s.id)
		h.d.chat.relayoutInner()
	})
	h.eventually("entry laid out", func() bool {
		found := false
		deepWalk(h.d.chat.box, func(o fyne.CanvasObject) {
			if e, ok := o.(*widget.Entry); ok && e.Size().Width > 0 {
				found = true
			}
		})
		return found
	})
	h.ui(func() {
		var entry *widget.Entry
		var at fyne.Position
		origin := h.d.chat.box.Position()
		driver.WalkVisibleObjectTree(h.d.chat.box, func(o fyne.CanvasObject, pos fyne.Position, _ fyne.Position, _ fyne.Size) bool {
			if e, ok := o.(*widget.Entry); ok {
				entry, at = e, pos.Subtract(origin)
			}
			return false
		}, nil)
		p := at.Add(fyne.NewPos(5, 5))
		dr, _ := h.d.chat.sel.draggableAt(p)
		if dr == nil || (dr != fyne.Draggable(entry) && !strings.Contains(fmt.Sprintf("%T", dr), "entry")) {
			t.Fatalf("a drag that starts in a text box inside the chat goes to that box, got %T", dr)
		}
		h.d.chat.sel.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: p.Add(fyne.NewPos(20, 0))}, Dragged: fyne.NewDelta(20, 0)})
		if h.d.chat.sel.has || h.d.chat.sel.pass == nil {
			t.Fatal("and no chat selection starts")
		}
		h.d.chat.sel.DragEnd()
		ask.PermState = chat.PermCancelled
		msg := &chat.Item{Seq: 971, Kind: chat.Assistant, Text: "Done.\n\n**Push now, or wait?**"}
		s.conv.Items = append(s.conv.Items, msg)
		h.d.chat.update([]*chat.Item{msg})
		count := func() int {
			n := 0
			deepWalk(h.d.chat.views[msg.Seq].holder, func(o fyne.CanvasObject) {
				if b, ok := o.(*widget.Button); ok && (b.Text == "Push now" || b.Text == "Wait") {
					n++
				}
			})
			return n
		}
		if count() != 2 {
			t.Fatalf("choice buttons shown: %d", count())
		}
		h.d.sendTo(s, "my own answer")
		if count() != 0 {
			t.Fatal("replying removes the old card's buttons right away")
		}
	})
}

func TestReadOnlyHoles(t *testing.T) {
	for c, want := range map[string]bool{
		"sort -no /home/u/.bashrc in.txt":  false,
		"sort -rn data.txt":                true,
		"tree -ao out":                     false,
		"cat x | uniq - /tmp/target":       false,
		"cat x | uniq -c":                  true,
		`awk 'BEGIN{system ("rm -rf x")}'`: false,
		`awk '@load "inplace"; {print}' f`: false,
		`awk '{print $1}' f`:               true,
	} {
		if readOnlyCommand(c, nil) != want {
			t.Errorf("readOnlyCommand(%q) = %v", c, !want)
		}
	}
}

func TestStartFailureKeepsRewindAndCancelsKmemRestart(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.cfg.ClaudeBin = "/nonexistent/claude"
		s := h.d.createSession(h.proj, "default", "default")
		s.resumeAt = "A1"
		if err := h.d.ensureProcess(s); err == nil {
			t.Fatal("start should fail")
		}
		if s.resumeAt != "A1" {
			t.Fatal("a failed start keeps the rewind point for the next try")
		}
		h.d.org.Kmem[s.id] = "quill"
		h.d.relaunchViaKmem(s.id, "quill")
		if s.kmemSave != nil {
			t.Fatal("a kmem restart whose save request can't be sent is cancelled, not left waiting forever")
		}
	})
}

func TestLiveUsageAndPause(t *testing.T) {
	raw := json.RawMessage(`{"subscription_type":"max","rate_limits":{"five_hour":{"utilization":6,"resets_at":"2026-11-21T13:20:00.469099+00:00"},"seven_day":{"utilization":41,"resets_at":"2026-11-26T14:00:00.469118+00:00"},"seven_day_opus":null}}`)
	w, ok := parseUsageWindows(raw)
	if !ok || w["five_hour"].Utilization != 0.06 || w["seven_day"].Utilization != 0.41 || w["five_hour"].ResetsAt != time.Date(2026, 11, 21, 13, 20, 0, 0, time.UTC).Unix() {
		t.Fatalf("get_usage reply: %+v", w)
	}
	h := newHarness(t)
	var busy, idle *deskSession
	h.ui(func() {
		busy = h.d.createSession(h.proj, "default", "default")
		idle = h.d.createSession(h.proj, "default", "default")
		h.d.sendTo(busy, "hi")
		h.d.sendTo(idle, "hi")
	})
	h.eventually("idle", func() bool { return !busy.conv.Busy && !idle.conv.Busy && busy.running() && idle.running() })
	h.ui(func() {
		h.d.org.Kmem[editsID] = "launcher"
		h.d.phoneSync = &phoneSync{rc: map[string]string{"rc-1": editsID}}
		if m := h.d.phoneUsage().Meta["rc-1"]; m.Kmem != "launcher" {
			t.Fatalf("the phone feed carries each session's kmem name: %+v", m)
		}
		h.d.phoneSync = nil
		h.d.org.Kmem[idle.id] = "quill"
		h.d.rate = &protocol.RateLimit{OverageStatus: "allowed"}
		h.d.applyUsageWindows(map[string]protocol.RateWindow{"five_hour": {Utilization: 0.5, ResetsAt: time.Now().Add(time.Hour).Unix()}})
		if h.d.usagePaused || h.d.rate.OverageStatus != "allowed" {
			t.Fatal("polled windows merge into the stored usage; 50% doesn't pause")
		}
		busy.conv.Busy = true
		idle.queue = []string{"later"}
		reset := time.Now().Add(40 * time.Minute)
		h.d.applyUsageWindows(map[string]protocol.RateWindow{"five_hour": {Utilization: 0.96, ResetsAt: reset.Unix()}})
		if !h.d.usagePaused || !h.d.pauseBanner.Visible() || !reflect.DeepEqual(h.d.pausedIDs, []string{busy.id}) {
			t.Fatalf("96%% pauses: banner shown, working sessions asked to stop: %v", h.d.pausedIDs)
		}
		var asked bool
		for _, it := range busy.conv.Items {
			asked = asked || it.Kind == chat.User && strings.Contains(it.Text, "usage is at 96%")
		}
		if !asked {
			t.Fatal("the working session got the pause message")
		}
		h.d.flushQueue(idle)
		if len(idle.queue) != 1 {
			t.Fatal("queued messages wait while paused")
		}
		var st restoreState
		h.d.saveQueues(&st)
		h.d.saveSessions(&st)
		if h.d.usagePaused {
			st.Paused = &pauseState{Until: h.d.pauseUntil.Unix(), IDs: h.d.pausedIDs, Text: h.d.pauseText.Text}
		}
		if st.Paused == nil || st.Paused.Until != reset.Unix() {
			t.Fatal("the pause survives a live update")
		}
		busy.conv.Busy = false
		h.d.resumeFromPause()
		if h.d.usagePaused || h.d.pauseBanner.Visible() || len(idle.queue) != 0 {
			t.Fatal("resume lifts the pause and sends held queues")
		}
		var resumed bool
		for _, it := range busy.conv.Items {
			resumed = resumed || it.Kind == chat.User && it.Text == resumeMessage
		}
		if !resumed {
			t.Fatal("paused sessions are told to continue")
		}
		h.d.cmdCost(idle)
		h.d.cmdContext(idle)
		h.d.cmdStatus(idle)
		var notes []string
		for _, it := range idle.conv.Items {
			if it.Kind == chat.Notice {
				notes = append(notes, it.Text)
			}
		}
		all := strings.Join(notes, "\n")
		if !strings.Contains(all, "This session: $") || !strings.Contains(all, "kmem: quill") || !strings.Contains(all, "5-hour window") {
			t.Fatalf("/cost, /context, /status answer at once: %s", all)
		}
	})
}

func TestEscapePausedNotesAndChoiceReply(t *testing.T) {
	if got := choiceReply(0, "Take the square-building split next"); got != "I pick the first option: Take the square-building split next." {
		t.Fatalf("choice reply: %q", got)
	}
	if got := choiceReply(1, "Stop here"); got != "I pick the second option: Stop here." {
		t.Fatalf("choice reply: %q", got)
	}
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.sendTo(s, "hi")
	})
	h.eventually("idle", func() bool { return s.running() && !s.conv.Busy })
	h.ui(func() {
		h.d.usagePaused = true
		h.d.send("what is the status?")
		last := s.conv.Items[len(s.conv.Items)-1]
		if !strings.HasPrefix(last.Text, pausedNote) || !strings.HasSuffix(last.Text, "what is the status?") {
			t.Fatalf("questions still go through during a usage pause, marked so paused work stays paused: %q", last.Text)
		}
		h.d.usagePaused = false
	})
	h.eventually("idle again", func() bool { return !s.conv.Busy })
	h.ui(func() {
		h.d.chat.sel.has = true
		if !h.d.escape(s) || h.d.chat.sel.has {
			t.Fatal("Escape clears a selection first")
		}
		if h.d.escape(s) {
			t.Fatal("with nothing to clear and an idle session, Escape does nothing")
		}
		s.conv.Busy = true
		if !h.d.escape(s) {
			t.Fatal("Escape interrupts a working session")
		}
		h.d.setShare("inputShare", 1)
		if w := h.d.toolbarBox.Objects[0].Size().Width; w < h.d.inputWidth()-1 {
			t.Fatalf("the Settings/view row follows the message box width: %v vs %v", w, h.d.inputWidth())
		}
	})
}

func TestApprovalStopWordsAreWholeWords(t *testing.T) {
	for text, want := range map[string]bool{
		"yes, item 1 and the round corner button": true,
		"yes, nothing else":                       true,
		"ok, the stopwatch feature too":           true,
		"yes but skip the push":                   false,
		"yes, don't push":                         false,
		"yes, don’t push":                         false,
		"yes, wait for the build":                 false,
		"yes, no push":                            false,
		"yes, do it before lunch":                 false,
		"yes, can you also add tests?":            false,
		"yes approve all, but skip 3":             false,
		"yes approve all, add the buttons":        true,
	} {
		if isApproval(text) != want {
			t.Errorf("isApproval(%q) = %v", text, !want)
		}
	}
}

func TestSplitKeepsQueueAndToolbarPlacement(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		s.compacting = true
		h.d.send("queued while busy")
		h.d.setSplit(2)
		if !reflect.DeepEqual(s.queue, []string{"queued while busy"}) {
			t.Fatal("queue kept when splitting")
		}
		if h.d.toolbarBox.Visible() || len(h.d.panes[0].slot.Objects) == 0 || h.d.panes[0].slot.Objects[0] != fyne.CanvasObject(h.d.settingsBtn) {
			t.Fatal("in split view Settings sits in the first pane's gap above its message box")
		}
		last := h.d.panes[len(h.d.panes)-1].slot.Objects
		if last[len(last)-1] != fyne.CanvasObject(h.d.viewBtn) {
			t.Fatal("and the view button in the last pane's gap")
		}
		var empty *pane
		for _, p := range h.d.panes {
			if p.view.s == nil {
				empty = p
			}
		}
		h.d.closePane(empty)
		if h.d.split() || !h.d.toolbarBox.Visible() || h.d.toolbarRow.Objects[0] != fyne.CanvasObject(h.d.settingsBtn) {
			t.Fatal("closing the empty pane returns the toolbar to its single-view place")
		}
		if !reflect.DeepEqual(s.queue, []string{"queued while busy"}) {
			t.Fatalf("closing the empty pane keeps the queued message: %v", s.queue)
		}
		rows := 0
		for _, o := range h.d.chat.queueBox.Objects {
			if _, ok := o.(*queueRow); ok {
				rows++
			}
		}
		if rows != 1 {
			t.Fatalf("and the single view shows it again: %d rows", rows)
		}
	})
}

func TestNoStaleQueueRowsAcrossViews(t *testing.T) {
	h := newHarness(t)
	rows := func(v *chatView) int {
		n := 0
		for _, o := range v.queueBox.Objects {
			if _, ok := o.(*queueRow); ok {
				n++
			}
		}
		return n
	}
	var s, other *deskSession
	h.ui(func() {
		other = h.d.createSession(h.proj, "default", "default")
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		s.compacting = true
		h.d.send("queued in single view")
		if rows(h.d.single) != 1 {
			t.Fatal("queued row shows in single view")
		}
		h.d.setSplit(2)
		s.queue = nil
		h.d.syncQueue(s)
		h.d.setSplit(1)
		if rows(h.d.chat) != 0 {
			t.Fatal("a queue emptied in split view leaves no row behind in single view")
		}
		s.queue = []string{"again"}
		h.d.syncQueue(s)
		h.d.setSplit(2)
		var empty *pane
		for _, p := range h.d.panes {
			if p.view.s == nil {
				empty = p
			}
		}
		s.queue = nil
		h.d.syncQueue(s)
		h.d.closePane(empty)
		if rows(h.d.chat) != 0 {
			t.Fatal("closing the empty pane shows no stale row")
		}
		s.queue = []string{"third"}
		h.d.syncQueue(s)
		h.d.open(other.id)
		if rows(h.d.chat) != 0 {
			t.Fatal("another tab shows its own empty queue")
		}
		h.d.open(s.id)
		if rows(h.d.chat) != 1 {
			t.Fatal("and the queued tab shows its row again")
		}
	})
}

func TestSizeSlidersInSettings(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		inMain := false
		deepWalk(h.d.mainPane, func(o fyne.CanvasObject) {
			inMain = inMain || o == h.d.sizesCard
		})
		if inMain {
			t.Fatal("the sliders card no longer sits in the main window's corner")
		}
		h.d.showSettings()
		var sliders []*widget.Slider
		labels := map[string]bool{}
		deepWalk(h.w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
			if s, ok := o.(*widget.Slider); ok {
				sliders = append(sliders, s)
			}
			if c, ok := o.(*canvas.Text); ok {
				labels[c.Text] = true
			}
		})
		if len(sliders) != 2 || !labels["Chat width"] || !labels["Message box"] {
			t.Fatalf("Settings → General holds both sliders: %d %v", len(sliders), labels)
		}
		before := h.d.chatShare()
		sliders[0].SetValue(float64(minChatShare))
		if h.d.chatShare() == before && before != minChatShare {
			t.Fatal("moving the chat width slider still resizes the chat")
		}
		h.d.closeSettings()
	})
}

func TestLandscapePortraitGlide(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.layoutBtn.Text != labelLandscape {
			t.Fatalf("one switch showing the mode: %q", h.d.layoutBtn.Text)
		}
		test.Tap(h.d.layoutBtn)
		if h.d.layoutBtn.Text != labelPortrait || h.d.layoutMode() != layoutVertical || h.d.app.Preferences().String("tabLayout") != layoutVertical {
			t.Fatal("a click flips to Portrait and keeps the saved preference value")
		}
		if !glideUntil.IsZero() && time.Now().Before(glideUntil) {
			t.Fatal("no glide with animations off")
		}
		test.Tap(h.d.layoutBtn)
		if h.d.layoutBtn.Text != labelLandscape || h.d.layoutMode() != layoutHorizontal {
			t.Fatal("and back")
		}
	})
	mid := glideStep(rect{fyne.NewPos(0, 100), fyne.NewSize(400, 300)}, rect{fyne.NewPos(200, 0), fyne.NewSize(400, 500)}, 0.5)
	if mid != (rect{fyne.NewPos(100, 50), fyne.NewSize(400, 400)}) {
		t.Fatalf("halfway: %+v", mid)
	}
	h.ui(func() {
		box := canvas.NewRectangle(colCard)
		box.Move(fyne.NewPos(0, 100))
		box.Resize(fyne.NewSize(400, 300))
		to := rect{fyne.NewPos(200, 0), fyne.NewSize(400, 500)}
		placeGliding(box, to)
		if box.Position() != to.pos || box.Size() != to.size {
			t.Fatal("outside a view switch the layout is instant (window resizes, sliders)")
		}
		from := rect{fyne.NewPos(0, 100), fyne.NewSize(400, 300)}
		glideUntil = time.Now().Add(glideWindow)
		if !shouldGlide(from, to) || shouldGlide(to, to) || shouldGlide(rect{}, to) {
			t.Fatal("during a view switch a placed box glides; unplaced or unmoved boxes don't")
		}
		box.Move(from.pos)
		box.Resize(from.size)
		placeGliding(box, to)
		if box.Position() != to.pos || box.Size() != to.size {
			t.Fatal("and it ends at its new place")
		}
		glideUntil = time.Time{}
	})
}

func TestKmemRestartStaysInBackground(t *testing.T) {
	h := newHarness(t)
	var bg, mine, ns *deskSession
	h.ui(func() {
		bg = h.d.createSession(h.proj, "default", "default")
		mine = h.d.createSession(h.proj, "default", "default")
		h.d.open(mine.id)
		h.d.org.Kmem[bg.id] = "bgwork"
		slot := slices.Index(h.d.tabs, bg.id)
		bg.kmemSave = &kmemSave{name: "bgwork"}
		if bg.status() != stRestart {
			t.Fatal("saving the handoff shows the restart mark")
		}
		bg.kmemSave = nil
		h.d.finishKmemRelaunch(bg, "bgwork", "resume please")
		if h.d.current != mine {
			t.Fatal("a background restart doesn't switch the tab")
		}
		for _, id := range h.d.tabs {
			if s := h.d.sessions[id]; s != nil && h.d.org.Kmem[id] == "bgwork" {
				ns = s
			}
		}
		if ns == nil || slices.Index(h.d.tabs, ns.id) != slot {
			t.Fatalf("the new session takes the old tab's place: %v", h.d.tabs)
		}
		if ns.status() != stRestart {
			t.Fatalf("restarting until its first reply: %v", ns.status())
		}
		mark := statusMark(stRestart)
		if mark.Objects[1].Visible() != true || mark.Objects[0].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].Visible() {
			t.Fatal("the restart mark is the refresh icon, not the dot")
		}
		setStatusMark(mark, stIdle)
		if mark.Objects[1].Visible() {
			t.Fatal("other states show the dot")
		}
	})
	h.eventually("first reply clears the restart mark", func() bool { return ns.status() != stRestart && !ns.conv.Busy })
}
