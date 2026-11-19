package main

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/live"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
	"github.com/katachinosekai/quill/internal/transcript"
)

var modelChoices = []string{"default", "fable", "opus", "sonnet", "haiku"}
var modeChoices = []string{"default", "acceptEdits", "plan", "auto"}

type status int

const (
	stNone status = iota
	stRunning
	stWaiting
	stIdle
	stError
	stTerminal
)

func (s status) color() color.Color {
	switch s {
	case stRunning:
		return colRunning
	case stWaiting:
		return colWaiting
	case stIdle:
		return colIdle
	case stError:
		return colError
	case stTerminal:
		return colTerminal
	}
	return color.Transparent
}

func (s status) String() string {
	return [...]string{"", "running", "waiting for you", "idle", "error", "open in a terminal"}[s]
}

type deskSession struct {
	id        string
	cwd       string
	title     string
	conv      *chat.Conversation
	loaded    bool
	loadedMod time.Time
	persisted bool
	proc      *session.Process
	model     string
	mode      string
	failed    string
	stopping  bool
	terminal  *live.Process
	rcURL     string
	rcOn      bool
	created   time.Time
}

func (s *deskSession) running() bool {
	return s.proc != nil && !s.proc.Exited()
}

func (s *deskSession) status() status {
	if s.terminal != nil && !s.running() {
		return stTerminal
	}
	if !s.running() {
		if s.failed != "" {
			return stError
		}
		return stNone
	}
	if len(s.conv.PendingPermissions()) > 0 {
		return stWaiting
	}
	if s.conv.Busy || s.proc.Busy() {
		return stRunning
	}
	return stIdle
}

type config struct {
	ProjectsRoot string
	SessionsDir  string
	ClaudeBin    string
}

type sideRow struct {
	header string
	id     string
}

type desk struct {
	app      fyne.App
	win      fyne.Window
	cfg      config
	scanner  *transcript.Scanner
	finder   *live.Finder
	center   bool
	sessions map[string]*deskSession
	infos    map[string]transcript.Info
	order    []transcript.Info
	terms    map[string]live.Process
	current  *deskSession
	ownPIDs  sync.Map

	rows      []sideRow
	list      *widget.List
	search    *widget.Entry
	chat      *chatView
	title     *widget.Label
	subtitle  *canvas.Text
	note      *widget.Label
	rcCheck   *widget.Check
	rcLink    *widget.Hyperlink
	input     *sendEntry
	modelSel  *widget.Select
	modeSel   *widget.Select
	folder    *canvas.Text
	stats     *canvas.Text
	stopBtn   *widget.Button
	sendBtn   *widget.Button
	suppress  bool
	refreshMu sync.Mutex
	headerBox *fyne.Container
	mainPane  *fyne.Container
	do        func(func())
	doWait    func(func())
}

func newDesk(a fyne.App, w fyne.Window, cfg config) *desk {
	d := &desk{
		app:      a,
		win:      w,
		cfg:      cfg,
		scanner:  transcript.NewScanner(cfg.ProjectsRoot),
		finder:   &live.Finder{Dir: cfg.SessionsDir, ProcDir: "/proc"},
		sessions: map[string]*deskSession{},
		infos:    map[string]transcript.Info{},
		terms:    map[string]live.Process{},
		center:   a.Preferences().Bool("centerText"),
		do:       fyne.Do,
		doWait:   fyne.DoAndWait,
	}
	return d
}

func (d *desk) build() fyne.CanvasObject {
	d.chat = newChatView(d)
	split := container.NewHSplit(d.buildSidebar(), d.buildMain())
	split.Offset = 0.27
	return split
}

