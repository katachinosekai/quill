package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/protocol"
)

func TestClaudeCommandsAdaptedToQuill(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.cmdList = []protocol.Command{{Name: "color"}, {Name: "focus"}, {Name: "rename", Description: "Rename the current conversation"}, {Name: "compact"}}
		names := map[string]string{}
		for _, c := range h.d.commandsFor(s) {
			names[c.Name] = c.Description
		}
		if _, ok := names["color"]; ok {
			t.Error("/color (terminal only) is hidden")
		}
		if _, ok := names["focus"]; ok {
			t.Error("/focus (terminal only) is hidden")
		}
		if !strings.Contains(names["rename"], "Quill") || names["compact"] != "" || !strings.Contains(names["remote-control"], "Quill") || names["rc"] == "" {
			t.Errorf("Quill's /rename, /remote-control and /rc replace Claude Code's; others stay: %v", names)
		}
		if _, ok := names["compact"]; !ok {
			t.Error("/compact still goes to Claude Code")
		}

		if !h.d.runLocalCommandFor(s, "/rename Bug hunt") {
			t.Fatal("/rename is handled by Quill")
		}
		if title, _, _ := h.d.rowText(s.id); title != "Bug hunt" {
			t.Fatalf("/rename NAME renames the tab: %q", title)
		}
		if !h.d.runLocalCommandFor(s, "/focus") || !strings.Contains(s.conv.Items[len(s.conv.Items)-1].Text, "terminal") {
			t.Fatal("/focus is answered by Quill, not sent")
		}
		if !h.d.runLocalCommandFor(s, "/rc off") || !strings.Contains(s.conv.Items[len(s.conv.Items)-1].Text, "already off") {
			t.Fatal("/rc off on a session without Remote Control says so")
		}
		if !deviceCommandBlocked("/rename") || deviceCommandBlocked("/rename Phone name") || deviceCommandBlocked("/rc on") {
			t.Fatal("phones may rename with a name and use /rc; a bare /rename (a desktop dialog) is blocked")
		}
	})
}

func TestBackendMenuShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render the Online/Local/Remote menu to .shots/backend-menu.png")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "opus", "default")
		h.d.open(s.id)
		s.rcOn = true
		h.d.updateChrome()
		h.d.showBackendMenu(s, h.d.backendBtn, h.d.updateChrome)
	})
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { shot(t, h.w, dir, "backend-menu.png") })
}

func TestPCConnectShot(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render the paired-PC row and password dialog to .shots/")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	h := newHarness(t)
	conf := remotePCConf{ID: "a1b2c3d4", Name: "Office PC", Hosts: []string{"192.0.2.10"}, Port: 8443, Key: "k", FP: strings.Repeat("ab", 32)}
	h.ui(func() {
		h.d.saveRemotePCs([]remotePCConf{conf})
		h.d.pcNote[conf.ID] = "disconnected after 15 min without activity"
		h.d.refreshPCBox()
	})
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { test.Tap(h.d.pcsBtn) })
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { shot(t, h.w, dir, "pc-row.png") })
	h.ui(func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
	})
	h.ui(func() { h.d.connectPCDialog(conf) })
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { shot(t, h.w, dir, "pc-password.png") })
}
