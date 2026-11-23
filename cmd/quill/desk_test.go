package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/fakeclaude"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
	"github.com/katachinosekai/quill/internal/whisper"
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
	os.Exit(runIsolated(m))
}

// realTranscriptRoot: the user's transcripts, for the opt-in real probes;
// realHome, realKmemPath: the user's home and kmem, read (never run) by
// TestBundledKmemIsCurrent.
var realTranscriptRoot, realHome, realKmemPath string

// runIsolated runs the tests away from the real kmem and its live store
// (Kmem Lock Audit 2026-11-22: test desks ran ~/.local/bin/kmem thousands
// of times, as the running Claude session): a temporary HOME, no session
// variables, and a "kmem" first on PATH that only records that it ran, which
// fails the run.
func runIsolated(m *testing.M) int {
	realTranscriptRoot = transcript.DefaultRoot()
	realHome, _ = os.UserHomeDir()
	realKmemPath, _ = exec.LookPath("kmem")
	tmp, err := os.MkdirTemp("", "quill-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	home := filepath.Join(tmp, "home")
	bin := filepath.Join(tmp, "bin")
	calls := filepath.Join(tmp, "kmem-calls")
	for _, dir := range []string{home, bin} {
		os.MkdirAll(dir, 0o755)
	}
	for _, k := range []string{"CLAUDE_CODE_SESSION_ID", "KMEM_SESSION", "KMEM_CS", "KMEM_HOOK_INPUT", "KMEM_CONFIG", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		os.Unsetenv(k)
	}
	os.Setenv("HOME", home)
	if runtime.GOOS != "windows" {
		script := "#!/bin/sh\necho \"kmem $*\" >> '" + calls + "'\nexit 1\n"
		os.WriteFile(filepath.Join(bin, "kmem"), []byte(script), 0o755)
		os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	code := m.Run()
	if b, err := os.ReadFile(calls); err == nil && len(b) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: tests ran kmem %d times; stub d.kmemRun (stubKmem) instead:\n%s", strings.Count(string(b), "\n"), b)
		return 1
	}
	return code
}

// stubKmem: a test desk's kmem, which never runs a program. It reports the
// kmem API Quill Desktop speaks, as an installed kmem would, and fails
// everything else; tests that need kmem output set their own d.kmemRun.
func stubKmem(d *desk) {
	d.kmemRun = func(prog string, args ...string) (string, error) {
		if prog == "kmem" && len(args) >= 1 && args[0] == "version" {
			return fmt.Sprintf(`{"api":%d,"version":"0.0.0-test"}`, kmemAPI), nil
		}
		return "", fmt.Errorf("kmem is stubbed in tests")
	}
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
	uiMu.Lock()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(deskTheme{})
	h.w = test.NewWindow(nil)
	t.Cleanup(h.w.Close)
	h.d = newDesk(a, h.w, config{ProjectsRoot: projects, SessionsDir: sessions, ClaudeBin: exe})
	stubKmem(h.d)
	uiMu.Unlock()
	h.d.planFirst = false
	h.d.kmemOff = true
	h.d.fetchLocal = func(string) []localInfo { return nil }
	h.d.pingLocal = func(string) bool { return true }
	h.d.compactor = func(context.Context, string, string) (string, error) { return "Goal: test brief", nil }
	gateUI(t, h.d)
	h.ui(func() {
		h.w.SetContent(h.d.build())
		h.w.Resize(fyne.NewSize(1280, 860))
	})
	t.Cleanup(func() {
		var running []*deskSession
		h.ui(func() { running = h.d.runningSessions() })
		h.d.stopAll(running)
		deadline := time.Now().Add(30 * time.Second)
		for {
			var left []string
			h.d.ownPIDs.Range(func(k, v any) bool { left = append(left, fmt.Sprint(k, "=", v)); return true })
			if len(left) == 0 {
				break
			}
			// Sessions started in the background after the first snapshot
			// (resume-all, rotations) are stopped too.
			var late []*deskSession
			h.ui(func() {
				for _, s := range h.d.runningSessions() {
					if !s.stopping {
						late = append(late, s)
					}
				}
			})
			if len(late) > 0 {
				h.d.stopAll(late)
			}
			if time.Now().After(deadline) {
				var ids []string
				h.ui(func() {
					for id, s := range h.d.sessions {
						ids = append(ids, fmt.Sprintf("%s running=%v stopping=%v", id, s.running(), s.stopping))
					}
				})
				h.t.Fatalf("exits not handled: pids %v; sessions %v", left, ids)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	h.d.refresh()
	return h
}

// gateUI runs a test desk's UI work under uiMu and drops it once the test
// has ended (its cleanup runs after the desk's own shutdown cleanups, which
// still need it). fyne's theme-override stack is global and unsynchronised:
// a finished test's background work (stream reveal, timers) laying out its
// old desk while the next test builds one made theme lookups return nil
// (TestDeskWithFixtures panicked now and then, 2026-11-22).
func gateUI(t *testing.T, d *desk) {
	done := false
	d.do = func(f func()) {
		uiMu.Lock()
		defer uiMu.Unlock()
		if !done {
			f()
		}
	}
	d.doWait = d.do
	t.Cleanup(func() {
		uiMu.Lock()
		done = true
		uiMu.Unlock()
	})
}

func (h *harness) ui(f func()) {
	uiMu.Lock()
	defer uiMu.Unlock()
	f()
}

func (h *harness) eventually(what string, cond func() bool) {
	h.t.Helper()
	h.eventuallyFor(what, 10*time.Second, cond)
}

func (h *harness) eventuallyFor(what string, wait time.Duration, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(wait)
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
		if hasRow(h.d.rows, smokeID) || hasRow(h.d.rows, editsID) {
			t.Fatalf("the sidebar lists live sessions only, not every transcript: %+v", h.d.rows)
		}
		h.d.open(editsID)
		if !hasRow(h.d.rows, editsID) || hasRow(h.d.rows, smokeID) {
			t.Fatalf("an opened session is live and listed: %+v", h.d.rows)
		}
		if p := folderRow(h.d.rows, projectPrefix+h.d.projectOf(editsID)); p == nil || p.count != 1 || !p.open {
			t.Fatalf("live sessions sit under their project: %+v", h.d.rows)
		}
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
		h.d.chat.btnChanged = time.Time{}
		test.Tap(h.button("Allow always (this session)"))
	})
	h.eventually("turn done", func() bool {
		it := lastItem(s)
		return it != nil && it.Text == "done allow+always" && s.status() == stIdle
	})
	h.ui(func() {
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"bgq","name":"Bash","input":{"command":"./launch.sh","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"bgq","content":"Command running in background with ID: tqa1. Output is being written to: /tmp/q"}]`)}})
		if s.status() != stRunning {
			t.Fatalf("a background task alone still reads as running: %v bg=%d running=%v", s.status(), s.conv.Background(), s.running())
		}
		if _, meta, _ := h.d.rowText(s.id); !strings.Contains(meta, "running  ·  1 in background") {
			t.Fatalf("the sidebar says the work is in the background: %q", meta)
		}
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"text","text":"Tests are running.\n\n**Should I also rebuild the launcher?**"}]`)}})
		if s.conv.Background() == 0 || s.status() != stWaiting {
			t.Fatalf("a finished turn ending in a question waits for the user even with background tasks: %v", s.status())
		}
		s.conv.AddUser("yes")
		if s.status() != stRunning {
			t.Fatal("once answered, the background task counts again")
		}
		s.conv.ClearBackground()
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
	if a := h.args(); !strings.Contains(a, "interrupt:spare") || strings.Contains(a, "interrupt:all") {
		t.Fatalf("Stop must spare background subagents: %q", a)
	}
}

// Stop keeps a running background subagent: the interrupt spares it and the
// session still lists it; "Stop everything" in the tasks menu ends it all.
func TestStopKeepsBackgroundSubagents(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("slow")
	})
	h.eventually("streaming text", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && it.Streaming && len(it.Text) > 2
	})
	var stopAll *fyne.MenuItem
	h.ui(func() {
		s.conv.Apply(&protocol.Event{Type: "assistant", Message: &protocol.Message{Role: "assistant", Content: []byte(`[{"type":"tool_use","id":"ag1","name":"Agent","input":{"description":"Check docs","prompt":"look","run_in_background":true}}]`)}})
		s.conv.Apply(&protocol.Event{Type: "user", Message: &protocol.Message{Role: "user", Content: []byte(`[{"type":"tool_result","tool_use_id":"ag1","content":"Async agent launched successfully. agentId: a1b2c3d4e5"}]`)}})
		if s.conv.Background() != 1 {
			t.Fatalf("background agent not tracked: %d", s.conv.Background())
		}
		for _, it := range h.d.taskItems(s) {
			if it.Label == stopEverythingLabel {
				stopAll = it
			}
		}
		if stopAll == nil {
			t.Fatalf("tasks menu has no stop-everything item: %v", menuLabels(h.d.taskItems(s)))
		}
		test.Tap(h.d.stopBtn)
	})
	// The session still shows as working: its background subagent runs on.
	h.eventually("interrupted", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Notice && it.Text == "Interrupted" && !s.conv.Busy && s.status() == stRunning
	})
	h.ui(func() {
		if a := h.args(); !strings.Contains(a, "interrupt:spare") {
			t.Fatalf("Stop sent %q", a)
		}
		if s.conv.Background() != 1 {
			t.Fatal("Stop must not drop the running background subagent")
		}
		h.d.send("slow")
	})
	h.eventually("streaming again", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && it.Streaming && len(it.Text) > 2
	})
	h.ui(func() { stopAll.Action() })
	h.eventually("stopped everything", func() bool { return strings.Contains(h.args(), "interrupt:all") })
	h.ui(func() {
		s.conv.ClearBackground()
		for _, it := range h.d.taskItems(s) {
			if it.Label == stopEverythingLabel {
				t.Fatal("no stop-everything item without background tasks")
			}
		}
	})
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
		if !h.d.cmds.visible() || len(h.d.cmds.matches) != min(len(localCommands)+1, maxCmdRows) {
			t.Fatalf("startup list should show: %+v", h.d.cmds.matches)
		}
		h.d.send("hi")
	})
	h.eventually("session commands", func() bool { return len(s.proc.Commands()) == 3 })
	h.ui(func() {
		h.d.input.SetText("/co")
		got := []string{}
		for _, c := range h.d.cmds.matches {
			got = append(got, c.Name)
		}
		if strings.Join(got, ",") != "code-review,compact,context,copy,cost,remote-control" {
			t.Fatalf("matches %v", got)
		}
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
		if h.d.sidebar.Visible() || !h.d.sideOff || h.d.sidebarWidth() != 0 || h.d.chatWidth() != max(chatMinWidth, free*h.d.chatShare()) {
			t.Fatal("sidebar should be hidden")
		}
		test.Tap(h.d.sideToggle)
		if !h.d.sidebar.Visible() || h.d.sideOff || h.d.chatWidth() != max(chatMinWidth, (h.d.root.Size().Width-sideW)*h.d.chatShare()) {
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

func TestRemoteControlNamesAreStableTitles(t *testing.T) {
	h := newHarness(t)
	out := filepath.Join(t.TempDir(), "renames")
	t.Setenv("FAKE_RENAME_OUT", out)
	renames := func() []string {
		b, _ := os.ReadFile(out)
		var r []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "" {
				r = append(r, l)
			}
		}
		return r
	}
	var s *deskSession
	h.ui(func() {
		h.d.autoRC = true
		h.d.org.Titles[editsID] = "Fix edits"
		h.d.open(editsID)
		s = h.d.current
	})
	h.eventually("history", func() bool { return s.loaded && !s.loading })
	h.ui(func() { h.d.send("hello") })
	var first string
	h.eventually("rename to the Quill title", func() bool {
		r := renames()
		if len(r) == 0 {
			return false
		}
		first = r[0]
		return true
	})
	h.ui(func() {
		if first != "Fix edits" {
			t.Fatalf("agent name should be the Quill title, got %q", first)
		}
		h.d.setPlacement(editsID, placeArchive)
		h.d.rebuildRows()
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(renames()); n != 1 {
		t.Fatalf("moving folders must not rename the agent, got %q", renames())
	}
	h.ui(func() {
		h.d.org.Titles[editsID] = "Fix edits v2"
		h.d.saveOrg()
	})
	h.eventually("rename after title change", func() bool {
		r := renames()
		return len(r) == 2 && r[1] == "Fix edits v2"
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

func TestSidebarProjects(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.org.Titles[editsID] = "My rename"
		h.d.saveOrg()
		if title, _, _ := h.d.rowText(editsID); title != "My rename" {
			t.Fatalf("rename: %q", title)
		}
		h.d.open(editsID)
		other := h.d.createSession(h.proj, "default", "default")
		if h.d.projectOf(editsID) != "My" {
			t.Fatalf("without kmem config the project is the title's first word: %q", h.d.projectOf(editsID))
		}
		h.d.setProject(editsID, "Zomboid")
		if f := folderRow(h.d.rows, "p:Zomboid"); f == nil || f.count != 1 || f.header != "Zomboid" {
			t.Fatalf("project header: %+v", h.d.rows)
		}
		h.d.dropOn(other.id, *folderRow(h.d.rows, "p:Zomboid"))
		if f := folderRow(h.d.rows, "p:Zomboid"); f == nil || f.count != 2 {
			t.Fatalf("drop onto a project header moves the session there: %+v", h.d.rows)
		}
		h.d.renameProject("Zomboid", "PZ")
		if f := folderRow(h.d.rows, "p:PZ"); f == nil || f.count != 2 || folderRow(h.d.rows, "p:Zomboid") != nil {
			t.Fatalf("rename project moves its sessions: %+v", h.d.rows)
		}
		h.d.org.Kmem[editsID] = "edits"
		h.d.setProject(editsID, "Quill")
		if h.d.org.Projects["edits"] != "Quill" {
			t.Fatal("the project belongs to the kmem name (one identity)")
		}
		h.d.setProject(other.id, "")
		h.d.loadOrg()
		if h.d.org.Titles[editsID] != "My rename" || h.d.org.Projects["edits"] != "Quill" {
			t.Fatal("projects persist in preferences")
		}
		h.d.closeTab(editsID)
		if hasRow(h.d.rows, editsID) {
			t.Fatal("closing the tab takes the session out of the sidebar")
		}
	})
}

func TestProjectFromKmemConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	os.WriteFile(cfg, []byte(`{"sessions": {"quill": {"as": "Quill", "topic": "Quill desktop app"}, "pz": {"topic": "kmem and pzlock upkeep", "project": "Upkeep"}}}`), 0o600)
	old := kmemCfgPath
	kmemCfgPath = func() string { return cfg }
	kmemCfgMu.Lock()
	kmemCfgSess = nil
	kmemCfgMu.Unlock()
	defer func() {
		kmemCfgPath = old
		kmemCfgMu.Lock()
		kmemCfgSess = nil
		kmemCfgMu.Unlock()
	}()
	h := newHarness(t)
	h.ui(func() {
		h.d.org.Kmem["a"] = "quill"
		h.d.org.Kmem["b"] = "pz"
		if p := h.d.projectOf("a"); p != "Quill" {
			t.Fatalf("first word of the kmem topic: %q", p)
		}
		if p := h.d.projectOf("b"); p != "Upkeep" {
			t.Fatalf("kmem's project label wins over the topic: %q", p)
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
	var item, header *sideItem
	var other *deskSession
	h.ui(func() {
		h.d.open(editsID)
		other = h.d.createSession(h.proj, "default", "default")
		h.d.setProject(other.id, "Drop here")
		h.d.list.Refresh()
	})
	h.eventually("rows rendered", func() bool {
		item, header = nil, nil
		for _, it := range h.d.sideItems {
			r, ok := it.row()
			if !ok || !it.Visible() {
				continue
			}
			if r.id == editsID {
				item = it
			}
			if r.key == "p:Drop here" {
				header = it
			}
		}
		return item != nil && header != nil
	})
	h.ui(func() {
		labels := func(items []*fyne.MenuItem) string {
			var out []string
			for _, m := range items {
				out = append(out, m.Label)
			}
			return strings.Join(out, "|")
		}
		sm := labels(h.d.sessionMenuItems(editsID))
		for _, gone := range []string{"Set active", "Set inactive", "Set archive", "Move to folder", "kmem name...", "Automatic placement"} {
			if strings.Contains(sm, gone) {
				t.Fatalf("the session menu has no folder or kmem-name items: %s", sm)
			}
		}
		for _, want := range []string{"Rename...", "Project...", "End session", "Delete session..."} {
			if !strings.Contains(sm, want) {
				t.Fatalf("the session menu has %q: %s", want, sm)
			}
		}
		pm := labels(h.d.projectMenuItems(sideRow{key: "p:Drop here", header: "Drop here"}))
		if !strings.Contains(pm, "End all...") || !strings.Contains(pm, "kmem...") || strings.Contains(pm, "New folder") {
			t.Fatalf("project menu: %s", pm)
		}
		test.TapSecondary(item)
		test.TapSecondary(header)
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		at := fyne.CurrentApp().Driver().AbsolutePositionForObject(header).Add(fyne.NewPos(20, 5))
		item.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{AbsolutePosition: at}, Dragged: fyne.Delta{DY: 1}})
		if !header.hl.Visible() {
			t.Fatal("drop target should be highlighted")
		}
		item.DragEnd()
		if h.d.projectOf(editsID) != "Drop here" {
			t.Fatalf("dragging onto a project header moves the session: %q", h.d.projectOf(editsID))
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
		if !h.d.chat.isOpen(bash) {
			t.Fatal("chips should start open by default")
		}
		h.d.chat.toggleStep(bash)
		if h.d.chat.isOpen(bash) {
			t.Fatal("chip should close its step")
		}
		h.d.chat.toggleStep(bash)
		var other0 *chat.Item
		for _, it := range h.d.sessions[editsID].conv.Items {
			if it.Kind == chat.Tool && it != bash {
				other0 = it
			}
		}
		h.d.setCollapseChips(true)
		if !h.d.chat.isOpen(bash) || other0 != nil && !h.d.chat.isOpen(other0) {
			t.Fatal("default toggle must not change existing chips")
		}
		h.d.setAllChips(false)
		if h.d.chat.isOpen(bash) {
			t.Fatal("collapse all should close chips")
		}
		h.d.setAllChips(true)
		if !h.d.chat.isOpen(bash) {
			t.Fatal("open all should open chips")
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

type fakeLive struct{ stop func() }

func (f *fakeLive) Stop() { f.stop() }

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
	var startMu sync.Mutex
	started := ""
	getStarted := func() string {
		startMu.Lock()
		defer startMu.Unlock()
		return started
	}
	sp.ready = func(string) error { return nil }
	sp.start = func(lang string, update func(whisper.Update)) (liveSession, error) {
		startMu.Lock()
		started = lang
		startMu.Unlock()
		update(whisper.Update{Partial: "hello from"})
		return &fakeLive{stop: func() { update(whisper.Update{Committed: "hello from my voice", Final: true}) }}, nil
	}
	h.ui(func() {
		h.d.createSession(h.proj, "default", "default")
		h.d.app.Preferences().SetBool("sttNoticeShown", false)
		sp.click()
		if getStarted() != "" || len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("first use must show the privacy notice before listening")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.app.Preferences().SetBool("sttNoticeShown", true)
		h.d.input.SetText("note:")
		sp.click()
	})
	h.eventually("live partial shown", func() bool { return getStarted() == "en" && h.d.input.Text == "note: hello from" && sp.live != nil })
	h.ui(func() {
		if sp.state != sttRecording {
			t.Fatal("should be listening")
		}
		sp.click()
	})
	h.eventually("final text", func() bool { return sp.state == sttIdle && h.d.input.Text == "note: hello from my voice" })
	h.ui(func() {
		sp.setLang("de")
		if sp.lang() != "de" || modelFile("de") != "ggml-base-q5_1.bin" || modelFile("en") != "ggml-base.en-q5_1.bin" {
			t.Fatal("language choice and model mapping")
		}
		sp.setLang("en")
		sp.ready = func(string) error { return fmt.Errorf("missing") }
		sp.click()
		if len(h.w.Canvas().Overlays().List()) == 0 {
			t.Fatal("missing model should open the setup dialog")
		}
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.pickLanguage("en", func(string) {})
		if languages[0].code != "en" {
			t.Fatal("English should be first")
		}
	})
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
		var tap *jumpTap
		var buttons int
		deepWalk(h.d.chat.jump, func(o fyne.CanvasObject) {
			if j, ok := o.(*jumpTap); ok {
				tap = j
			}
			if _, ok := o.(*widget.Button); ok {
				buttons++
			}
		})
		if tap == nil || buttons != 0 {
			t.Fatal("the Latest arrow is a round tap target, not a button with a square hover background")
		}
		tap.MouseIn(nil)
		if h.d.chat.glow.StrokeWidth != 2.5 || h.d.chat.glow.CornerRadius != 20 {
			t.Fatal("hover brightens the round ring")
		}
		tap.MouseOut()
		if h.d.chat.glow.StrokeWidth != 1.5 {
			t.Fatal("leaving restores the ring")
		}
		tap.Tapped(nil)
		if !h.d.chat.follow || h.d.chat.jump.Visible() || !h.d.chat.atBottom() {
			t.Fatal("Latest should bring the view to the bottom and resume following")
		}
		h.d.chat.update([]*chat.Item{s.conv.AddUser("one more")})
		if !h.d.chat.atBottom() {
			t.Fatal("following view should stay at the bottom")
		}
		grow := widget.NewLabel("late growth")
		h.d.chat.box.Add(grow)
		h.d.chat.box.Refresh()
		grow2 := canvas.NewRectangle(color.Transparent)
		grow2.SetMinSize(fyne.NewSize(10, 900))
		h.d.chat.box.Add(grow2)
		h.d.chat.scroll.OnScrolled(h.d.chat.scroll.Offset)
		if !h.d.chat.follow {
			t.Fatal("content growing below must not count as scrolling up")
		}
		h.d.chat.stick()
		if !h.d.chat.pinned() {
			t.Fatal("follow loop should catch up when content grows after layout")
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

func TestPlanFirstApproval(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		h.d.planFirst = true
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("write")
	})
	var plan *chat.Item
	h.eventually("plan card after the held request", func() bool {
		for _, it := range s.conv.Items {
			if it.Kind == chat.Plan && it.PermState == chat.PermPending {
				plan = it
			}
		}
		return plan != nil && !s.conv.Busy
	})
	h.ui(func() {
		held := false
		for _, it := range s.conv.Items {
			if it.Kind == chat.Permission && it.PermState == chat.PermPlanAsked {
				held = true
			}
		}
		if !held || !strings.Contains(h.args(), "") {
			t.Fatal("the first permission request should be held and Claude asked for a plan")
		}
		if s.conv.PlanText(plan) == "" {
			t.Fatal("Claude's plan should be shown inside the approval box")
		}
		h.d.answerPlan(s, plan, true)
		if !s.runAll || plan.PermState != chat.PermAllowed {
			t.Fatal("approving the plan should pre-approve the request")
		}
	})
	h.eventually("approval turn done", func() bool { return !s.conv.Busy })
	h.ui(func() {
		h.d.send("write")
		startPlan(s)
	})
	h.eventually("tool auto-approved", func() bool {
		it := lastItem(s)
		return it != nil && it.Text == "done allow"
	})
	h.ui(func() {
		for _, it := range s.conv.Items {
			if it.Kind == chat.Permission && it.PermState == chat.PermPending {
				t.Fatal("no prompt should wait for the user during an approved plan")
			}
		}
	})
}

func TestLocalSlashCommands(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("/model sonnet")
		if s.model != "sonnet" || s.proc != nil {
			t.Fatalf("/model should switch locally without starting claude: %q", s.model)
		}
		h.d.send("/upgrade")
		if s.proc != nil || h.d.input.Text != "" {
			t.Fatal("/upgrade must be handled by Quill, not sent to the CLI")
		}
		before, tabs := len(h.d.sessions), len(h.d.tabs)
		oldID := s.id
		h.d.send("/clear")
		if len(h.d.sessions) != before+1 || h.d.current == s || len(h.d.tabs) != tabs {
			t.Fatalf("/clear starts a fresh session in the same tab: tabs %d -> %d", tabs, len(h.d.tabs))
		}
		for _, id := range h.d.tabs {
			if id == oldID {
				t.Fatal("the cleared session leaves the tab strip")
			}
		}
		if h.d.placement(oldID) != placeInactive || h.d.current.cwd != s.cwd || h.d.current.model != s.model {
			t.Fatal("and stays resumable in Inactive; the new one keeps folder and model")
		}
		h.d.current.conv.Suggestion = "/clear"
		if h.d.shownSuggestion(h.d.current) != "" {
			t.Fatal("a /clear suggestion is hidden at low context")
		}
		h.d.current.conv.Stats.ContextMax, h.d.current.conv.Stats.ContextUsed = 1000, 600
		if h.d.shownSuggestion(h.d.current) != "/clear" {
			t.Fatal("and shown once context passes the soft threshold")
		}
		h.d.current.conv.Suggestion = "run the tests"
		h.d.current.conv.Stats.ContextUsed = 10
		if h.d.shownSuggestion(h.d.current) != "run the tests" {
			t.Fatal("other suggestions are untouched")
		}
		var hasUpgrade bool
		for _, c := range h.d.commandsFor(h.d.current) {
			if c.Name == "upgrade" {
				hasUpgrade = true
			}
		}
		if !hasUpgrade {
			t.Fatal("local commands should be suggested")
		}
	})
}

func TestNewSessionInstant(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.newSessionDialog()
		a := h.d.current
		h.d.newSessionDialog()
		b := h.d.current
		ovs := h.w.Canvas().Overlays().List()
		if len(ovs) != 1 {
			t.Fatalf("only one name field, no dialog: %d overlays", len(ovs))
		}
		if _, ok := ovs[0].(*dropdown); !ok {
			t.Fatalf("the name field is a drop-down, not a dialog: %T", ovs[0])
		}
		if h.d.org.Titles[a.id] != "New session 1" || h.d.org.Titles[b.id] != "New session 2" {
			t.Fatalf("names: %q %q", h.d.org.Titles[a.id], h.d.org.Titles[b.id])
		}
		if h.d.placement(b.id) != "" {
			t.Fatal("new session must follow the sorting rules, not be pinned")
		}
		if !hasRow(h.d.rows, a.id) || !hasRow(h.d.rows, b.id) {
			t.Fatalf("new sessions are live and listed at once: %+v", h.d.rows)
		}
	})
}

func TestSortRules(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.rules != defaultRules() {
			t.Fatalf("defaults: %+v", h.d.rules)
		}
		last := time.Now().Add(-3 * 24 * time.Hour)
		if h.d.isInactive("x", last) {
			t.Fatal("3 days old should be inactive, not archive")
		}
		h.d.setRules(sortRules{ActiveMinutes: 30, ArchiveDays: 2, ClaudeFolder: true})
		if !h.d.isInactive("x", last) {
			t.Fatal("archive after 2 days should archive a 3-day-old session")
		}
		h.d.loadRules()
		if h.d.rules.ArchiveDays != 2 {
			t.Fatal("rules should persist")
		}
		if h.d.isActive("x", time.Now().Add(-40*time.Minute)) {
			t.Fatal("40 min old should not be active")
		}
		h.d.setRules(sortRules{ActiveMinutes: 60, ArchiveDays: 7, ClaudeFolder: true})
		if !h.d.isActive("x", time.Now().Add(-40*time.Minute)) {
			t.Fatal("active for 60 min should keep it active")
		}
	})
}

func TestCenteredTyping(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.createSession(h.proj, "default", "default")
		h.d.setInputCenter(false)
		h.d.input.SetText("hi")
		h.d.input.CursorColumn = 2
		h.d.input.Refresh()
		left := h.d.input.CursorPosition().X
		h.d.setInputCenter(true)
		h.d.input.Refresh()
		mid := h.d.input.CursorPosition().X
		w := h.d.input.Size().Width
		if !(mid > left+w/4) {
			t.Fatalf("centered cursor should move toward the middle: left %.0f center %.0f width %.0f", left, mid, w)
		}
		h.d.setInputCenter(false)
		if h.d.input.Alignment != fyne.TextAlignLeading {
			t.Fatal("left typing")
		}
	})
}

func TestSwitchBackCatchesUp(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 30; i++ {
			a.conv.AddUser(fmt.Sprintf("a %d with enough words to take some room in the chat", i))
		}
		h.d.chat.show(a)
		b := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(b)
		late := a.conv.AddUser("arrived while away")
		h.d.chat.show(a)
		if _, ok := h.d.chat.views[late.Seq]; !ok {
			t.Fatal("messages that arrived while away should appear")
		}
		if !h.d.chat.follow || !h.d.chat.pinned() {
			t.Fatal("a session left at the bottom should keep following")
		}
		h.d.chat.scroll.Offset = fyne.NewPos(0, 0)
		h.d.chat.scroll.OnScrolled(h.d.chat.scroll.Offset)
		h.d.chat.show(b)
		a.conv.AddUser("another while away")
		h.d.chat.show(a)
		if h.d.chat.follow || !h.d.chat.jump.Visible() || h.d.chat.scroll.Offset.Y != 0 {
			t.Fatal("a session left reading history should stay put and offer Latest")
		}
	})
}

func TestScrollUpLoadsEarlierHistory(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 200; i++ {
			s.conv.AddUser(fmt.Sprintf("history %d", i))
		}
		h.d.chat.show(s)
		v := h.d.chat
		if v.shownFrom != 200-historyWindow {
			t.Fatalf("start window: %d", v.shownFrom)
		}
		first := v.views[s.conv.Items[v.shownFrom].Seq].holder
		v.scroll.Offset = fyne.NewPos(0, 0)
		v.scroll.OnScrolled(v.scroll.Offset)
		if v.shownFrom != 200-historyWindow-historyBatch {
			t.Fatalf("scrolling to the top should load an earlier batch: %d", v.shownFrom)
		}
		if v.scroll.Offset.Y <= 0 || first.Position().Y < v.scroll.Offset.Y-5 {
			t.Fatalf("view should stay on the same message: offset %.0f, message at %.0f", v.scroll.Offset.Y, first.Position().Y)
		}
		for v.shownFrom > 0 {
			v.scroll.Offset = fyne.NewPos(0, 0)
			v.scroll.OnScrolled(v.scroll.Offset)
		}
		if len(v.box.Objects) != 200 || v.moreObj != nil {
			t.Fatalf("all history should be loaded without a leftover button: %d", len(v.box.Objects))
		}
	})
}

func TestReviewFixes(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 150; i++ {
			s.conv.AddUser(fmt.Sprintf("m %d", i))
		}
		h.d.chat.show(s)
		v := h.d.chat
		n := len(v.box.Objects)
		v.update([]*chat.Item{s.conv.Items[3]})
		if len(v.box.Objects) != n {
			t.Fatal("updates to messages above the loaded window must not be appended at the bottom")
		}
		q := &chat.Item{Kind: chat.Permission, ToolName: "AskUserQuestion", PermState: chat.PermPending,
			Input: []byte(`{"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]}]}`)}
		first := v.renderPermission(q)
		var radio *widget.RadioGroup
		walk(first, func(o fyne.CanvasObject) {
			if r, ok := o.(*widget.RadioGroup); ok {
				radio = r
			}
		})
		radio.SetSelected("B")
		if v.renderPermission(q) != first || radio.Selected != "B" {
			t.Fatal("re-rendering a pending question must keep the user's picks")
		}
	})
}

func TestTypedApproval(t *testing.T) {
	for in, want := range map[string]bool{"yes": true, "Yes!": true, "go ahead": true, "yes, do it": true, "ok.": true,
		"yes but change the second step": false, "no": false, "write a test": false, "": false} {
		if isApproval(in) != want {
			t.Errorf("isApproval(%q) = %v", in, !want)
		}
	}
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		h.d.planFirst = true
		s = h.d.createSession(h.proj, "default", "default")
		h.d.send("write")
	})
	var plan *chat.Item
	h.eventually("plan card", func() bool {
		for _, it := range s.conv.Items {
			if it.Kind == chat.Plan && it.PermState == chat.PermPending {
				plan = it
			}
		}
		return plan != nil && !s.conv.Busy
	})
	h.ui(func() {
		h.d.send("yes")
		if plan.PermState != chat.PermAllowed || !s.runAll {
			t.Fatal("typing yes should approve the plan, no second approval needed")
		}
	})
	h.eventually("turn done", func() bool { return !s.conv.Busy })
	h.ui(func() {
		if !s.runAll {
			t.Fatal("an approval lasts across turns (background tasks restart the turn) until the next real request")
		}
		h.d.planGate(s, &protocol.Event{Type: "user"}, []*chat.Item{{Kind: chat.User, Text: "now something else"}})
		if s.runAll {
			t.Fatal("a new request from any channel ends the approval")
		}
		h.d.planGate(s, &protocol.Event{Type: "user"}, []*chat.Item{{Kind: chat.User, Text: "yes"}})
		if s.runAll {
			t.Fatal("a plain yes with no plan card or approval question open approves nothing")
		}
		s.inTurn = false
		remote := s.conv.AddPlan()
		h.d.planGate(s, &protocol.Event{Type: "user"}, []*chat.Item{{Kind: chat.User, Text: "yes"}})
		if !s.runAll || remote.PermState != chat.PermAllowed {
			t.Fatal("yes typed on another device (Remote Control) approves an open plan card too")
		}
		s.runAll = false
		s.conv.Busy = true
		h.d.approveByText(s)
		if !s.runAll {
			t.Fatal("approval typed during a turn applies at once: Claude Code delivers it into the running turn")
		}
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		if !s.runAll || s.runAllNext {
			t.Fatal("and it still holds for the next request")
		}
		s.conv.Busy = false
	})
}

func TestCenteringOptions(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.centerAll.SetChecked(false)
		h.d.centerAll.SetChecked(true)
		if !h.d.center || h.d.input.Alignment != fyne.TextAlignCenter || h.d.chat.live.Alignment != fyne.TextAlignCenter {
			t.Fatal("Center everything should center chat, typing and status")
		}
		h.d.centerChecks[2].SetChecked(false)
		if h.d.chat.live.Alignment != fyne.TextAlignLeading || !h.d.center || h.d.input.Alignment != fyne.TextAlignCenter {
			t.Fatal("status toggles on its own")
		}
		if h.d.centerAll.Checked || !h.d.centerAll.Partial {
			t.Fatal("the master toggle shows a partial square when only some are centered")
		}
		h.d.centerChecks[2].SetChecked(true)
		if !h.d.centerAll.Checked || h.d.centerAll.Partial {
			t.Fatal("the master toggle shows on when everything is centered")
		}
		h.d.centerAll.SetChecked(false)
		if h.d.center || h.d.input.Alignment != fyne.TextAlignLeading || h.d.chat.live.Alignment != fyne.TextAlignLeading {
			t.Fatal("turning the master off uncenters everything")
		}
	})
}

