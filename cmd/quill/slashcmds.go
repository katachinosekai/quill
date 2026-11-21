package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
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
	if !s.running() {
		if err := d.ensureProcess(s); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.proc.Control(ctx, req)
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

func exportMarkdown(title string, c *chat.Conversation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
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

func (d *desk) cmdExport(s *deskSession, arg string) {
	title, _, _ := d.rowText(s.id)
	text := exportMarkdown(title, s.conv)
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
	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			return
		}
		path := w.URI().Path()
		w.Close()
		write(path)
	}, d.win)
	dlg.SetFileName(safeName(title) + ".md")
	if l, err := storage.ListerForURI(storage.NewFileURI(s.cwd)); err == nil {
		dlg.SetLocation(l)
	}
	dlg.Show()
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
		diff = diff[:400000] + "\n… (cut at 400 KB)"
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

func (d *desk) cmdMemory(s *deskSession) {
	var rows []fyne.CanvasObject
	for _, f := range memoryFiles(s.cwd) {
		f := f
		_, err := os.Stat(f)
		exists := err == nil
		l := widget.NewLabel(tildePath(f))
		l.Truncation = fyne.TextTruncateEllipsis
		var btn *widget.Button
		if exists {
			btn = widget.NewButtonWithIcon("Open", theme.DocumentIcon(), func() { d.openPath(f) })
		} else {
			btn = widget.NewButtonWithIcon("Create", theme.ContentAddIcon(), func() {
				_ = os.MkdirAll(filepath.Dir(f), 0o755)
				if err := os.WriteFile(f, []byte("# Notes for Claude\n"), 0o644); err == nil {
					d.openPath(f)
				}
			})
			l.Importance = widget.LowImportance
		}
		rows = append(rows, container.NewBorder(nil, nil, nil, btn, l))
	}
	dlg := dialog.NewCustom("Memory files", "Close", container.NewVBox(rows...), d.win)
	dlg.Resize(fyne.NewSize(700, 320))
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

func removeRule(r permRule) error {
	m, err := readSettings(r.file)
	if err != nil {
		return err
	}
	perms, _ := m["permissions"].(map[string]any)
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
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.file + ".quill-tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.file)
}

func (d *desk) cmdPermissions(s *deskSession) {
	var dlg dialog.Dialog
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		rules := permissionRules(s.cwd)
		if len(rules) == 0 {
			box.Add(widget.NewLabel("No allow, ask or deny rules in your user, project or local settings."))
		}
		for _, r := range rules {
			r := r
			l := widget.NewLabel(fmt.Sprintf("%s · %s · %s", r.kind, r.scope, r.rule))
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
	note := widget.NewLabel("Rules from ~/.claude/settings.json (User), .claude/settings.json (Project) and .claude/settings.local.json (Local). Add rules with \"Always allow...\" on a blocked command.")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	dlg = dialog.NewCustom("Permissions", "Close", container.NewBorder(nil, note, nil, nil, container.NewVScroll(box)), d.win)
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
	agents := agentFiles(s.cwd)
	var rows []fyne.CanvasObject
	if len(agents) == 0 {
		rows = append(rows, widget.NewLabel("No subagents in ~/.claude/agents or .claude/agents of this folder."))
	}
	for _, a := range agents {
		a := a
		l := widget.NewLabel(a.name + " · " + oneLine(a.desc, 120))
		l.Truncation = fyne.TextTruncateEllipsis
		open := widget.NewButtonWithIcon("Open", theme.DocumentIcon(), func() { d.openPath(a.path) })
		open.Importance = widget.LowImportance
		rows = append(rows, container.NewBorder(nil, nil, nil, open, l))
	}
	dlg := dialog.NewCustom("Subagents", "Close", container.NewVScroll(container.NewVBox(rows...)), d.win)
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
			d.addDir(s, u.Path())
		}
	}, d.win)
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
	state := "default (your Claude Code setting)"
	switch s.chrome {
	case "on":
		state = "on"
	case "off":
		state = "off"
	}
	var dlg dialog.Dialog
	on := widget.NewButtonWithIcon("Turn on", theme.ConfirmIcon(), func() { dlg.Hide(); d.setChrome(s, true) })
	off := widget.NewButtonWithIcon("Turn off", theme.CancelIcon(), func() { dlg.Hide(); d.setChrome(s, false) })
	msg := widget.NewLabel("Claude in Chrome for this session: " + state + ".\nIt lets Claude use your Chrome through the Claude extension. Changing it restarts the session's Claude process on your next message.")
	msg.Wrapping = fyne.TextWrapWord
	dlg = dialog.NewCustom("Claude in Chrome", "Close", container.NewVBox(msg, container.NewHBox(on, off)), d.win)
	dlg.Resize(fyne.NewSize(520, 220))
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
	Name      string `json:"name"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	Transport string `json:"transport"`
	Scope     string `json:"scope"`
}

func parseMCP(raw json.RawMessage) []mcpServer {
	var r struct {
		Servers []mcpServer `json:"mcpServers"`
	}
	_ = json.Unmarshal(raw, &r)
	return r.Servers
}

func (d *desk) cmdMCP(s *deskSession) {
	box := container.NewVBox(widget.NewLabel("Asking Claude Code for its MCP servers..."))
	dlg := dialog.NewCustom("MCP servers", "Close", container.NewVScroll(box), d.win)
	dlg.Resize(fyne.NewSize(780, 440))
	dlg.Show()
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
}

// fork

func (d *desk) forkSession(s *deskSession) (*deskSession, error) {
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
	d.org.Titles[id] = title + " (fork)"
	if place := d.placement(s.id); strings.HasPrefix(place, folderPrefix) {
		d.org.Place[id] = place
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
	go func() {
		raw, err := d.control(s, map[string]any{"subtype": "rewind_files", "user_message_id": u.UUID})
		d.do(func() {
			if err != nil {
				d.notify(s, "Files were not rewound ("+err.Error()+"); the conversation was left as it was.")
				return
			}
			var r struct {
				Changed []string `json:"filesChanged"`
			}
			_ = json.Unmarshal(raw, &r)
			note := "Restored the files to how they were at that message. "
			if len(r.Changed) > 0 {
				note = fmt.Sprintf("Restored %s to how they were at that message. ", plural(len(r.Changed), "file"))
			}
			finish(note)
		})
	}()
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
		rows = append(rows, container.NewBorder(nil, nil, nil, container.NewHBox(conv, both), l))
	}
	note := widget.NewLabel("Rewind removes that message and everything after it from the conversation and puts the message back in the box to edit. Rewind + files also undoes Claude's file edits since then (only edits made since Quill turned on file checkpoints).")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	dlg = dialog.NewCustom("Rewind to before…", "Cancel", container.NewBorder(nil, note, nil, nil, container.NewVScroll(container.NewVBox(rows...))), d.win)
	dlg.Resize(fyne.NewSize(820, 480))
	dlg.Show()
}
