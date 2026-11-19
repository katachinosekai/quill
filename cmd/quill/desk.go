package main

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"os"
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

	"github.com/katachinosekai/quill/internal/account"
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
	takeover  bool
	loading   bool
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
	key    string
	depth  int
	count  int
	open   bool
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

	rows          []sideRow
	list          *widget.List
	search        *widget.Entry
	chat          *chatView
	title         *widget.Label
	subtitle      *canvas.Text
	note          *widget.Label
	rcStatus      *canvas.Text
	usage         *usagePanel
	rate          *protocol.RateLimit
	rateAt        time.Time
	rcLink        *widget.Hyperlink
	input         *sendEntry
	modelSel      *widget.Select
	modeSel       *widget.Select
	folder        *canvas.Text
	stats         *canvas.Text
	stopBtn       *widget.Button
	sendBtn       *widget.Button
	acctBtn       *widget.Button
	acct          account.Status
	acctErr       string
	signingIn     context.CancelFunc
	swapTo        string
	swapping      bool
	restore       *restoreState
	cmdList       []protocol.Command
	pending       []*chat.Item
	pendingSeen   map[*chat.Item]bool
	pendingChrome bool
	pendingFor    *deskSession
	act           *activity
	lastTyped     time.Time
	autoRC        bool
	flushTimer    *time.Timer
	cmds          *cmdMenu
	sidebar       fyne.CanvasObject
	background    bool
	hidden        bool
	sideFrac      float32
	sideAnim      *fyne.Animation
	animate       bool
	root          *fyne.Container
	sideToggle    *tapRow
	headPad       *canvas.Rectangle
	chatFreeze    float32
	sideOff       bool
	sideBody      *fyne.Container
	folderOpen    map[string]bool
	created       map[string]bool
	suppress      bool
	refreshMu     sync.Mutex
	headerBox     *fyne.Container
	mainPane      *fyne.Container
	do            func(func())
	doWait        func(func())
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
		autoRC:   a.Preferences().BoolWithFallback("autoRemoteControl", true),
		created:  map[string]bool{},
		do:       fyne.Do,
		doWait:   fyne.DoAndWait,
	}
	for _, id := range a.Preferences().StringList("createdSessions") {
		d.created[id] = true
	}
	return d
}

func (d *desk) isAuto(id string) bool {
	info, ok := d.infos[id]
	return ok && strings.HasPrefix(info.Entrypoint, "sdk") && !d.created[id]
}

func (d *desk) buildSidebar() fyne.CanvasObject {
	newBtn := widget.NewButtonWithIcon("New session", theme.ContentAddIcon(), d.newSessionDialog)
	newBtn.Importance = widget.HighImportance
	d.search = widget.NewEntry()
	d.search.SetPlaceHolder("Search sessions")
	d.search.OnChanged = func(string) { d.rebuildRows() }
	d.list = d.newSessionList(&d.rows)
	centerChk := widget.NewCheck("Center message text", func(on bool) {
		d.center = on
		d.app.Preferences().SetBool("centerText", on)
		if d.current != nil {
			d.chat.show(d.current)
		}
	})
	centerChk.Checked = d.center
	rcChk := widget.NewCheck("Remote Control for every session", func(on bool) {
		d.autoRC = on
		d.app.Preferences().SetBool("autoRemoteControl", on)
		if on {
			for _, s := range d.runningSessions() {
				if !s.rcOn {
					d.autoRemote(s)
				}
			}
		}
	})
	rcChk.Checked = d.autoRC
	legend := container.NewHBox(
		legendDot(stRunning), legendDot(stWaiting), legendDot(stIdle), legendDot(stError), legendDot(stTerminal),
	)
	logo := canvas.NewImageFromResource(appIcon)
	logo.FillMode = canvas.ImageFillContain
	brandName := canvas.NewText("Quill", colText)
	brandName.TextSize = 18
	brandName.TextStyle.Bold = true
	brand := container.NewHBox(sized(32, 32, logo), container.NewCenter(brandName))
	top := container.NewVBox(brand, newBtn, d.search)
	bottom := container.NewVBox(padTop(6), widget.NewSeparator(), centerChk, rcChk, legend)
	r := canvas.NewRectangle(colSidebar)
	d.sideBody = container.NewBorder(top, bottom, nil, nil, container.NewThemeOverride(d.list, compactTheme{}))
	return container.NewStack(r, container.NewPadded(d.sideBody))
}