func (d *desk) buildSidebar() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("New session", theme.ContentAddIcon(), d.newSessionDialog)
	newBtn.Importance = widget.HighImportance
	d.search = widget.NewEntry()
	d.search.SetPlaceHolder("Search sessions")
	d.search.OnChanged = func(string) { d.rebuildRows() }
	d.list = widget.NewList(
		func() int { return len(d.rows) },
		func() fyne.CanvasObject {
			head := canvas.NewText("", colMuted)
			head.TextSize = 12
			head.TextStyle.Bold = true
			title := widget.NewLabel("")
			title.Truncation = fyne.TextTruncateEllipsis
			meta := canvas.NewText("", colMuted)
			meta.TextSize = 11
			dt := dot(color.Transparent)
			row := container.NewBorder(nil, nil, sized(10, 10, dt), nil,
				container.New(&tightVBox{}, title, container.NewPadded(meta)))
			return container.NewStack(container.NewPadded(head), row)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(d.rows) {
				return
			}
			st := o.(*fyne.Container)
			headBox := st.Objects[0].(*fyne.Container)
			head := headBox.Objects[0].(*canvas.Text)
			row := st.Objects[1].(*fyne.Container)
			r := d.rows[id]
			if r.header != "" {
				head.Text = r.header
				headBox.Show()
				row.Hide()
				head.Refresh()
				return
			}
			headBox.Hide()
			row.Show()
			col := row.Objects[0].(*fyne.Container)
			title := col.Objects[0].(*widget.Label)
			meta := col.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			dt := row.Objects[1].(*fyne.Container).Objects[0].(*canvas.Circle)
			t, m, st2 := d.rowText(r.id)
			title.SetText(t)
			meta.Text = m
			meta.Refresh()
			dt.FillColor = st2.color()
			dt.Refresh()
		},
	)
	d.list.OnSelected = func(id widget.ListItemID) {
		if id >= len(d.rows) || d.suppress {
			return
		}
		if d.rows[id].header != "" {
			d.list.Unselect(id)
			return
		}
		d.open(d.rows[id].id)
	}
	centerChk := widget.NewCheck("Center message text", func(on bool) {
		d.center = on
		d.app.Preferences().SetBool("centerText", on)
		if d.current != nil {
			d.chat.show(d.current)
		}
	})
	centerChk.Checked = d.center
	legend := container.NewHBox(
		legendDot(stRunning), legendDot(stWaiting), legendDot(stIdle), legendDot(stError), legendDot(stTerminal),
	)
	top := container.NewVBox(newBtn, d.search)
	bottom := container.NewVBox(widget.NewSeparator(), centerChk, legend)
	r := canvas.NewRectangle(colSidebar)
	return container.NewStack(r, container.NewPadded(container.NewBorder(top, bottom, nil, nil, d.list)))
}

func legendDot(s status) fyne.CanvasObject {
	label := map[status]string{stRunning: "run", stWaiting: "wait", stIdle: "idle", stError: "err", stTerminal: "term"}[s]
	return container.NewHBox(sized(8, 8, dot(s.color())), mutedText(label, 11))
}

type tightVBox struct{}

func (tightVBox) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		m := o.MinSize()
		s.Width = max(s.Width, m.Width)
		s.Height += m.Height
	}
	return fyne.NewSize(s.Width, s.Height-14)
}

func (tightVBox) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i, o := range objs {
		h := o.MinSize().Height
		if i > 0 {
			y -= 14
		}
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h
	}
}

func (d *desk) rowText(id string) (string, string, status) {
	s := d.sessions[id]
	info, ok := d.infos[id]
	title := info.Title
	var last time.Time
	msgs := info.Messages
	if ok {
		last = info.Last
	}
	st := stNone
	if s != nil {
		st = s.status()
		if !ok {
			title = s.title
			last = s.created
		}
		if s.loaded && countMessages(s.conv) > msgs {
			msgs = countMessages(s.conv)
		}
	} else if _, ok := d.terms[id]; ok {
		st = stTerminal
	}
	if title == "" {
		title = "New session"
	}
	meta := ago(last)
	if msgs > 0 {
		meta += fmt.Sprintf("  ·  %d msgs", msgs)
	}
	if st != stNone {
		meta += "  ·  " + st.String()
	}
	return title, meta, st
}

