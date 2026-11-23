package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"fyne.io/fyne/v2/canvas"
	"image/color"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/account"
	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
	"github.com/katachinosekai/quill/internal/transcript"
)

const checkpointEnv = "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=1"

func (d *desk) restartOnNext(s *deskSession, msg string) bool {
	if !s.running() {
		d.notify(s, msg)
		return true
	}
	if s.conv.Busy {
		d.notify(s, "Wait for the current turn to finish, then try again: this change restarts the session's Claude process.")
		return false
	}
	p := s.proc
	s.proc = nil
	s.inTurn, s.planWait = false, false
	s.rcOn, s.rcURL, s.rcName = false, "", ""
	d.unkeepSession(s.id)
	go func() { _ = p.Stop(5 * time.Second) }()
	d.notify(s, msg+" The session restarts on your next message.")
	return true
}

func (d *desk) control(s *deskSession, req map[string]any) (json.RawMessage, error) {
	if d.controlFn != nil {
		return d.controlFn(s, req)
	}
	var p *session.Process
	var err error
	d.doWait(func() {
		if !s.running() {
			err = d.ensureProcess(s)
		}
		p = s.proc
	})
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("the session has no Claude process")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.Control(ctx, req)
}

func (d *desk) textDialog(title, text string) {
	l := widget.NewLabel(text)
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.Selectable = true
	l.Wrapping = fyne.TextWrapBreak
	scroll := container.NewScroll(l)
	dlg := dialog.NewCustom(title, "Close", scroll, d.win)
	dlg.Resize(fyne.NewSize(900, 600))
	dlg.Show()
}

func (d *desk) openPath(path string) {
	if u, err := url.Parse("file://" + path); err == nil {
		_ = d.app.OpenURL(u)
	}
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(p, "~"))
	}
	return p
}

// copy

func lastReplyText(c *chat.Conversation) string {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if it := c.Items[i]; it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" {
			return it.Text
		}
	}
	return ""
}

func (d *desk) cmdCopy(s *deskSession) {
	text := lastReplyText(s.conv)
	if text == "" {
		d.notify(s, "There's no reply from Claude to copy yet.")
		return
	}
	d.app.Clipboard().SetContent(text)
	d.notify(s, fmt.Sprintf("Copied Claude's last reply (%s).", plural(len([]rune(text)), "character")))
}

// export

func exportMarkdown(title string, c *chat.Conversation, meta ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, m := range meta {
		fmt.Fprintf(&b, "- %s\n", m)
	}
	if len(meta) > 0 {
		b.WriteString("\n")
	}
	for _, it := range c.Items {
		switch it.Kind {
		case chat.User:
			fmt.Fprintf(&b, "**You:**\n\n%s\n\n", strings.TrimSpace(it.Text))
		case chat.Assistant:
			if t := strings.TrimSpace(it.Text); t != "" {
				fmt.Fprintf(&b, "**Claude:**\n\n%s\n\n", t)
			}
		case chat.Tool:
			fmt.Fprintf(&b, "> `%s`\n\n", oneLine(toolHeader(it), 200))
		}
	}
	return b.String()
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < ' ' {
			return '-'
		}
		return r
	}, s)
	if s = strings.TrimSpace(s); s == "" {
		s = "session"
	}
	return s
}

// exportMeta: who and where, for the export's header (kmem name, project,
// folder, date).
func (d *desk) exportMeta(s *deskSession) []string {
	var meta []string
	if k := d.org.Kmem[s.id]; k != "" {
		meta = append(meta, "kmem: "+k)
	}
	meta = append(meta, "Project: "+d.projectOf(s.id), "Folder: "+tildePath(s.cwd), "Exported: "+time.Now().Format("2006-01-02 15:04"))
	return meta
}

func (d *desk) cmdExport(s *deskSession, arg string) {
	title, _, _ := d.rowText(s.id)
	text := exportMarkdown(title, s.conv, d.exportMeta(s)...)
	write := func(path string) {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			d.notify(s, "Export failed: "+err.Error())
			return
		}
		d.notify(s, "Exported the conversation to "+tildePath(path)+".")
	}
	if arg != "" {
		path := expandHome(arg)
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.cwd, path)
		}
		write(path)
		return
	}
	save := func() {
		dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
			if err != nil || w == nil {
				return
			}
			path := w.URI().Path()
			w.Close()
			write(path)
		}, d.win)
		dlg.SetFileName(safeName(firstNonEmpty(d.org.Kmem[s.id], title)) + ".md")
		if l, err := storage.ListerForURI(storage.NewFileURI(s.cwd)); err == nil {
			dlg.SetLocation(l)
		}
		dlg.Show()
	}
	var choose dialog.Dialog
	copyBtn := widget.NewButtonWithIcon("Copy to clipboard", theme.ContentCopyIcon(), func() {
		choose.Hide()
		d.app.Clipboard().SetContent(text)
		d.notify(s, fmt.Sprintf("Copied the conversation as Markdown (%s).", plural(len([]rune(text)), "character")))
	})
	saveBtn := widget.NewButtonWithIcon("Save to file...", theme.DocumentSaveIcon(), func() { choose.Hide(); save() })
	copyBtn.Importance = widget.HighImportance
	btns := container.NewHBox(copyBtn, saveBtn)
	objs := []fyne.CanvasObject{caption("The conversation as Markdown: your messages, Claude's replies and one line per tool call, with its kmem name, project and folder on top.")}
	if name := d.org.Kmem[s.id]; name != "" && d.kmemAvailable() {
		label, note := "Save as kmem handoff “"+name+"”", "A kmem handoff lets a new session continue this work (New session → resume from kmem); the session saves it itself and goes on."
		if d.endCheckFor(s) {
			label, note = "Checkpoint now", "Asks the session to checkpoint its claimed tasks and update its status fact, then shows what the end-of-session check still lists; the session goes on."
		}
		ho := widget.NewButtonWithIcon(label, theme.HistoryIcon(), func() { choose.Hide(); d.saveHandoffNow(s) })
		if !s.running() || s.kmemSave != nil || s.endWait != nil {
			ho.Disable()
		}
		btns.Add(ho)
		objs = append(objs, caption(note))
	}
	choose = dialog.NewCustom("Export conversation", "Cancel", container.NewVBox(append(objs[:1], append([]fyne.CanvasObject{btns}, objs[1:]...)...)...), d.win)
	choose.Show()
}

// diff

func gitDiff(dir string) (string, error) {
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("%s", strings.TrimSpace(errb.String()))
		}
		return out.String(), nil
	}
	status, err := run("status", "--short")
	if err != nil {
		return "", err
	}
	diff, err := run("--no-pager", "diff", "HEAD")
	if err != nil {
		diff, _ = run("--no-pager", "diff")
	}
	if len(diff) > 400000 {
		diff = headText(diff, 400000) + "\n… (cut at 400 KB)"
	}
	if strings.TrimSpace(status) == "" {
		return "No changes in " + tildePath(dir) + ".", nil
	}
	return "git status --short\n\n" + status + "\n" + diff, nil
}

func (d *desk) cmdDiff(s *deskSession) {
	dir := s.cwd
	go func() {
		text, err := gitDiff(dir)
		d.do(func() {
			if err != nil {
				d.notify(s, "No git diff for "+tildePath(dir)+": "+err.Error())
				return
			}
			d.textDialog("Changes in "+tildePath(dir), text)
		})
	}()
}