func (d *desk) newSessionList(rows *[]sideRow) *widget.List {
	list := widget.NewList(
		func() int { return len(*rows) },
		func() fyne.CanvasObject {
			indent := canvas.NewRectangle(color.Transparent)
			icon := widget.NewIcon(theme.FolderIcon())
			name := canvas.NewText("", colMuted)
			name.TextStyle.Bold = true
			count := canvas.NewText("", colMuted)
			count.TextSize = 11
			folder := container.NewHBox(indent, container.NewCenter(sized(18, 18, icon)), container.NewCenter(name), container.NewCenter(count))
			sindent := canvas.NewRectangle(color.Transparent)
			title := widget.NewLabel("")
			title.Truncation = fyne.TextTruncateEllipsis
			meta := canvas.NewText("", colMuted)
			meta.TextSize = 10
			dt := dot(color.Transparent)
			row := container.NewBorder(nil, nil, container.NewHBox(sindent, container.NewCenter(sized(10, 10, dt))), nil,
				container.New(&tightVBox{}, title, container.NewPadded(meta)))
			return container.NewStack(folder, row)
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(*rows) {
				return
			}
			st := o.(*fyne.Container)
			folder := st.Objects[0].(*fyne.Container)
			row := st.Objects[1].(*fyne.Container)
			r := (*rows)[id]
			if r.header != "" {
				indent := folder.Objects[0].(*canvas.Rectangle)
				icon := folder.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*widget.Icon)
				name := folder.Objects[2].(*fyne.Container).Objects[0].(*canvas.Text)
				count := folder.Objects[3].(*fyne.Container).Objects[0].(*canvas.Text)
				indent.SetMinSize(fyne.NewSize(float32(r.depth)*16, 1))
				res := theme.FolderIcon()
				if r.open {
					res = theme.FolderOpenIcon()
				}
				top := r.depth == 0 && !strings.HasPrefix(r.key, "a:")
				if top {
					icon.SetResource(theme.NewPrimaryThemedResource(res))
					name.Color, name.TextSize = colText, 13
				} else {
					icon.SetResource(res)
					name.Color, name.TextSize = colMuted, 11
				}
				name.Text = r.header
				count.Text = fmt.Sprintf("%d", r.count)
				name.Refresh()
				count.Refresh()
				folder.Show()
				row.Hide()
				folder.Refresh()
				return
			}
			folder.Hide()
			row.Show()
			left := row.Objects[1].(*fyne.Container)
			left.Objects[0].(*canvas.Rectangle).SetMinSize(fyne.NewSize(float32(r.depth-1)*16, 1))
			col := row.Objects[0].(*fyne.Container)
			title := col.Objects[0].(*widget.Label)
			meta := col.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			dt := left.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Circle)
			t, m, st2 := d.rowText(r.id)
			title.SetText(t)
			meta.Text = m
			meta.Refresh()
			dt.FillColor = st2.color()
			dt.Refresh()
			row.Refresh()
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		if id >= len(*rows) || d.suppress {
			return
		}
		r := (*rows)[id]
		if r.header != "" {
			list.Unselect(id)
			d.toggleFolder(r.key)
			return
		}
		d.open(r.id)
	}
	return list
}

func legendDot(s status) fyne.CanvasObject {
	label := map[status]string{stRunning: "run", stWaiting: "wait", stIdle: "idle", stError: "err", stTerminal: "term"}[s]
	return container.NewHBox(sized(8, 8, dot(s.color())), mutedText(label, 11))
}