func countMessages(c *chat.Conversation) int {
	n := 0
	for _, it := range c.Items {
		if it.Kind == chat.User || it.Kind == chat.Assistant {
			n++
		}
	}
	return n
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "now"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}

func tildePath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

func (d *desk) rebuildRows() {
	q := strings.ToLower(strings.TrimSpace(d.search.Text))
	type entry struct {
		id   string
		cwd  string
		last time.Time
	}
	var entries []entry
	seen := map[string]bool{}
	for _, s := range d.sessions {
		if _, ok := d.infos[s.id]; !ok {
			entries = append(entries, entry{s.id, s.cwd, s.created})
			seen[s.id] = true
		}
	}
	for _, info := range d.order {
		entries = append(entries, entry{info.ID, info.Cwd, info.Last})
	}
	var filtered []entry
	for _, e := range entries {
		if q != "" {
			t, _, _ := d.rowText(e.id)
			if !strings.Contains(strings.ToLower(t), q) && !strings.Contains(strings.ToLower(e.cwd), q) {
				continue
			}
		}
		filtered = append(filtered, e)
	}
	groupLast := map[string]time.Time{}
	for _, e := range filtered {
		if e.last.After(groupLast[e.cwd]) {
			groupLast[e.cwd] = e.last
		}
		if _, ok := groupLast[e.cwd]; !ok {
			groupLast[e.cwd] = e.last
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		if a.cwd != b.cwd {
			ga, gb := groupLast[a.cwd], groupLast[b.cwd]
			if !ga.Equal(gb) {
				return ga.After(gb)
			}
			return a.cwd < b.cwd
		}
		return a.last.After(b.last)
	})
	rows := make([]sideRow, 0, len(filtered)+8)
	cur := "\x00"
	for _, e := range filtered {
		if e.cwd != cur {
			cur = e.cwd
			rows = append(rows, sideRow{header: tildePath(e.cwd)})
		}
		rows = append(rows, sideRow{id: e.id})
	}
	d.rows = rows
	d.suppress = true
	d.list.UnselectAll()
	for i, r := range rows {
		if r.header != "" {
			d.list.SetItemHeight(i, 30)
		} else {
			d.list.SetItemHeight(i, 54)
		}
		if d.current != nil && r.id == d.current.id {
			d.list.Select(i)
		}
	}
	d.suppress = false
	d.list.Refresh()
}

func (d *desk) refreshSidebar() {
	d.list.Refresh()
}

func (d *desk) buildMain() fyne.CanvasObject {
	d.title = widget.NewLabel("Quill")
	d.title.TextStyle = fyne.TextStyle{Bold: true}
	d.title.Truncation = fyne.TextTruncateEllipsis
	d.subtitle = mutedText("", 12)
	d.note = widget.NewLabel("")
	d.note.Importance = widget.WarningImportance
	d.note.Wrapping = fyne.TextWrapWord
	d.note.Hide()
	d.rcLink = widget.NewHyperlink("", nil)
	d.rcLink.Hide()
	d.rcCheck = widget.NewCheck("Remote Control", func(on bool) {
		if d.suppress || d.current == nil {
			return
		}
		d.setRemoteControl(d.current, on)
	})
	d.rcCheck.Hide()
	header := container.NewBorder(nil, nil, nil, container.NewHBox(d.rcLink, d.rcCheck),
		container.NewVBox(d.title, container.NewPadded(d.subtitle)))
	headerBox := container.NewVBox(container.NewPadded(header), d.note, widget.NewSeparator())
	d.headerBox = headerBox

	d.input = newSendEntry(d.send)
	d.input.SetPlaceHolder("Message Claude (Enter to send, Shift+Enter for a new line)")
	d.modelSel = widget.NewSelect(modelChoices, func(v string) {
		if d.suppress || d.current == nil {
			return
		}
		d.setModel(d.current, v)
	})
	d.modeSel = widget.NewSelect(modeChoices, func(v string) {
		if d.suppress || d.current == nil {
			return
		}
		d.setMode(d.current, v)
	})
	d.folder = mutedText("", 12)
	d.stats = mutedText("", 12)
	d.stopBtn = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		if d.current != nil {
			d.interrupt(d.current)
		}
	})
	d.stopBtn.Importance = widget.DangerImportance
	d.sendBtn = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.send(d.input.Text) })
	d.sendBtn.Importance = widget.HighImportance
	controls := container.NewHBox(d.modelSel, d.modeSel, container.NewCenter(d.folder), layout.NewSpacer(), d.stopBtn)
	inputCard := bg(colInput, 12, container.NewBorder(nil, controls, nil, container.NewVBox(layout.NewSpacer(), d.sendBtn), d.input))
	statsRow := container.New(&clipLayout{}, d.stats)
	bottom := container.NewPadded(centered(chatMaxWidth, container.NewVBox(inputCard, statsRow)))
	d.mainPane = container.NewBorder(headerBox, bottom, nil, nil, d.chat.root)
	d.updateChrome()
	return d.mainPane
}