// memory

var memoryNames = []string{"Your notes", "Project notes", "Project notes", "Private project notes", "Claude's auto memory"}

func memoryFiles(cwd string) []string {
	h := homeDir()
	return []string{
		filepath.Join(h, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, "CLAUDE.md"),
		filepath.Join(cwd, ".claude", "CLAUDE.md"),
		filepath.Join(cwd, "CLAUDE.local.md"),
		filepath.Join(h, ".claude", "projects", transcript.Sanitize(cwd), "memory", "MEMORY.md"),
	}
}

// memoryPane (/memory and Settings → Memory), fitted to Quill and kmem
// (user 2026-11-21): the memory Quill sessions use comes first; with kmem,
// Claude's auto memory is off in Quill sessions, so its switch and MEMORY.md
// are shown as belonging to terminal sessions only.
func (d *desk) memoryPane(s *deskSession, cwd string) fyne.CanvasObject {
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		kmemMode := d.kmemMemory()
		box.Add(d.memoryModeRadio(fill))
		box.Add(widget.NewSeparator())
		if d.kmemAvailable() {
			box.Add(d.kmemMemoryRow())
			var btns []fyne.CanvasObject
			if s != nil {
				if name := d.org.Kmem[s.id]; name != "" {
					btns = append(btns, widget.NewButtonWithIcon("Handoff “"+name+"”", theme.HistoryIcon(), func() {
						text := d.kmemHandoffText(name)
						if strings.TrimSpace(text) == "" {
							text = "No handoff saved for “" + name + "” yet."
						}
						d.textDialog("kmem handoff "+name, text)
					}))
				}
			}
			if rf := kmemRulesFile(); rf != "" {
				btns = append(btns, widget.NewButtonWithIcon("Shared rules", theme.DocumentIcon(), func() {
					b, err := os.ReadFile(rf)
					if err != nil {
						dialog.ShowError(err, d.win)
						return
					}
					d.textDialog("kmem shared rules", string(b))
				}))
			}
			if len(btns) > 0 {
				box.Add(container.NewHBox(btns...))
			}
			box.Add(widget.NewSeparator())
		}
		if kmemMode {
			box.Add(boldLabel("Terminal sessions (not Quill sessions)"))
			box.Add(caption("Quill sessions use kmem and run with Claude Code's auto memory off. The switch and MEMORY.md below only matter for Claude Code run in a terminal."))
		}
		box.Add(d.autoMemoryRow(cwd))
		box.Add(widget.NewSeparator())
		box.Add(boldLabel("Instruction files (CLAUDE.md)"))
		for i, f := range memoryFiles(cwd) {
			f := f
			autoMem := i == len(memoryFiles(cwd))-1
			_, err := os.Stat(f)
			exists := err == nil
			name := widget.NewLabel(memoryNames[i])
			name.TextStyle = fyne.TextStyle{Bold: true}
			path := tildePath(f)
			if !exists {
				path += "  ·  not created yet"
			}
			if autoMem && kmemMode {
				path += "  ·  not used by Quill sessions (they use kmem)"
			}
			sub := widget.NewLabel(path)
			sub.SizeName = theme.SizeNameCaptionText
			sub.Wrapping = fyne.TextWrapBreak
			var btn fyne.CanvasObject
			if exists {
				b := widget.NewButtonWithIcon("Open", theme.DocumentIcon(), func() { d.openPath(f) })
				b.Importance = widget.MediumImportance
				btn = b
			} else if !(autoMem && kmemMode) {
				b := widget.NewButtonWithIcon("Create", theme.ContentAddIcon(), func() {
					_ = os.MkdirAll(filepath.Dir(f), 0o755)
					if err := os.WriteFile(f, []byte("# Notes for Claude\n"), 0o644); err == nil {
						d.openPath(f)
						fill()
					}
				})
				b.Importance = widget.MediumImportance
				btn = b
			} else {
				btn = layout.NewSpacer()
			}
			text := container.New(&tightVBox{}, name, sub)
			box.Add(container.NewBorder(nil, nil, nil, container.NewCenter(btn), text))
			box.Add(widget.NewSeparator())
		}
		box.Refresh()
	}
	fill()
	return box
}

// autoMemoryRow: Claude Code's Auto-memory switch and its folder for this
// project, like its own /memory dialog.
func (d *desk) autoMemoryRow(cwd string) fyne.CanvasObject {
	chk := widget.NewCheck("Auto-memory (Claude Code reads and writes its memory folder)", nil)
	chk.Checked = autoMemoryOn()
	reverting := false
	chk.OnChanged = func(on bool) {
		if reverting {
			return
		}
		if err := setAutoMemory(on); err != nil {
			reverting = true
			chk.SetChecked(!on)
			reverting = false
			dialog.ShowError(err, d.win)
		}
	}
	dir := filepath.Join(homeDir(), ".claude", "projects", transcript.Sanitize(cwd), "memory")
	open := widget.NewButtonWithIcon("Open auto-memory folder", theme.FolderOpenIcon(), func() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		d.openPath(dir)
	})
	open.Importance = widget.MediumImportance
	note := caption("Saved in ~/.claude/settings.json (autoMemoryEnabled); sessions started after the change use it. Folder: " + tildePath(dir))
	note.Wrapping = fyne.TextWrapBreak
	return container.NewVBox(container.NewBorder(nil, nil, nil, container.NewCenter(open), chk), note)
}

func (d *desk) cmdMemory(s *deskSession) {
	dlg := dialog.NewCustom("Memory files", "Close", container.NewVScroll(d.memoryPane(s, s.cwd)), d.win)
	dlg.Resize(fyne.NewSize(720, 460))
	dlg.Show()
}

// settings files: permissions and hooks

func settingsFiles(cwd string) [][2]string {
	return [][2]string{
		{"User", filepath.Join(homeDir(), ".claude", "settings.json")},
		{"Project", filepath.Join(cwd, ".claude", "settings.json")},
		{"Local", filepath.Join(cwd, ".claude", "settings.local.json")},
	}
}

func readSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

type permRule struct {
	scope, file, kind, rule string
}

func permissionRules(cwd string) []permRule {
	var out []permRule
	for _, f := range settingsFiles(cwd) {
		m, err := readSettings(f[1])
		if err != nil {
			continue
		}
		perms, _ := m["permissions"].(map[string]any)
		for _, kind := range []string{"allow", "ask", "deny"} {
			list, _ := perms[kind].([]any)
			for _, r := range list {
				if s, ok := r.(string); ok {
					out = append(out, permRule{f[0], f[1], kind, s})
				}
			}
		}
	}
	return out
}

