package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/fakeclaude"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	editsID = "11111111-2222-3333-4444-555555555555"
	smokeID = "514ff30e-119d-4e8f-a1e8-660618817758"
)

func TestMain(m *testing.M) {
	if os.Getenv("CLAUDE_DESK_FAKE") == "1" {
		fakeclaude.Run()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var uiMu sync.Mutex

type harness struct {
	t       *testing.T
	d       *desk
	w       fyne.Window
	proj    string
	argsOut string
	termPID int
}

func procStart(t *testing.T, pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Skip("no /proc")
	}
	s := string(b)
	return strings.Fields(s[strings.LastIndexByte(s, ')')+1:])[19]
}

func newHarness(t *testing.T) *harness {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	ws := filepath.Join(root, "ws")
	for _, dir := range []string{proj, ws} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projects := filepath.Join(root, "projects")
	copyFixture := func(name, oldCwd, cwd, id string) {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "transcript", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(projects, transcript.Sanitize(cwd))
		os.MkdirAll(dir, 0o755)
		b = []byte(strings.ReplaceAll(string(b), `"`+oldCwd+`"`, `"`+cwd+`"`))
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyFixture("edits.jsonl", "/home/user/proj", proj, editsID)
	copyFixture("real-smoke.jsonl", "/tmp/ws", ws, smokeID)
	sessions := filepath.Join(root, "sessions")
	os.MkdirAll(sessions, 0o755)
	term := exec.Command("sleep", "120")
	if err := term.Start(); err != nil {
		t.Fatal(err)
	}
	go term.Wait()
	t.Cleanup(func() { term.Process.Kill() })
	live := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,"procStart":%q,"kind":"interactive","entrypoint":"cli"}`, term.Process.Pid, smokeID, ws, procStart(t, term.Process.Pid))
	os.WriteFile(filepath.Join(sessions, "1.json"), []byte(live), 0o600)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, proj: proj, argsOut: filepath.Join(root, "args"), termPID: term.Process.Pid}
	t.Setenv("CLAUDE_DESK_FAKE", "1")
	t.Setenv("FAKE_ARGS_OUT", h.argsOut)
	a := test.NewTempApp(t)
	a.Settings().SetTheme(deskTheme{})
	h.w = test.NewWindow(nil)
	t.Cleanup(h.w.Close)
	h.d = newDesk(a, h.w, config{ProjectsRoot: projects, SessionsDir: sessions, ClaudeBin: exe})
	h.d.do = func(f func()) {
		uiMu.Lock()
		defer uiMu.Unlock()
		f()
	}
	h.d.doWait = h.d.do
	h.ui(func() {
		h.w.SetContent(h.d.build())
		h.w.Resize(fyne.NewSize(1280, 860))
	})
	t.Cleanup(func() {
		var running []*deskSession
		h.ui(func() { running = h.d.runningSessions() })
		h.d.stopAll(running)
		h.eventually("exits handled", func() bool {
			n := 0
			h.d.ownPIDs.Range(func(any, any) bool { n++; return true })
			return n == 0
		})
	})
	h.d.refresh()
	return h
}

func (h *harness) ui(f func()) {
	uiMu.Lock()
	defer uiMu.Unlock()
	f()
}

func (h *harness) eventually(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		h.ui(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("timeout waiting for %s", what)
}

func walk(o fyne.CanvasObject, fn func(fyne.CanvasObject)) {
	fn(o)
	switch c := o.(type) {
	case *fyne.Container:
		for _, ch := range c.Objects {
			walk(ch, fn)
		}
	case *container.Scroll:
		walk(c.Content, fn)
	}
}

func (h *harness) button(label string) *widget.Button {
	var found *widget.Button
	walk(h.d.chat.box, func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == label && found == nil {
			found = b
		}
	})
	return found
}

func lastItem(s *deskSession) *chat.Item {
	if len(s.conv.Items) == 0 {
		return nil
	}
	return s.conv.Items[len(s.conv.Items)-1]
}

func (h *harness) args() string {
	b, _ := os.ReadFile(h.argsOut)
	return string(b)
}

func TestSidebarAndTerminalSession(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if len(h.d.order) != 2 {
			t.Fatalf("order %+v", h.d.order)
		}
		auto := folderRow(h.d.rows, "auto")
		if auto == nil || auto.count != 1 || auto.open || hasRow(h.d.rows, smokeID) || !hasRow(h.d.rows, editsID) {
			t.Fatalf("rows %+v", h.d.rows)
		}
		h.d.toggleFolder("auto")
		if !hasRow(h.d.rows, smokeID) {
			t.Fatal("folder should open")
		}
		h.d.toggleFolder("auto")
		h.d.created[smokeID] = true
		h.d.rebuildRows()
		if folderRow(h.d.rows, "auto") != nil || !hasRow(h.d.rows, smokeID) {
			t.Fatal("sessions made in Quill stay in the main tree")
		}
		delete(h.d.created, smokeID)
		h.d.rebuildRows()
		if _, meta, st := h.d.rowText(smokeID); st != stTerminal || !strings.Contains(meta, "open in a terminal") {
			t.Fatalf("terminal row %q %v", meta, st)
		}
		h.d.search.SetText("proj")
		if !hasRow(h.d.rows, editsID) || hasRow(h.d.rows, smokeID) {
			t.Fatalf("search rows %+v", h.d.rows)
		}
		h.d.search.SetText("")
		h.d.open(smokeID)
	})
	h.eventually("terminal transcript", func() bool { return h.d.sessions[smokeID].loaded })
	h.ui(func() {
		if h.d.input.Disabled() || !h.d.note.Visible() || !strings.Contains(h.d.note.Text, "takes it over") {
			t.Fatalf("terminal session should offer takeover: %q", h.d.note.Text)
		}
		h.d.send("hello")
	})
	h.eventually("took over", func() bool { return h.d.sessions[smokeID].running() })
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", h.termPID)); err == nil {
		b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", h.termPID))
		if !strings.Contains(string(b), ") Z ") {
			t.Fatal("terminal process still running")
		}
	}
	h.eventually("echo", func() bool {
		it := lastItem(h.d.sessions[smokeID])
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "hello")
	})
	h.ui(func() {
		s := h.d.sessions[smokeID]
		if s.terminal != nil || h.d.note.Visible() || !strings.Contains(h.args(), "--resume "+smokeID) {
			t.Fatalf("after takeover: note %v args %q", h.d.note.Visible(), h.args())
		}
	})
}

func TestResumePermissionAndInterrupt(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	s := h.d.sessions[editsID]
	h.ui(func() {
		diffs := 0
		for _, it := range s.conv.Items {
			if it.Kind == chat.Tool && len(it.Diff) > 0 {
				diffs++
			}
		}
		if diffs == 0 {
			t.Fatal("history should show edit diffs")
		}
		if h.d.input.Disabled() {
			t.Fatal("input should be enabled")
		}
		h.d.send("write")
	})
	h.eventually("permission prompt", func() bool { return s.status() == stWaiting && h.button("Allow") != nil })
	if a := h.args(); !strings.Contains(a, "--resume "+editsID) || !strings.Contains(a, "--permission-prompt-tool stdio") {
		t.Fatalf("args %s", a)
	}
	h.ui(func() {
		if h.d.stopBtn.Disabled() {
			t.Fatal("stop should be enabled while waiting")
		}
		test.Tap(h.button("Allow always (this session)"))
	})
	h.eventually("turn done", func() bool {
		it := lastItem(s)
		return it != nil && it.Text == "done allow+always" && s.status() == stIdle
	})
	h.ui(func() {
		if h.button("Allow") != nil {
			t.Fatal("answered prompt should drop its buttons")
		}
		h.d.send("slow")
	})
	h.eventually("streaming", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && it.Streaming && len(it.Text) > 2
	})
	h.ui(func() { test.Tap(h.d.stopBtn) })
	h.eventually("interrupted", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Notice && it.Text == "Interrupted" && s.status() == stIdle
	})
	if !s.running() {
		t.Fatal("interrupt should keep the process")
	}
}

func TestNewSessionsRunSideBySide(t *testing.T) {
	h := newHarness(t)
	var a, b *deskSession
	h.ui(func() {
		a = h.d.createSession(h.proj, "haiku", "plan")
		h.d.send("hello")
	})
	h.eventually("first reply", func() bool { it := lastItem(a); return it != nil && it.Text == "echo: hello" })
	if args := h.args(); !strings.Contains(args, "--session-id "+a.id) || !strings.Contains(args, "--model haiku") || !strings.Contains(args, "--permission-mode plan") {
		t.Fatalf("args %s", args)
	}
	h.ui(func() {
		b = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("second busy", func() bool { return b.status() == stRunning })
	h.ui(func() {
		h.d.open(a.id)
		if h.d.current != a || !b.running() || !a.running() {
			t.Fatal("switching must keep both processes")
		}
		if len(h.d.runningSessions()) != 2 {
			t.Fatal("expected two running sessions")
		}
		_, meta, _ := h.d.rowText(b.id)
		if !strings.Contains(meta, "running") {
			t.Fatalf("meta %q", meta)
		}
	})
	h.d.stopAll(h.d.runningSessions())
	h.eventually("all stopped", func() bool { return !a.running() && !b.running() && a.status() == stNone && b.status() == stNone })
	h.ui(func() {
		for _, it := range b.conv.Items {
			if it.Kind == chat.Error {
				t.Fatalf("clean stop should not report an error: %s", it.Text)
			}
		}
	})
}

func TestSendWaitsForHistoryLoad(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.open(editsID)
		s := h.d.sessions[editsID]
		if !s.loading || !h.d.sendBtn.Disabled() {
			t.Fatal("send should wait while the history loads")
		}
		h.d.send("too early")
		if s.proc != nil {
			t.Fatal("must not start claude before the history is loaded")
		}
	})
	h.eventually("history", func() bool { s := h.d.sessions[editsID]; return s.loaded && !s.loading })
	h.ui(func() {
		if h.d.sendBtn.Disabled() {
			t.Fatal("send should be enabled after loading")
		}
		h.d.send("hello")
	})
	h.eventually("echo", func() bool {
		it := lastItem(h.d.sessions[editsID])
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "hello")
	})
	h.ui(func() {
		s := h.d.sessions[editsID]
		before := s.conv
		h.d.infos[editsID] = transcript.Info{ID: editsID, Path: h.d.infos[editsID].Path, ModTime: time.Now()}
		s.loaded = false
		h.d.open(editsID)
		if s.conv != before || s.loading {
			t.Fatal("a running session must not reload its history")
		}
	})
}

func TestPromptSuggestion(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("suggestion", func() bool { return s.conv.Suggestion == "next after hello" })
	h.ui(func() {
		if !strings.Contains(h.d.input.PlaceHolder, "next after hello") || !h.d.input.AcceptsTab() {
			t.Fatalf("placeholder %q", h.d.input.PlaceHolder)
		}
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
		if h.d.input.Text != "next after hello" {
			t.Fatalf("tab gave %q", h.d.input.Text)
		}
		h.d.send(h.d.input.Text)
		if s.conv.Suggestion != "" || strings.Contains(h.d.input.PlaceHolder, "next after hello") {
			t.Fatal("suggestion should clear when a message is sent")
		}
	})
}

func TestSlashCommandMenu(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.cmdList = []protocol.Command{{Name: "help"}, {Name: "model"}}
		h.d.input.SetText("/")
		if !h.d.cmds.visible() || len(h.d.cmds.matches) != 2 {
			t.Fatalf("startup list should show: %+v", h.d.cmds.matches)
		}
		h.d.send("hi")
	})
	h.eventually("session commands", func() bool { return len(s.proc.Commands()) == 3 })
	h.ui(func() {
		h.d.input.SetText("/c")
		got := []string{}
		for _, c := range h.d.cmds.matches {
			got = append(got, c.Name)
		}
		if strings.Join(got, ",") != "clear,code-review,compact" {
			t.Fatalf("matches %v", got)
		}
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
		if h.d.input.Text != "/code-review " || h.d.cmds.visible() {
			t.Fatalf("tab gave %q", h.d.input.Text)
		}
		h.d.input.SetText("/comp")
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		if h.d.input.Text != "/compact " {
			t.Fatalf("enter gave %q", h.d.input.Text)
		}
		h.d.input.SetText("/zzz")
		if h.d.cmds.visible() {
			t.Fatal("no matches should hide the menu")
		}
		h.d.input.SetText("/clear")
		if h.d.cmds.visible() {
			t.Fatal("an exact full command should not keep the menu open")
		}
	})
}

func TestSidebarCollapse(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.setSidebar(false)
		free := h.d.root.Size().Width
		if h.d.sidebar.Visible() || !h.d.sideOff || h.d.sidebarWidth() != 0 || h.d.chatWidth() != max(chatMinWidth, free*chatShare) {
			t.Fatal("sidebar should be hidden")
		}
		test.Tap(h.d.sideToggle)
		if !h.d.sidebar.Visible() || h.d.sideOff || h.d.chatWidth() != max(chatMinWidth, (h.d.root.Size().Width-sideW)*chatShare) {
			t.Fatal("sidebar should be back")
		}
	})
}

func TestSwitchingKeepsRenderedViews(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	var first *fyne.Container
	h.ui(func() {
		first = h.d.chat.box
		h.d.createSession(h.proj, "default", "default")
		h.d.open(editsID)
		if h.d.chat.box != first {
			t.Fatal("switching back should reuse the rendered view")
		}
		h.d.chat.forget(h.d.sessions[editsID])
	})
}

func TestCtrlEnterStopsAndSends(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("busy", func() bool { return s.running() && s.proc.Busy() })
	h.ui(func() {
		h.d.input.SetText("hello")
		h.d.input.KeyDown(&fyne.KeyEvent{Name: desktop.KeyControlLeft})
		h.d.input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
		h.d.input.KeyUp(&fyne.KeyEvent{Name: desktop.KeyControlLeft})
	})
	h.eventually("echo after interrupt", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "hello")
	})
}

func TestAutoRemoteControl(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		h.d.autoRC = true
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("hello")
	})
	h.eventually("remote control on", func() bool { return s.rcOn && s.rcURL != "" })
	h.ui(func() {
		for _, it := range s.conv.Items {
			if it.Kind == chat.Notice && strings.Contains(it.Text, "Remote Control") {
				t.Fatal("auto Remote Control should not add chat notices")
			}
		}
	})
}

func TestInactiveAndArchiveFolders(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if f := folderRow(h.d.rows, "inactive"); f == nil || !f.open || !hasRow(h.d.rows, editsID) {
			t.Fatalf("recent idle sessions sit in an open Inactive folder: %+v", h.d.rows)
		}
		for i, info := range h.d.order {
			if info.ID == editsID {
				info.Last = time.Now().Add(-30 * 24 * time.Hour)
				h.d.order[i] = info
				h.d.infos[editsID] = info
			}
		}
		h.d.rebuildRows()
		if f := folderRow(h.d.rows, "archive"); f == nil || f.open || hasRow(h.d.rows, editsID) {
			t.Fatalf("old sessions go to a closed Archive folder: %+v", h.d.rows)
		}
		h.d.toggleFolder("archive")
		if !hasRow(h.d.rows, editsID) {
			t.Fatal("archive should open")
		}
		h.d.toggleFolder("archive")
		h.d.search.SetText("proj")
		if !hasRow(h.d.rows, editsID) {
			t.Fatal("search shows archived sessions")
		}
		h.d.search.SetText("")
		h.d.open(editsID)
		if !hasRow(h.d.rows, editsID) || folderRow(h.d.rows, "archive") != nil {
			t.Fatal("the open session is active")
		}
	})
}

func folderRow(rows []sideRow, key string) *sideRow {
	for i := range rows {
		if rows[i].header != "" && rows[i].key == key {
			return &rows[i]
		}
	}
	return nil
}

func hasRow(rows []sideRow, id string) bool {
	for _, r := range rows {
		if r.id == id {
			return true
		}
	}
	return false
}

func TestKfmt(t *testing.T) {
	for n, want := range map[int]string{999: "999", 1500: "1.5k", 75_700_000: "75.7M", 1_090_000_000: "1.09B", 3_530_000_000: "3.53B"} {
		if got := kfmt(n); got != want {
			t.Errorf("kfmt(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestTypingContinuesWithCommandList(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.createSession(h.proj, "default", "default")
		h.d.cmdList = []protocol.Command{{Name: "compact"}, {Name: "code-review"}, {Name: "clear"}}
		h.w.Canvas().Focus(h.d.input)
		typeFocused := func(text string) {
			f := h.w.Canvas().Focused()
			if f == nil {
				t.Fatal("the input lost keyboard focus")
			}
			test.Type(f, text)
		}
		typeFocused("/c")
		if !h.d.cmds.visible() {
			t.Fatal("list should be open")
		}
		typeFocused("om")
		if h.d.input.Text != "/com" || len(h.d.cmds.matches) != 1 || h.d.cmds.matches[0].Name != "compact" {
			t.Fatalf("typing stopped reaching the input: %q %+v", h.d.input.Text, h.d.cmds.matches)
		}
	})
}

func TestSidebarOrganisation(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.org.Titles[editsID] = "My rename"
		h.d.saveOrg()
		if title, _, _ := h.d.rowText(editsID); title != "My rename" {
			t.Fatalf("rename: %q", title)
		}
		h.d.setPlacement(editsID, placeArchive)
		if f := folderRow(h.d.rows, "archive"); f == nil || f.count != 1 {
			t.Fatalf("set archive: %+v", h.d.rows)
		}
		h.d.org.Folders = append(h.d.org.Folders, orgFolder{ID: "x", Name: "Work"})
		h.d.saveOrg()
		if f := folderRow(h.d.rows, "f:x"); f == nil || f.count != 0 || f.header != "Work" {
			t.Fatalf("empty custom folder should show: %+v", h.d.rows)
		}
		h.d.dropOn(editsID, *folderRow(h.d.rows, "f:x"))
		if f := folderRow(h.d.rows, "f:x"); f == nil || f.count != 1 || !hasRow(h.d.rows, editsID) {
			t.Fatalf("drop into folder: %+v", h.d.rows)
		}
		h.d.created[smokeID] = true
		h.d.rebuildRows()
		var target sideRow
		for _, r := range h.d.rows {
			if r.id == editsID {
				target = r
			}
		}
		h.d.dropOn(smokeID, target)
		if f := folderRow(h.d.rows, "f:x"); f == nil || f.count != 2 {
			t.Fatalf("drop onto a session joins its folder: %+v", h.d.rows)
		}
		h.d.org.Names["inactive"] = "Later"
		h.d.setPlacement(smokeID, placeInactive)
		if f := folderRow(h.d.rows, "inactive"); f == nil || f.header != "Later" {
			t.Fatalf("renamed built-in folder: %+v", h.d.rows)
		}
		h.d.setPlacement(editsID, "")
		h.d.loadOrg()
		if h.d.org.Titles[editsID] != "My rename" || h.d.org.Place[smokeID] != placeInactive {
			t.Fatal("organisation should persist in preferences")
		}
	})
}

func TestResumeAndEndSession(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() { h.d.resumeSession(editsID) })
	h.eventually("resumed", func() bool { return h.d.sessions[editsID].running() })
	h.ui(func() { h.d.endSession(editsID) })
	h.eventually("ended", func() bool { return !h.d.sessions[editsID].running() })
}

func TestSidebarDragAndMenus(t *testing.T) {
	h := newHarness(t)
	var item, folder *sideItem
	h.ui(func() {
		h.d.org.Folders = append(h.d.org.Folders, orgFolder{ID: "y", Name: "Drop here"})
		h.d.saveOrg()
		h.d.list.Refresh()
	})
	h.eventually("rows rendered", func() bool {
		item, folder = nil, nil
		for _, it := range h.d.sideItems {
			r, ok := it.row()
			if !ok || !it.Visible() {
				continue
			}
			if r.id == editsID {
				item = it
			}
			if r.key == "f:y" {
				folder = it
			}
		}
		return item != nil && folder != nil
	})
	h.ui(func() {
		test.TapSecondary(item)
		test.TapSecondary(folder)
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		at := fyne.CurrentApp().Driver().AbsolutePositionForObject(folder).Add(fyne.NewPos(20, 5))
		item.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: at}, Dragged: fyne.Delta{DY: 1}})
		if !folder.hl.Visible() {
			t.Fatal("drop target should be highlighted")
		}
		item.DragEnd()
		if h.d.org.Place[editsID] != "f:y" || folder.hl.Visible() {
			t.Fatalf("drag should move the session: %q", h.d.org.Place[editsID])
		}
	})
}

func TestStepChipsToggleInline(t *testing.T) {
	h := newHarness(t)
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() {
		var bash *chat.Item
		for _, it := range h.d.sessions[editsID].conv.Items {
			if it.Kind == chat.Tool && it.ToolName == "Bash" {
				bash = it
			}
		}
		if bash == nil {
			t.Fatal("fixture has no Bash step")
		}
		h.d.chat.toggleStep(bash)
		if !h.d.chat.stepOpen[bash] {
			t.Fatal("chip should open its step")
		}
		other := h.d.createSession(h.proj, "default", "default")
		other.conv.AddUser("x")
		for _, it := range other.conv.Items {
			if h.d.chat.stepOpen[it] {
				t.Fatal("open state must not leak into another session")
			}
		}
	})
}

func TestSpeechToText(t *testing.T) {
	h := newHarness(t)
	h.d.do = func(f func()) {
		go func() {
			uiMu.Lock()
			defer uiMu.Unlock()
			f()
		}()
	}
	sp := h.d.stt
	recorded := false
	sp.ready = func() error { return nil }
	sp.record = func() (func() (string, error), error) {
		recorded = true
		return func() (string, error) { return "/tmp/fake.wav", nil }, nil
	}
	sp.recognize = func(ctx context.Context, wav string) (string, error) { return "hello from my voice", nil }
	h.ui(func() {
		h.d.createSession(h.proj, "default", "default")
		h.d.app.Preferences().SetBool("sttNoticeShown", false)
		sp.click()
		if recorded || len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("first use must show the privacy notice before recording")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.app.Preferences().SetBool("sttNoticeShown", true)
		h.d.input.SetText("note:")
		sp.click()
		if !recorded || sp.state != sttRecording {
			t.Fatal("second click should record")
		}
		sp.click()
	})
	h.eventually("transcribed", func() bool { return sp.state == sttIdle && h.d.input.Text == "note: hello from my voice" })
}

func TestCleanTranscript(t *testing.T) {
	if got := cleanTranscript("\n [BLANK_AUDIO]\n Hello there.\n  How are you?\n"); got != "Hello there. How are you?" {
		t.Fatalf("%q", got)
	}
}

func TestChatFollowsAndJumpButton(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 40; i++ {
			s.conv.AddUser(fmt.Sprintf("message %d with enough words to take some room in the chat", i))
		}
		h.d.chat.show(s)
		if !h.d.chat.follow || h.d.chat.jump.Visible() {
			t.Fatal("a freshly opened chat follows the bottom")
		}
		h.d.chat.scroll.Offset = fyne.NewPos(0, 0)
		h.d.chat.scroll.OnScrolled(h.d.chat.scroll.Offset)
		if h.d.chat.follow || !h.d.chat.jump.Visible() {
			t.Fatal("scrolling up should stop following and show the Latest button")
		}
		h.d.chat.update([]*chat.Item{s.conv.AddUser("new while reading history")})
		if h.d.chat.scroll.Offset.Y != 0 {
			t.Fatal("must not scroll down while the user reads history")
		}
		h.d.chat.glideBottom()
		if !h.d.chat.follow || h.d.chat.jump.Visible() || !h.d.chat.atBottom() {
			t.Fatal("Latest should bring the view to the bottom and resume following")
		}
		h.d.chat.update([]*chat.Item{s.conv.AddUser("one more")})
		if !h.d.chat.atBottom() {
			t.Fatal("following view should stay at the bottom")
		}
	})
}

func TestCenteredListsBecomeParagraphs(t *testing.T) {
	rt := widget.NewRichTextFromMarkdown("intro\n\n- one\n- two\n\n1. first\n2. second")
	segs := centerLists(rt.Segments, "")
	var text []string
	for _, s := range segs {
		if _, ok := s.(*widget.ListSegment); ok {
			t.Fatal("lists should be flattened when centered")
		}
		if p, ok := s.(*widget.ParagraphSegment); ok {
			var b strings.Builder
			for _, x := range p.Texts {
				b.WriteString(x.Textual())
			}
			text = append(text, b.String())
		}
	}
	joined := strings.Join(text, "|")
	for _, want := range []string{"• one", "• two", "1. first", "2. second"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}

func TestFlatSessionsUnderMainFolders(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		for _, r := range h.d.rows {
			if r.header != "" && r.depth > 0 {
				t.Fatalf("no project subfolders by default: %+v", h.d.rows)
			}
		}
		if !hasRow(h.d.rows, editsID) {
			t.Fatal("sessions should still be listed")
		}
		h.d.groupProjects = true
		h.d.rebuildRows()
		sub := false
		for _, r := range h.d.rows {
			if r.header != "" && r.depth > 0 {
				sub = true
			}
		}
		if !sub {
			t.Fatal("grouping by project should bring the subfolders back")
		}
	})
}