func (d *desk) updateChrome() {
	s := d.current
	d.suppress = true
	defer func() {
		d.suppress = false
		if d.mainPane != nil {
			d.mainPane.Refresh()
		}
	}()
	if s == nil {
		d.title.SetText("Quill")
		d.subtitle.Text = ""
		d.subtitle.Refresh()
		d.note.Hide()
		d.rcCheck.Hide()
		d.rcLink.Hide()
		d.input.Disable()
		d.sendBtn.Disable()
		d.stopBtn.Disable()
		d.modelSel.Disable()
		d.modeSel.Disable()
		d.folder.Text = ""
		d.stats.Text = ""
		d.folder.Refresh()
		d.stats.Refresh()
		return
	}
	title, _, st := d.rowText(s.id)
	d.title.SetText(title)
	sub := tildePath(s.cwd)
	if st != stNone {
		sub += "  ·  " + st.String()
	}
	d.subtitle.Text = sub
	d.subtitle.Refresh()
	readOnly := s.terminal != nil && !s.running()
	if readOnly {
		d.note.SetText(fmt.Sprintf("This session is open in a terminal (pid %d). Showing it read-only; continue it there, or close it there to continue here.", s.terminal.PID))
		d.note.Show()
		d.input.Disable()
		d.sendBtn.Disable()
		d.modelSel.Disable()
		d.modeSel.Disable()
		d.rcCheck.Hide()
		d.rcLink.Hide()
	} else {
		d.note.Hide()
		d.input.Enable()
		d.sendBtn.Enable()
		d.modelSel.Enable()
		d.modeSel.Enable()
		d.rcCheck.Show()
		d.rcCheck.SetChecked(s.rcOn)
		if s.rcOn && s.rcURL != "" {
			if u, err := url.Parse(s.rcURL); err == nil {
				d.rcLink.SetText("Open remotely")
				d.rcLink.SetURL(u)
				d.rcLink.Show()
			}
		} else {
			d.rcLink.Hide()
		}
	}
	d.modelSel.SetSelected(s.model)
	d.modeSel.SetSelected(s.mode)
	if st == stRunning || st == stWaiting {
		d.stopBtn.Enable()
	} else {
		d.stopBtn.Disable()
	}
	d.folder.Text = filepath.Base(s.cwd)
	d.folder.Refresh()
	d.stats.Text = statsLine(s)
	d.stats.Refresh()
}

func statsLine(s *deskSession) string {
	var parts []string
	c := s.conv
	if c.Thinking && c.Busy {
		parts = append(parts, "thinking...")
	}
	if c.Stats.ContextUsed > 0 {
		ctx := "context " + kfmt(c.Stats.ContextUsed)
		if c.Stats.ContextMax > 0 {
			ctx += fmt.Sprintf(" / %s (%d%%)", kfmt(c.Stats.ContextMax), c.Stats.ContextUsed*100/c.Stats.ContextMax)
		}
		parts = append(parts, ctx)
	}
	if c.Stats.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", c.Stats.CostUSD))
	}
	if c.Stats.Model != "" {
		parts = append(parts, c.Stats.Model)
	}
	return strings.Join(parts, "  ·  ")
}