// updateSettings changes one Claude Code settings file: read (a missing
// file is empty), change, write a temp file next to it and rename it over
// (symlinks are followed; the mode is kept). Invalid JSON is left alone.
func updateSettings(path string, change func(m map[string]any) error) error {
	target := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		target = real
	}
	m := map[string]any{}
	if b, err := os.ReadFile(target); err == nil {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON; not changed: %w", tildePath(path), err)
		}
		if m == nil {
			m = map[string]any{}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := change(m); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(target); err == nil {
		mode = st.Mode().Perm()
	} else if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	tmp := target + ".quill-tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), mode); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

func permsOf(m map[string]any) map[string]any {
	perms, _ := m["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
		m["permissions"] = perms
	}
	return perms
}

func removeRule(r permRule) error {
	return updateSettings(r.file, func(m map[string]any) error {
		perms := permsOf(m)
		list, _ := perms[r.kind].([]any)
		var kept []any
		removed := false
		for _, x := range list {
			if s, ok := x.(string); ok && s == r.rule && !removed {
				removed = true
				continue
			}
			kept = append(kept, x)
		}
		if !removed {
			return fmt.Errorf("rule not found")
		}
		if kept == nil {
			kept = []any{}
		}
		perms[r.kind] = kept
		return nil
	})
}

// addRule appends an allow/ask/deny rule (once) to one settings file.
func addRule(file, kind, rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return fmt.Errorf("the rule is empty")
	}
	return updateSettings(file, func(m map[string]any) error {
		perms := permsOf(m)
		list, _ := perms[kind].([]any)
		for _, x := range list {
			if s, _ := x.(string); s == rule {
				return fmt.Errorf("%s already has %s rule %q", tildePath(file), kind, rule)
			}
		}
		perms[kind] = append(list, rule)
		return nil
	})
}

// Workspace folders: permissions.additionalDirectories of a settings file.
type workDir struct {
	scope, file, dir string
}

func workspaceDirs(cwd string) []workDir {
	var out []workDir
	for _, f := range settingsFiles(cwd) {
		m, err := readSettings(f[1])
		if err != nil {
			continue
		}
		perms, _ := m["permissions"].(map[string]any)
		list, _ := perms["additionalDirectories"].([]any)
		for _, x := range list {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, workDir{f[0], f[1], s})
			}
		}
	}
	return out
}

func setWorkspaceDir(file, dir string, add bool) error {
	return updateSettings(file, func(m map[string]any) error {
		perms := permsOf(m)
		list, _ := perms["additionalDirectories"].([]any)
		var kept []any
		found := false
		for _, x := range list {
			if s, _ := x.(string); s == dir {
				found = true
				if !add {
					continue
				}
			}
			kept = append(kept, x)
		}
		switch {
		case add && found:
			return fmt.Errorf("%s is already a workspace folder there", tildePath(dir))
		case add:
			kept = append(kept, dir)
		case !found:
			return fmt.Errorf("folder not found")
		}
		if kept == nil {
			kept = []any{}
		}
		perms["additionalDirectories"] = kept
		return nil
	})
}

// autoMemoryOn: Claude Code's auto-memory setting (autoMemoryEnabled in the
// user settings, default on), as its own /memory switch writes it.
func autoMemoryOn() bool {
	m, err := readSettings(filepath.Join(homeDir(), ".claude", "settings.json"))
	if err != nil {
		return true
	}
	v, ok := m["autoMemoryEnabled"].(bool)
	return !ok || v
}

func setAutoMemory(on bool) error {
	return updateSettings(filepath.Join(homeDir(), ".claude", "settings.json"), func(m map[string]any) error {
		m["autoMemoryEnabled"] = on
		return nil
	})
}

func (d *desk) permissionsPane(cwd string) fyne.CanvasObject {
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		rules := permissionRules(cwd)
		if len(rules) == 0 {
			box.Add(widget.NewLabel("No allow, ask or deny rules in your user, project or local settings."))
		}
		for _, r := range rules {
			r := r
			text := fmt.Sprintf("%s · %s · %s", r.kind, r.scope, r.rule)
			if r.kind == "allow" {
				text += "  ·  runs without asking or a plan"
			}
			if touchesKmemData(r.rule) && r.kind != "deny" {
				text = "⚠ " + text + "  ·  changes kmem's data directly"
			}
			l := widget.NewLabel(text)
			l.Truncation = fyne.TextTruncateEllipsis
			rm := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
				if err := removeRule(r); err != nil {
					dialog.ShowError(err, d.win)
					return
				}
				fill()
			})
			rm.Importance = widget.LowImportance
			box.Add(container.NewBorder(nil, nil, nil, rm, l))
		}
		box.Refresh()
	}
	fill()
	note := widget.NewLabel("Rules from ~/.claude/settings.json (User), .claude/settings.json (Project) and .claude/settings.local.json (Local) of " + tildePath(cwd) + ". Also added by \"Always allow...\" on a blocked command.")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	ro := widget.NewButtonWithIcon("Read-only commands (run without a plan)...", theme.SettingsIcon(), d.readOnlyDialog)
	ro.Importance = widget.LowImportance
	ro.Alignment = widget.ButtonAlignLeading
	add := widget.NewButtonWithIcon("Add rule...", theme.ContentAddIcon(), func() { d.addRuleDialog(cwd, fill) })
	add.Importance = widget.MediumImportance
	rules := container.NewBorder(container.NewHBox(add), note, nil, nil, container.NewVScroll(box))
	return container.NewAppTabs(container.NewTabItem("Rules", rules), container.NewTabItem("Plan-first", d.planFirstPane(ro)),
		container.NewTabItem("Workspace", d.workspacePane(cwd)))
}

// planFirstPane: what runs without a plan round when Plan first is on.
func (d *desk) planFirstPane(ro *widget.Button) fyne.CanvasObject {
	state := "Plan first is off: Claude Code's own permission prompts apply."
	if d.planFirst {
		state = "Plan first is on: anything that isn't read-only waits for an approved plan, except tools your allow rules let through."
	}
	head := widget.NewLabel(state)
	head.Wrapping = fyne.TextWrapWord
	km := widget.NewLabel("kmem's read-only commands always run without a plan:\n" + strings.ReplaceAll(builtinReadOnly, "\n", ", ") +
		"\nDuring a kmem handoff save Quill asked for, the session may also run its kmem writes.")
	km.Wrapping = fyne.TextWrapWord
	extra := strings.TrimSpace(d.app.Preferences().String("extraReadOnly"))
	if extra == "" {
		extra = "(none)"
	}
	mine := widget.NewLabel("Your own read-only commands: " + strings.ReplaceAll(extra, "\n", ", "))
	mine.Wrapping = fyne.TextWrapWord
	return container.NewVScroll(container.NewVBox(head, widget.NewSeparator(), km, widget.NewSeparator(), mine, container.NewHBox(ro)))
}

// kmemDataRe: rules reaching kmem's store or live files directly; kmem's data
// must only change through kmem itself (shared rule 18).
var kmemDataRe = regexp.MustCompile(`(?i)share/kmem|kmem\.db|sqlite3?\b.*kmem|\.kmem`)

func touchesKmemData(rule string) bool {
	return kmemDataRe.MatchString(rule) && !strings.HasPrefix(strings.TrimSpace(rule), "Read(")
}

var settingsScopes = []string{"Local (this folder, only you)", "Project (this folder, shared in the repo)", "User (all folders)"}

func scopeFile(cwd, label string) string {
	for _, f := range settingsFiles(cwd) {
		if strings.HasPrefix(label, f[0]) {
			return f[1]
		}
	}
	return ""
}

