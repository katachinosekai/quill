package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/transcript"
)

// fakeKmemOpts: how the fake kmem binary answers.
type fakeKmemOpts struct {
	old       bool   // kmem 1.13: no `features`, no import
	noRules   bool   // `kmem rules` lists none
	failTopic string // the import into this topic fails (exit 2)
}

// fakeKmemBinary writes a kmem stand-in (a shell script) that logs its
// arguments and answers like kmem 1.14.0's contract; it returns its path and
// the log.
func fakeKmemBinary(t *testing.T, o fakeKmemOpts) (bin, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake kmem is a shell script")
	}
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "kmem"), filepath.Join(dir, "calls")
	features := `echo '{"api":1,"features":["topic-context","import-memory-md"]}'`
	ver := "1.14.0"
	if o.old {
		features = `echo "unknown command 'features'; kmem --help lists them"; exit 2`
		ver = "1.13.0"
	}
	rules := `echo '{"rules":{"edition":1,"file":"/placeholder/RULES.md","rules":[{"number":"1","id":null,"text":"1. placeholder rule"}]}}'`
	if o.noRules {
		rules = `echo '{"rules":{"edition":0,"file":"/placeholder/RULES.md","rules":[]}}'`
	}
	script := `#!/bin/sh
echo "$*" >> '` + log + `'
case "$1" in
version) echo '{"kmem":"` + ver + `","api":1,"features":["topic-context"]}' ;;
features) ` + features + ` ;;
rules) ` + rules + ` ;;
import)
  if [ "$5" = '` + o.failTopic + `' ]; then
    echo '{"error":"placeholder store failure","code":"store_error"}'; exit 2
  fi
  echo "{\"imported\": 2, \"skipped\": 1, \"topic\": \"$5\", \"file\": \"$3\", \"kmem\": \"1.14.0\", \"extra\": true}" ;;
*) echo "fake kmem: $1 not supported" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// useFakeKmem routes the desk's kmem calls to the fake binary.
func useFakeKmem(d *desk, bin string) {
	d.kmemRun = func(prog string, args ...string) (string, error) {
		if prog != "kmem" {
			return "", exec.ErrNotFound
		}
		out, err := exec.Command(bin, args...).CombinedOutput()
		return string(out), err
	}
}

// switchHome: a temp HOME with kmem-guard, python and a settings.json that
// has the user's own hooks.
const userSettings = `{
  "model": "placeholder-model",
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "/placeholder/own-guard"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "/placeholder/on-stop"}]}]
  }
}
`

func switchHome(t *testing.T, settings string) string {
	t.Helper()
	home := t.TempDir()
	// The real kmem and kmem-guard stay out of reach (only TestMain's stub
	// kmem and system tools on PATH).
	var path []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !strings.Contains(dir, string(filepath.Separator)+".local"+string(filepath.Separator)) {
			path = append(path, dir)
		}
	}
	t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
	t.Setenv("HOME", home)
	t.Setenv("KMEM_CONFIG", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "kmem-guard"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if settings != "" {
		os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
		os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(settings), 0o600)
	}
	old := switchPython
	switchPython = func() string { return "/placeholder/python3" }
	t.Cleanup(func() { switchPython = old })
	return home
}

// writeMemory writes a project's MEMORY.md (and note files) under root.
func writeMemory(t *testing.T, root, slug, memory string, notes ...string) string {
	t.Helper()
	dir := filepath.Join(root, slug, "memory")
	os.MkdirAll(dir, 0o755)
	for _, n := range notes {
		os.WriteFile(filepath.Join(dir, n), []byte("---\nname: placeholder\n---\nplaceholder note body\n"), 0o600)
	}
	f := filepath.Join(dir, "MEMORY.md")
	if err := os.WriteFile(f, []byte(memory), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

const sampleMemory = "# Memory index\n\n- [Placeholder one](note_one.md) — a hook line\n- [Placeholder two](note_two.md)\n- [Placeholder one again](note_one.md#part)\n- a plain placeholder bullet\n- [web](https://example.invalid/x.md)\n- [missing](gone.md)\n\nplain text is not a fact\n"

func TestMemoryFacts(t *testing.T) {
	root := t.TempDir()
	f := writeMemory(t, root, "-placeholder-proj", sampleMemory+"- [escape](../../outside.md)\n- [self](MEMORY.md)\n1. a numbered placeholder\n", "note_one.md", "note_two.md")
	os.WriteFile(filepath.Join(root, "-placeholder-proj", "outside.md"), []byte("x"), 0o600)
	notes, facts := memoryFacts(f)
	if len(notes) != 2 || filepath.Base(notes[0]) != "note_one.md" || filepath.Base(notes[1]) != "note_two.md" {
		t.Fatalf("linked notes beside MEMORY.md, once each, none outside: %v", notes)
	}
	// 2 notes + the plain bullet + the numbered one; the web link, the
	// missing note, the escape and the self link are plain bullets too.
	if facts != 2+1+1+4 {
		t.Fatalf("facts = %d", facts)
	}
	if n, f := memoryFacts(filepath.Join(root, "none.md")); n != nil || f != 0 {
		t.Fatal("no file: nothing")
	}
	for in, want := range map[string]string{"Quill": "quill", " My Project! ": "my-project", "日本": "", "a_b.c": "a-b-c"} {
		if got := switchTopic(in); got != want {
			t.Fatalf("switchTopic(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKmemHookMergeKeepsOtherHooks(t *testing.T) {
	var m map[string]any
	json.Unmarshal([]byte(userSettings), &m)
	kmem := kmemHooksForPaths("linux", "/placeholder/bin/kmem", "/placeholder/bin/kmem-guard", "")
	added, changed, err := mergeKmemHookGroups(m, kmem)
	if err != nil || !changed || strings.Join(added, ",") != "PreToolUse,SessionStart" {
		t.Fatalf("added %v changed %v err %v", added, changed, err)
	}
	hooks := m["hooks"].(map[string]any)
	if pre := hooks["PreToolUse"].([]any); len(pre) != 2 || !hasHookCommand(pre[:1], "own-guard") || !hasHookCommand(pre[1:], "kmem-guard") {
		t.Fatalf("own PreToolUse hook kept, kmem-guard added: %v", pre)
	}
	if hooks["Stop"] == nil || m["model"] != "placeholder-model" {
		t.Fatal("other hooks and settings kept")
	}
	if added, changed, _ := mergeKmemHookGroups(m, kmem); added != nil || changed {
		t.Fatal("a second merge adds nothing")
	}
	if _, _, err := mergeKmemHookGroups(map[string]any{"hooks": "bad"}, kmem); err == nil {
		t.Fatal("unexpected hooks entry: error, no change")
	}
	removeKmemHookGroups(m, []string{"PreToolUse", "SessionStart"}, map[string]bool{"hooks": true, "PreToolUse": true})
	hooks = m["hooks"].(map[string]any)
	if hooks["SessionStart"] != nil || len(hooks["PreToolUse"].([]any)) != 1 || hooks["Stop"] == nil {
		t.Fatalf("only the kmem hooks are removed: %v", hooks)
	}
}

func TestKmemSwitchDetect(t *testing.T) {
	switchHome(t, "")
	h := newHarness(t)
	h.d.kmemOff = false
	cases := []struct {
		name  string
		o     fakeKmemOpts
		setup func()
		want  string
	}{
		{"ready", fakeKmemOpts{}, nil, ""},
		{"old kmem", fakeKmemOpts{old: true}, nil, "needs kmem 1.14.0"},
		{"no core rules", fakeKmemOpts{noRules: true}, nil, "core rules"},
		{"no python", fakeKmemOpts{}, func() { switchPython = func() string { return "" } }, "python3"},
		{"no kmem-guard", fakeKmemOpts{}, func() { os.Remove(filepath.Join(homeDir(), ".local", "bin", "kmem-guard")) }, "kmem-guard is missing"},
	}
	for _, c := range cases {
		switchPython = func() string { return "/placeholder/python3" }
		bin, _ := fakeKmemBinary(t, c.o)
		useFakeKmem(h.d, bin)
		if c.setup != nil {
			c.setup()
		}
		got := h.d.kmemSwitchDetect(homeDir())
		all := strings.Join(got.Problems, "\n")
		if c.want == "" && (all != "" || got.Version != "1.14.0" || got.Guard == "") {
			t.Fatalf("%s: %+v", c.name, got)
		}
		if c.want != "" && !strings.Contains(all, c.want) {
			t.Fatalf("%s: problems %q lack %q", c.name, all, c.want)
		}
	}
	h.d.kmemOff = true
	if got := h.d.kmemSwitchDetect(homeDir()); len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "not installed") {
		t.Fatalf("no kmem: %+v", got)
	}
}

// labelsText: every label and check text in o.
func labelsText(o fyne.CanvasObject) string {
	var b strings.Builder
	deepWalk(o, func(x fyne.CanvasObject) {
		switch w := x.(type) {
		case *widget.Label:
			b.WriteString(w.Text + "\n")
		case *widget.Check:
			b.WriteString(w.Text + "\n")
		}
	})
	return b.String()
}

// TestKmemSwitchFlow: the banner opens the preview (projects with fact
// counts and editable topics, hooks, auto memory, mode, backup); Switch
// backs up, imports the ticked projects, merges the hooks, turns auto memory
// off and sets the mode, leaving the running session alone; Undo reverts it.
func TestKmemSwitchFlow(t *testing.T) {
	home := switchHome(t, userSettings)
	h := newHarness(t)
	bin, log := fakeKmemBinary(t, fakeKmemOpts{})
	root := h.d.cfg.ProjectsRoot
	projFile := writeMemory(t, root, transcript.Sanitize(h.proj), sampleMemory, "note_one.md", "note_two.md")
	otherFile := writeMemory(t, root, "-placeholder-other-place", "- one placeholder bullet\n")
	writeMemory(t, root, "-placeholder-empty", "# nothing yet\n")

	// A running session before the switch.
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() { h.d.resumeSession(editsID) })
	h.eventually("resumed", func() bool { return h.d.sessions[editsID].running() })
	var proc any
	h.ui(func() { proc = h.d.sessions[editsID].proc })
	h.eventually("launch args", func() bool { return strings.Contains(h.args(), "--resume "+editsID) })
	argsBefore := h.args()

	h.ui(func() {
		useFakeKmem(h.d, bin)
		h.d.kmemOff = false
		h.d.syncKmemButton()
		if !h.d.kmemSwitch.Visible() {
			t.Fatal("kmem installed, Claude memory in use: the switch offer shows")
		}
		test.Tap(findButton(h.d.kmemSwitch, "Switch to kmem..."))
	})
	var v *kmemSwitchView
	h.eventually("preview", func() bool { v = h.d.kmemSwitchUI; return v != nil && v.ticks != nil })
	h.ui(func() {
		text := labelsText(v.body)
		for _, want := range []string{
			// 2 linked notes + 3 bullets that link no note (plain, web link, missing note)
			tildePath(h.proj) + " — 5 facts", "1 fact (",
			"add hook PreToolUse", "add hook SessionStart",
			"turn Claude Code's auto memory off", "Claude's auto memory → kmem", "Running sessions keep going unchanged",
			"kmem-switch-backup-",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("preview lacks %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "-placeholder-empty") {
			t.Fatal("a MEMORY.md with nothing to import isn't listed")
		}
		if len(v.ticks) != 2 || v.topics[0].Text != "place" || v.topics[1].Text != "proj" {
			t.Fatalf("topics default to the project folder name: %q %q", v.topics[0].Text, v.topics[1].Text)
		}
		if overlayButton(h, "Switch") == nil {
			t.Fatal("Switch button")
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "share", "quill")); err == nil {
			t.Fatal("nothing happens before Switch")
		}
		v.ticks[0].SetChecked(false)
		v.topics[1].SetText("Placeholder Topic")
		test.Tap(overlayButton(h, "Switch"))
	})
	h.eventually("switched", func() bool { return v.result != nil })
	res := *v.result
	if res.Err != nil || len(res.Projects) != 1 || res.Projects[0].Topic != "placeholder-topic" || res.Projects[0].Imported != 2 || res.Projects[0].Skipped != 1 {
		t.Fatalf("result %+v", res)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "import memory-md "+projFile+" --topic placeholder-topic --json") || strings.Contains(string(calls), otherFile) {
		t.Fatalf("imports only the ticked project into its typed topic:\n%s", calls)
	}

	// Backup.
	if !strings.HasPrefix(res.Backup, filepath.Join(home, ".local", "share", "quill", "kmem-switch-backup-")) {
		t.Fatalf("backup folder %s", res.Backup)
	}
	if b, _ := os.ReadFile(filepath.Join(res.Backup, "settings.json")); string(b) != userSettings {
		t.Fatalf("backup of settings.json: %q", b)
	}
	slug := transcript.Sanitize(h.proj)
	for _, f := range []string{"MEMORY.md", "note_one.md", "note_two.md"} {
		if _, err := os.Stat(filepath.Join(res.Backup, "memory", slug, f)); err != nil {
			t.Fatalf("backup of %s: %v", f, err)
		}
	}
	if pf := h.d.prefsFile(); pf != "" {
		if _, err := os.Stat(pf); err == nil {
			if _, err := os.Stat(filepath.Join(res.Backup, "preferences.json")); err != nil {
				t.Fatal("backup of the preferences file")
			}
		}
	}
	rec, err := readSwitchRecord(res.Backup)
	if err != nil || rec.State != "switched" || rec.AutoMemoryBefore != nil || strings.Join(rec.HooksAdded, ",") != "PreToolUse,SessionStart" {
		t.Fatalf("switch.json %+v %v", rec, err)
	}
	if b, _ := os.ReadFile(projFile); string(b) != sampleMemory {
		t.Fatal("MEMORY.md stays as it is")
	}

	// settings.json: hooks merged, other settings kept, auto memory off.
	m, err := readSettings(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	hooks := m["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if !hasHookCommand(pre, "own-guard") || !hasHookCommand(pre, "kmem-guard") || !hasHookCommand(hooks["SessionStart"].([]any), "kmem brief") || hooks["Stop"] == nil {
		t.Fatalf("hooks merged, the user's kept: %v", hooks)
	}
	if m["autoMemoryEnabled"] != false || m["model"] != "placeholder-model" || autoMemoryOn() {
		t.Fatalf("auto memory off, other settings kept: %v", m)
	}

	h.ui(func() {
		p := h.d.app.Preferences()
		if p.String(memoryModeKey) != memoryModeKmem || !h.d.kmemMemory() || h.d.kmemSwitch.Visible() {
			t.Fatal("memory mode kmem; the offer is gone")
		}
		if overlayButton(h, "Open folder") == nil || !strings.Contains(labelsText(v.body), "Switched to kmem.") {
			t.Fatal("result with the backup's Open folder")
		}
		s := h.d.sessions[editsID]
		if !s.running() || any(s.proc) != proc {
			t.Fatal("the running session keeps going, not restarted")
		}
		test.Tap(overlayButton(h, "Close"))
	})
	if h.args() != argsBefore {
		t.Fatalf("no session started or restarted by the switch:\n%s", strings.TrimPrefix(h.args(), argsBefore))
	}

	// Settings → Memory offers Undo; the user changes settings meanwhile.
	h.ui(func() {
		box, refresh := h.d.kmemSwitchButtons(nil)
		refresh()
		if b := findButton(box, "Switch back to Claude memory..."); b == nil || b.Hidden {
			t.Fatal("Undo is offered after a switch")
		}
		if b := findButton(box, "Switch to kmem..."); b == nil || !b.Hidden {
			t.Fatal("no second switch while kmem is in use")
		}
	})
	updateSettings(filepath.Join(home, ".claude", "settings.json"), func(m map[string]any) error {
		m["theme"] = "placeholder-later"
		return nil
	})
	if err := undoKmemSwitch(res.Backup, h.d.app.Preferences()); err != nil {
		t.Fatal(err)
	}
	m, _ = readSettings(filepath.Join(home, ".claude", "settings.json"))
	hooks = m["hooks"].(map[string]any)
	if hasHookCommand(hooks["PreToolUse"].([]any), "kmem-guard") || hooks["SessionStart"] != nil || !hasHookCommand(hooks["PreToolUse"].([]any), "own-guard") || hooks["Stop"] == nil {
		t.Fatalf("undo removes only the switch's hooks: %v", hooks)
	}
	if _, set := m["autoMemoryEnabled"]; set || !autoMemoryOn() || m["theme"] != "placeholder-later" {
		t.Fatalf("undo turns auto memory back on and keeps later changes: %v", m)
	}
	h.ui(func() {
		if h.d.app.Preferences().String(memoryModeKey) != memoryModeClaude || h.d.kmemMemory() || h.d.kmemSwitchUndoDir() != "" {
			t.Fatal("undo sets Claude memory and clears the undo")
		}
	})
	if rec, _ := readSwitchRecord(res.Backup); rec.State != "undone" {
		t.Fatalf("switch.json state %q", rec.State)
	}
	if err := undoKmemSwitch(res.Backup, h.d.app.Preferences()); err == nil {
		t.Fatal("a second undo has nothing to undo")
	}
}

// TestKmemSwitchPreviewProblems: an old kmem gets no Switch button.
func TestKmemSwitchPreviewProblems(t *testing.T) {
	switchHome(t, "")
	h := newHarness(t)
	bin, log := fakeKmemBinary(t, fakeKmemOpts{old: true})
	h.ui(func() {
		useFakeKmem(h.d, bin)
		h.d.kmemOff = false
		h.d.showKmemSwitch()
	})
	h.eventually("problems", func() bool { return h.d.kmemSwitchUI.buttons.Objects[0].(*widget.Button).Text == "Close" })
	h.ui(func() {
		if text := labelsText(h.d.kmemSwitchUI.body); !strings.Contains(text, "kmem 1.13.0 is too old") || !strings.Contains(text, "1.14.0") {
			t.Fatalf("says kmem is too old: %s", text)
		}
		if overlayButton(h, "Switch") != nil {
			t.Fatal("no switch")
		}
	})
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "import") {
		t.Fatal("nothing imported")
	}
}

// TestKmemSwitchRollback: a failed import or settings write restores
// settings.json and the preferences; switch.json says what happened.
func TestKmemSwitchRollback(t *testing.T) {
	for _, c := range []struct {
		name, settings, failTopic, want string
		afterSettings                   bool
	}{
		{"import fails", userSettings, "second", "placeholder store failure (store_error)", false},
		{"settings.json can't merge", `{"hooks": "placeholder"}`, "", "unexpected \"hooks\" entry", false},
		{"failure after the settings write", userSettings, "", "placeholder late failure", true},
		{"no settings.json before", "", "", "placeholder late failure", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := switchHome(t, c.settings)
			h := newHarness(t)
			bin, _ := fakeKmemBinary(t, fakeKmemOpts{failTopic: c.failTopic})
			root := h.d.cfg.ProjectsRoot
			f1 := writeMemory(t, root, "-placeholder-first", "- placeholder a\n")
			f2 := writeMemory(t, root, "-placeholder-second", "- placeholder b\n")
			if c.afterSettings {
				kmemSwitchAfterSettings = func() error { return errPlaceholderLate }
				t.Cleanup(func() { kmemSwitchAfterSettings = nil })
			}
			var prefs fyne.Preferences
			h.ui(func() {
				useFakeKmem(h.d, bin)
				h.d.kmemOff = false
				prefs = h.d.app.Preferences()
				prefs.SetString(memoryModeKey, memoryModeClaude)
			})
			settings := filepath.Join(home, ".claude", "settings.json")
			plan := kmemSwitchPlan{Home: home, Settings: settings, BackupRoot: filepath.Join(home, ".local", "share", "quill"),
				Hooks: kmemHooksForPaths("linux", "/placeholder/kmem", "/placeholder/kmem-guard", ""), Now: time.Now(),
				Projects: []memoryProject{{File: f1, Topic: "first"}, {File: f2, Topic: "second"}}}
			res := h.d.runKmemSwitch(plan, prefs)
			if res.Err == nil || !strings.Contains(res.Err.Error(), c.want) || !res.RolledBack || res.RestoreErr != nil {
				t.Fatalf("result %+v", res)
			}
			b, err := os.ReadFile(settings)
			if c.settings == "" {
				if err == nil {
					t.Fatalf("the settings.json the switch created is removed: %s", b)
				}
			} else if string(b) != c.settings {
				t.Fatalf("settings.json restored: %s", b)
			}
			if prefs.String(memoryModeKey) != memoryModeClaude || prefs.String(prefKmemSwitchBackup) != "" || prefs.Bool(prefKmemSwitchOffered) {
				t.Fatal("preferences unchanged")
			}
			rec, err := readSwitchRecord(res.Backup)
			if err != nil || rec.State != "rolled-back" || !strings.Contains(rec.Error, c.want) {
				t.Fatalf("switch.json %+v %v", rec, err)
			}
			if c.failTopic != "" && (len(res.Projects) != 2 || !res.Projects[0].Done || res.Projects[1].Done) {
				t.Fatalf("the first import is done (kept, marked), the second not: %+v", res.Projects)
			}
		})
	}
}

var errPlaceholderLate = errors.New("placeholder late failure")

// TestKmemRemoveLeftHooks: kmem removed, its hooks still there: "Remove kmem
// hooks" removes them and keeps the user's.
func TestKmemRemoveLeftHooks(t *testing.T) {
	home := switchHome(t, userSettings)
	if _, _, err := mergeKmemHooks(filepath.Join(home, ".claude", "settings.json"), home, time.Now()); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t) // kmemOff: no kmem
	h.ui(func() {
		box, _ := h.d.kmemSwitchButtons(nil)
		if b := findButton(box, "Remove kmem hooks..."); b == nil || b.Hidden {
			t.Fatal("Remove kmem hooks is offered")
		}
	})
	if err := removeLeftKmemHooks(home); err != nil {
		t.Fatal(err)
	}
	if kmemHooksAnyPresent(home) {
		t.Fatal("kmem hooks removed")
	}
	m, _ := readSettings(filepath.Join(home, ".claude", "settings.json"))
	hooks := m["hooks"].(map[string]any)
	if !hasHookCommand(hooks["PreToolUse"].([]any), "own-guard") || hooks["Stop"] == nil || m["model"] != "placeholder-model" {
		t.Fatalf("the user's hooks stay: %v", m)
	}
}