func TestCatchUpSkipsUnchanged(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.createSession(h.proj, "default", "default")
		for i := 0; i < 30; i++ {
			a.conv.AddUser(fmt.Sprintf("a %d", i))
		}
		h.d.chat.show(a)
		holders := map[int]fyne.CanvasObject{}
		for seq, iv := range h.d.chat.views {
			holders[seq] = iv.holder.Objects[0]
		}
		b := h.d.createSession(h.proj, "default", "default")
		h.d.chat.show(b)
		h.d.chat.show(a)
		for seq, iv := range h.d.chat.views {
			if holders[seq] != nil && iv.holder.Objects[0] != holders[seq] {
				t.Fatal("switching back must not re-render unchanged messages")
			}
		}
	})
}

func TestWidthSliders(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.inputShare() != defaultInputShare || h.d.chatShare() != defaultChatShare {
			t.Fatal("defaults: chat 65%, message box 45%")
		}
		h.d.setShare("chatShare", 1)
		h.d.setShare("inputShare", 1)
		if h.d.inputWidth() > h.d.chatWidth() {
			t.Fatal("message box never wider than the chat")
		}
		h.d.setShare("inputShare", minInputShare)
		if h.d.inputWidth() < inputMinWidth && h.d.chatWidth() >= inputMinWidth {
			t.Fatal("message box keeps its usable minimum")
		}
		h.d.setShare("chatShare", minChatShare)
		if h.d.chatWidth() < chatMinWidth {
			t.Fatal("chat keeps its usable minimum")
		}
	})
}