func (d *desk) addRuleDialog(cwd string, done func()) {
	kind := widget.NewRadioGroup([]string{"allow", "ask", "deny"}, nil)
	kind.Horizontal = true
	kind.SetSelected("allow")
	rule := widget.NewEntry()
	rule.SetPlaceHolder("Bash(npm test *)   or   Read(./docs/**)   or   WebFetch(domain:example.com)")
	where := widget.NewSelect(settingsScopes, nil)
	where.SetSelected(settingsScopes[0])
	warn := widget.NewLabel("")
	warn.Wrapping = fyne.TextWrapWord
	warn.Importance = widget.WarningImportance
	update := func() {
		var w []string
		if kind.Selected == "allow" {
			w = append(w, "Allowed tools run without asking you and without a plan: Claude Code allows them before Quill's plan-first sees them.")
		}
		if kind.Selected != "deny" && touchesKmemData(rule.Text) {
			w = append(w, "This reaches kmem's data directly; kmem's data must only change through the kmem command (shared rule 18).")
		}
		warn.SetText(strings.Join(w, "\n"))
	}
	kind.OnChanged = func(string) { update() }
	rule.OnChanged = func(string) { update() }
	update()
	dlg := dialog.NewForm("Add permission rule", "Add", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Kind", kind), widget.NewFormItem("Rule", rule), widget.NewFormItem("Save in", where),
		widget.NewFormItem("", caption("Tool(pattern), as in Claude Code's /permissions. Deny wins over ask, ask over allow.")),
		widget.NewFormItem("", warn),
	}, func(ok bool) {
		if !ok {
			return
		}
		if err := addRule(scopeFile(cwd, where.Selected), kind.Selected, rule.Text); err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		done()
	}, d.win)
	dlg.Resize(fyne.NewSize(620, 320))
	dlg.Show()
}

// workspacePane: the extra folders Claude may use (additionalDirectories in
// the settings files, plus this session's /add-dir folders).
func (d *desk) workspacePane(cwd string) fyne.CanvasObject {
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		dirs := workspaceDirs(cwd)
		if s := d.current; s != nil && s.cwd == cwd {
			for _, dir := range s.addDirs {
				dirs = append(dirs, workDir{scope: "This session", dir: dir})
			}
			p := d.projectOf(s.id)
			for _, dir := range d.org.Dirs[p] {
				dirs = append(dirs, workDir{scope: "Project “" + p + "” (Quill)", file: "quill:" + p, dir: dir})
			}
		}
		if len(dirs) == 0 {
			box.Add(widget.NewLabel("No extra working folders. Claude works in " + tildePath(cwd) + "."))
		}
		if pd := kmemProjectsDir(); pd != "" && d.current != nil && !strings.HasPrefix(cwd+"/", strings.TrimSuffix(pd, "/")+"/") {
			has := false
			for _, w := range dirs {
				has = has || w.dir == pd
			}
			if !has {
				s := d.current
				sugg := widget.NewButtonWithIcon("Add kmem's Projects folder "+tildePath(pd)+"...", theme.FolderNewIcon(), func() { d.addDirWhere(s, pd, fill) })
				sugg.Importance = widget.LowImportance
				box.Add(container.NewVBox(caption("This session works outside kmem's projects folder:"), container.NewHBox(sugg)))
			}
		}
		for _, w := range dirs {
			w := w
			l := widget.NewLabel(w.scope + " · " + tildePath(w.dir))
			l.Truncation = fyne.TextTruncateEllipsis
			rm := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
				var err error
				if p, ok := strings.CutPrefix(w.file, "quill:"); ok {
					d.setProjectDir(p, w.dir, false)
					if s := d.current; s != nil {
						d.restartOnNext(s, "Removed "+tildePath(w.dir)+" from project “"+p+"”.")
					}
				} else if w.file == "" {
					if s := d.current; s != nil {
						s.addDirs = removeString(s.addDirs, w.dir)
						d.restartOnNext(s, "Removed "+tildePath(w.dir)+" from this session's working folders.")
					}
				} else {
					err = setWorkspaceDir(w.file, w.dir, false)
				}
				if err != nil {
					dialog.ShowError(err, d.win)
				}
				fill()
			})
			rm.Importance = widget.LowImportance
			box.Add(container.NewBorder(nil, nil, nil, rm, l))
		}
		box.Refresh()
	}
	fill()
	add := widget.NewButtonWithIcon("Add folder...", theme.FolderNewIcon(), func() {
		s := d.current
		if s == nil {
			return
		}
		dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
			if err == nil && u != nil {
				d.addDirWhere(s, u.Path(), fill)
			}
		}, d.win)
	})
	add.Importance = widget.MediumImportance
	note := caption("Settings folders apply to sessions started after the change (Claude Code's permissions.additionalDirectories); session folders restart the session's Claude on your next message.")
	note.Wrapping = fyne.TextWrapWord
	return container.NewBorder(container.NewHBox(add), note, nil, nil, container.NewVScroll(box))
}

func removeString(list []string, v string) []string {
	var out []string
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func (d *desk) cmdPermissions(s *deskSession) {
	dlg := dialog.NewCustom("Permissions", "Close", d.permissionsPane(s.cwd), d.win)
	dlg.Resize(fyne.NewSize(760, 480))
	dlg.Show()
}

func hooksText(cwd string) string {
	var b strings.Builder
	for _, f := range settingsFiles(cwd) {
		m, err := readSettings(f[1])
		if err != nil {
			continue
		}
		h, ok := m["hooks"]
		if !ok {
			continue
		}
		pretty, _ := json.MarshalIndent(h, "", "  ")
		fmt.Fprintf(&b, "%s (%s)\n%s\n\n", f[0], tildePath(f[1]), pretty)
	}
	if b.Len() == 0 {
		return "No hooks are configured in your user, project or local settings."
	}
	return b.String()
}

// agents

type agentFile struct {
	name, desc, path string
}

func agentFiles(cwd string) []agentFile {
	var out []agentFile
	for _, dir := range []string{filepath.Join(homeDir(), ".claude", "agents"), filepath.Join(cwd, ".claude", "agents")} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.md"))
		sort.Strings(matches)
		for _, p := range matches {
			a := agentFile{name: strings.TrimSuffix(filepath.Base(p), ".md"), path: p}
			if b, err := os.ReadFile(p); err == nil {
				for _, line := range strings.Split(string(b), "\n")[1:] {
					if strings.TrimSpace(line) == "---" {
						break
					}
					if v, ok := strings.CutPrefix(line, "name:"); ok {
						a.name = strings.Trim(strings.TrimSpace(v), `"'`)
					}
					if v, ok := strings.CutPrefix(line, "description:"); ok {
						a.desc = strings.Trim(strings.TrimSpace(v), `"'`)
					}
				}
			}
			out = append(out, a)
		}
	}
	return out
}

func (d *desk) cmdAgents(s *deskSession) {
	dlg := dialog.NewCustom("Subagents", "Close", container.NewVScroll(d.agentsPane(s, s.cwd)), d.win)
	dlg.Resize(fyne.NewSize(760, 420))
	dlg.Show()
}

// add-dir