func kfmt(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.3gM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func (d *desk) session(id string) *deskSession {
	if s, ok := d.sessions[id]; ok {
		return s
	}
	info, ok := d.infos[id]
	if !ok {
		return nil
	}
	s := &deskSession{id: id, cwd: info.Cwd, title: info.Title, persisted: true, model: "default", mode: "default", conv: chat.New()}
	if p, ok := d.terms[id]; ok {
		pp := p
		s.terminal = &pp
	}
	d.sessions[id] = s
	return s
}

func (d *desk) open(id string) {
	s := d.session(id)
	if s == nil {
		return
	}
	d.current = s
	info, has := d.infos[id]
	needLoad := has && !s.running() && (!s.loaded || !info.ModTime.Equal(s.loadedMod))
	if needLoad {
		path, mod := info.Path, info.ModTime
		d.chat.show(nil)
		d.updateChrome()
		go func() {
			conv, err := transcript.Load(path)
			d.do(func() {
				if err != nil && conv == nil {
					conv = chat.New()
					conv.AddError("Could not read transcript: " + err.Error())
				}
				s.conv = conv
				s.loaded = true
				s.loadedMod = mod
				if conv.Cwd != "" {
					s.cwd = conv.Cwd
				}
				if conv.Mode != "" && s.mode == "default" && contains(modeChoices, conv.Mode) {
					s.mode = conv.Mode
				}
				if d.current == s {
					d.chat.show(s)
					d.updateChrome()
				}
				d.refreshSidebar()
			})
		}()
		return
	}
	d.chat.show(s)
	d.updateChrome()
	d.win.Canvas().Focus(d.input)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (d *desk) newSessionDialog() {
	home, _ := os.UserHomeDir()
	start := home
	if d.current != nil && d.current.cwd != "" {
		start = d.current.cwd
	}
	dir := widget.NewEntry()
	dir.SetText(start)
	browse := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err == nil && u != nil {
				dir.SetText(u.Path())
			}
		}, d.win)
		if l, err := listableURI(dir.Text); err == nil {
			fd.SetLocation(l)
		}
		fd.Resize(fyne.NewSize(700, 500))
		fd.Show()
	})
	model := widget.NewSelect(modelChoices, nil)
	model.SetSelected("default")
	mode := widget.NewSelect(modeChoices, nil)
	mode.SetSelected("default")
	form := widget.NewForm(
		widget.NewFormItem("Folder", container.NewBorder(nil, nil, nil, browse, dir)),
		widget.NewFormItem("Model", model),
		widget.NewFormItem("Permissions", mode),
	)
	dlg := dialog.NewCustomConfirm("New session", "Create", "Cancel", form, func(ok bool) {
		if !ok {
			return
		}
		path := strings.TrimSpace(dir.Text)
		if strings.HasPrefix(path, "~") {
			path = home + path[1:]
		}
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			dialog.ShowError(fmt.Errorf("not a folder: %s", path), d.win)
			return
		}
		d.createSession(path, model.Selected, mode.Selected)
	}, d.win)
	dlg.Resize(fyne.NewSize(560, 260))
	dlg.Show()
}

func (d *desk) createSession(cwd, model, mode string) *deskSession {
	id := session.NewUUID()
	s := &deskSession{id: id, cwd: cwd, model: model, mode: mode, conv: chat.New(), loaded: true, created: time.Now()}
	d.sessions[id] = s
	d.current = s
	d.rebuildRows()
	d.chat.show(s)
	d.updateChrome()
	d.win.Canvas().Focus(d.input)
	return s
}

func (d *desk) canAnswer(s *deskSession) bool {
	return s != nil && s.running()
}