func TestReadOnlyRequests(t *testing.T) {
	bash := func(c string) *protocol.ControlRequest {
		in, _ := json.Marshal(map[string]string{"command": c})
		return &protocol.ControlRequest{ToolName: "Bash", Input: in}
	}
	for c, want := range map[string]bool{
		"ls -la ~/.ssh":                              true,
		"grep -rn foo src | head -5":                 true,
		"git -C repo status --short":                 true,
		"git status --short && git log --oneline -3": true,
		"sed -n 1,40p file.go":                       true,
		"cat a 2>/dev/null; wc -l b":                 true,
		"sed -i s/a/b/ f":                            false,
		"find . -name x -delete":                     false,
		"echo hi > file":                             false,
		"cat $(ls)":                                  false,
		"rm -rf x":                                   false,
		"git push":                                   false,
		"ls; rm x":                                   false,
		"sed 's/a/b/w out' f":                        false,
		"sed -n '/x/w out' f":                        false,
		"sed -n '5p' f":                              true,
		"git branch -D old":                          false,
		"git branch newname":                         false,
		"git branch -a":                              true,
		"git remote add x url":                       false,
		"git remote -v":                              true,
		"git diff --output=f":                        false,
		"sort -o out in":                             false,
		"tree -o out":                                false,
		"uniq in out":                                false,
		"sort in | uniq -c":                          true,
	} {
		if readOnlyRequest(bash(c), nil) != want {
			t.Errorf("readOnlyRequest(%q) = %v", c, !want)
		}
	}
	if !readOnlyRequest(&protocol.ControlRequest{ToolName: "Read"}, nil) || readOnlyRequest(&protocol.ControlRequest{ToolName: "Write"}, nil) {
		t.Fatal("Read is read-only, Write is not")
	}
}

