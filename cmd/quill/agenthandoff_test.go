package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/codex"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
)

func runHandoffTestServer() {
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	var turn int
	var thread string
	for in.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
			Params struct {
				Input    []struct{ Text string }
				ThreadID string
			}
		}
		if json.Unmarshal(in.Bytes(), &m) != nil {
			continue
		}
		reply := func(body any) { _ = out.Encode(map[string]any{"id": m.ID, "result": body}) }
		event := func(method string, params map[string]any) {
			params["threadId"] = thread
			_ = out.Encode(map[string]any{"method": method, "params": params})
		}
		switch m.Method {
		case "initialize":
			reply(map[string]any{})
		case "thread/start", "thread/resume":
			thread = m.Params.ThreadID
			if m.Method == "thread/start" {
				thread = session.NewUUID()
			}
			reply(map[string]any{"thread": map[string]string{"id": thread}})
		case "turn/interrupt":
			reply(map[string]any{})
			time.Sleep(120 * time.Millisecond)
			event("turn/completed", map[string]any{"turn": map[string]string{"id": fmt.Sprint(turn), "status": "interrupted"}})
		case "turn/start":
			turn++
			id := fmt.Sprint(turn)
			reply(map[string]any{"turn": map[string]string{"id": id}})
			text := "Codex answer"
			if len(m.Params.Input) > 0 && m.Params.Input[len(m.Params.Input)-1].Text == "Wait for command approval" {
				_ = out.Encode(map[string]any{
					"id": 678, "method": "item/commandExecution/requestApproval",
					"params": map[string]string{"threadId": thread, "turnId": id, "itemId": "command", "command": "echo test"},
				})
				continue
			}
			for _, input := range m.Params.Input {
				switch {
				case strings.Contains(input.Text, "Write a concise handoff"):
					text = "Goal: finish the current work. Next: verify the result."
				case strings.Contains(input.Text, "fixture operator brief"):
					text = "Received the operator brief"
					if strings.Contains(input.Text, "fixture saved operator handoff") {
						text += " and the saved handoff"
					}
				}
			}
			event("item/completed", map[string]any{"turnId": id, "item": map[string]string{"type": "agentMessage", "id": id, "text": text}})
			event("turn/completed", map[string]any{"turn": map[string]string{"id": id, "status": "completed"}})
		}
	}
}

func TestOnlineAgentSwitchResumesFromKmemInTheSameTab(t *testing.T) {
	for _, toCodex := range []bool{true, false} {
		t.Run(fmt.Sprintf("toCodex=%t", toCodex), func(t *testing.T) {
			h := newHarness(t)
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("QUILL_CODEX_BIN", self)
			t.Setenv("QUILL_HANDOFF_FAKE", "1")
			t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
			h.d.cfg.CodexRoot = filepath.Join(t.TempDir(), "codex")
			h.d.kmemOff = false
			var gets, saves atomic.Int32
			// Both engines save to the topic the incoming engine will use.
			h.d.kmemRun = func(prog string, args ...string) (string, error) {
				if prog == "kmem" && len(args) >= 3 && args[0] == "handoff" && args[1] == "get" {
					if gets.Add(1) == 1 {
						return "previous handoff", nil
					}
					return "saved handoff: finish the work", nil
				}
				if prog == "kmem" && len(args) == 8 && args[0] == "--as" && args[2] == "handoff" && args[3] == "save" &&
					args[1] == "continuation" && args[1] == args[4] && args[5] == "--same" && args[6] == "--state" &&
					strings.Contains(args[7], "verify the result") {
					saves.Add(1)
					return "saved", nil
				}
				return "", errors.New("unexpected kmem command")
			}
			var old *deskSession
			h.ui(func() {
				old = h.d.addSessionWithBackend(h.proj, "default", "default", true, !toCodex)
				h.d.org.Kmem[old.id] = "continuation"
				h.d.sendTo(old, "Continue this work")
			})
			h.eventually("the source finishes its first turn", func() bool { return old.running() && !old.conv.Busy && !old.proc.Busy() })
			h.ui(func() {
				h.d.input.SetText("retain this draft")
				h.d.selectTabAgent(old, toCodex)
			})
			h.eventually("the target resumes from the saved handoff", func() bool {
				next := h.d.current
				return next != old && next != nil && next.codex == toCodex &&
					strings.Contains(lastUser(next), "saved handoff: finish the work") && !next.conv.Busy
			})
			h.ui(func() {
				next := h.d.current
				if len(h.d.tabs) != 1 || h.d.tabs[0] != next.id || h.d.sessions[old.id] != old {
					t.Fatal("the provider switch must reuse the tab and retain the source history")
				}
				if old.running() || h.d.runningKmemOwner("continuation", old) != next {
					t.Fatal("the incoming engine must be the topic's only running owner")
				}
				if h.d.input.Text != "retain this draft" || h.d.org.Kmem[next.id] != "continuation" {
					t.Fatalf("draft or kmem name lost: %q %q", h.d.input.Text, h.d.org.Kmem[next.id])
				}
				if toCodex && (gets.Load() != 2 || saves.Load() != 0) || !toCodex && saves.Load() != 1 {
					t.Fatalf("handoff was not verified or saved by the expected writer: %d reads, %d saves", gets.Load(), saves.Load())
				}
			})
		})
	}
}