func (d *desk) addDir(s *deskSession, dir string) {
	dir = expandHome(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(s.cwd, dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		d.notify(s, "Not a folder: "+dir)
		return
	}
	if contains(s.addDirs, dir) {
		d.notify(s, tildePath(dir)+" is already added.")
		return
	}
	s.addDirs = append(s.addDirs, dir)
	d.restartOnNext(s, "Added "+tildePath(dir)+" as a working folder.")
}

func (d *desk) cmdAddDir(s *deskSession, arg string) {
	if arg != "" {
		d.addDir(s, arg)
		return
	}
	dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
		if err == nil && u != nil {
			d.addDirWhere(s, u.Path(), nil)
		}
	}, d.win)
}

// addDirWhere asks where to remember a new working folder: this session,
// every session of its Quill project (the kmem project in the sidebar), or
// Claude Code's Local/Project/User settings.
func (d *desk) addDirWhere(s *deskSession, dir string, done func()) {
	project := d.projectOf(s.id)
	optSession := "This session only"
	optProject := "Every session of project “" + project + "” (Quill)"
	where := widget.NewSelect(append([]string{optSession, optProject}, settingsScopes...), nil)
	where.SetSelected(optSession)
	dlg := dialog.NewForm("Add "+tildePath(dir), "Add", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Remember for", where),
		widget.NewFormItem("", caption("Project folders go to every session Quill starts for that project (new ones, forks, restarts); settings folders to every Claude Code session that reads that file.")),
	}, func(ok bool) {
		if !ok {
			return
		}
		switch where.Selected {
		case optSession:
			d.addDir(s, dir)
		case optProject:
			if contains(d.org.Dirs[project], dir) {
				d.notify(s, tildePath(dir)+" is already a folder of project “"+project+"”.")
				break
			}
			d.setProjectDir(project, dir, true)
			d.restartOnNext(s, "Added "+tildePath(dir)+" for every session of project “"+project+"”.")
		default:
			if err := setWorkspaceDir(scopeFile(s.cwd, where.Selected), dir, true); err != nil {
				dialog.ShowError(err, d.win)
				return
			}
			d.restartOnNext(s, "Added "+tildePath(dir)+" to Claude Code's "+strings.Fields(where.Selected)[0]+" settings.")
		}
		if done != nil {
			done()
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(640, 300))
	dlg.Show()
}

// chrome

func (d *desk) setChrome(s *deskSession, on bool) {
	want := "off"
	if on {
		want = "on"
	}
	if s.chrome == want {
		d.notify(s, "Claude in Chrome is already "+want+" for this session.")
		return
	}
	s.chrome = want
	d.restartOnNext(s, "Claude in Chrome turned "+want+" for this session.")
}

func (d *desk) cmdChrome(s *deskSession, arg string) {
	switch strings.ToLower(arg) {
	case "on", "enable":
		d.setChrome(s, true)
		return
	case "off", "disable":
		d.setChrome(s, false)
		return
	}
	defOn := account.ChromeDefault()
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	state := onOff(defOn) + " (Enabled by default)"
	switch s.chrome {
	case "on", "off":
		state = s.chrome
	}
	var dlg dialog.Dialog
	on := widget.NewButtonWithIcon("Turn on", theme.ConfirmIcon(), func() { dlg.Hide(); d.setChrome(s, true) })
	off := widget.NewButtonWithIcon("Turn off", theme.CancelIcon(), func() { dlg.Hide(); d.setChrome(s, false) })
	msg := widget.NewLabel("Claude in Chrome for this session: " + state + ".\nIt lets Claude use your Chrome through the Claude extension. Changing it restarts the session's Claude process on your next message.")
	msg.Wrapping = fyne.TextWrapWord
	defChk := widget.NewCheck("Enabled by default", nil)
	defChk.Checked = defOn
	reverting := false
	defChk.OnChanged = func(v bool) {
		if reverting {
			return
		}
		if err := account.SetChromeDefault(v); err != nil {
			reverting = true
			defChk.SetChecked(!v)
			reverting = false
			dialog.ShowError(err, d.win)
		}
	}
	defHint := widget.NewLabel("Claude Code's own setting (also in its /config): new sessions, and sessions without their own on/off, connect Chrome when they start.")
	defHint.Wrapping = fyne.TextWrapWord
	defHint.Importance = widget.LowImportance
	dlg = dialog.NewCustom("Claude in Chrome", "Close", container.NewVBox(msg, container.NewHBox(on, off), widget.NewSeparator(), defChk, defHint), d.win)
	dlg.Resize(fyne.NewSize(520, 300))
	dlg.Show()
}

// tasks

func (d *desk) cmdTasks(s *deskSession) {
	tasks := s.conv.BackgroundTasks()
	var rows []fyne.CanvasObject
	var dlg dialog.Dialog
	if len(tasks) == 0 {
		rows = append(rows, widget.NewLabel("No background tasks are running in this session."))
	}
	for _, t := range tasks {
		t := t
		what := t.What
		if what == "" {
			what = t.Tool
		}
		l := widget.NewLabel(oneLine(what, 140) + "  ·  " + t.ID)
		l.Truncation = fyne.TextTruncateEllipsis
		btns := container.NewHBox()
		if t.Log != "" {
			btns.Add(widget.NewButtonWithIcon("Log", theme.DocumentIcon(), func() { d.openPath(t.Log) }))
		}
		stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
			dlg.Hide()
			d.sendTo(s, "Please stop background task "+t.ID+" with TaskStop.")
		})
		stop.Importance = widget.DangerImportance
		btns.Add(stop)
		rows = append(rows, container.NewBorder(nil, nil, nil, btns, l))
	}
	dlg = dialog.NewCustom("Background tasks", "Close", container.NewVScroll(container.NewVBox(rows...)), d.win)
	dlg.Resize(fyne.NewSize(760, 360))
	dlg.Show()
}

// mcp

type mcpServer struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	Transport string    `json:"transport"`
	Scope     string    `json:"scope"`
	Tools     []mcpTool `json:"tools"`
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Annotations struct {
		ReadOnly    *bool `json:"readOnly"`
		Destructive *bool `json:"destructive"`
	} `json:"annotations"`
}

// mcpToolsDialog lists a server's tools, and whether Quill's plan-first lets
// each run without a plan.
func (d *desk) mcpToolsDialog(sv mcpServer) {
	var rows []fyne.CanvasObject
	if len(sv.Tools) == 0 {
		rows = append(rows, widget.NewLabel("Claude Code didn't list tools for "+sv.Name+" (not connected, or the list was too large to send)."))
	}
	extra := d.readOnlyExtra()
	for _, t := range sv.Tools {
		wire := "mcp__" + sv.Name + "__" + t.Name
		if strings.HasPrefix(t.Name, "mcp__") {
			wire = t.Name
		}
		var tags []string
		if r := t.Annotations.ReadOnly; r != nil && *r {
			tags = append(tags, "read-only")
		}
		if x := t.Annotations.Destructive; x != nil && *x {
			tags = append(tags, "destructive")
		}
		if d.planFirst {
			if readOnlyRequest(&protocol.ControlRequest{ToolName: wire, Input: json.RawMessage(`{}`)}, extra) {
				tags = append(tags, "runs without a plan")
			} else {
				tags = append(tags, "needs a plan (plan-first)")
			}
		}
		name := boldLabel(t.Name)
		info := caption(strings.Join(append(tags, oneLine(t.Description, 200)), " · "))
		info.Wrapping = fyne.TextWrapWord
		rows = append(rows, container.NewVBox(name, info), widget.NewSeparator())
	}
	dlg := dialog.NewCustom(sv.Name+" tools ("+strconv.Itoa(len(sv.Tools))+")", "Close", container.NewVScroll(container.NewVBox(rows...)), d.win)
	dlg.Resize(fyne.NewSize(720, 480))
	dlg.Show()
}

