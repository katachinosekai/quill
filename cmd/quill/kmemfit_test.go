package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

func bashItem(cmd string) *chat.Item {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return &chat.Item{Kind: chat.Tool, ToolName: "Bash", Input: in}
}

func TestKmemWritesInRewoundPart(t *testing.T) {
	writes, claims := kmemWrites([]*chat.Item{
		bashItem("kmem brief; kmem get quill/state"),
		bashItem(`kmem --as "Quill" add quill/decisions "x" --new`),
		bashItem("kmem task claim t1 && kmem task claim t2"),
		bashItem("kmem task done t1"),
		bashItem("kmem handoff save quill --state y"),
		{Kind: chat.Assistant, Text: "kmem add is how you save"},
	})
	if len(writes) != 5 || !strings.HasPrefix(writes[0], "kmem --as \"Quill\" add") || !strings.HasPrefix(writes[4], "kmem handoff save") {
		t.Fatalf("writes: %q", writes)
	}
	if len(claims) != 1 || claims[0] != "t2" {
		t.Fatalf("only the still-open claim: %v", claims)
	}
}

func TestRewindPastKmemWrites(t *testing.T) {
	h := newHarness(t)
	var s *deskSession
	h.ui(func() {
		h.d.planFirst = true
		s = h.d.createSession(h.proj, "default", "default")
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Kind: chat.User, Text: "first", UUID: "U1"},
			&chat.Item{Kind: chat.Assistant, Text: "one", UUID: "A1"},
			&chat.Item{Kind: chat.User, Text: "claim it", UUID: "U2"},
			bashItem("kmem task claim t9"),
			&chat.Item{Kind: chat.Assistant, Text: "claimed", UUID: "A2"})
		s.runAll = true
		h.d.open(s.id)
		h.d.rewindTo(s, s.conv.Items[2], false)
		if len(s.conv.Items) != 5 {
			t.Fatal("nothing is cut before the kmem warning is confirmed")
		}
		var rel *widget.Check
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if c, ok := x.(*widget.Check); ok && strings.Contains(c.Text, "release task t9") {
					rel = c
				}
			})
		}
		if rel == nil || !rel.Checked {
			t.Fatal("the claim can be released, on by default")
		}
		test.Tap(overlayButton(h, "Rewind"))
		users := []string{}
		for _, it := range s.conv.Items {
			if it.Kind == chat.User {
				users = append(users, it.Text)
			}
			if it.Kind == chat.Tool {
				t.Fatal("the rewound part is gone")
			}
		}
		if len(users) != 2 || users[0] != "first" || !strings.Contains(users[1], "kmem task release t9") || s.runAll || s.planWait {
			t.Fatalf("rewound, approval ended, release asked: %q %v", users, s.runAll)
		}
		if len(s.releaseTasks) != 1 || s.releaseTasks[0] != "t9" {
			t.Fatalf("the release is allowed for that turn: %v", s.releaseTasks)
		}
	})
}

func TestExportAndPermissionsFitQuill(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "quill-test"
		meta := strings.Join(h.d.exportMeta(s), "|")
		if !strings.Contains(meta, "kmem: quill-test") || !strings.Contains(meta, "Project: ") || !strings.Contains(meta, "Folder: ") {
			t.Fatalf("export header: %s", meta)
		}
		if md := exportMarkdown("T", s.conv, h.d.exportMeta(s)...); !strings.HasPrefix(md, "# T\n\n- kmem: quill-test\n") {
			t.Fatalf("markdown header: %q", md)
		}
		h.d.saveHandoffNow(s)
		if s.kmemSave != nil {
			t.Fatal("no handoff save without kmem or a running session")
		}
	})
	for rule, want := range map[string]bool{
		"Bash(sqlite3 ~/.local/share/kmem/kmem.db *)": true,
		"Edit(~/.local/share/kmem/**)":                true,
		"Read(~/.local/share/kmem/**)":                false,
		"Bash(kmem add *)":                            false,
		"Bash(npm test *)":                            false,
	} {
		if touchesKmemData(rule) != want {
			t.Errorf("touchesKmemData(%q) != %v", rule, want)
		}
	}
}

func TestMemoryPaneKmemMode(t *testing.T) {
	h := newHarness(t)
	h.d.kmemRun = func(prog string, args ...string) (string, error) { return "{}", nil }
	h.d.kmemOff = false
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		texts := func() string {
			var out []string
			deepWalk(h.d.memoryPane(s, s.cwd), func(x fyne.CanvasObject) {
				switch w := x.(type) {
				case *widget.Label:
					out = append(out, w.Text)
				case *widget.Button:
					out = append(out, "["+w.Text+"]")
				}
			})
			return strings.Join(out, "\n")
		}
		got := texts()
		if !strings.Contains(got, "Terminal sessions (not Quill sessions)") || !strings.Contains(got, "not used by Quill sessions (they use kmem)") {
			t.Fatalf("kmem mode explains auto memory is for terminals:\n%s", got)
		}
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
		if got := texts(); strings.Contains(got, "Terminal sessions") || strings.Contains(got, "not used by Quill sessions") {
			t.Fatalf("auto-memory mode has no kmem notes:\n%s", got)
		}
	})
}

func TestKmemFitShots(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render the kmem-fitted panels to .shots/")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	os.MkdirAll(dir, 0o755)
	t.Setenv("HOME", t.TempDir())
	h := newHarness(t)
	h.d.kmemRun = func(prog string, args ...string) (string, error) { return "{}", nil }
	h.d.kmemOff = false
	var s *deskSession
	h.ui(func() {
		h.d.planFirst = true
		h.d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		s = h.d.createSession(h.proj, "default", "default")
		h.d.org.Kmem[s.id] = "quill-test"
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Kind: chat.User, Text: "first", UUID: "U1"},
			&chat.Item{Kind: chat.Assistant, Text: "one", UUID: "A1"},
			&chat.Item{Kind: chat.User, Text: "claim the task and note the decision", UUID: "U2"},
			bashItem("kmem task claim t9"), bashItem(`kmem add quill/decisions "x" --new`),
			&chat.Item{Kind: chat.Assistant, Text: "done", UUID: "A2"})
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
	snap("fit-memory.png", func() { h.d.cmdMemory(s) })
	snap("fit-rewind.png", func() { h.d.rewindTo(s, s.conv.Items[2], false) })
	snap("fit-permissions.png", func() {
		h.d.cmdPermissions(s)
		for _, o := range h.w.Canvas().Overlays().List() {
			deepWalk(o, func(x fyne.CanvasObject) {
				if tabs, ok := x.(*container.AppTabs); ok {
					tabs.SelectIndex(1)
				}
			})
		}
	})
	snap("fit-addrule.png", func() { h.d.addRuleDialog(s.cwd, func() {}) })
}