func TestReadOnlyShellParsing(t *testing.T) {
	extra := parseReadOnlyExtra("pzlock status\nssh srv powershell -File 'D:\\s\\status.ps1'\n\nrm -rf (")
	for c, want := range map[string]bool{
		"cd /x && git log --oneline -3":                                   true,
		"Q=/r/quill; git -C $Q log --oneline -3; git -C $Q status | head": true,
		`grep -E "amdgpu|gpu reset" f | tail -8`:                          true,
		`journalctl -k --since "-40 min" --no-pager 2>/dev/null | grep x`: true,
		"journalctl --vacuum-size=1M":                                     false,
		"X=-i; sed $X s/a/b/ f":                                           false,
		"sed -n 1,5p $UNSET":                                              false,
		"PATH=/tmp/evil:$PATH; ls":                                        false,
		"GIT_PAGER=x git log":                                             false,
		"git --no-pager log -1":                                           true,
		"git -c core.pager=x log":                                         false,
		"ls | tee out":                                                    false,
		`echo "a > b | c"`:                                                true,
		`echo "$(rm x)"`:                                                  false,
		`echo '$(rm x)' "${HOME}"`:                                        true,
		"ls # ; rm x":                                                     true,
		"ls #x\nrm x":                                                     false,
		"cat <<EOF\nx\nEOF":                                               false,
		"wc -l < file":                                                    true,
		"ls >| out":                                                       false,
		"ls &> /dev/null; ls 2>&1 >/dev/null":                             true,
		"/usr/bin/ls -la":                                                 true,
		"./ls":                                                            false,
		"$CMD x":                                                          false,
		"date +%s":                                                        true,
		"date -s 2020-01-01":                                              false,
		"awk '{print $1}' f":                                              true,
		`awk '{print > "x"}' f`:                                           false,
		`awk '{system("rm x")}' f`:                                        false,
		"awk -f prog f":                                                   false,
		"(rm x)":                                                          false,
		"ls 'unterminated":                                                false,
		"~/.local/bin/pzlock status 2>&1 | head -3":                       true,
		"pzlock acquire me":                                               false,
		`ssh srv powershell -File 'D:\s\status.ps1' 2>&1 | sed -n 2,3p`: true,
		`ssh srv powershell -File D:\\s\\status.ps1`:                    true,
		"ssh srv powershell -Command rm":                                false,
	} {
		if readOnlyCommand(c, extra) != want {
			t.Errorf("readOnlyCommand(%q) = %v", c, !want)
		}
	}
	if readOnlyCommand("pzlock status", nil) {
		t.Fatal("pzlock is only read-only when the user lists it")
	}
}