const tightOverlap = 2

type tightVBox struct{}

func (tightVBox) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		m := o.MinSize()
		s.Width = max(s.Width, m.Width)
		s.Height += m.Height
	}
	return fyne.NewSize(s.Width, s.Height-tightOverlap)
}

func (tightVBox) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for i, o := range objs {
		h := o.MinSize().Height
		if i > 0 {
			y -= tightOverlap
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
	var entries []sideEntry
	for _, s := range d.sessions {
		if _, ok := d.infos[s.id]; !ok {
			entries = append(entries, sideEntry{s.id, s.cwd, s.created})
		}
	}
	for _, info := range d.order {
		entries = append(entries, sideEntry{info.ID, info.Cwd, info.Last})
	}
	var active, idle, old, auto []sideEntry
	for _, e := range entries {
		if q != "" && !d.matches(e.id, e.cwd, q) {
			continue
		}
		switch {
		case d.isAuto(e.id):
			auto = append(auto, e)
		case q != "" || d.isActive(e.id, e.last):
			active = append(active, e)
		case d.isInactive(e.id, e.last):
			old = append(old, e)
		default:
			idle = append(idle, e)
		}
	}
	rows := d.projectRows(active, 0, "a:", q != "")
	for _, g := range []struct {
		key, title string
		es         []sideEntry
	}{{"inactive", "Inactive", idle}, {"archive", "Archive", old}, {"auto", "Created by Claude", auto}} {
		if len(g.es) == 0 {
			continue
		}
		open := q != "" || d.isOpen(g.key)
		rows = append(rows, sideRow{header: g.title, key: g.key, count: len(g.es), open: open})
		if open {
			rows = append(rows, d.projectRows(g.es, 1, g.key+":", q != "")...)
		}
	}
	d.rows = rows
	d.suppress = true
	d.list.UnselectAll()
	for i, r := range rows {
		switch {
		case r.header != "" && r.depth == 0 && !strings.HasPrefix(r.key, "a:"):
			d.list.SetItemHeight(i, 34)
		case r.header != "":
			d.list.SetItemHeight(i, 26)
		default:
			d.list.SetItemHeight(i, 40)
		}
		if d.current != nil && r.id == d.current.id {
			d.list.Select(i)
		}
	}
	d.list.Refresh()
	d.suppress = false
}

func (d *desk) projectRows(es []sideEntry, depth int, prefix string, forceOpen bool) []sideRow {
	var rows []sideRow
	for _, g := range groupByFolder(es) {
		key := prefix + g.cwd
		open := forceOpen || d.isOpen(key)
		rows = append(rows, sideRow{header: tildePath(g.cwd), key: key, depth: depth, count: len(g.items), open: open})
		if open {
			for _, e := range g.items {
				rows = append(rows, sideRow{id: e.id, depth: depth + 1})
			}
		}
	}
	return rows
}

func (d *desk) isOpen(key string) bool {
	if v, ok := d.folderOpen[key]; ok {
		return v
	}
	return key != "archive" && key != "auto"
}

func (d *desk) toggleFolder(key string) {
	if d.folderOpen == nil {
		d.folderOpen = map[string]bool{}
	}
	d.folderOpen[key] = !d.isOpen(key)
	d.rebuildRows()
}

func (d *desk) rowShown(id string) bool {
	for _, r := range d.rows {
		if r.id == id {
			return true
		}
	}
	return false
}

func (d *desk) matches(id, cwd, q string) bool {
	t, _, _ := d.rowText(id)
	return strings.Contains(strings.ToLower(t), q) || strings.Contains(strings.ToLower(cwd), q)
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
	d.rcStatus = mutedText("", 12)
	d.rcStatus.Hide()
	d.acctBtn = widget.NewButtonWithIcon("Account", theme.AccountIcon(), d.accountMenu)
	d.headPad = canvas.NewRectangle(color.Transparent)
	header := container.NewBorder(nil, nil, d.headPad, container.NewHBox(container.NewCenter(d.rcStatus), d.rcLink, container.NewCenter(d.acctBtn)),
		container.NewVBox(d.title, container.NewPadded(d.subtitle)))
	headerBox := container.NewVBox(container.NewPadded(header), d.note, widget.NewSeparator())
	d.headerBox = headerBox

	d.input = newSendEntry(d.send)
	d.cmds = &cmdMenu{d: d}
	d.input.onKey = d.cmds.key
	d.input.menuOpen = d.cmds.visible
	d.input.OnChanged = func(t string) {
		d.lastTyped = time.Now()
		d.cmds.update(t)
	}
	d.input.onSendNow = d.sendNow
	d.input.hint = "Message Claude"
	d.input.SetPlaceHolder(d.input.hint)
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
	d.folder = mutedText("⏎ send · Ctrl+⏎ stop & send · Shift+⏎ new line · / commands", 10)
	d.stats = mutedText("", 12)
	d.stopBtn = widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		if d.current != nil {
			d.interrupt(d.current)
		}
	})
	d.stopBtn.Importance = widget.DangerImportance
	d.sendBtn = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.send(d.input.Text) })
	d.sendBtn.Importance = widget.HighImportance
	controls := container.NewHBox(d.modelSel, d.modeSel, layout.NewSpacer(), d.stopBtn)
	inputCard := bg(colInput, 12, container.NewBorder(nil, controls, nil, container.NewVBox(layout.NewSpacer(), d.sendBtn), d.input))
	statsRow := container.NewGridWithColumns(2, container.New(&clipLayout{}, d.stats), container.New(fitRight{}, d.folder))
	d.act = newActivity(d)
	bottom := container.NewPadded(windowCentered(d.chatWidth, d.sidebarWidth, container.NewVBox(inputCard, d.act.root, statsRow)))
	d.usage = newUsagePanel(d)
	d.mainPane = container.NewBorder(headerBox, bottom, nil, nil, container.New(usageLayout{d}, d.chat.root, d.usage.root))
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
		d.rcStatus.Hide()
		d.rcLink.Hide()
		d.input.setSuggestion("")
		d.input.Disable()
		d.sendBtn.Disable()
		d.stopBtn.Disable()
		d.modelSel.Disable()
		d.modeSel.Disable()
		d.stats.Text = ""
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
	held := s.terminal != nil && !s.running()
	if s.takeover {
		d.note.SetText("Taking this session over from the terminal...")
		d.note.Show()
		d.input.Disable()
		d.sendBtn.Disable()
		d.rcStatus.Hide()
		d.rcLink.Hide()
	} else if held {
		where := "a terminal"
		if s.terminal.Background() {
			where = "a background Claude process"
		}
		d.note.SetText(fmt.Sprintf("This session is open in %s (pid %d). Sending a message here takes it over and closes it there.", where, s.terminal.PID))
		d.note.Show()
		d.input.Enable()
		d.sendBtn.Enable()
		d.modelSel.Enable()
		d.modeSel.Enable()
		d.rcStatus.Hide()
		d.rcLink.Hide()
	} else {
		d.note.Hide()
		d.input.Enable()
		d.sendBtn.Enable()
		d.modelSel.Enable()
		d.modeSel.Enable()
		d.rcStatus.Text, d.rcStatus.Color = "Remote Control off", colMuted
		if s.rcOn {
			d.rcStatus.Text, d.rcStatus.Color = "● Remote Control on", colIdle
		} else if s.running() && d.autoRC {
			d.rcStatus.Text = "Remote Control connecting..."
		}
		d.rcStatus.Show()
		d.rcStatus.Refresh()
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
	if s.loading {
		d.sendBtn.Disable()
	}
	if held || s.takeover {
		d.input.setSuggestion("")
	} else {
		d.input.setSuggestion(s.conv.Suggestion)
	}
	d.modelSel.SetSelected(s.model)
	d.modeSel.SetSelected(s.mode)
	if st == stRunning || st == stWaiting {
		d.stopBtn.Enable()
	} else {
		d.stopBtn.Disable()
	}
	d.stats.Text = statsLine(s)
	d.stats.Refresh()
	if d.usage != nil {
		d.usage.update()
	}
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
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1e9)
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
	if d.cmds != nil {
		d.cmds.hide()
	}
	d.flushUI()
	s := d.session(id)
	if s == nil {
		return
	}
	d.current = s
	if !d.rowShown(id) {
		d.rebuildRows()
	}
	info, has := d.infos[id]
	needLoad := has && !s.running() && (!s.loaded || !info.ModTime.Equal(s.loadedMod))
	if needLoad {
		path, mod := info.Path, info.ModTime
		s.loading = true
		d.chat.show(nil)
		d.updateChrome()
		go func() {
			conv, err := transcript.Load(path)
			d.do(func() {
				s.loading = false
				if s.running() || s.takeover {
					d.updateChrome()
					return
				}
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
	prefs := d.app.Preferences()
	model := widget.NewSelect(modelChoices, nil)
	model.SetSelected(prefs.StringWithFallback("newModel", "default"))
	mode := widget.NewSelect(modeChoices, nil)
	mode.SetSelected(prefs.StringWithFallback("newMode", "auto"))
	opts := widget.NewAccordion(widget.NewAccordionItem("Options", widget.NewForm(
		widget.NewFormItem("Model", model),
		widget.NewFormItem("Permissions", mode),
	)))
	form := container.NewVBox(container.NewBorder(nil, nil, nil, browse, dir), opts)
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
		prefs.SetString("newModel", model.Selected)
		prefs.SetString("newMode", mode.Selected)
		d.createSession(path, model.Selected, mode.Selected)
	}, d.win)
	dlg.Resize(fyne.NewSize(560, 180))
	dlg.Show()
}

func (d *desk) createSession(cwd, model, mode string) *deskSession {
	id := session.NewUUID()
	d.created[id] = true
	ids := append(d.app.Preferences().StringList("createdSessions"), id)
	if len(ids) > 2000 {
		ids = ids[len(ids)-2000:]
	}
	d.app.Preferences().SetStringList("createdSessions", ids)
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
	d.autoRemote(s)
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
		d.queueUI(s, items)
	} else {
		d.chat.forget(s)
	}
	if ev.Type == "rate_limit_event" {
		d.setRateLimit(ev.RateLimit)
	}
	urgent := ev.Type == "result" || ev.Type == "control_request" || ev.Type == "prompt_suggestion" || ev.Type == "system" && ev.Subtype == "init"
	if urgent {
		d.flushUI()
		d.refreshSidebar()
	}
	if ev.Type == "result" {
		d.trySwap()
	}
}