func TestOperatorPickerHandsOffAndRetargetsMainBox(t *testing.T) {
	for _, role := range []string{boxQuill, boxKmem} {
		t.Run(role, func(t *testing.T) {
			h := newHarness(t)
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("QUILL_CODEX_BIN", self)
			t.Setenv("QUILL_HANDOFF_FAKE", "1")
			t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
			if runtime.GOOS == "windows" {
				t.Setenv("USERPROFILE", t.TempDir())
			}
			d := h.d
			d.cfg.CodexRoot = t.TempDir()
			d.kmemOff = false
			var reads atomic.Int32
			d.kmemRun = func(prog string, args ...string) (string, error) {
				if prog == "kmem" && len(args) == 3 && args[0] == "handoff" && args[1] == "get" {
					if reads.Add(1) == 1 {
						return "old handoff", nil
					}
					return "saved operator handoff", nil
				}
				return "", errors.New("unexpected kmem command")
			}
			var source, tab *deskSession
			desired := roleCodexQuill
			h.ui(func() {
				tab = d.createSession(h.proj, "default", "default")
				d.input.SetText("tab draft")
				d.openAssistBox(role)
				a := d.quillAssist()
				if role == boxKmem {
					a = d.assist()
					desired = roleCodexKmem
				}
				source = d.addSession(h.proj, "default", "default", false)
				d.markHiddenAs(source, a.role.hidden)
				d.org.Kmem[source.id] = a.role.name
				a.s, a.state = source, asLive
				d.assistAttach(a)
				d.sendTo(source, "Operator work")
			})
			h.eventually("operator turn finishes", func() bool { return source.running() && !source.conv.Busy })
			h.ui(func() {
				d.input.SetText("operator draft")
				if role == boxQuill {
					d.abox.agent.SetSelected(codexAgent)
				} else {
					d.kmemWin.agent.SetSelected(codexAgent)
				}
				if d.main.role != role || source.agentHandoff == nil {
					t.Fatal("the selector must keep the current operator in control until kmem saves")
				}
			})
			h.eventually("selected operator resumes", func() bool {
				a := d.assistByRole(desired)
				return d.main.role == desired && a != nil && a.s != nil && !a.s.conv.Busy &&
					strings.Contains(lastUser(a.s), "saved operator handoff")
			})
			if runtime.GOOS != "windows" {
				h.ui(func() {
					for _, item := range d.assistByRole(desired).s.conv.Items {
						if strings.HasPrefix(item.Text, "Received the operator brief") {
							return
						}
					}
					t.Error("the selected Codex operator did not receive the startup brief")
				})
			}
			h.ui(func() {
				active := d.assistByRole(desired).s
				name := d.kmemTopicFor(source)
				if source.running() || d.kmemTopicFor(active) != name || d.topicOwner(name, source) != active {
					t.Fatal("both engines must share the operator topic, with one live owner")
				}
				contender := d.addSession(h.proj, "default", "default", false)
				d.org.Kmem[contender.id] = name
				if err := d.ensureProcess(contender); err == nil || contender.running() {
					t.Fatal("a second session started under the live operator topic")
				}
				if d.current != tab || d.messageSession(d.input, tab) == tab {
					t.Fatal("the main box must follow the open operator window")
				}
				d.closeAssistBox()
				if d.current != tab || d.input.Text != "tab draft" {
					t.Fatalf("closing did not restore the tab draft: %q", d.input.Text)
				}
			})
		})
	}
}