func TestAsksApprovalShowsPlanCard(t *testing.T) {
	c := chat.New()
	c.AddUser("do it all")
	c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: "Plan:\n1. build\n\n**Approve in Quill?**"})
	if !c.AsksApproval() {
		t.Fatal("a reply ending with an approval question should show the plan card")
	}
	c.AddUser("ok")
	c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: "Done. Anything else?"})
	if c.AsksApproval() {
		t.Fatal("an ordinary question is not a plan")
	}
	for text, want := range map[string]bool{
		"1. render\n2. commit\n\n**Go ahead with steps 1–3?**":                   true,
		"Plan.\n\n**Okay to run this?**":                                         true,
		"Plan.\n\n**Shall I proceed?**":                                          true,
		"**Pick C1 or C2. Do you approve steps 1–6 to put it in the launcher?**": true,
		"**Should I add a Settings row for this?**":                              true, // proposals get the card since 2026-11-21 (user)
		"Which size do you want, 48 or 64?":                                      false,
	} {
		c.AddUser("x")
		c.Items = append(c.Items, &chat.Item{Kind: chat.Assistant, Text: text})
		if c.AsksApproval() != want {
			t.Errorf("AsksApproval(%q) = %v, want %v", text, !want, want)
		}
	}
	for text, want := range map[string]bool{
		"C1, go ahead": true, "option B - approved": true, "2: yes": true, "c2, approve": true,
		"64, hand-touch the pixels": false, "C1, but wait": false, "C1": false,
		"yes, add the settings row": true, "go ahead, also inform quill 2 agent that this did not have a card": false,
	} {
		if isApproval(text) != want {
			t.Errorf("isApproval(%q) = %v, want %v", text, !want, want)
		}
	}
}

