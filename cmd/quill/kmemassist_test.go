package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// kmemAssistStub answers the kmem calls of the assistant's lifecycle; the
// real kmem never runs.
type kmemAssistStub struct {
	mu    sync.Mutex
	saved map[string]time.Time
	calls []string
}

func (k *kmemAssistStub) run(prog string, args ...string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.calls = append(k.calls, prog+" "+strings.Join(args, " "))
	switch {
	case prog == "kmem-resume" && len(args) > 0:
		return "RESUME " + args[0], nil
	case prog == "kmem" && len(args) == 2 && args[0] == "handoff" && args[1] == "list":
		out := ""
		for name, t := range k.saved {
			out += name + "  3 items, saved " + t.UTC().Format(time.RFC3339) + "\n"
		}
		return out, nil
	}
	return "", errors.New("not in the stub")
}

func (k *kmemAssistStub) save(name string, t time.Time) {
	k.mu.Lock()
	k.saved[name] = t
	k.mu.Unlock()
}

// winRec records what the assistant hands the kmem window (kmemwindow.go's API).
type winRec struct {
	chat    fyne.CanvasObject
	reason  string
	button  string
	tap     func()
	prefill func(string)
}

func assistSetup(t *testing.T) (*harness, *kmemAssistStub, *winRec) {
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	k := &kmemAssistStub{saved: map[string]time.Time{}}
	h.d.kmemRun = k.run
	h.d.kmemOff = false
	rec := &winRec{}
	oldChat, oldReason, oldWith, oldPre := kmemWinSetChat, kmemWinSetChatReason, kmemWinSetChatReasonWith, kmemWinSetPrefill
	oldWait, oldPoll, oldTick, oldSigned := kmemWinSaveWait, kmemWinSavePoll, kmemWinTick, kmemAssistSignedIn
	kmemWinSetChat = func(w *kmemWindow, obj fyne.CanvasObject) { rec.chat, rec.reason, rec.button = obj, "", "" }
	kmemWinSetChatReason = func(w *kmemWindow, r string) { rec.chat, rec.reason, rec.button, rec.tap = nil, r, "", nil }
	kmemWinSetChatReasonWith = func(w *kmemWindow, r, b string, tap func()) {
		rec.chat, rec.reason, rec.button, rec.tap = nil, r, b, tap
	}
	kmemWinSetPrefill = func(w *kmemWindow, f func(string)) { rec.prefill = f }
	kmemWinSaveWait, kmemWinSavePoll, kmemWinTick = 200*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() {
		uiMu.Lock()
		defer uiMu.Unlock()
		kmemWinSetChat, kmemWinSetChatReason, kmemWinSetChatReasonWith, kmemWinSetPrefill = oldChat, oldReason, oldWith, oldPre
		kmemWinSaveWait, kmemWinSavePoll, kmemWinTick, kmemAssistSignedIn = oldWait, oldPoll, oldTick, oldSigned
	})
	return h, k, rec
}

// openAssist opens a (stand-in) kmem window and waits for the running, idle
// assistant.
func openAssist(h *harness, w *kmemWindow) *deskSession {
	h.t.Helper()
	h.ui(func() {
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
	})
	var s *deskSession
	h.eventually("the assistant runs", func() bool {
		a := h.d.assist()
		s = a.s
		return s != nil && a.state == asLive && s.running() && !s.conv.Busy && !s.inTurn
	})
	return s
}

func closeAssist(h *harness, w *kmemWindow) {
	h.ui(func() {
		h.d.kmemWin = nil
		h.d.kmemAssistClose(w)
	})
}

func lastUser(s *deskSession) string {
	for i := len(s.conv.Items) - 1; i >= 0; i-- {
		if s.conv.Items[i].Kind == chat.User {
			return s.conv.Items[i].Text
		}
	}
	return ""
}

func hasNotice(s *deskSession, sub string) bool {
	for _, it := range s.conv.Items {
		if it.Kind == chat.Notice && strings.Contains(it.Text, sub) {
			return true
		}
	}
	return false
}

