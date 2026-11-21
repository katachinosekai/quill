package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

func TestProjectDirs(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		p := h.d.projectOf(s.id)
		extra := t.TempDir()
		s.addDirs = []string{"/own"}
		h.d.setProjectDir(p, extra, true)
		h.d.setProjectDir(p, extra, true)
		if got := h.d.sessionDirs(s); len(got) != 2 || got[0] != "/own" || got[1] != extra {
			t.Fatalf("a session gets its own and its project's folders once: %v", got)
		}
		other := h.d.createSession(h.proj, "default", "default")
		if h.d.projectOf(other.id) == p && !contains(h.d.sessionDirs(other), extra) {
			t.Fatal("every session of the project gets the folder")
		}
		h.d.setProjectDir(p, extra, false)
		if contains(h.d.sessionDirs(s), extra) || h.d.org.Dirs[p] != nil {
			t.Fatal("removed")
		}
	})
}

func TestHooksKmemAware(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "kmem"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "kmem-guard"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	user := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(user), 0o755)
	os.WriteFile(user, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]}}`), 0o600)
	cwd := t.TempDir()
	if kmemHooksPresent(home) {
		t.Fatal("setup: kmem hooks missing")
	}
	if _, _, err := mergeKmemHooks(user, home, time.Now()); err != nil || !kmemHooksPresent(home) {
		t.Fatalf("repair adds the kmem hooks: %v", err)
	}
	rows := hookRows(cwd)
	var kmem, other int
	for _, r := range rows {
		if isKmemHook(r.command) {
			kmem++
		} else if r.event == "Stop" && r.command == "notify-send done" {
			other++
		}
	}
	if kmem != 2 || other != 1 {
		t.Fatalf("kmem hooks marked, others kept: %+v", rows)
	}
	if err := setHooksOff(user, true); err != nil || !hooksOff(user) {
		t.Fatal("hooks off")
	}
	if err := setHooksOff(user, false); err != nil || hooksOff(user) {
		t.Fatal("hooks on again")
	}
	m, _ := readSettings(user)
	if _, ok := m["disableAllHooks"]; ok {
		t.Fatal("turning hooks back on removes the key")
	}
	h := newHarness(t)
	h.ui(func() { _ = h.d.hooksPane(cwd) })
}

func TestAgentFiles(t *testing.T) {
	dir := t.TempDir()
	a := agentDef{Name: "code-reviewer", Description: "Reviews diffs: finds bugs", Tools: "Read, Grep", Model: "sonnet", Body: "Review the diff.", Kmem: true}
	path, err := saveAgent(dir, a, "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	text := string(b)
	for _, want := range []string{"---\nname: code-reviewer\n", "description: \"Reviews diffs: finds bugs\"\n", "tools: Read, Grep\n", "model: sonnet\n", "Review the diff.", "kmem context", "KMEM-BRIEF"} {
		if !strings.Contains(text, want) {
			t.Fatalf("agent file misses %q:\n%s", want, text)
		}
	}
	back := parseAgentFile(text)
	if back.Name != a.Name || back.Description != a.Description || back.Tools != a.Tools || back.Model != a.Model || back.Body != a.Body || !back.Kmem {
		t.Fatalf("round trip: %+v", back)
	}
	if _, err := saveAgent(dir, a, ""); err == nil {
		t.Fatal("a second agent with the same name is refused")
	}
	if _, err := saveAgent(dir, agentDef{Name: "Bad Name", Description: "x"}, ""); err == nil {
		t.Fatal("names are lowercase-with-dashes")
	}
	a.Kmem, a.Model = false, "inherit"
	if _, err := saveAgent(dir, a, path); err != nil {
		t.Fatalf("editing in place: %v", err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "kmem context") || strings.Contains(string(b), "model:") {
		t.Fatalf("edit drops the briefing and inherit model: %s", b)
	}
}

func TestMCPToolsAndClearAuth(t *testing.T) {
	raw := json.RawMessage(`{"mcpServers":[{"name":"linear","status":"connected","transport":"http","tools":[{"name":"list_issues","description":"List issues","annotations":{"readOnly":true}},{"name":"create_issue"}]}]}`)
	sv := parseMCP(raw)
	if len(sv) != 1 || len(sv[0].Tools) != 2 || sv[0].Tools[0].Annotations.ReadOnly == nil || !*sv[0].Tools[0].Annotations.ReadOnly {
		t.Fatalf("tools parsed: %+v", sv)
	}
	h := newHarness(t)
	var mu sync.Mutex
	var reqs []map[string]any
	h.d.controlFn = func(_ *deskSession, req map[string]any) (json.RawMessage, error) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		if req["subtype"] == "mcp_status" {
			return raw, nil
		}
		return json.RawMessage(`{}`), nil
	}
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		h.d.cmdMCP(s)
	})
	h.eventually("servers listed", func() bool { return overlayButton(h, "Clear sign-in") != nil })
	h.ui(func() {
		test.Tap(overlayButton(h, "Tools"))
		test.Tap(overlayButton(h, "Clear sign-in"))
	})
	h.eventually("confirm", func() bool { return overlayButton(h, "Yes") != nil })
	h.ui(func() { test.Tap(overlayButton(h, "Yes")) })
	h.eventually("clear auth sent", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range reqs {
			if r["subtype"] == "mcp_clear_auth" && r["serverName"] == "linear" {
				return true
			}
		}
		return false
	})
}

func TestHooksAgentsShots(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render /hooks, /agents and /add-dir to .shots/")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "kmem"), []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	user := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(user), 0o755)
	os.WriteFile(user, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]}}`), 0o600)
	saveAgent(filepath.Join(home, ".claude", "agents"), agentDef{Name: "code-reviewer", Description: "Reviews diffs for bugs", Body: "Review.", Kmem: true}, "")
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
	})
	snap := func(name string, open func()) {
		h.ui(open)
		h.ui(func() { shot(t, h.w, dir, name) })
		h.ui(func() {
			for _, o := range h.w.Canvas().Overlays().List() {
				h.w.Canvas().Overlays().Remove(o)
			}
		})
	}
	snap("panel-hooks.png", func() { h.d.cmdHooks(s) })
	snap("panel-agents.png", func() { h.d.cmdAgents(s) })
	snap("panel-agent-new.png", func() { h.d.agentDialog(s.cwd, agentDef{}, "", func() {}) })
	snap("panel-adddir.png", func() { h.d.addDirWhere(s, "/home/user/shared-notes", nil) })
}