func parseMCP(raw json.RawMessage) []mcpServer {
	var r struct {
		Servers []mcpServer `json:"mcpServers"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Servers
}

func (d *desk) cmdMCP(s *deskSession) {
	dlg := dialog.NewCustom("MCP servers", "Close", container.NewVScroll(d.mcpPane(s)), d.win)
	dlg.Resize(fyne.NewSize(780, 440))
	dlg.Show()
}

func (d *desk) mcpPane(s *deskSession) *fyne.Container {
	box := container.NewVBox(widget.NewLabel("Asking Claude Code for its MCP servers..."))
	var load func()
	act := func(req map[string]any, done string) {
		go func() {
			raw, err := d.control(s, req)
			d.do(func() {
				if err != nil {
					dialog.ShowError(err, d.win)
					return
				}
				var auth struct {
					AuthURL string `json:"authUrl"`
				}
				if json.Unmarshal(raw, &auth) == nil && auth.AuthURL != "" {
					if u, err := url.Parse(auth.AuthURL); err == nil {
						_ = d.app.OpenURL(u)
					}
					d.notify(s, "Opened the sign-in page for "+fmt.Sprint(req["serverName"])+" in your browser.")
				} else if done != "" {
					d.notify(s, done)
				}
				load()
			})
		}()
	}
	load = func() {
		go func() {
			raw, err := d.control(s, map[string]any{"subtype": "mcp_status"})
			d.do(func() {
				box.Objects = nil
				if err != nil {
					box.Add(widget.NewLabel("Could not get the MCP status: " + err.Error()))
					box.Refresh()
					return
				}
				servers := parseMCP(raw)
				if len(servers) == 0 {
					box.Add(widget.NewLabel("No MCP servers are configured for this session."))
				}
				for _, sv := range servers {
					sv := sv
					text := sv.Name + "  ·  " + sv.Status
					if sv.Scope != "" {
						text += "  ·  " + sv.Scope
					}
					if sv.Error != "" {
						text += "  ·  " + oneLine(sv.Error, 100)
					}
					l := widget.NewLabel(text)
					l.Truncation = fyne.TextTruncateEllipsis
					btns := container.NewHBox()
					if sv.Status == "needs-auth" {
						btns.Add(widget.NewButtonWithIcon("Sign in", theme.LoginIcon(), func() {
							act(map[string]any{"subtype": "mcp_authenticate", "serverName": sv.Name}, "")
						}))
					}
					btns.Add(widget.NewButtonWithIcon("Tools", theme.ListIcon(), func() { d.mcpToolsDialog(sv) }))
					if sv.Transport == "http" || sv.Transport == "sse" {
						btns.Add(widget.NewButtonWithIcon("Clear sign-in", theme.LogoutIcon(), func() {
							dialog.ShowConfirm("Clear "+sv.Name+"'s sign-in?", "Claude Code forgets the saved sign-in (OAuth tokens) for this server; it asks you to sign in again when needed.", func(ok bool) {
								if ok {
									act(map[string]any{"subtype": "mcp_clear_auth", "serverName": sv.Name}, "Cleared the sign-in for "+sv.Name+".")
								}
							}, d.win)
						}))
					}
					if sv.Status != "disabled" {
						btns.Add(widget.NewButtonWithIcon("Reconnect", theme.ViewRefreshIcon(), func() {
							act(map[string]any{"subtype": "mcp_reconnect", "serverName": sv.Name}, "Reconnected "+sv.Name+".")
						}))
						btns.Add(widget.NewButtonWithIcon("Disable", theme.CancelIcon(), func() {
							act(map[string]any{"subtype": "mcp_toggle", "serverName": sv.Name, "enabled": false}, "Disabled "+sv.Name+".")
						}))
					} else {
						btns.Add(widget.NewButtonWithIcon("Enable", theme.ConfirmIcon(), func() {
							act(map[string]any{"subtype": "mcp_toggle", "serverName": sv.Name, "enabled": true}, "Enabled "+sv.Name+".")
						}))
					}
					box.Add(container.NewBorder(nil, nil, nil, btns, l))
				}
				box.Refresh()
			})
		}()
	}
	load()
	return box
}

// fork

func (d *desk) forkSession(s *deskSession) (*deskSession, error) {
	if s.running() && s.conv.Busy {
		return nil, fmt.Errorf("wait for the current turn to finish")
	}
	info, ok := d.infos[s.id]
	if !ok || info.Path == "" {
		return nil, fmt.Errorf("this session has nothing saved to fork yet")
	}
	b, err := os.ReadFile(info.Path)
	if err != nil {
		return nil, err
	}
	id := session.NewUUID()
	b = bytes.ReplaceAll(b, []byte(s.id), []byte(id))
	dst := filepath.Join(filepath.Dir(info.Path), id+".jsonl")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return nil, err
	}
	title, _, _ := d.rowText(s.id)
	ns := &deskSession{id: id, cwd: s.cwd, model: s.model, mode: s.mode, chrome: s.chrome, addDirs: append([]string(nil), s.addDirs...), conv: chat.New(), persisted: true, created: time.Now()}
	if conv, err := transcript.Load(dst); err == nil && conv != nil {
		ns.conv, ns.loaded = conv, true
	}
	d.sessions[id] = ns
	if k := d.org.Kmem[s.id]; k != "" {
		ns.forkOf = k
		d.org.Kmem[id] = kmemSlug(k+"-fork", d.kmemTaken(nil))
	}
	d.org.Titles[id] = title + " (fork)"
	if place := d.placement(s.id); strings.HasPrefix(place, folderPrefix) {
		d.putPlace(id, place)
	}
	d.saveOrg()
	go d.refresh()
	return ns, nil
}

func (d *desk) cmdFork(s *deskSession) {
	ns, err := d.forkSession(s)
	if err != nil {
		d.notify(s, "Could not fork: "+err.Error())
		return
	}
	d.open(ns.id)
	d.notify(ns, "Forked from “"+strings.TrimSuffix(d.org.Titles[ns.id], " (fork)")+"”. The original session is unchanged.")
}

// rewind

func (d *desk) rewindTo(s *deskSession, u *chat.Item, files bool) {
	d.rewindMode(s, u, files, false)
}

// rewindCode undoes Claude's file edits since a message and keeps the whole
// conversation (Claude Code's "Restore code").
func (d *desk) rewindCode(s *deskSession, u *chat.Item) {
	d.rewindMode(s, u, true, true)
}

// kmemWriteRe finds kmem commands that change kmem (they stay when the chat
// that ran them is rewound).
var kmemWriteRe = regexp.MustCompile(`\bkmem\b(?:\s+--as\s+(?:"[^"]*"|\S+))?\s+(add|retire|trash|handoff\s+save|task\s+(?:add|claim|done|release))\b[^\n;&|]*`)
var kmemClaimRe = regexp.MustCompile(`\btask\s+claim\s+(\S+)`)

// kmemWrites lists the kmem writes in Bash calls among items, and the task
// ids those calls claimed (and didn't finish or release).
func kmemWrites(items []*chat.Item) (writes, claims []string) {
	open := map[string]bool{}
	var order []string
	for _, it := range items {
		if it.Kind != chat.Tool || it.ToolName != "Bash" {
			continue
		}
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(it.Input, &in) != nil {
			continue
		}
		for _, m := range kmemWriteRe.FindAllString(in.Command, -1) {
			writes = append(writes, strings.TrimSpace(m))
			if c := kmemClaimRe.FindStringSubmatch(m); c != nil {
				if !open[c[1]] {
					order = append(order, c[1])
				}
				open[c[1]] = true
			}
			for _, verb := range []string{"done", "release"} {
				if c := regexp.MustCompile(`\btask\s+` + verb + `\s+(\S+)`).FindStringSubmatch(m); c != nil {
					open[c[1]] = false
				}
			}
		}
	}
	for _, id := range order {
		if open[id] {
			claims = append(claims, id)
		}
	}
	return writes, claims
}

// rewindMode: before cutting the chat, kmem writes in the part that goes
// away are listed (they stay in kmem) and claimed tasks can be released
// (user 2026-11-21).
func (d *desk) rewindMode(s *deskSession, u *chat.Item, files, keepChat bool) {
	if keepChat {
		d.rewindGo(s, u, files, keepChat, nil)
		return
	}
	idx := -1
	for i, it := range s.conv.Items {
		if it == u {
			idx = i
		}
	}
	if idx < 0 {
		d.rewindGo(s, u, files, keepChat, nil)
		return
	}
	writes, claims := kmemWrites(s.conv.Items[idx:])
	if len(writes) == 0 {
		d.rewindGo(s, u, files, keepChat, nil)
		return
	}
	shown := writes
	if len(shown) > 8 {
		shown = append(append([]string{}, shown[:8]...), fmt.Sprintf("… and %d more", len(writes)-8))
	}
	msg := widget.NewLabel("The part you rewind ran these kmem commands. kmem keeps them: its history is never undone.\n\n" + strings.Join(shown, "\n"))
	msg.Wrapping = fyne.TextWrapWord
	objs := []fyne.CanvasObject{msg}
	release := widget.NewCheck("", nil)
	if len(claims) > 0 {
		release.SetText("Ask this session to release task " + strings.Join(claims, ", ") + " after the rewind")
		release.Checked = true
		objs = append(objs, release)
	}
	c := dialog.NewCustomConfirm("Rewind past kmem changes?", "Rewind", "Cancel", container.NewVBox(objs...), func(ok bool) {
		if !ok {
			return
		}
		var rel []string
		if len(claims) > 0 && release.Checked {
			rel = claims
		}
		d.rewindGo(s, u, files, keepChat, rel)
	}, d.win)
	c.Resize(fyne.NewSize(640, 360))
	c.Show()
}

func (d *desk) rewindGo(s *deskSession, u *chat.Item, files, keepChat bool, releaseTasks []string) {
	if s.running() && s.conv.Busy {
		d.notify(s, "Wait for the current turn to finish before rewinding.")
		return
	}
	idx := -1
	for i, it := range s.conv.Items {
		if it == u {
			idx = i
		}
	}
	if idx < 0 || u.UUID == "" {
		d.notify(s, "That message can't be rewound to.")
		return
	}
	prev := ""
	for i := idx - 1; i >= 0; i-- {
		if it := s.conv.Items[i]; it.Kind == chat.Assistant && it.UUID != "" {
			prev = it.UUID
			break
		}
	}
	finish := func(note string) {
		if s.running() && s.conv.Busy {
			d.notify(s, "A turn started in the meantime; nothing was rewound.")
			return
		}
		text := u.Text
		if prev == "" {
			title, _, _ := d.rowText(s.id)
			ns := d.replaceWith(s, title)
			d.inputFor(ns).SetText(text)
			d.notify(ns, note+"Rewound to the start: this is a fresh session with your first message ready to edit. The old one is in the sidebar.")
			return
		}
		s.conv.Items = append([]*chat.Item(nil), s.conv.Items[:idx]...)
		s.conv.Busy = false
		s.resumeAt = prev
		// An approval given in the rewound part ends with it.
		s.runAll, s.runAllNext, s.planWait = false, false, false
		if len(releaseTasks) > 0 {
			s.releaseTasks = releaseTasks
			defer d.sendTo(s, "The conversation was rewound past your kmem task claim(s). Release them now, since that work was undone: "+
				"run `kmem task release "+strings.Join(releaseTasks, "`, `kmem task release ")+"` (they're yours, so the release is allowed), then reply with one line.")
		}
		if v := d.viewFor(s); v != nil {
			v.cache = map[*deskSession]*sessView{}
			v.s = nil
			v.show(s)
		}
		d.restartOnNext(s, note+"Rewound to before “"+oneLine(text, 60)+"”; it's back in the message box to edit.")
		d.inputFor(s).SetText(text)
	}
	if !files {
		finish("")
		return
	}
	type filesReply struct {
		CanRewind    *bool    `json:"canRewind"`
		Error        string   `json:"error"`
		FilesChanged []string `json:"filesChanged"`
		Insertions   int      `json:"insertions"`
		Deletions    int      `json:"deletions"`
	}
	apply := func(preview filesReply) {
		go func() {
			raw, err := d.control(s, map[string]any{"subtype": "rewind_files", "user_message_id": u.UUID})
			var r filesReply
			_ = json.Unmarshal(raw, &r)
			d.do(func() {
				if err == nil && r.CanRewind != nil && !*r.CanRewind {
					err = fmt.Errorf("%s", firstNonEmpty(r.Error, "Claude Code couldn't restore them"))
				}
				if err != nil {
					d.notify(s, "Files were not rewound ("+err.Error()+"); the conversation was left as it was.")
					return
				}
				note := fmt.Sprintf("Restored %s to how they were at that message (+%d −%d). ", plural(len(preview.FilesChanged), "file"), preview.Insertions, preview.Deletions)
				if keepChat {
					d.notify(s, note+"The conversation is unchanged.")
					return
				}
				finish(note)
			})
		}()
	}
	go func() {
		raw, err := d.control(s, map[string]any{"subtype": "rewind_files", "user_message_id": u.UUID, "dry_run": true})
		var p filesReply
		_ = json.Unmarshal(raw, &p)
		d.do(func() {
			if err != nil {
				d.notify(s, "Files were not rewound ("+err.Error()+"); the conversation was left as it was.")
				return
			}
			if p.CanRewind != nil && !*p.CanRewind || len(p.FilesChanged) == 0 {
				if keepChat {
					d.notify(s, "There are no file changes to undo since that message.")
					return
				}
				finish("There were no file changes to undo since that message. ")
				return
			}
			var names []string
			for i, f := range p.FilesChanged {
				if i == 10 {
					names = append(names, fmt.Sprintf("… and %d more", len(p.FilesChanged)-10))
					break
				}
				names = append(names, tildePath(f))
			}
			what := ", and rewind the conversation?"
			if keepChat {
				what = "? The conversation stays as it is."
			}
			msg := fmt.Sprintf("Restore %s (+%d −%d) to how they were before “%s”%s\n\n%s", plural(len(p.FilesChanged), "file"), p.Insertions, p.Deletions, oneLine(u.Text, 60), what, strings.Join(names, "\n"))
			dlg := dialog.NewConfirm("Rewind files?", msg, func(ok bool) {
				if ok {
					apply(p)
				}
			}, d.win)
			dlg.SetConfirmText("Restore files")
			dlg.Show()
		})
	}()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (d *desk) cmdRewind(s *deskSession) {
	var users []*chat.Item
	for i := len(s.conv.Items) - 1; i >= 0 && len(users) < 40; i-- {
		if it := s.conv.Items[i]; it.Kind == chat.User && it.UUID != "" {
			users = append(users, it)
		}
	}
	var dlg dialog.Dialog
	var rows []fyne.CanvasObject
	if len(users) == 0 {
		rows = append(rows, widget.NewLabel("No earlier messages to rewind to in this session yet."))
	}
	for _, u := range users {
		u := u
		l := widget.NewLabel(oneLine(u.Text, 140))
		l.Truncation = fyne.TextTruncateEllipsis
		conv := widget.NewButtonWithIcon("Rewind", theme.MediaSkipPreviousIcon(), func() { dlg.Hide(); d.rewindTo(s, u, false) })
		both := widget.NewButtonWithIcon("Rewind + files", theme.HistoryIcon(), func() { dlg.Hide(); d.rewindTo(s, u, true) })
		code := widget.NewButtonWithIcon("Files only", theme.DocumentIcon(), func() { dlg.Hide(); d.rewindCode(s, u) })
		rows = append(rows, container.NewBorder(nil, nil, nil, container.NewHBox(conv, both, code), l))
	}
	note := widget.NewLabel("Rewind removes that message and everything after it from the conversation and puts the message back in the box to edit. Rewind + files also undoes Claude's file edits since then; Files only undoes those edits and keeps the conversation (only edits made since Quill turned on file checkpoints).")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	dlg = dialog.NewCustom("Rewind to before…", "Cancel", container.NewBorder(nil, note, nil, nil, container.NewVScroll(container.NewVBox(rows...))), d.win)
	dlg.Resize(fyne.NewSize(820, 480))
	dlg.Show()
}

// message menu, fork from here

type msgMenu struct {
	widget.BaseWidget
	content  fyne.CanvasObject
	onMenu   func(fyne.Position)
	onTap    func()
	onDouble func(fyne.Position)
}

func newMsgMenu(content fyne.CanvasObject, onMenu func(fyne.Position)) *msgMenu {
	m := &msgMenu{content: content, onMenu: onMenu}
	m.ExtendBaseWidget(m)
	return m
}

func (m *msgMenu) Tapped(*fyne.PointEvent) {
	if m.onTap != nil {
		m.onTap()
	}
}

func (m *msgMenu) DoubleTapped(e *fyne.PointEvent) {
	if m.onDouble != nil {
		m.onDouble(e.AbsolutePosition)
	}
}

type bgTap struct {
	widget.BaseWidget
	onTap func()
}

func newBgTap(onTap func()) *bgTap {
	b := &bgTap{onTap: onTap}
	b.ExtendBaseWidget(b)
	return b
}

func (b *bgTap) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (b *bgTap) Tapped(*fyne.PointEvent) {
	if b.onTap != nil {
		b.onTap()
	}
}

func (m *msgMenu) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(m.content)
}

func (m *msgMenu) TappedSecondary(e *fyne.PointEvent) {
	if m.onMenu != nil {
		m.onMenu(e.AbsolutePosition)
	}
}

func (d *desk) messageMenuItems(s *deskSession, it *chat.Item) []*fyne.MenuItem {
	var items []*fyne.MenuItem
	for _, v := range d.allViews() {
		if v.s == s && v.sel != nil && v.sel.text() != "" {
			sel := v.sel
			items = append(items, fyne.NewMenuItem("Copy selection", func() { sel.copy() }))
		}
	}
	items = append(items, fyne.NewMenuItem("Copy message", func() {
		d.app.Clipboard().SetContent(it.Text)
		d.notify(s, "Copied the message.")
	}))
	if it.UUID != "" {
		items = append(items, fyne.NewMenuItemSeparator())
		if it.Kind == chat.User {
			items = append(items,
				fyne.NewMenuItem("Rewind to here", func() { d.rewindTo(s, it, false) }),
				fyne.NewMenuItem("Rewind to here and undo file changes", func() { d.rewindTo(s, it, true) }),
				fyne.NewMenuItem("Undo file changes since here (keep the chat)", func() { d.rewindCode(s, it) }))
		}
		items = append(items, fyne.NewMenuItem("Fork from here", func() { d.forkFrom(s, it) }))
	}
	return items
}

func (d *desk) messageMenu(s *deskSession, it *chat.Item, at fyne.Position) {
	if s == nil {
		return
	}
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", d.messageMenuItems(s, it)...), d.win.Canvas(), at)
}

func (d *desk) forkFrom(s *deskSession, it *chat.Item) {
	ns, err := d.forkSession(s)
	if err != nil {
		d.notify(s, "Could not fork: "+err.Error())
		return
	}
	var match *chat.Item
	idx := -1
	for i, x := range ns.conv.Items {
		if x.UUID == it.UUID && x.Kind == it.Kind {
			match, idx = x, i
		}
	}
	d.open(ns.id)
	if match == nil {
		d.notify(ns, "Forked the whole session (that message wasn't found in the copy).")
		return
	}
	if match.Kind == chat.User {
		d.rewindTo(ns, match, false)
		return
	}
	ns.conv.Items = append([]*chat.Item(nil), ns.conv.Items[:idx+1]...)
	ns.resumeAt = match.UUID
	if v := d.viewFor(ns); v != nil {
		v.cache = map[*deskSession]*sessView{}
		v.s = nil
		v.show(ns)
	}
	d.notify(ns, "Forked from this message; the original session is unchanged.")
}

func (d *desk) sessionToolsItem(id string) *fyne.MenuItem {
	run := func(f func(s *deskSession)) func() {
		return func() {
			if s := d.session(id); s != nil {
				f(s)
			}
		}
	}
	tools := fyne.NewMenuItem("Session tools", nil)
	tools.ChildMenu = fyne.NewMenu("",
		fyne.NewMenuItem("Rewind...", run(d.cmdRewind)),
		fyne.NewMenuItem("Fork", run(d.cmdFork)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Export...", run(func(s *deskSession) { d.cmdExport(s, "") })),
		fyne.NewMenuItem("Show changes (git diff)", run(d.cmdDiff)),
		fyne.NewMenuItem("Background tasks", run(d.cmdTasks)),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("MCP servers", run(d.cmdMCP)),
		fyne.NewMenuItem("Claude in Chrome...", run(func(s *deskSession) { d.cmdChrome(s, "") })),
		fyne.NewMenuItem("Add working folder...", run(func(s *deskSession) { d.cmdAddDir(s, "") })),
	)
	return tools
}

func forkIntro(kmemName, own string) string {
	return fmt.Sprintf("(Quill: this session is a fork of the session with kmem name %q. Its handoff (`kmem handoff get %s`) is context only: don't save to it or use its task claims; that session still owns them. This fork's own kmem name is %q: save handoffs and claim tasks under that name.)\n\n", kmemName, kmemName, own)
}