func runningHidden(d *desk) int {
	n := 0
	for _, s := range d.sessions {
		if isHiddenSession(s) && s.running() {
			n++
		}
	}
	return n
}

func TestKmemAssistantHint(t *testing.T) {
	if n := len([]rune(kmemAssistantHint)); n > 900 {
		t.Fatalf("the operator hint is at most 900 characters: %d", n)
	}
	for _, want := range []string{"kmem --as kmem-lock-audit", `"kmem issue:"`, "rule 29", "kmem trash empty", "pzlock clear", "release or transfer claims"} {
		if !strings.Contains(kmemAssistantHint, want) {
			t.Fatalf("hint names %q", want)
		}
	}
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		normal := h.d.sessionPromptFor(s, true)
		if !strings.Contains(normal, coordinatorHint) || !strings.Contains(normal, buildGateHint) || strings.Contains(normal, kmemAssistantHint) {
			t.Fatalf("a normal session keeps its hints: %q", normal)
		}
		if h.d.sessionPromptFor(s, true) != sessionPrompt(true, h.d.buildGateOn(), h.d.coordinatorOn(s.model)) {
			t.Fatal("a normal session's prompt is unchanged")
		}
		s.hidden = true
		p := h.d.sessionPromptFor(s, false)
		if !strings.HasSuffix(p, " "+kmemAssistantHint) || strings.Contains(p, coordinatorHint) || strings.Contains(p, buildGateHint) || !strings.Contains(p, kmemMemoryHint) {
			t.Fatalf("the hidden session: operator hint and kmem, no coordinator or build gate: %q", p)
		}
	})
}

func TestKmemAssistModelSetting(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		if h.d.kmemAssistModel() != "default" {
			t.Fatalf("follows New sessions: %q", h.d.kmemAssistModel())
		}
		p.SetString("newModel", "opus")
		if h.d.kmemAssistModel() != "opus" {
			t.Fatal("follows a changed New sessions model")
		}
		var sel *widget.Select
		walk(h.d.kmemAssistSetting(), func(o fyne.CanvasObject) {
			if s, ok := o.(*widget.Select); ok {
				sel = s
			}
		})
		if sel == nil || sel.Selected != optAssistSame || sel.Options[1] != optAssistOff || !slices.Contains(sel.Options, "sonnet") {
			t.Fatalf("Same as New sessions (default), Off, then the models: %+v", sel)
		}
		sel.SetSelected(optAssistOff)
		if h.d.kmemAssistModel() != kmemAssistOff {
			t.Fatal("Off")
		}
		sel.SetSelected("sonnet")
		if h.d.kmemAssistModel() != "sonnet" || p.String(prefKmemAssistModel) != "sonnet" {
			t.Fatal("a listed model")
		}
		sel.SetSelected(optAssistSame)
		if p.String(prefKmemAssistModel) != "" || h.d.kmemAssistModel() != "opus" {
			t.Fatal("back to New sessions")
		}
	})
}

func TestLocalModelUsesTools(t *testing.T) {
	oldProbe, oldTTL := ollamaToolProbe, toolProbeTTL
	t.Cleanup(func() {
		ollamaToolProbe, toolProbeTTL = oldProbe, oldTTL
		toolProbeMu.Lock()
		toolProbeCache = map[string]toolProbe{}
		toolProbeMu.Unlock()
	})
	toolProbeCache = map[string]toolProbe{}
	asked := map[string]int{}
	known := true
	ollamaToolProbe = func(host, name string) (bool, bool) {
		asked[name]++
		return name == "qwen3:8b", known
	}
	if !localModelUsesTools("ollama:qwen3:8b") || localModelUsesTools("ollama:plain:1b") {
		t.Fatal("tools from the capabilities")
	}
	if localModelUsesTools("ollama:plain:1b") || asked["plain:1b"] != 1 {
		t.Fatalf("cached for 24 h: %v", asked)
	}
	toolProbeTTL = 0
	localModelUsesTools("ollama:plain:1b")
	if asked["plain:1b"] != 2 {
		t.Fatal("asked again once the cache is old")
	}
	if !localModelUsesTools("default") || !localModelUsesTools("local:srv/model") || len(asked) != 2 {
		t.Fatal("Claude models and other local servers are not probed")
	}
	known = false
	toolProbeTTL = time.Hour
	if !localModelUsesTools("ollama:gone:1b") || !localModelUsesTools("ollama:gone:1b") || asked["gone:1b"] != 2 {
		t.Fatal("no answer counts as usable and is not cached")
	}
}