func TestAgentSwitchKeepsSourceWhenHandoffWasNotSaved(t *testing.T) {
	h := newHarness(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	d := h.d
	d.kmemOff = false
	var reads atomic.Int32
	d.kmemRun = func(prog string, args ...string) (string, error) {
		if prog == "kmem" && len(args) == 3 && args[0] == "handoff" && args[1] == "get" {
			reads.Add(1)
			return "the same handoff", nil
		}
		return "", errors.New("unexpected kmem command")
	}
	var old *deskSession
	h.ui(func() {
		old = d.createSession(h.proj, "default", "default")
		d.org.Kmem[old.id] = "continuation"
		d.sendTo(old, "Continue this work")
	})
	h.eventually("source is idle", func() bool { return old.running() && !old.conv.Busy })
	h.ui(func() { d.selectTabAgent(old, true) })
	h.eventually("the switch reports the unchanged handoff", func() bool {
		return reads.Load() == 2 && old.agentHandoff == nil
	})
	h.ui(func() {
		if d.current != old || !old.running() || old.codex || len(d.tabs) != 1 {
			t.Fatal("the source must keep running in its tab")
		}
	})
}

func TestCodexOperatorsReplaceLegacyBridgeBeforeSending(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the isolated kmem fixture requires a shell")
	}
	h := newHarness(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", exe)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
	d := h.d
	d.cfg.CodexRoot = t.TempDir()
	d.kmemOff = false
	for _, role := range []string{roleCodexQuill, roleCodexKmem} {
		var s *deskSession
		var old *session.Process
		h.ui(func() {
			s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
			d.markHiddenAs(s, role)
			opts, err := d.codexProcessOptions(s)
			if err != nil {
				t.Fatal(err)
			}
			var launch codex.Options
			if err := json.Unmarshal([]byte(opts.BridgeArgs[1]), &launch); err != nil {
				t.Fatal(err)
			}
			launch.Ephemeral = false
			raw, err := json.Marshal(launch)
			if err != nil {
				t.Fatal(err)
			}
			opts.BridgeArgs[1] = string(raw)
			old, err = session.Start(opts, func(ev *protocol.Event) {
				d.do(func() { d.handleEvent(s, ev) })
			})
			if err != nil {
				t.Fatal(err)
			}
			d.adopt(s, old)
			d.sendTo(s, "Did you get your brief?")
			if s.proc == old || !old.Exited() {
				t.Error("the legacy bridge should exit before starting its replacement")
			}
		})
		h.eventually("replacement bridge delivered the brief", func() bool {
			for _, item := range s.conv.Items {
				if strings.HasPrefix(item.Text, "Received the operator brief") {
					return true
				}
			}
			return false
		})
	}
}

func TestCodexQuillResumesFromKmemInFreshThread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the isolated kmem fixture requires a shell")
	}
	h := newHarness(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", exe)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
	d := h.d
	d.cfg.CodexRoot = t.TempDir()
	d.kmemOff = false
	var a *assistant
	var old, next *deskSession
	hasBrief := func(s *deskSession) bool {
		for _, item := range s.conv.Items {
			if item.Text == "Received the operator brief and the saved handoff" {
				return !s.conv.Busy
			}
		}
		return false
	}
	h.ui(func() {
		a = d.codexAssistant(codexQuillRole)
		d.startAgent(a)
		old = a.s
	})
	h.eventually("initial start receives kmem context", func() bool { return hasBrief(old) })
	if err := old.proc.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
	h.ui(func() {
		d.applyRestore(&restoreState{Running: []string{old.id}})
		next = a.s
		if next == old || next.id == old.id || !next.running() {
			t.Fatal("resuming the stopped operator must create a fresh session")
		}
		d.startAgent(a)
		if a.s != next {
			t.Fatal("starting while the operator is live should reuse the running session")
		}
	})
	h.eventually("resumed operator receives kmem context", func() bool { return hasBrief(next) })
	for _, s := range []*deskSession{old, next} {
		if _, err := os.Stat(d.codexTranscript(s.id)); !os.IsNotExist(err) {
			t.Fatalf("Quill kept a Codex transcript for %s (stat error %v)", s.id, err)
		}
	}
}

