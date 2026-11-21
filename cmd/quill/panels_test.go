package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func overlayButton(h *harness, text string) *widget.Button {
	var found *widget.Button
	for _, o := range h.w.Canvas().Overlays().List() {
		deepWalk(o, func(x fyne.CanvasObject) {
			if b, ok := x.(*widget.Button); ok && b.Text == text {
				found = b
			}
		})
	}
	return found
}

func TestSettingsEdits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := t.TempDir()
	user := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(user), 0o755)
	os.WriteFile(user, []byte(`{"hooks":{"SessionStart":[]},"permissions":{"allow":["Bash(ls)"]}}`), 0o600)

	if !autoMemoryOn() {
		t.Fatal("auto-memory defaults to on")
	}
	if err := setAutoMemory(false); err != nil || autoMemoryOn() {
		t.Fatalf("auto-memory off: %v", err)
	}
	m, _ := readSettings(user)
	if m["hooks"] == nil || m["autoMemoryEnabled"] != false {
		t.Fatalf("only the one key changes: %v", m)
	}
	if st, _ := os.Stat(user); st.Mode().Perm() != 0o600 {
		t.Fatal("mode kept")
	}

	local := scopeFile(cwd, settingsScopes[0])
	if local != filepath.Join(cwd, ".claude", "settings.local.json") || scopeFile(cwd, settingsScopes[2]) != user {
		t.Fatalf("scopes map to files: %q", local)
	}
	if err := addRule(local, "deny", "Bash(rm -rf *)"); err != nil {
		t.Fatalf("add to a missing local file creates it: %v", err)
	}
	if err := addRule(local, "deny", "Bash(rm -rf *)"); err == nil {
		t.Fatal("the same rule twice is refused")
	}
	if err := addRule(user, "ask", "WebFetch"); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range permissionRules(cwd) {
		got[r.scope+" "+r.kind+" "+r.rule] = true
	}
	if !got["Local deny Bash(rm -rf *)"] || !got["User ask WebFetch"] || !got["User allow Bash(ls)"] {
		t.Fatalf("rules listed: %v", got)
	}

	extra := t.TempDir()
	if err := setWorkspaceDir(user, extra, true); err != nil {
		t.Fatal(err)
	}
	if err := setWorkspaceDir(user, extra, true); err == nil {
		t.Fatal("a folder twice is refused")
	}
	if dirs := workspaceDirs(cwd); len(dirs) != 1 || dirs[0].dir != extra || dirs[0].scope != "User" {
		t.Fatalf("workspace folders: %+v", dirs)
	}
	if err := setWorkspaceDir(user, extra, false); err != nil || len(workspaceDirs(cwd)) != 0 {
		t.Fatalf("remove: %v", err)
	}

	os.WriteFile(local, []byte("{broken"), 0o600)
	if err := addRule(local, "allow", "Read"); err == nil {
		t.Fatal("invalid JSON is left alone")
	}
	if b, _ := os.ReadFile(local); string(b) != "{broken" {
		t.Fatal("broken file unchanged")
	}
}

func TestExportCopyAndRewindFilesOnly(t *testing.T) {
	h := newHarness(t)
	var reqs []map[string]any
	var mu sync.Mutex
	h.d.controlFn = func(_ *deskSession, req map[string]any) (json.RawMessage, error) {
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		if req["dry_run"] == true {
			return json.RawMessage(`{"canRewind":true,"filesChanged":["/w/a.go"],"insertions":1,"deletions":2}`), nil
		}
		return json.RawMessage(`{"canRewind":true}`), nil
	}
	var s *deskSession
	var u *chat.Item
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Kind: chat.User, Text: "first", UUID: "U1"},
			&chat.Item{Kind: chat.Assistant, Text: "one", UUID: "A1"},
			&chat.Item{Kind: chat.User, Text: "second", UUID: "U2"},
			&chat.Item{Kind: chat.Assistant, Text: "two", UUID: "A2"})
		u = s.conv.Items[2]
		h.d.open(s.id)
		h.d.cmdExport(s, "")
	})
	h.ui(func() {
		b := overlayButton(h, "Copy to clipboard")
		if b == nil {
			t.Fatal("/export offers Copy to clipboard")
		}
		test.Tap(b)
		if c := h.d.app.Clipboard().Content(); !strings.Contains(c, "**You:**") || !strings.Contains(c, "second") {
			t.Fatalf("the Markdown is on the clipboard: %q", c)
		}
	})
	n := 0
	h.ui(func() {
		n = len(s.conv.Items)
		h.d.rewindCode(s, u)
	})
	h.eventually("confirm", func() bool { return overlayButton(h, "Restore files") != nil })
	h.ui(func() { test.Tap(overlayButton(h, "Restore files")) })
	h.eventually("restored", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reqs) == 2
	})
	h.eventually("note", func() bool {
		last := s.conv.Items[len(s.conv.Items)-1]
		return strings.Contains(last.Text, "conversation is unchanged")
	})
	h.ui(func() {
		users := 0
		for _, it := range s.conv.Items {
			if it.Kind == chat.User {
				users++
			}
		}
		if users != 2 || s.resumeAt != "" || len(s.conv.Items) < n {
			t.Fatalf("files only keeps the whole conversation: %d users, resumeAt %q", users, s.resumeAt)
		}
		mu.Lock()
		if reqs[1]["user_message_id"] != "U2" || reqs[1]["dry_run"] != nil {
			t.Fatalf("restores the files at that message: %v", reqs[1])
		}
		mu.Unlock()
	})
}

func TestPanelShots(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render /export, /rewind, /memory and /permissions to .shots/")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Kind: chat.User, Text: "Add a settings row for the idle time", UUID: "U1"},
			&chat.Item{Kind: chat.Assistant, Text: "Done.", UUID: "A1"},
			&chat.Item{Kind: chat.User, Text: "Now write the tests", UUID: "U2"},
			&chat.Item{Kind: chat.Assistant, Text: "Done.", UUID: "A2"})
		h.d.open(s.id)
		addRule(scopeFile(s.cwd, settingsScopes[2]), "allow", "Bash(go test *)")
		setWorkspaceDir(scopeFile(s.cwd, settingsScopes[2]), "/home/user/shared-notes", true)
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
	snap("panel-export.png", func() { h.d.cmdExport(s, "") })
	snap("panel-rewind.png", func() { h.d.cmdRewind(s) })
	snap("panel-memory.png", func() { h.d.cmdMemory(s) })
	snap("panel-permissions.png", func() { h.d.cmdPermissions(s) })
	snap("panel-workspace.png", func() {
		h.d.cmdPermissions(s)
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if tabs, ok := x.(*container.AppTabs); ok {
					tabs.SelectIndex(1)
				}
			})
		}
	})
}