func TestKmemAssistCrashNote(t *testing.T) {
	start := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	if kmemAssistCrashNote("", start) != "" || kmemAssistCrashNote(start.Format(time.RFC3339), start.Add(time.Minute)) != "" {
		t.Fatal("no note without a start or after a save that followed it")
	}
	if n := kmemAssistCrashNote(start.Format(time.RFC3339), start.Add(-time.Hour)); !strings.Contains(n, "ended without saving; working from the save of") {
		t.Fatalf("older save: %q", n)
	}
	if n := kmemAssistCrashNote(start.Format(time.RFC3339), time.Time{}); !strings.Contains(n, "no saved handoff yet") {
		t.Fatalf("no save: %q", n)
	}
}

func TestKmemAssistOpenAndSavedClose(t *testing.T) {
	h, k, rec := assistSetup(t)
	k.save(kmemAssistName, time.Now().Add(-2*time.Hour))
	h.ui(func() {
		h.d.app.Preferences().SetString(prefKmemWinStarted, time.Now().Add(-time.Hour).Format(time.RFC3339))
	})
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.ui(func() {
		if !s.hidden || s.cwd != kmemWindowDir() || !strings.HasPrefix(s.cwd, os.Getenv("HOME")) {
			t.Fatalf("a hidden session in the window folder: hidden %v cwd %q", s.hidden, s.cwd)
		}
		if st, err := os.Stat(kmemWindowDir()); err != nil || !st.IsDir() {
			t.Fatal("the window folder is created")
		}
		if h.d.org.Kmem[s.id] != kmemAssistName || !h.d.org.Hidden[s.id] || slices.Contains(h.d.tabs, s.id) || h.d.current == s {
			t.Fatal("kmem name set, hidden in org, no tab, never current")
		}
		if w.s != s || w.view == nil || rec.chat == nil || rec.prefill == nil || rec.reason != "" || !h.d.kmemAssistChatOn() {
			t.Fatal("the window shows the chat and takes prefills")
		}
		first := ""
		for _, it := range s.conv.Items {
			if it.Kind == chat.User {
				first = it.Text
				break
			}
		}
		if !strings.HasPrefix(first, "RESUME "+kmemAssistName) || !strings.Contains(first, `"kmem issue:"`) || s.resumeAs != kmemAssistName {
			t.Fatalf("the resume prompt with resumeNext: %q (as %q)", first, s.resumeAs)
		}
		if !hasNotice(s, "The last session ended without saving") {
			t.Fatal("the crash note comes first")
		}
		n := len(s.conv.Items)
		rec.prefill("Explain the audit line")
		a := h.d.assist()
		if a.input.Text != "Explain the audit line" || len(s.conv.Items) != n {
			t.Fatal("a prefill fills the input and never sends")
		}
	})
	closeAssist(h, w)
	h.ui(func() {
		if s.kmemSave == nil || !s.kmemSave.end || h.d.assist().state != asSaving {
			t.Fatal("closing asks for the save")
		}
		if m := lastUser(s); !strings.Contains(m, "handoff save kmem-lock-audit") || !strings.Contains(m, "task transfer ID kmem-lock-audit") || !strings.Contains(m, "48 h") {
			t.Fatalf("the close message: %q", m)
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("saved: stopped", func() bool { return h.d.assist().state == asClosed && !s.running() })
	h.ui(func() {
		if !h.d.org.Ended[s.id] || h.d.org.Place[s.id] == placeArchive || slices.Contains(h.d.tabs, s.id) || s.kmemSave != nil {
			t.Fatal("ended, not archived, no tab")
		}
		if !hasNotice(s, "Saved kmem handoff “kmem-lock-audit”") {
			t.Fatal("the save is reported")
		}
	})
}

func TestKmemAssistTimeoutChoices(t *testing.T) {
	h, k, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	closeAssist(h, w)
	h.eventually("asks after the deadline", func() bool { return h.d.assist().choice != nil })
	h.ui(func() {
		a := h.d.assist()
		h.d.kmemAssistChoose(a, 1)
		if !a.untilSaved || a.state != asSaving || !s.running() {
			t.Fatal("keeps running hidden")
		}
	})
	time.Sleep(3 * kmemWinSaveWait)
	h.ui(func() {
		if h.d.assist().choice != nil || !s.running() {
			t.Fatal("no second question while it keeps running")
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("stopped once saved", func() bool { return h.d.assist().state == asClosed && !s.running() })

	// Reopening while it asks keeps the session.
	s2 := openAssist(h, w)
	if s2 == s {
		t.Fatal("each open starts a fresh session")
	}
	k.save(kmemAssistName, time.Now().Add(-time.Hour))
	closeAssist(h, w)
	h.eventually("asks again", func() bool { return h.d.assist().choice != nil })
	h.ui(func() {
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
		a := h.d.assist()
		if a.state != asLive || a.s != s2 || s2.kmemSave != nil || a.choice != nil || w.s != s2 {
			t.Fatal("reopening keeps it running in the window")
		}
	})

	// Stop without saving.
	closeAssist(h, w)
	h.eventually("asks", func() bool { return h.d.assist().choice != nil })
	h.ui(func() { h.d.kmemAssistChoose(h.d.assist(), 0) })
	h.eventually("stopped", func() bool { return h.d.assist().state == asClosed && !s2.running() })
	h.ui(func() {
		if !hasNotice(s2, "did not save its handoff within") || !hasNotice(s2, "The last save is from") {
			t.Fatal("the failure is shown")
		}
	})
}

func TestKmemAssistQuit(t *testing.T) {
	h, _, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	done := false
	h.ui(func() { h.d.kmemAssistQuit(func() { done = true }) })
	h.eventually("Quit stops it after the deadline without asking", func() bool { return done && !s.running() })
	h.ui(func() {
		if h.d.assist().choice != nil || !hasNotice(s, "did not save") {
			t.Fatal("no question on Quit; the failure is noted")
		}
		done = false
		h.d.kmemAssistQuit(func() { done = true })
		if !done {
			t.Fatal("Quit with nothing running goes on at once")
		}
	})
}

func TestKmemAssistReopenWhileSaving(t *testing.T) {
	h, k, rec := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	closeAssist(h, w)
	h.ui(func() {
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
		if !h.d.assist().pendingOpen || !strings.Contains(rec.reason, "still saving") || runningHidden(h.d) != 1 {
			t.Fatal("reopening while saving waits; no second process")
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("a fresh session after the stop", func() bool {
		a := h.d.assist()
		if runningHidden(h.d) > 1 {
			t.Fatal("never two assistants")
		}
		return a.s != s && a.s != nil && a.s.running() && a.state == asLive && !s.running()
	})
}

func TestKmemAssistPanelsOnly(t *testing.T) {
	h, k, rec := assistSetup(t)
	w := &kmemWindow{}
	h.ui(func() {
		h.d.app.Preferences().SetString(prefKmemAssistModel, kmemAssistOff)
		h.d.kmemAssistOpen(w)
		if rec.reason != kmemAssistOffReason || h.d.assist().s != nil || rec.prefill != nil || h.d.kmemAssistChatOn() {
			t.Fatalf("Off: panels only: %q", rec.reason)
		}
		h.d.kmemAssistClose(w)
		h.d.app.Preferences().SetString(prefKmemAssistModel, "")
		kmemAssistSignedIn = func(*desk) bool { return false }
		h.d.kmemAssistOpen(w)
		if !strings.Contains(rec.reason, "not signed in") || rec.button != "Sign in" {
			t.Fatalf("not signed in: %q", rec.reason)
		}
		h.d.kmemAssistClose(w)
		h.d.app.Preferences().SetString(prefKmemAssistModel, "ollama:plain:1b")
	})
	oldProbe := ollamaToolProbe
	ollamaToolProbe = func(string, string) (bool, bool) { return false, true }
	t.Cleanup(func() {
		ollamaToolProbe = oldProbe
		toolProbeMu.Lock()
		toolProbeCache = map[string]toolProbe{}
		toolProbeMu.Unlock()
	})
	h.ui(func() { h.d.kmemAssistOpen(w) })
	h.eventually("a local model without tools", func() bool { return strings.Contains(rec.reason, "can't use tools") })
	h.ui(func() {
		h.d.kmemAssistClose(w)
		h.d.kmemOff = true
		h.d.kmemAssistOpen(w)
		if !strings.Contains(rec.reason, "kmem is not available") || h.d.assist().s != nil {
			t.Fatal("no kmem: no assistant")
		}
	})
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, c := range k.calls {
		if strings.HasPrefix(c, "kmem-resume") {
			t.Fatal("nothing started")
		}
	}
}

func TestKmemAssistElsewhereMovesIn(t *testing.T) {
	h, k, rec := assistSetup(t)
	h.ui(func() { kmemWinSaveWait = 3 * time.Second }) // the tab's own save takes a turn
	var tab *deskSession
	h.ui(func() {
		tab = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[tab.id] = kmemAssistName
		h.d.send("start")
	})
	h.eventually("the tab runs", func() bool { return tab.running() && !tab.conv.Busy })
	w := &kmemWindow{}
	h.ui(func() {
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
		if h.d.assist().state != asElsewhere || rec.button != "Move it here" || !strings.Contains(rec.reason, "is live in the tab") || runningHidden(h.d) != 0 {
			t.Fatalf("ELSEWHERE, nothing started: %q", rec.reason)
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.ui(func() {
		rec.tap()
		if tab.kmemSave == nil || !tab.kmemSave.end {
			t.Fatal("moving in asks the tab to save")
		}
	})
	h.eventually("resumed in the window", func() bool {
		a := h.d.assist()
		return !tab.running() && a.s != nil && a.s != tab && a.s.hidden && a.s.running() && a.state == asLive
	})
	h.ui(func() {
		if slices.Contains(h.d.tabs, tab.id) {
			t.Fatal("the tab closed")
		}
	})
}

func TestKmemAssistMidTurnClose(t *testing.T) {
	h, k, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.ui(func() { h.d.kmemAssistSend("slow") })
	h.eventually("mid-turn", func() bool { return s.conv.Busy })
	closeAssist(h, w)
	h.eventually("interrupted, then asked to save", func() bool {
		return strings.Contains(lastUser(s), "stop what you are doing now")
	})
	// The fake reads the next message only after an interrupt.
	h.eventually("the turn was stopped and the close message answered", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "echo: Quill Desktop is closing the kmem window")
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("saved and stopped", func() bool { return h.d.assist().state == asClosed && !s.running() })
}

func TestKmemWindowIPC(t *testing.T) {
	h := newHarness(t)
	path := filepath.Join(t.TempDir(), "q.sock")
	h.d.listenInstance(path)
	if !sendCommand(path, "kmem-window") {
		t.Fatal("quill --kmem-window is answered")
	}
	if sendCommand(path, "kmem-windows") {
		t.Fatal("unknown verbs are not")
	}
}
