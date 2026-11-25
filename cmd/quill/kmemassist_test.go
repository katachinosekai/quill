package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	h.ui(func() { h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem) })
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
		h.d.startAgent(h.d.assist())
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

// stopAssist presses the window's "Stop assistant".
func stopAssist(h *harness) {
	h.ui(func() { h.d.kmemAssistStopNow(h.d.assist()) })
}

func resumeCalls(k *kmemAssistStub) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, c := range k.calls {
		if strings.HasPrefix(c, "kmem-resume") {
			n++
		}
	}
	return n
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
	for _, want := range []string{"kmem --as kmem-lock-audit", `"kmem issue:"`, "rule 29", "release or transfer claims",
		"developer and operator", "Projects/kmem", "install.sh", "kmem-guard", "rollouts", "RULES file", "pzlock code", "backlog", "user's approval before any write or release",
		"Never: kmem trash empty, hand repair of history/kmem data/backups, pzlock clear", "always run in the background", "SendMessage", "safe points"} {
		if !strings.Contains(kmemAssistantHint, want) {
			t.Fatalf("hint names %q", want)
		}
	}
	for _, gone := range []string{"kmem-dev", "No background jobs", "kmem window"} {
		if strings.Contains(kmemAssistantHint, gone) {
			t.Fatalf("hint drops %q", gone)
		}
	}
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		normal := h.d.sessionPromptFor(s, true)
		if !strings.Contains(normal, coordinatorHint) || !strings.Contains(normal, buildGateHint) || strings.Contains(normal, kmemAssistantHint) {
			t.Fatalf("a normal session keeps its hints: %q", normal)
		}
		want := sessionPrompt(true, h.d.buildGateOn(), h.d.coordinatorOn(s.model))
		if h.d.planFirst {
			want += " " + planApprovalHint
		}
		if h.d.sessionPromptFor(s, true) != want {
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
			t.Fatalf("Online Claude by default: %q", h.d.kmemAssistModel())
		}
		p.SetString("newModel", "ollama:qwen3:8b")
		if h.d.kmemAssistModel() != "default" {
			t.Fatalf("a local New sessions model never runs the assistant: %q", h.d.kmemAssistModel())
		}
		p.SetString("newModel", "opus")
		if h.d.kmemAssistModel() != "opus" {
			t.Fatal("follows a Claude New sessions model")
		}
		var sel *widget.Select
		walk(h.d.kmemAssistSetting(), func(o fyne.CanvasObject) {
			if s, ok := o.(*widget.Select); ok {
				sel = s
			}
		})
		if sel == nil || sel.Selected != optAssistSame || sel.Options[1] != optAssistOff || !slices.Contains(sel.Options, "sonnet") {
			t.Fatalf("Online Claude (default), Off, then the models: %+v", sel)
		}
		sel.SetSelected(optAssistOff)
		if h.d.kmemAssistModel() != kmemAssistOff {
			t.Fatal("Off")
		}
		p.SetString(prefKmemAssistModel, "ollama:qwen3:8b")
		if h.d.kmemAssistModel() != "ollama:qwen3:8b" {
			t.Fatal("a local model chosen explicitly for the assistant")
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

func TestKmemAssistOpenHideAndStop(t *testing.T) {
	h, k, rec := assistSetup(t)
	k.save(kmemAssistName, time.Now().Add(-2*time.Hour))
	h.ui(func() {
		h.d.app.Preferences().SetString(prefKmemWinStarted, time.Now().Add(-time.Hour).Format(time.RFC3339))
	})
	w := &kmemWindow{}
	s := openAssist(h, w)
	var chatObj fyne.CanvasObject
	var view *chatView
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
		if !strings.HasPrefix(first, "RESUME "+kmemAssistName) || !strings.Contains(first, `"kmem issue:"`) || !strings.Contains(first, "The user started you") || s.resumeAs != kmemAssistName {
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
		chatObj, view = rec.chat, a.view
	})

	// Closing the window only hides it: no save, no stop.
	closeAssist(h, w)
	time.Sleep(3 * kmemWinSaveWait)
	h.ui(func() {
		a := h.d.assist()
		if a.state != asLive || !s.running() || s.kmemSave != nil || a.choice != nil || h.d.org.Ended[s.id] || h.d.kmemAssistChatOn() {
			t.Fatalf("hidden, still running, nothing asked: %v", a.state)
		}
		if m := lastUser(s); strings.Contains(m, "handoff save") || strings.Contains(m, "stop what you are doing") {
			t.Fatalf("no save asked: %q", m)
		}
		if !h.d.kmemAssistRunning() || runningHidden(h.d) != 1 {
			t.Fatal("it runs on")
		}
		h.d.kmemAssistSend("still there?")
	})
	h.eventually("it still answers", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "still there?")
	})

	// Reopening shows the same session in the same chat (unsent text kept).
	h.ui(func() {
		h.d.assist().input.SetText("half typed")
		w2 := &kmemWindow{}
		h.d.kmemWin = w2
		h.d.kmemAssistOpen(w2)
		a := h.d.assist()
		if a.s != s || w2.s != s || w2.view != view || a.view != view || rec.chat != chatObj || a.input.Text != "half typed" || runningHidden(h.d) != 1 {
			t.Fatal("reopen reuses the session and its chat")
		}
		if resumeCalls(k) != 1 {
			t.Fatal("no second start")
		}
		a.input.SetText("")
		w = w2
	})

	// Stop assistant: the save-and-stop flow; the window stays open.
	stopAssist(h)
	h.ui(func() {
		if s.kmemSave == nil || !s.kmemSave.end || h.d.assist().state != asSaving || h.d.kmemAssistChatOn() {
			t.Fatal("Stop assistant asks for the save")
		}
		if m := lastUser(s); !strings.Contains(m, "handoff save kmem-lock-audit") || !strings.Contains(m, "task transfer ID kmem-lock-audit") || !strings.Contains(m, "48 h") {
			t.Fatalf("the stop message: %q", m)
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
		if !strings.Contains(rec.reason, "is stopped") || h.d.kmemAssistRunning() {
			t.Fatalf("the window says it is stopped: %q", rec.reason)
		}
		// Closing and reopening doesn't start it again; Start assistant does.
		h.d.kmemWin = nil
		h.d.kmemAssistClose(w)
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
		if h.d.assist().state != asClosed || !strings.Contains(rec.reason, "is stopped") {
			t.Fatal("stays stopped after Stop assistant")
		}
		h.d.kmemAssistBoot()
		if h.d.assist().state != asClosed {
			t.Fatal("boot doesn't undo the user's stop")
		}
		h.d.startAgent(h.d.assist())
	})
	h.eventually("Start assistant starts a fresh session", func() bool {
		a := h.d.assist()
		return a.s != nil && a.s != s && a.s.running() && a.state == asLive && !a.userStopped
	})
}