func TestPlanApprovalSymptoms(t *testing.T) {
	for in, want := range map[string]bool{
		"approve": true, "Approved": true, "yes": true, "yes, use option B": true, "approved - go with the second one": true,
		"yes but skip step 2": false, "yes, wait for the build": false, "ok?": false, "yesterday's log": false,
		"don't run it": false, "stuck": false, "run emulator in background": false,
	} {
		if isApproval(in) != want {
			t.Errorf("isApproval(%q) = %v, want %v", in, !want, want)
		}
	}
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "default", "default")
		s.runAll = true
		s.inTurn = true
		h.d.planGate(s, &protocol.Event{Type: "user"}, []*chat.Item{{Kind: chat.User, Text: "stuck?"}})
		if !s.runAll {
			t.Fatal("a message sent mid-turn must not reset the approval")
		}
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		h.d.planGate(s, &protocol.Event{Type: "user"}, []*chat.Item{{Kind: chat.User, Text: "now a brand new task"}})
		if s.runAll {
			t.Fatal("a new request after the turn ends needs a new plan")
		}
	})
}

func TestKmemStartupIsReadOnly(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		ex := h.d.readOnlyExtra()
		req := func(cmd string) *protocol.ControlRequest {
			b, _ := json.Marshal(map[string]string{"command": cmd})
			return &protocol.ControlRequest{ToolName: "Bash", Input: b}
		}
		for _, c := range []string{"kmem brief; kmem handoff get quill; kmem rules", "kmem task list", "kmem handoff policy"} {
			if !readOnlyRequest(req(c), ex) {
				t.Errorf("kmem startup command runs without a plan: %q", c)
			}
		}
		for _, c := range []string{"kmem handoff save quill --state x", "kmem add quill/decisions x", "kmem task done t1"} {
			if readOnlyRequest(req(c), ex) {
				t.Errorf("kmem writes still need a plan: %q", c)
			}
		}
	})
}