func (d *desk) ensureProcess(s *deskSession) error {
	if s.running() {
		return nil
	}
	if s.terminal != nil {
		return fmt.Errorf("this session is open in a terminal")
	}
	opts := session.Options{Binary: d.cfg.ClaudeBin, Cwd: s.cwd, Model: s.model, Mode: s.mode}
	if s.persisted {
		opts.Resume = s.id
	} else {
		opts.SessionID = s.id
	}
	s.failed = ""
	s.stopping = false
	s.rcOn, s.rcURL = false, ""
	p, err := session.Start(opts, func(ev *protocol.Event) {
		d.do(func() { d.handleEvent(s, ev) })
	})
	if err != nil {
		s.failed = err.Error()
		return err
	}
	s.proc = p
	s.persisted = true
	d.ownPIDs.Store(p.PID(), true)
	go func() {
		<-p.Done()
		err := p.ExitError()
		d.do(func() { d.handleExit(s, p, err) })
	}()
	return nil
}

func (d *desk) handleEvent(s *deskSession, ev *protocol.Event) {
	items := s.conv.Apply(ev)
	if ev.Type == "system" && ev.Subtype == "init" && ev.SessionID != "" && ev.SessionID != s.id {
		s.conv.AddNotice("Claude reported a different session id: " + ev.SessionID)
	}
	if d.current == s {
		d.chat.update(items)
		d.updateChrome()
	}
	if ev.Type == "result" || ev.Type == "control_request" || ev.Type == "system" && ev.Subtype == "init" {
		d.refreshSidebar()
	}
}

func (d *desk) handleExit(s *deskSession, p *session.Process, err error) {
	d.ownPIDs.Delete(p.PID())
	if s.proc != p {
		return
	}
	var items []*chat.Item
	items = append(items, s.conv.CancelPending()...)
	for _, it := range s.conv.Items {
		if it.Streaming {
			it.Streaming = false
			items = append(items, it)
		}
	}
	s.conv.Busy = false
	s.rcOn, s.rcURL = false, ""
	if err != nil && !s.stopping {
		msg := "Claude process exited: " + err.Error()
		if tail := strings.TrimSpace(p.Stderr()); tail != "" {
			msg += "\n" + lastLines(tail, 6)
		}
		s.failed = msg
		items = append(items, s.conv.AddError(msg))
	}
	if d.current == s {
		d.chat.update(items)
		d.updateChrome()
	}
	d.refreshSidebar()
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (d *desk) send(text string) {
	s := d.current
	text = strings.TrimSpace(text)
	if s == nil || text == "" {
		return
	}
	if err := d.ensureProcess(s); err != nil {
		d.chat.update([]*chat.Item{s.conv.AddError("Could not start claude: " + err.Error())})
		d.updateChrome()
		return
	}
	it := s.conv.AddUser(text)
	if s.title == "" {
		s.title = oneLine(text, 60)
	}
	if err := s.proc.Send(text); err != nil {
		d.chat.update([]*chat.Item{it, s.conv.AddError("Send failed: " + err.Error())})
		return
	}
	s.conv.Busy = true
	d.input.SetText("")
	d.chat.update([]*chat.Item{it})
	d.chat.scrollBottom()
	d.updateChrome()
	d.rebuildRows()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func (d *desk) interrupt(s *deskSession) {
	p := s.proc
	if p == nil || p.Exited() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Interrupt(ctx)
	}()
}

func (d *desk) answer(s *deskSession, it *chat.Item, dec protocol.Decision) {
	if it.PermState != chat.PermPending || !s.running() {
		return
	}
	if err := s.proc.Respond(it.RequestID, dec, it.Request); err != nil {
		d.chat.update([]*chat.Item{s.conv.AddError("Could not answer: " + err.Error())})
		return
	}
	switch dec {
	case protocol.Allow:
		it.PermState = chat.PermAllowed
	case protocol.AllowAlways:
		it.PermState = chat.PermAllowedAlways
	default:
		it.PermState = chat.PermDenied
	}
	if d.current == s {
		d.chat.update([]*chat.Item{it})
		d.updateChrome()
	}
	d.refreshSidebar()
}

func (d *desk) setModel(s *deskSession, model string) {
	s.model = model
	if !s.running() {
		return
	}
	p := s.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.SetModel(ctx, model); err != nil {
			d.do(func() { d.notify(s, "Could not switch model: "+err.Error()) })
		}
	}()
}