func TestKmemAssistTimeoutChoices(t *testing.T) {
	h, k, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	stopAssist(h)
	h.eventually("asks after the deadline", func() bool { return h.d.assist().choice != nil })
	h.ui(func() {
		a := h.d.assist()
		h.d.kmemAssistChoose(a, 1)
		if !a.untilSaved || a.state != asSaving || !s.running() {
			t.Fatal("keeps running until saved")
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

	// Keep it running cancels the stop.
	h.ui(func() { h.d.assist().userStopped = false })
	s2 := openAssist(h, w)
	if s2 == s {
		t.Fatal("a fresh session after a stop")
	}
	k.save(kmemAssistName, time.Now().Add(-time.Hour))
	stopAssist(h)
	h.eventually("asks again", func() bool { return h.d.assist().choice != nil })
	h.ui(func() {
		a := h.d.assist()
		h.d.kmemAssistChoose(a, 2)
		if a.state != asLive || a.s != s2 || s2.kmemSave != nil || a.choice != nil || a.userStopped || !h.d.kmemAssistRunning() {
			t.Fatal("Keep it running keeps it")
		}
	})

	// Stop without saving.
	stopAssist(h)
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
	h, k, _ := assistSetup(t)
	w := &kmemWindow{}

	// With the window closed, Quit still saves and stops it.
	s0 := openAssist(h, w)
	closeAssist(h, w)
	quitDone := false
	h.ui(func() {
		h.d.kmemAssistQuit(func() { quitDone = true })
		if s0.kmemSave == nil || !strings.Contains(lastUser(s0), "handoff save kmem-lock-audit") {
			t.Fatal("Quit asks the hidden assistant to save")
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("saved, stopped, then Quit goes on", func() bool { return quitDone && !s0.running() })

	k.save(kmemAssistName, time.Now().Add(-time.Hour))
	s := openAssist(h, w)
	done := false
	h.ui(func() { h.d.kmemAssistQuit(func() { done = true }) })
	h.eventually("Quit stops it after the deadline without asking", func() bool { return done && !s.running() })
	h.ui(func() {
		if h.d.assist().choice != nil || !hasNotice(s, "did not save") || h.d.assist().state != asClosed || runningHidden(h.d) != 0 {
			t.Fatal("no question on Quit; the failure is noted; nothing restarts")
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
	stopAssist(h)
	closeAssist(h, w)
	h.ui(func() {
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
		a := h.d.assist()
		if a.state != asSaving || rec.chat == nil || w.s != s || runningHidden(h.d) != 1 || h.d.kmemAssistChatOn() {
			t.Fatal("reopening while saving shows the save; no second process")
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("stopped, not restarted", func() bool {
		return h.d.assist().state == asClosed && !s.running() && strings.Contains(rec.reason, "is stopped")
	})
	if runningHidden(h.d) != 0 {
		t.Fatal("never restarted by itself")
	}
}

func TestKmemAssistPanelsOnly(t *testing.T) {
	h, k, rec := assistSetup(t)
	w := &kmemWindow{}
	h.ui(func() {
		h.d.app.Preferences().SetString(prefKmemAssistModel, kmemAssistOff)
		h.d.kmemAssistOpen(w)
		h.d.startAgent(h.d.assist())
		if rec.reason != kmemAssistOffReason || h.d.assist().s != nil || rec.prefill != nil || h.d.kmemAssistChatOn() {
			t.Fatalf("Off: panels only: %q", rec.reason)
		}
		h.d.kmemAssistClose(w)
		h.d.app.Preferences().SetString(prefKmemAssistModel, "")
		kmemAssistSignedIn = func(*desk) bool { return false }
		h.d.kmemAssistOpen(w)
		h.d.startAgent(h.d.assist())
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
	h.ui(func() {
		h.d.kmemAssistOpen(w)
		h.d.startAgent(h.d.assist())
	})
	h.eventually("a local model without tools", func() bool { return strings.Contains(rec.reason, "can't use tools") })
	h.ui(func() {
		h.d.kmemAssistClose(w)
		h.d.kmemOff = true
		h.d.kmemAssistOpen(w)
		h.d.startAgent(h.d.assist())
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
		h.d.startAgent(h.d.assist())
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

func TestKmemAssistMidTurnStop(t *testing.T) {
	h, k, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.ui(func() { h.d.kmemAssistSend("slow") })
	h.eventually("mid-turn", func() bool { return s.conv.Busy })
	stopAssist(h)
	h.eventually("interrupted, then asked to save", func() bool {
		return strings.Contains(lastUser(s), "stop what you are doing now")
	})
	// The fake reads the next message only after an interrupt.
	h.eventually("the turn was stopped and the close message answered", func() bool {
		it := lastItem(s)
		return it != nil && it.Kind == chat.Assistant && strings.Contains(it.Text, "echo: Quill Desktop is stopping the kmem assistant")
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

func TestKmemAssistBoot(t *testing.T) {
	h, k, rec := assistSetup(t)
	argsOut := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS_OUT", argsOut)
	h.ui(func() {
		if h.d.kmemRunDot == nil || h.d.kmemRunDot.Visible() {
			t.Fatal("the kmem button has a dot, off while nothing runs")
		}
		h.d.kmemAssistBoot()
	})
	var s *deskSession
	h.eventually("it starts with Quill Desktop, no window", func() bool {
		a := h.d.assist()
		s = a.s
		return s != nil && a.state == asLive && s.running() && !s.conv.Busy
	})
	h.ui(func() {
		a := h.d.assist()
		if h.d.kmemWin != nil || a.w != nil || rec.chat != nil || !s.hidden || !h.d.kmemRunDot.Visible() {
			t.Fatal("hidden, no window, the dot shows")
		}
		h.d.kmemAssistBoot()
		if a.s != s || runningHidden(h.d) != 1 {
			t.Fatal("never two")
		}
	})
	// Peer name: other sessions see kmem-lock-audit, not the folder name.
	b, err := os.ReadFile(argsOut)
	if err != nil || !strings.Contains(string(b), "--name "+kmemPeerName) || kmemPeerName != "kmem-lock-audit" {
		t.Fatalf("the hidden session is named for its peers: %q %v", b, err)
	}
	h.ui(func() {
		tab := h.d.createSession(h.proj, "default", "default")
		if strings.Contains(strings.Join(h.d.processOptions(tab).Args(), " "), "--name") {
			t.Fatal("tabs keep Claude's own name")
		}
	})
	// Hot reload with the window closed: the next desk adopts the handed-over
	// session and doesn't open the window.
	h.ui(func() {
		kmemAssistsMu.Lock()
		delete(kmemAssists, h.d)
		kmemAssistsMu.Unlock()
		h.d.setKmemRunDot(false)
		h.d.applyRestore(&restoreState{})
		h.d.kmemAssistBoot()
		a := h.d.assist()
		if a.s != s || a.state != asLive || h.d.kmemWin != nil || runningHidden(h.d) != 1 || resumeCalls(k) != 1 || !h.d.kmemRunDot.Visible() {
			t.Fatalf("adopted without a window: %v", a.state)
		}
	})
	// The dot goes when it stops.
	h.ui(func() { h.d.kmemAssistQuit(nil) })
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("stopped", func() bool { return !s.running() && h.d.assist().state == asClosed })
	h.ui(func() {
		if h.d.kmemRunDot.Visible() {
			t.Fatal("no dot once stopped")
		}
	})
}

func TestKmemAssistBootSkips(t *testing.T) {
	h, k, rec := assistSetup(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetString(prefKmemAssistModel, kmemAssistOff)
		h.d.kmemAssistBoot()
		if h.d.assist().s != nil || h.d.assist().state != asPanels {
			t.Fatal("model Off: nothing starts")
		}
		p.SetString(prefKmemAssistModel, "")
		kmemAssistsMu.Lock()
		delete(kmemAssists, h.d)
		kmemAssistsMu.Unlock()
		p.SetString(memoryModeKey, memoryModeClaude)
		h.d.kmemAssistBoot()
		if h.d.assist().state != asClosed {
			t.Fatal("kmem memory off: nothing starts")
		}
		p.SetString(memoryModeKey, memoryModeKmem)
	})
	var tab *deskSession
	h.ui(func() {
		tab = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[tab.id] = kmemAssistName
		h.d.send("start")
	})
	h.eventually("the tab runs", func() bool { return tab.running() && !tab.conv.Busy })
	h.ui(func() {
		h.d.kmemAssistBoot()
		if h.d.assist().state != asElsewhere || runningHidden(h.d) != 0 || h.d.kmemAssistRunning() {
			t.Fatal("live in a tab: no second one")
		}
		w := &kmemWindow{}
		h.d.kmemWin = w
		h.d.kmemAssistOpen(w)
		h.d.startAgent(h.d.assist())
		if rec.button != "Move it here" {
			t.Fatalf("the window keeps its offer: %q", rec.reason)
		}
	})
	if resumeCalls(k) != 0 {
		t.Fatal("nothing resumed")
	}
}

// td7337f/t9249e3: a changed model setting shows Restart in the header; it
// saves, stops and starts the assistant again on the new model.
func TestKmemAssistRestartOnNewModel(t *testing.T) {
	h, k, _ := assistSetup(t)
	w := &kmemWindow{}
	s := openAssist(h, w)
	h.ui(func() {
		a := h.d.assist()
		if a.reboot.Visible() || strings.Contains(a.modelLbl.Text, "Restart") {
			t.Fatal("no Restart while the setting matches")
		}
		h.d.app.Preferences().SetString(prefKmemAssistModel, "sonnet")
		h.d.kmemAssistRefresh(a)
		if !a.reboot.Visible() || !strings.Contains(a.modelLbl.Text, "Restart to use the new setting") {
			t.Fatalf("Restart offered: %q", a.modelLbl.Text)
		}
		a.reboot.OnTapped()
		if a.state != asSaving || s.kmemSave == nil {
			t.Fatal("Restart saves first")
		}
	})
	k.save(kmemAssistName, time.Now().Add(time.Minute))
	h.eventually("started again on the new model", func() bool {
		a := h.d.assist()
		return a.s != nil && a.s != s && a.s.model == "sonnet" && a.s.running() && a.state == asLive && !a.userStopped
	})
	h.ui(func() {
		if s.running() || runningHidden(h.d) != 1 || h.d.assist().reboot.Visible() {
			t.Fatal("the old one stopped, one assistant runs, no Restart")
		}
	})
}

// t9249e3: the header counts down the save; with fyne.Do-like (async) UI
// calls the save loop still asks at the deadline, and a process that dies
// while saving ends the flow at once with "Start assistant".
func TestKmemAssistSaveCountdownAndExit(t *testing.T) {
	h, _, rec := assistSetup(t)
	kmemWinSaveWait = 1500 * time.Millisecond
	ended := false // guarded by uiMu: no late UI calls once the test ends
	doWait := h.d.doWait
	h.d.do = func(f func()) { // fyne.Do returns before f runs
		go doWait(func() {
			if !ended {
				f()
			}
		})
	}
	t.Cleanup(func() {
		uiMu.Lock()
		ended = true
		uiMu.Unlock()
	})
	w := &kmemWindow{}
	s := openAssist(h, w)
	stopAssist(h)
	h.ui(func() {
		if got := h.d.assist().stateLbl.Text; !strings.Contains(got, "saving its handoff, then stopping · ") || !strings.HasSuffix(got, " s") {
			t.Fatalf("a countdown while saving: %q", got)
		}
	})
	h.eventually("asks at the deadline", func() bool { return h.d.assist().choice != nil })
	h.ui(func() {
		if strings.Contains(h.d.assist().stateLbl.Text, " · ") {
			t.Fatal("no countdown once asked")
		}
		h.d.kmemAssistChoose(h.d.assist(), 1) // keep running until saved
	})
	var pid int
	h.ui(func() { pid = s.proc.PID() })
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	h.eventually("stopped with Start assistant", func() bool {
		return h.d.assist().state == asClosed && !s.running() && strings.Contains(rec.reason, "is stopped")
	})
}

// The kmem chat follows the same centring settings as a session chat: the
// text (d.center), the status line and the typing box.
func TestKmemChatCentering(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		a := h.d.assist()
		a.chat = h.d.kmemAssistChat(a)
		if !slices.Contains(h.d.allViews(), a.view) {
			t.Fatal("the kmem chat view takes the centring settings")
		}
		h.d.centerAll.SetChecked(true)
		if a.view.align() != h.d.chat.align() || a.view.align() != fyne.TextAlignCenter ||
			a.view.live.Alignment != h.d.chat.live.Alignment || a.view.live.Alignment != fyne.TextAlignCenter ||
			a.input.Alignment != h.d.input.Alignment || a.input.Alignment != fyne.TextAlignCenter {
			t.Fatal("centred like the main chat")
		}
		h.d.centerAll.SetChecked(false)
		if a.view.align() != fyne.TextAlignLeading || a.view.live.Alignment != fyne.TextAlignLeading || a.input.Alignment != fyne.TextAlignLeading {
			t.Fatal("left like the main chat")
		}
	})
}
