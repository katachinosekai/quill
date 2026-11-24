package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/protocol"
)

// /hooks and /agents, adapted to Quill and kmem (user 2026-11-21): kmem's
// own hooks are marked and can be repaired; turning all hooks off warns that
// it also stops the kmem brief and guard; new agents can carry the kmem
// briefing the shared rules ask of every subagent (rule 23).

type hookRow struct {
	scope, file, event, matcher, command string
}

func isKmemHook(cmd string) bool {
	c := strings.NewReplacer(`"`, "", "'", "").Replace(cmd)
	return strings.Contains(c, "kmem brief") || strings.Contains(c, "kmem-guard")
}

func hookRows(cwd string) []hookRow {
	var out []hookRow
	for _, f := range settingsFiles(cwd) {
		m, err := readSettings(f[1])
		if err != nil {
			continue
		}
		hooks, _ := m["hooks"].(map[string]any)
		events := make([]string, 0, len(hooks))
		for ev := range hooks {
			events = append(events, ev)
		}
		sort.Strings(events)
		for _, ev := range events {
			groups, _ := hooks[ev].([]any)
			for _, g := range groups {
				gm, _ := g.(map[string]any)
				matcher, _ := gm["matcher"].(string)
				hs, _ := gm["hooks"].([]any)
				for _, h := range hs {
					hm, _ := h.(map[string]any)
					cmd, _ := hm["command"].(string)
					if cmd == "" {
						t, _ := hm["type"].(string)
						cmd = "(" + t + " hook)"
					}
					out = append(out, hookRow{f[0], f[1], ev, matcher, cmd})
				}
			}
		}
	}
	return out
}

func hooksOff(path string) bool {
	m, err := readSettings(path)
	if err != nil {
		return false
	}
	v, _ := m["disableAllHooks"].(bool)
	return v
}

func setHooksOff(path string, off bool) error {
	return updateSettings(path, func(m map[string]any) error {
		if off {
			m["disableAllHooks"] = true
		} else {
			delete(m, "disableAllHooks")
		}
		return nil
	})
}

func (d *desk) hooksPane(cwd string) fyne.CanvasObject {
	home := homeDir()
	userFile := claudeSettingsFile(home)
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		// kmem's hooks first: they carry the session brief and the guards.
		if kmemBin() != "" {
			if kmemHooksPresent(home) {
				box.Add(caption("kmem hooks: installed (SessionStart: kmem brief, PreToolUse: kmem-guard)."))
			} else {
				msg := widget.NewLabel("⚠ kmem is installed but its hooks are missing: sessions start without the kmem brief and without the guards.")
				msg.Wrapping = fyne.TextWrapWord
				msg.Importance = widget.WarningImportance
				repair := widget.NewButtonWithIcon("Repair kmem hooks", theme.ConfirmIcon(), func() {
					changed, backup, err := mergeKmemHooks(userFile, home, time.Now())
					if err != nil {
						dialog.ShowError(err, d.win)
						return
					}
					if changed {
						text := "Added the kmem brief and kmem-guard hooks to " + tildePath(userFile) + "; new sessions use them."
						if backup != "" {
							text += " Backup: " + tildePath(backup)
						}
						dialog.ShowInformation("kmem hooks", text, d.win)
					}
					fill()
				})
				repair.Importance = widget.HighImportance
				box.Add(container.NewVBox(msg, container.NewHBox(repair)))
			}
		}
		off := widget.NewCheck("Turn off all hooks (Claude Code's disableAllHooks, user settings)", nil)
		off.Checked = hooksOff(userFile)
		reverting := false
		set := func(on bool) {
			if err := setHooksOff(userFile, on); err != nil {
				dialog.ShowError(err, d.win)
			}
			fill()
		}
		off.OnChanged = func(on bool) {
			if reverting {
				return
			}
			if on && kmemHooksPresent(home) {
				reverting = true
				off.SetChecked(false)
				reverting = false
				c := dialog.NewConfirm("Turn off all hooks?", "This also turns off kmem's hooks: new sessions start without the kmem brief, and kmem-guard stops checking tool calls. The shared rules rely on both.", func(ok bool) {
					if ok {
						set(true)
					}
				}, d.win)
				c.SetConfirmText("Turn off anyway")
				c.SetDismissText("Cancel")
				c.Show()
				return
			}
			set(on)
		}
		box.Add(off)
		for _, f := range settingsFiles(cwd) {
			if f[1] != userFile && hooksOff(f[1]) {
				box.Add(caption("⚠ " + f[0] + " settings (" + tildePath(f[1]) + ") turn all hooks off for this folder."))
			}
		}
		box.Add(widget.NewSeparator())
		rows := hookRows(cwd)
		if len(rows) == 0 {
			box.Add(widget.NewLabel("No hooks in your user, project or local settings."))
		}
		lastFile := ""
		for _, r := range rows {
			r := r
			if r.file != lastFile {
				lastFile = r.file
				open := widget.NewButtonWithIcon("Open", theme.DocumentIcon(), func() { d.openPath(r.file) })
				open.Importance = widget.LowImportance
				box.Add(container.NewBorder(nil, nil, nil, open, boldLabel(r.scope+" · "+tildePath(r.file))))
			}
			head := r.event
			if r.matcher != "" {
				head += " (" + r.matcher + ")"
			}
			if isKmemHook(r.command) {
				head += " · kmem"
			}
			l := widget.NewLabel(head + ": " + oneLine(r.command, 160))
			l.Wrapping = fyne.TextWrapBreak
			box.Add(l)
		}
		box.Refresh()
	}
	fill()
	return box
}