func TestCodexWorkTabResumesFromItsKmemTopic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the isolated kmem fixture requires a shell")
	}
	h := newHarness(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", exe)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	t.Setenv("QUILL_TEST_OPERATOR_KMEM_BRIEF", "1")
	d := h.d
	d.cfg.CodexRoot = t.TempDir()
	d.kmemOff = false
	var old, next *deskSession
	var name string
	h.ui(func() {
		old = d.addSessionWithBackend(h.proj, "default", "default", true, true)
		d.sendTo(old, "Start working")
		name = d.org.Kmem[old.id]
		if name == "" || d.org.CodexTopics[name] != h.proj {
			t.Fatal("the working folder was not associated with a kmem topic")
		}
	})
	h.eventually("the original Codex turn completes", func() bool { return old.running() && !old.conv.Busy })
	if err := old.proc.Stop(time.Second); err != nil {
		t.Fatal(err)
	}
	h.ui(func() {
		d.closeTab(old.id)
		next = d.resumeHandoffNamed(name)
		if next == nil || next == old || !next.codex || d.org.Kmem[next.id] != name || next.cwd != h.proj {
			t.Fatal("reopening the topic did not create a fresh Codex session in its working folder")
		}
	})
	h.eventually("the fresh work tab receives a kmem brief", func() bool {
		for _, item := range next.conv.Items {
			if item.Text == "Received the operator brief" {
				return !next.conv.Busy
			}
		}
		return false
	})
	if _, err := os.Stat(d.codexTranscript(next.id)); !os.IsNotExist(err) {
		t.Fatalf("Quill wrote a Codex transcript: %v", err)
	}
}

func TestTabBackendMenuSwitchesOnlyTheSelectedTab(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		d := h.d
		d.fetchLocal = func(string) []localInfo { return nil }
		d.localModels = []string{localFallback[0]}
		codexTab := d.addSessionWithBackend(h.proj, "default", "default", true, true)
		claudeTab := d.addSessionWithBackend(h.proj, "default", "default", true, false)
		menu := d.tabMenuItems(codexTab.id)[0]
		if menu.Label != "Tab backend" || menu.ChildMenu == nil {
			t.Fatal("the tab should offer its own backend menu")
		}
		for _, item := range menu.ChildMenu.Items {
			if item.Label == "Local (Ollama on this PC)" {
				item.Action()
				if codexTab.codex || codexTab.model != localFallback[0] ||
					claudeTab.codex || claudeTab.model != "default" || d.current != claudeTab {
					t.Fatal("selecting Local should set Qwen on only the chosen tab")
				}
				return
			}
		}
		t.Fatal("Local should be directly available in a Codex tab")
	})
}