func TestApprovalQuestionShowsCardEvenAfterApproval(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.planFirst = true
		s := h.d.createSession(h.proj, "default", "default")
		// A blocked call left a plan card; the user's "yes" to Claude's
		// question approved it, so this request runs with runAll.
		card := s.conv.AddPlan()
		s.conv.AddUser("yes, add the settings row and close the task")
		h.d.approveByText(s)
		if !s.runAll || card.PermState != chat.PermAllowed {
			t.Fatal("setup: the yes approves the open card")
		}
		// The turn ends with a new plan asking for approval.
		s.conv.Items = append(s.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "1. Add the row.\n\n**Approve this plan?**"})
		out := h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		var plan *chat.Item
		for _, it := range out {
			if it.Kind == chat.Plan && it.PermState == chat.PermPending {
				plan = it
			}
		}
		if plan == nil {
			t.Fatal("a turn that ends with 'Approve this plan?' shows the plan card")
		}
		if s.runAll || s.runAllNext {
			t.Fatal("the earlier approval ends when Claude asks for a new one")
		}
		h.d.planGate(s, &protocol.Event{Type: "result"}, nil)
		n := 0
		for _, it := range s.conv.Items {
			if it.Kind == chat.Plan && it.PermState == chat.PermPending {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("a second result adds no second card: %d", n)
		}
	})
}