const uiFrame = 50 * time.Millisecond

func (d *desk) queueUI(s *deskSession, items []*chat.Item) {
	if d.pendingFor != s {
		d.flushUI()
		d.pendingFor = s
	}
	if d.pendingSeen == nil {
		d.pendingSeen = map[*chat.Item]bool{}
	}
	for _, it := range items {
		if !d.pendingSeen[it] {
			d.pendingSeen[it] = true
			d.pending = append(d.pending, it)
		}
	}
	d.pendingChrome = true
	if d.flushTimer == nil {
		d.flushTimer = time.AfterFunc(uiFrame, func() { d.do(d.flushUI) })
	}
}

func (d *desk) flushUI() {
	if d.flushTimer != nil {
		d.flushTimer.Stop()
		d.flushTimer = nil
	}
	items, owner := d.pending, d.pendingFor
	d.pending, d.pendingSeen, d.pendingFor = nil, nil, nil
	if owner != nil && d.chat.s == owner {
		d.chat.update(items)
	}
	if owner != nil && d.act.v.s == owner {
		d.act.update(items)
	} else if owner != nil {
		d.chat.forget(owner)
	}
	if d.pendingChrome {
		d.pendingChrome = false
		d.updateChrome()
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
	d.trySwap()
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (d *desk) send(text string) {
	d.cmds.hide()
	s := d.current
	text = strings.TrimSpace(text)
	if s == nil || text == "" {
		return
	}
	if s.takeover || s.loading {
		return
	}
	if s.terminal != nil && !s.running() {
		t := *s.terminal
		if t.Busy() {
			dialog.ShowConfirm("Take over?", "Claude is in the middle of a turn in the terminal. Stop it there and continue here?", func(ok bool) {
				if ok {
					d.takeOver(s, t, text)
				}
			}, d.win)
			return
		}
		d.takeOver(s, t, text)
		return
	}
	d.sendTo(s, text)
}

func (d *desk) takeOver(s *deskSession, t live.Process, text string) {
	s.takeover = true
	d.input.SetText("")
	d.updateChrome()
	path := ""
	if info, ok := d.infos[s.id]; ok {
		path = info.Path
	}
	go func() {
		err := d.finder.Release(t, d.cfg.ClaudeBin, 5*time.Second)
		var conv *chat.Conversation
		var mod time.Time
		if err == nil && path != "" {
			if st, serr := os.Stat(path); serr == nil {
				mod = st.ModTime()
			}
			conv, _ = transcript.Load(path)
		}
		d.do(func() {
			s.takeover = false
			if err != nil {
				d.chatFor(s, []*chat.Item{s.conv.AddError("Could not take the session over: " + err.Error())})
				if d.current == s && d.input.Text == "" {
					d.input.SetText(text)
				}
				d.updateChrome()
				return
			}
			s.terminal = nil
			delete(d.terms, s.id)
			if conv != nil {
				s.conv = conv
				s.loaded = true
				s.loadedMod = mod
				if d.current == s {
					d.chat.show(s)
				}
			}
			d.sendTo(s, text)
		})
	}()
}

func (d *desk) sendTo(s *deskSession, text string) {
	if err := d.ensureProcess(s); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not start claude: " + err.Error())})
		d.updateChrome()
		return
	}
	it := s.conv.AddUser(text)
	if s.title == "" {
		s.title = oneLine(text, 60)
	}
	if err := s.proc.Send(text); err != nil {
		d.chatFor(s, []*chat.Item{it, s.conv.AddError("Send failed: " + err.Error())})
		return
	}
	s.conv.Busy = true
	if d.current == s && strings.TrimSpace(d.input.Text) == text {
		d.input.SetText("")
	}
	d.chatFor(s, []*chat.Item{it})
	if d.current == s {
		d.chat.scrollBottom()
	}
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

func (d *desk) chatFor(s *deskSession, items []*chat.Item) {
	if d.current == s {
		d.chat.update(items)
	}
}

func (d *desk) sendNow(text string) {
	s := d.current
	if s == nil || strings.TrimSpace(text) == "" {
		return
	}
	p := s.proc
	if p == nil || p.Exited() || !(s.conv.Busy || p.Busy()) {
		d.send(text)
		return
	}
	d.input.SetText("")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Interrupt(ctx)
		d.do(func() {
			prev := d.current
			d.current = s
			d.send(text)
			if prev != s {
				d.current = prev
				d.updateChrome()
			}
		})
	}()
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

func (d *desk) autoRemote(s *deskSession) {
	if !d.autoRC || s.proc == nil {
		return
	}
	p := s.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		info, err := p.RemoteControl(ctx, true)
		d.do(func() {
			if err != nil || s.proc != p {
				return
			}
			s.rcOn, s.rcURL = true, info.SessionURL
			if d.current == s {
				d.updateChrome()
			}
		})
	}()
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
		d.trySwap()
	})
}

