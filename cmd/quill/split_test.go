package main

import (
	"os"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
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
		if len(h.d.tabs) < 2 || len(h.d.tabBar.Objects) < 2 {
			t.Fatalf("opening sessions adds tabs: %v", h.d.tabs)
		}
		h.d.closeTab(b.id)
		if h.d.current != a {
			t.Fatal("closing the active tab switches to a neighbour")
		}
		h.d.rebuildRows()
		if len(h.d.groups) == 0 || len(h.d.bookBar.Objects) != len(h.d.groups) {
			t.Fatal("each folder gets a bookmark button")
		}
		h.d.folderMenu2(h.d.groups[0], fyne.NewPos(10, 10))
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("a folder opens as a drop-down overlay")
		}
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