func (d *desk) cmdHooks(s *deskSession) {
	dlg := dialog.NewCustom("Hooks", "Close", container.NewVScroll(d.hooksPane(s.cwd)), d.win)
	dlg.Resize(fyne.NewSize(780, 480))
	dlg.Show()
}

// agents

// kmemAgentNote is added to the instructions of agents made with "Start with
// a kmem briefing"; the marker line finds it again when editing.
const kmemAgentMarker = "<!-- quill: kmem briefing -->"

const kmemAgentNote = kmemAgentMarker + "\nBefore you start, run `kmem context \"<your task in a few words>\"` and follow the briefing it prints (its first line starts with KMEM-BRIEF:). Read more with `kmem get`/`kmem find`; write to kmem only when your task asks for it."

type agentDef struct {
	Name, Description, Tools, Model, Body string
	Kmem                                  bool
}

var agentNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func yamlValue(s string) string {
	if s == "" || strings.ContainsAny(s, ":#\n\"'{}[],&*!|>%@`") || strings.TrimSpace(s) != s {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return s
}

func (a agentDef) file() string {
	var b strings.Builder
	b.WriteString("---\nname: " + a.Name + "\ndescription: " + yamlValue(a.Description) + "\n")
	if t := strings.TrimSpace(a.Tools); t != "" {
		b.WriteString("tools: " + t + "\n")
	}
	if m := strings.TrimSpace(a.Model); m != "" && m != "inherit" {
		b.WriteString("model: " + m + "\n")
	}
	b.WriteString("---\n\n" + strings.TrimSpace(a.Body) + "\n")
	if a.Kmem {
		b.WriteString("\n" + kmemAgentNote + "\n")
	}
	return b.String()
}

func parseAgentFile(text string) agentDef {
	var a agentDef
	rest := text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			for _, line := range strings.Split(text[4:4+end], "\n") {
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				v = strings.TrimSpace(v)
				if strings.HasPrefix(v, "\"") {
					var u string
					if json.Unmarshal([]byte(v), &u) == nil {
						v = u
					}
				} else {
					v = strings.Trim(v, "'")
				}
				switch strings.TrimSpace(k) {
				case "name":
					a.Name = v
				case "description":
					a.Description = v
				case "tools":
					a.Tools = v
				case "model":
					a.Model = v
				}
			}
			rest = strings.TrimPrefix(text[4+end+4:], "\n")
		}
	}
	if i := strings.Index(rest, kmemAgentMarker); i >= 0 {
		a.Kmem = true
		rest = rest[:i]
	} else if strings.Contains(rest, "kmem context") {
		a.Kmem = true // written by hand: keep the text as it is
	}
	a.Body = strings.TrimSpace(rest)
	return a
}

func agentDirs(cwd string) [][2]string {
	return [][2]string{{"User", filepath.Join(homeDir(), ".claude", "agents")}, {"Project", filepath.Join(cwd, ".claude", "agents")}}
}

func saveAgent(dir string, a agentDef, old string) (string, error) {
	if !agentNameRe.MatchString(a.Name) {
		return "", fmt.Errorf("the name must be lowercase letters, digits and dashes (like code-reviewer)")
	}
	if strings.TrimSpace(a.Description) == "" {
		return "", fmt.Errorf("the description is empty: Claude uses it to decide when to use the agent")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, a.Name+".md")
	if path != old {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("%s already exists", tildePath(path))
		}
	}
	tmp := path + ".quill-tmp"
	if err := os.WriteFile(tmp, []byte(a.file()), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	if old != "" && old != path {
		_ = trashFile(old)
	}
	return path, nil
}