func (d *desk) watch(ctx context.Context) {
	d.refresh()
	if st := d.restore; st != nil {
		d.restore = nil
		d.do(func() { d.applyRestore(st) })
	}
	go d.watchBinary(ctx)
	go d.fetchCommands()
	d.refreshAccount()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refresh()
			if n++; n%12 == 0 {
				d.refreshAccount()
			}
		}
	}
}

func (d *desk) confirmClose() {
	if d.background {
		d.hideWindow()
		return
	}
	d.confirmQuit()
}

func (d *desk) confirmQuit() {
	running := d.runningSessions()
	if len(running) == 0 {
		d.app.Quit()
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
				d.app.Quit()
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

func (d *desk) refreshAccount() {
	st, err := account.Get(d.cfg.ClaudeBin)
	d.do(func() {
		d.acct = st
		d.acctErr = ""
		if err != nil {
			d.acctErr = err.Error()
		}
		d.updateAccount()
	})
}

func (d *desk) updateAccount() {
	if d.acctBtn == nil {
		return
	}
	switch {
	case d.signingIn != nil:
		d.acctBtn.SetText("Signing in...")
		d.acctBtn.Importance = widget.MediumImportance
	case d.acct.LoggedIn:
		label := d.acct.Email
		if label == "" {
			label = "Signed in"
		}
		if d.acct.Subscription != "" {
			label += "  ·  " + strings.ToUpper(d.acct.Subscription[:1]) + d.acct.Subscription[1:]
		}
		d.acctBtn.SetText(label)
		d.acctBtn.Importance = widget.LowImportance
	default:
		d.acctBtn.SetText("Sign in")
		d.acctBtn.Importance = widget.HighImportance
	}
	d.acctBtn.Refresh()
}

func (d *desk) accountMenu() {
	if d.signingIn != nil {
		d.showMenu(fyne.NewMenuItem("Cancel sign-in", func() { d.signingIn() }))
		return
	}
	if !d.acct.LoggedIn {
		d.signIn()
		return
	}
	info := d.acct.Email
	if d.acct.OrgName != "" {
		info += " (" + d.acct.OrgName + ")"
	}
	head := fyne.NewMenuItem(info, nil)
	head.Disabled = true
	d.showMenu(head,
		fyne.NewMenuItem("Refresh status", func() { go d.refreshAccount() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Sign out", func() {
			dialog.ShowConfirm("Sign out?", "Sign out of Claude Code on this computer? Terminal sessions use the same login.", func(ok bool) {
				if !ok {
					return
				}
				go func() {
					err := account.Logout(d.cfg.ClaudeBin)
					if err != nil {
						d.do(func() { dialog.ShowError(err, d.win) })
					}
					d.refreshAccount()
				}()
			}, d.win)
		}))
}

func (d *desk) showMenu(items ...*fyne.MenuItem) {
	c := d.win.Canvas()
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d.acctBtn).Add(fyne.NewPos(0, d.acctBtn.Size().Height))
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), c, pos)
}

func (d *desk) signIn() {
	ctx, cancel := context.WithCancel(context.Background())
	d.signingIn = cancel
	d.updateAccount()
	go func() {
		err := account.Login(ctx, d.cfg.ClaudeBin, func(u string) {
			d.do(func() {
				pu, perr := url.Parse(u)
				if perr != nil {
					return
				}
				link := widget.NewHyperlink("Open the sign-in page", pu)
				dialog.ShowCustom("Sign in", "Close", container.NewVBox(
					widget.NewLabel("Finish signing in in your browser. If it did not open:"), link), d.win)
			})
		})
		cancel()
		d.do(func() {
			d.signingIn = nil
			if err != nil && ctx.Err() == nil {
				dialog.ShowError(fmt.Errorf("sign-in failed: %w", err), d.win)
			}
		})
		d.refreshAccount()
	}()
}