func (d *desk) setMode(s *deskSession, mode string) {
	s.mode = mode
	if !s.running() {
		return
	}
	p := s.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.SetMode(ctx, mode); err != nil {
			d.do(func() { d.notify(s, "Could not switch permission mode: "+err.Error()) })
		}
	}()
}

func (d *desk) notify(s *deskSession, msg string) {
	it := s.conv.AddNotice(msg)
	if d.current == s {
		d.chat.update([]*chat.Item{it})
	}
}

func (d *desk) setRemoteControl(s *deskSession, on bool) {
	if on {
		if err := d.ensureProcess(s); err != nil {
			d.notify(s, "Could not start claude: "+err.Error())
			d.updateChrome()
			return
		}
	} else if !s.running() {
		s.rcOn = false
		d.updateChrome()
		return
	}
	p := s.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := p.RemoteControl(ctx, on)
		d.do(func() {
			if err != nil {
				s.rcOn = false
				d.notify(s, "Remote Control failed: "+err.Error())
			} else {
				s.rcOn = on
				s.rcURL = info.SessionURL
				if on {
					d.notify(s, "Remote Control on: "+info.SessionURL)
				} else {
					d.notify(s, "Remote Control off")
				}
			}
			if d.current == s {
				d.updateChrome()
			}
		})
	}()
}

func (d *desk) runningSessions() []*deskSession {
	var out []*deskSession
	for _, s := range d.sessions {
		if s.running() {
			out = append(out, s)
		}
	}
	return out
}

func (d *desk) refresh() {
	if !d.refreshMu.TryLock() {
		return
	}
	defer d.refreshMu.Unlock()
	infos, err := d.scanner.Scan()
	own := map[int]bool{}
	d.ownPIDs.Range(func(k, _ any) bool {
		own[k.(int)] = true
		return true
	})
	terms := d.finder.Running(own)
	d.doWait(func() {
		if err == nil {
			d.order = infos
			d.infos = make(map[string]transcript.Info, len(infos))
			for _, i := range infos {
				d.infos[i.ID] = i
			}
		}
		d.terms = terms
		for id, s := range d.sessions {
			if p, ok := terms[id]; ok && !s.running() {
				pp := p
				s.terminal = &pp
			} else {
				s.terminal = nil
			}
		}
		d.rebuildRows()
		if s := d.current; s != nil && !s.running() {
			if info, ok := d.infos[s.id]; ok && s.loaded && !info.ModTime.Equal(s.loadedMod) {
				d.open(s.id)
				return
			}
		}
		d.updateChrome()
	})
}

func (d *desk) watch(ctx context.Context) {
	d.refresh()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refresh()
		}
	}
}

func (d *desk) confirmClose() {
	running := d.runningSessions()
	if len(running) == 0 {
		d.win.Close()
		return
	}
	msg := fmt.Sprintf("%d session(s) are still running in this app. Closing stops them (their conversations are saved and can be resumed).", len(running))
	dialog.ShowCustomConfirm("Stop running sessions?", "Stop and quit", "Cancel", widget.NewLabel(msg), func(ok bool) {
		if !ok {
			return
		}
		prog := dialog.NewCustomWithoutButtons("Stopping sessions...", widget.NewProgressBarInfinite(), d.win)
		prog.Show()
		go func() {
			d.stopAll(running)
			d.do(func() {
				prog.Hide()
				d.win.Close()
			})
		}()
	}, d.win)
}

func (d *desk) stopAll(list []*deskSession) {
	var wg sync.WaitGroup
	for _, s := range list {
		p := s.proc
		d.doWait(func() { s.stopping = true })
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.Stop(5 * time.Second)
		}()
	}
	wg.Wait()
}