// trashFile moves a file to the desktop trash so it can be restored.
func trashFile(path string) error {
	if out, err := exec.Command("gio", "trash", path).CombinedOutput(); err != nil {
		return fmt.Errorf("could not move %s to the trash: %s", tildePath(path), strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return nil
}

var agentModels = []string{"inherit", "fable", "opus", "sonnet", "haiku"}

func (d *desk) agentDialog(cwd string, a agentDef, old string, done func()) {
	name := widget.NewEntry()
	name.SetText(a.Name)
	name.SetPlaceHolder("code-reviewer")
	desc := widget.NewMultiLineEntry()
	desc.SetText(a.Description)
	desc.SetPlaceHolder("When Claude should use this agent, e.g. \"Reviews a diff for bugs after code changes.\"")
	desc.SetMinRowsVisible(2)
	desc.Wrapping = fyne.TextWrapWord
	tools := widget.NewEntry()
	tools.SetText(a.Tools)
	tools.SetPlaceHolder("all tools (or a list: Read, Grep, Glob, Bash)")
	model := widget.NewSelect(agentModels, nil)
	model.SetSelected(firstNonEmpty(a.Model, "inherit"))
	where := widget.NewSelect([]string{"User (~/.claude/agents)", "Project (" + tildePath(filepath.Join(cwd, ".claude", "agents")) + ")"}, nil)
	where.SetSelected(where.Options[0])
	if old != "" && strings.HasPrefix(old, filepath.Join(cwd, ".claude", "agents")) {
		where.SetSelected(where.Options[1])
	}
	body := widget.NewMultiLineEntry()
	body.SetText(a.Body)
	body.SetPlaceHolder("The agent's instructions (its system prompt).")
	body.SetMinRowsVisible(8)
	body.Wrapping = fyne.TextWrapWord
	items := []*widget.FormItem{widget.NewFormItem("Name", name), widget.NewFormItem("Description", desc), widget.NewFormItem("Tools", tools),
		widget.NewFormItem("Model", model), widget.NewFormItem("Save in", where), widget.NewFormItem("Instructions", body)}
	kmemChk := widget.NewCheck("Start with a kmem briefing (kmem context, as the shared rules ask of every subagent)", nil)
	kmemChk.Checked = a.Kmem || old == ""
	if kmemBin() != "" {
		items = append(items, widget.NewFormItem("", kmemChk))
	}
	title := "New agent"
	if old != "" {
		title = "Edit agent"
	}
	dlg := dialog.NewForm(title, "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		dir := agentDirs(cwd)[0][1]
		if strings.HasPrefix(where.Selected, "Project") {
			dir = agentDirs(cwd)[1][1]
		}
		na := agentDef{Name: strings.TrimSpace(name.Text), Description: strings.TrimSpace(desc.Text), Tools: tools.Text, Model: model.Selected, Body: body.Text,
			Kmem: kmemBin() != "" && kmemChk.Checked}
		if _, err := saveAgent(dir, na, old); err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		done()
	}, d.win)
	dlg.Resize(fyne.NewSize(760, 640))
	dlg.Show()
}

func (d *desk) builtinAgents(s *deskSession) []protocol.Agent {
	if s != nil && s.proc != nil {
		if a := s.proc.Agents(); len(a) > 0 {
			return a
		}
	}
	return d.agentList
}

func (d *desk) agentsPane(s *deskSession, cwd string) fyne.CanvasObject {
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		files := agentFiles(cwd)
		mine := map[string]bool{}
		for _, a := range files {
			mine[a.name] = true
			def := agentDef{}
			if b, err := os.ReadFile(a.path); err == nil {
				def = parseAgentFile(string(b))
			}
			a := a
			scope := "User"
			if strings.HasPrefix(a.path, filepath.Join(cwd, ".claude", "agents")) {
				scope = "Project"
			}
			tag := ""
			if def.Kmem {
				tag = " · kmem"
			}
			l := widget.NewLabel(a.name + " · " + scope + tag + " · " + oneLine(a.desc, 100))
			l.Truncation = fyne.TextTruncateEllipsis
			edit := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() { d.agentDialog(cwd, def, a.path, fill) })
			edit.Importance = widget.LowImportance
			del := widget.NewButtonWithIcon("Delete", theme.DeleteIcon(), func() {
				dialog.ShowConfirm("Delete "+a.name+"?", tildePath(a.path)+" goes to the trash (you can restore it from there).", func(ok bool) {
					if !ok {
						return
					}
					if err := trashFile(a.path); err != nil {
						dialog.ShowError(err, d.win)
					}
					fill()
				}, d.win)
			})
			del.Importance = widget.LowImportance
			box.Add(container.NewBorder(nil, nil, nil, container.NewHBox(edit, del), l))
		}
		if len(files) == 0 {
			box.Add(widget.NewLabel("No agents of your own yet (~/.claude/agents, .claude/agents of this folder)."))
		}
		var built []fyne.CanvasObject
		for _, a := range d.builtinAgents(s) {
			if mine[a.Name] {
				continue
			}
			l := widget.NewLabel(a.Name + " · " + oneLine(a.Description, 120))
			l.Truncation = fyne.TextTruncateEllipsis
			built = append(built, l)
		}
		if len(built) > 0 {
			box.Add(widget.NewSeparator())
			box.Add(boldLabel("Built into Claude Code (read-only)"))
			for _, o := range built {
				box.Add(o)
			}
		}
		box.Refresh()
	}
	fill()
	add := widget.NewButtonWithIcon("New agent...", theme.ContentAddIcon(), func() { d.agentDialog(cwd, agentDef{}, "", fill) })
	add.Importance = widget.MediumImportance
	return container.NewVBox(container.NewHBox(add), box)
}
