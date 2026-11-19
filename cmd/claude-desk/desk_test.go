package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/claude-desk/internal/chat"
	"github.com/katachinosekai/claude-desk/internal/fakeclaude"
	"github.com/katachinosekai/claude-desk/internal/transcript"
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
}

func procStart(t *testing.T) string {
	b, err := os.ReadFile("/proc/self/stat")
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
	live := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":%q,"procStart":%q,"kind":"interactive","entrypoint":"cli"}`, os.Getpid(), smokeID, ws, procStart(t))
	os.WriteFile(filepath.Join(sessions, "1.json"), []byte(live), 0o600)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, proj: proj, argsOut: filepath.Join(root, "args")}
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
		headers := 0
		for _, r := range h.d.rows {
			if r.header != "" {
				headers++
			}
		}
		if headers != 2 || len(h.d.rows) != 4 {
			t.Fatalf("rows %+v", h.d.rows)
		}
		if _, meta, st := h.d.rowText(smokeID); st != stTerminal || !strings.Contains(meta, "open in a terminal") {
			t.Fatalf("terminal row %q %v", meta, st)
		}
		h.d.search.SetText("proj")
		if len(h.d.rows) != 2 || h.d.rows[1].id != editsID {
			t.Fatalf("search rows %+v", h.d.rows)
		}
		h.d.search.SetText("")
		h.d.open(smokeID)
	})
	h.eventually("terminal transcript", func() bool { return h.d.sessions[smokeID].loaded })
	h.ui(func() {
		if !h.d.input.Disabled() || !h.d.note.Visible() || !strings.Contains(h.d.note.Text, "terminal") {
			t.Fatal("terminal session should be read-only")
		}
		h.d.send("hello")
		if h.d.sessions[smokeID].proc != nil {
			t.Fatal("must not start claude for a session open in a terminal")
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
