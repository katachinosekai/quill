package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/katachinosekai/quill/internal/updater"
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
	stRestart
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
	case stRestart:
		return colRunning
	}
	return color.Transparent
}

func (s status) String() string {
	return [...]string{"", "running", "waiting for you", "idle", "error", "open in a terminal", "restarting"}[s]
}

type deskSession struct {
	id             string
	cwd            string
	title          string
	conv           *chat.Conversation
	loaded         bool
	loadedMod      time.Time
	persisted      bool
	proc           *session.Process
	model          string
	mode           string
	failed         string
	stopping       bool
	clearAfterTurn bool
	terminal       *live.Process
	planWait       bool
	runAll         bool
	runAllNext     bool
	queue          []string
	lastSent       string
	inTurn         bool
	restoreMode    string
	retryCmd       string
	retryRules     []protocol.Rule
	retryDest      string
	kmemSave       *kmemSave
	kmemNew        bool
	rotateAsk      int
	rotateTried    int
	lastClaude     string
	compactFrom    string
	chrome         string
	addDirs        []string
	resumeAt       string
	forkOf         string
	qedit          *queueEdit
	coldStamp      time.Time
	rotateNo       bool
	limitSeen      string
	compacting     bool
	restarting     bool
	takeover       bool
	loading        bool
	rcURL          string
	rcOn           bool
	rcName         string
	created        time.Time
}

func (s *deskSession) running() bool {
	return s.proc != nil && !s.proc.Exited()
}

func (s *deskSession) status() status {
	if s.terminal != nil && !s.running() {
		return stTerminal
	}
	restarting := s.restarting || s.compacting || s.kmemSave != nil
	if !s.running() {
		if restarting {
			return stRestart
		}
		if s.failed != "" {
			return stError
		}
		return stNone
	}
	if len(s.conv.PendingPermissions()) > 0 {
		return stWaiting
	}
	if restarting {
		return stRestart
	}
	busy := s.conv.Busy || s.proc.Busy()
	if !busy && s.conv.AwaitsReply() {
		return stWaiting
	}
	if busy || s.conv.Working() > 0 {
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
	parent string
	key    string
	depth  int
	count  int
	open   bool
}

type desk struct {
	nameClose    func()
	limitMsg     string
	notifier     *notifier
	notifySink   func(sid, title, body string)
	foreground   bool
	compactor    func(ctx context.Context, model, text string) (string, error)
	kmemRun      func(prog string, args ...string) (string, error)
	kmemOff      bool
	policy       handoffPolicy
	policyAt     time.Time
	policyBusy   bool
	localModels  []string
	localBusy    bool
	fetchLocal   func(host string) []localInfo
	after        func(time.Duration, func())
	toolbarRow   *fyne.Container
	chipBtns     [2]*widget.Button
	toolbarBox   *fyne.Container
	usagePaused  bool
	pauseUntil   time.Time
	pausedIDs    []string
	pauseBanner  *fyne.Container
	pauseText    *widget.Label
	controlFn    func(s *deskSession, req map[string]any) (json.RawMessage, error)
	pingLocal    func(host string) bool
	localWindows map[string]int
	warnedOllama bool
	backendBtn   *widget.Button
	app          fyne.App
	win          fyne.Window
	cfg          config
	scanner      *transcript.Scanner
	finder       *live.Finder
	center       bool
	sessions     map[string]*deskSession
	infos        map[string]transcript.Info
	order        []transcript.Info
	terms        map[string]live.Process
	current      *deskSession
	ownPIDs      sync.Map

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
	glowBoxes     map[*canvas.Rectangle]glow
	pulsing       atomic.Int32
	keeper        *keeperClient
	single        *chatView
	panes         []*pane
	focused       *pane
	chatHost      *fyne.Container
	chatDropHL    *canvas.Rectangle
	viewBtn       *widget.Button
	groups        []bookGroup
	tabs          []string
	tabBar        *fyne.Container
	bookBar       *fyne.Container
	tabSig        string
	tabChips      map[string]*tabChip
	strip         *tabStrip
	autoChk       *widget.Check
	autoSyncing   bool
	preAuto       string
	listTheme     *container.ThemeOverride
	usageBox      *fyne.Container
	resizeFreeze  float32
	resizeTimer   *time.Timer
	lastRootSize  fyne.Size
	tabDrag       string
	tabDragX      float32
	tabCenter     *fyne.Container
	bookCenter    *fyne.Container
	bookSig       string
	bookRow       fyne.CanvasObject
	tabRow        fyne.CanvasObject
	settingsBox   *fyne.Container
	setGeneral    *fyne.Container
	setSessions   *fyne.Container
	settingsDlg   dialog.Dialog
	tasksBtn      *tasksChip
	phoneSync     *phoneSync
	phoneFileBtn  *widget.Button
	phoneFileOff  *widget.Button
	settingsSlot  *fyne.Container
	settingsBtn   *widget.Button
	layoutBtn     *widget.Button
	viewMenu      *widget.PopUpMenu
	viewItems     []*fyne.MenuItem
	sideDirty     bool
	shares        map[string]float32
	bottomBox     *fyne.Container
	keptView      string
	keptHidden    bool
	lightboxClose func()
	swapTo        string
	swapping      bool
	restore       *restoreState
	cmdList       []protocol.Command
	pending       []*chat.Item
	pendingSeen   map[*chat.Item]bool
	pendingChrome bool
	pendingFor    *deskSession
	lastTyped     time.Time
	stt           *speech
	autoRC        bool
	groupProjects bool
	planFirst     bool
	collapseChips bool
	hideChips     bool
	updBtn        *widget.Button
	sizesCard     fyne.CanvasObject
	usageRow      fyne.CanvasObject
	updRelease    *updater.Release
	rules         sortRules
	centerAll     *widget.Check
	centerChecks  []*widget.Check
	suppressAlign bool
	forceSwap     bool
	flushTimer    *time.Timer
	cmds          *cmdMenu
	sidebar       fyne.CanvasObject
	background    bool
	hidden        bool
	sideFrac      float32
	ac            *autocorrector
	acKnown       map[string]*wordSet
	hintPrev      map[*canvas.Text]string
	sideAnim      *fyne.Animation
	animate       bool
	root          *fyne.Container
	sideToggle    *tapRow
	headPad       *canvas.Rectangle
	chatFreeze    float32
	sideOff       bool
	sideBody      *fyne.Container
	folderOpen    map[string]bool
	sorted        sortState
	sortDirty     bool
	org           sideOrg
	rowPlace      map[string]string
	sideItems     []*sideItem
	dragID        string
	dragAt        fyne.Position
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
		localWindows:  map[string]int{},
		app:           a,
		win:           w,
		cfg:           cfg,
		scanner:       transcript.NewScanner(cfg.ProjectsRoot),
		finder:        &live.Finder{Dir: cfg.SessionsDir, ProcDir: "/proc"},
		sessions:      map[string]*deskSession{},
		infos:         map[string]transcript.Info{},
		terms:         map[string]live.Process{},
		center:        a.Preferences().BoolWithFallback("centerText", true),
		autoRC:        a.Preferences().BoolWithFallback("autoRemoteControl", true),
		groupProjects: a.Preferences().Bool("groupByProject"),
		planFirst:     a.Preferences().BoolWithFallback("planFirst", true),
		collapseChips: a.Preferences().Bool("collapseChips"),
		hideChips:     a.Preferences().BoolWithFallback("hideChips", true),
		created:       map[string]bool{},
		do:            fyne.Do,
		doWait:        fyne.DoAndWait,
	}
	d.loadOrg()
	d.loadRules()
	d.loadSortState()
	for _, id := range a.Preferences().StringList("createdSessions") {
		d.created[id] = true
	}
	d.initPhone()
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
	prefs := d.app.Preferences()
	centerChk := func(label string, on bool, apply func(bool)) *widget.Check {
		c := widget.NewCheck(label, nil)
		c.Checked = on
		c.OnChanged = func(v bool) {
			apply(v)
			d.syncCenterAll()
		}
		return c
	}
	d.centerChecks = []*widget.Check{
		centerChk("Chat text", d.center, d.setCenter),
		centerChk("Typing", prefs.Bool("centerInput"), d.setInputCenter),
		centerChk("Status", prefs.Bool("centerStatus"), d.setStatusCenter),
		centerChk("Tabs", prefs.Bool("centerTabs"), d.setTabCenter),
	}
	d.centerAll = widget.NewCheck("Center everything", func(on bool) {
		if d.suppressAlign {
			return
		}
		for _, c := range d.centerChecks {
			c.SetChecked(on)
		}
	})
	d.syncCenterAll()
	indent := func(o fyne.CanvasObject) fyne.CanvasObject {
		pad := canvas.NewRectangle(color.Transparent)
		pad.SetMinSize(fyne.NewSize(16, 1))
		return container.NewHBox(pad, o)
	}
	alignRow := container.NewVBox(d.centerAll, container.NewGridWithColumns(2, indent(d.centerChecks[0]), d.centerChecks[1], indent(d.centerChecks[2]), d.centerChecks[3]))
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
	rcLocalChk := widget.NewCheck("Remote Control for local sessions (relayed through claude.ai)", func(on bool) {
		d.app.Preferences().SetBool("rcLocal", on)
		for _, s := range d.runningSessions() {
			if !isLocal(s.model) {
				continue
			}
			if !on && s.rcOn {
				d.setRemoteControl(s, false)
			} else if on && !s.rcOn {
				d.autoRemote(s)
			}
		}
	})
	rcLocalChk.Checked = d.rcForLocal()
	chromeChk := widget.NewCheck("Claude in Chrome for new sessions", func(on bool) { d.app.Preferences().SetBool("chromeNew", on) })
	chromeChk.Checked = d.app.Preferences().Bool("chromeNew")
	planChk := widget.NewCheck("Plan first, then approve all", func(on bool) {
		d.planFirst = on
		d.app.Preferences().SetBool("planFirst", on)
	})
	planChk.Checked = d.planFirst
	chipChk := widget.NewCheck("New tool chips start collapsed", d.setCollapseChips)
	chipChk.Checked = d.collapseChips
	rulesBtn := widget.NewButtonWithIcon("Format Rules...", theme.SettingsIcon(), d.rulesDialog)
	rulesBtn.Importance = widget.LowImportance
	rulesBtn.Alignment = widget.ButtonAlignLeading
	roBtn := widget.NewButtonWithIcon("Read-only commands...", theme.SettingsIcon(), d.readOnlyDialog)
	roBtn.Importance = widget.LowImportance
	roBtn.Alignment = widget.ButtonAlignLeading
	legend := container.NewHBox(
		legendDot(stRunning), legendDot(stWaiting), legendDot(stIdle), legendDot(stError), legendDot(stTerminal),
	)
	top := container.NewVBox(padTop(40), newBtn, d.search)
	updChk := widget.NewCheck("Check for updates (signed releases only)", func(on bool) { d.app.Preferences().SetBool("checkUpdates", on) })
	updChk.Checked = d.app.Preferences().BoolWithFallback("checkUpdates", true)
	notifyChk := widget.NewCheck("Notify me about approvals and finished sessions", func(on bool) { d.app.Preferences().SetBool("desktopNotify", on) })
	notifyChk.Checked = d.notifyOn()
	heading := func(t string) fyne.CanvasObject {
		l := widget.NewLabel(t)
		l.TextStyle = fyne.TextStyle{Bold: true}
		return l
	}
	_ = roBtn
	d.sizesCard = d.sizePanel()
	sizes := container.NewHBox(container.NewGridWrap(fyne.NewSize(360, d.sizesCard.MinSize().Height), d.sizesCard))
	d.setGeneral = container.NewVBox(planChk, chipChk, updChk, notifyChk, widget.NewSeparator(), heading("Alignment"), alignRow, widget.NewSeparator(), heading("Sizes"), sizes, widget.NewSeparator(), heading("Typing"), d.autocorrectSettings())
	d.setSessions = container.NewVBox(rcChk, rcLocalChk, chromeChk, widget.NewSeparator(), d.pauseSettings(), widget.NewSeparator(), rulesBtn, widget.NewSeparator(), heading("Phone"), d.buildPhoneRow())
	openSettings := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), d.showSettings)
	openSettings.Importance = widget.LowImportance
	openSettings.Alignment = widget.ButtonAlignLeading
	d.settingsBox = container.NewVBox(padTop(6), widget.NewSeparator(), openSettings, legend)
	d.settingsSlot = container.NewStack(d.settingsBox)
	bottom := d.settingsSlot
	r := canvas.NewRectangle(colSidebar)
	d.listTheme = container.NewThemeOverride(d.list, compactTheme{})
	d.sideBody = container.NewBorder(top, bottom, nil, nil, d.listTheme)
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
			count.TextSize = fontDense
			folder := container.NewHBox(indent, container.NewCenter(sized(18, 18, icon)), container.NewCenter(name), container.NewCenter(count))
			sindent := canvas.NewRectangle(color.Transparent)
			title := widget.NewLabel("")
			title.Truncation = fyne.TextTruncateEllipsis
			meta := canvas.NewText("", colMuted)
			meta.TextSize = fontDense
			row := container.NewBorder(nil, nil, container.NewHBox(sindent, container.NewCenter(sized(14, 14, statusMark(stNone)))), nil,
				container.New(&tightVBox{}, title, container.NewPadded(meta)))
			glow := canvas.NewRectangle(color.Transparent)
			glow.CornerRadius = 8
			glow.StrokeWidth = 1.5
			focus := canvas.NewRectangle(color.Transparent)
			focus.CornerRadius = 8
			focus.StrokeWidth = 2.5
			focus.StrokeColor = colText
			focus.Hide()
			return d.newSideItem(rows, container.NewStack(glow, folder, row, focus))
		},
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= len(*rows) {
				return
			}
			item := o.(*sideItem)
			item.idx = id
			st := item.content.(*fyne.Container)
			glow := st.Objects[0].(*canvas.Rectangle)
			folder := st.Objects[1].(*fyne.Container)
			row := st.Objects[2].(*fyne.Container)
			r := (*rows)[id]
			focus := st.Objects[3].(*canvas.Rectangle)
			if r.header == "" && d.current != nil && d.current.id == r.id {
				focus.Show()
			} else {
				focus.Hide()
			}
			if r.header != "" {
				d.setGlow(glow, nil, 0)
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
			mark := left.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*fyne.Container)
			t, m, st2 := d.rowText(r.id)
			title.SetText(t)
			meta.Text = m
			meta.Refresh()
			setStatusMark(mark, st2)
			if d.current == nil || d.current.id != r.id {
				d.setGlow(glow, st2.color(), glowStrength(st2))
			} else {
				d.setGlow(glow, nil, 0)
			}
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
	label := map[status]string{stRunning: "run", stWaiting: "wait", stIdle: "idle", stError: "err", stTerminal: "term", stRestart: "restart"}[s]
	return container.NewHBox(sized(8, 8, dot(s.color())), mutedText(label, fontMeta))
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
	if t := d.org.Titles[id]; t != "" {
		title = t
	}
	var last time.Time
	msgs := info.Messages
	if ok {
		last = info.Last
	}
	st := stNone
	if s != nil {
		st = s.status()
		if !ok {
			if d.org.Titles[id] == "" {
				title = s.title
			}
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
	if s != nil && st == stRunning && !s.conv.Busy && !s.proc.Busy() {
		meta += fmt.Sprintf("  ·  %d in background", s.conv.Working())
	}
	if k := d.org.Kmem[id]; k != "" {
		meta += "  ·  kmem " + k
	}
	if s != nil {
		if m := session.LocalModel(s.model); m != "" {
			meta += "  ·  local " + m
		}
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
	custom := map[string][]sideEntry{}
	demote := d.demoteDue()
	if q == "" {
		d.rowPlace = make(map[string]string, len(entries))
	}
	put := func(list *[]sideEntry, key string, e sideEntry) {
		*list = append(*list, e)
		if q == "" {
			d.rowPlace[e.id] = key
		}
	}
	for _, e := range entries {
		if q != "" && !d.matches(e.id, e.cwd, q) {
			continue
		}
		p := d.placement(e.id)
		switch {
		case strings.HasPrefix(p, folderPrefix):
			es := custom[p]
			put(&es, p, e)
			custom[p] = es
		case p == placeActive:
			put(&active, placeActive, e)
		case p == placeInactive:
			put(&idle, placeInactive, e)
		case p == placeArchive:
			put(&old, placeArchive, e)
		case p == placeAuto:
			put(&auto, placeAuto, e)
		case d.rules.ClaudeFolder && d.isAuto(e.id):
			put(&auto, placeAuto, e)
		case q != "":
			active = append(active, e)
		default:
			switch d.stickyPlace(e, demote) {
			case placeActive:
				put(&active, placeActive, e)
			case placeArchive:
				put(&old, placeArchive, e)
			default:
				put(&idle, placeInactive, e)
			}
		}
	}
	if q == "" {
		d.syncRCNames()
	}
	if demote && q == "" {
		d.sorted.At = time.Now().Unix()
		d.sortDirty = true
	}
	if d.sortDirty {
		d.sortDirty = false
		d.saveSortState()
	}
	fixed := q == "" && !d.rules.RecentActive
	if fixed {
		active = d.fixedActive(active)
		if d.sortDirty {
			d.sortDirty = false
			d.saveSortState()
		}
	}
	if q == "" {
		d.groups = d.groups[:0]
		addGroup := func(key, title string, es []sideEntry) {
			if len(es) == 0 {
				return
			}
			g := bookGroup{key: key, title: title}
			if !(fixed && key == placeActive) {
				es = sortRecent(es)
			}
			for _, e := range es {
				g.ids = append(g.ids, e.id)
			}
			d.groups = append(d.groups, g)
		}
		addGroup(placeActive, d.folderTitle(placeActive, "Active"), active)
		for _, f := range d.sortedFolders(custom) {
			addGroup(folderPrefix+f.ID, f.Name, custom[folderPrefix+f.ID])
		}
		addGroup("inactive", d.folderTitle("inactive", "Inactive"), idle)
		addGroup("archive", d.folderTitle("archive", "Archive"), old)
		addGroup("auto", d.folderTitle("auto", "Created by Claude"), auto)
		d.syncBookmarks()
		d.syncTabs()
		d.phoneChanged()
	}
	var rows []sideRow
	if len(active) > 0 {
		open := q != "" || d.isOpen(placeActive)
		rows = append(rows, sideRow{header: d.folderTitle(placeActive, "Active"), key: placeActive, count: len(active), open: open})
		if open {
			if fixed && !d.groupProjects {
				for _, e := range active {
					rows = append(rows, sideRow{id: e.id, depth: 1, parent: "a"})
				}
			} else {
				rows = append(rows, d.projectRows(active, 1, "a:", q != "")...)
			}
		}
	}
	for _, f := range d.sortedFolders(custom) {
		key := folderPrefix + f.ID
		es := custom[key]
		open := q != "" || d.isOpen(key)
		rows = append(rows, sideRow{header: f.Name, key: key, count: len(es), open: open})
		if open {
			for _, e := range sortRecent(es) {
				rows = append(rows, sideRow{id: e.id, depth: 1, parent: key})
			}
		}
	}
	for _, g := range []struct {
		key, title string
		es         []sideEntry
	}{{"inactive", "Inactive", idle}, {"archive", "Archive", old}, {"auto", "Created by Claude", auto}} {
		if len(g.es) == 0 {
			continue
		}
		open := q != "" || d.isOpen(g.key)
		rows = append(rows, sideRow{header: d.folderTitle(g.key, g.title), key: g.key, count: len(g.es), open: open})
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
	d.listTheme.Refresh()
	d.suppress = false
}

func (d *desk) projectRows(es []sideEntry, depth int, prefix string, forceOpen bool) []sideRow {
	var rows []sideRow
	if !d.groupProjects {
		parent := strings.TrimSuffix(prefix, ":")
		for _, e := range sortRecent(es) {
			rows = append(rows, sideRow{id: e.id, depth: depth, parent: parent})
		}
		return rows
	}
	for _, g := range groupByFolder(es) {
		key := prefix + g.cwd
		open := forceOpen || d.isOpen(key)
		rows = append(rows, sideRow{header: d.folderTitle(key, tildePath(g.cwd)), key: key, depth: depth, count: len(g.items), open: open})
		if open {
			parent := strings.TrimSuffix(prefix, ":")
			for _, e := range g.items {
				rows = append(rows, sideRow{id: e.id, depth: depth + 1, parent: parent})
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
	if b, err := json.Marshal(d.folderOpen); err == nil {
		d.app.Preferences().SetString("folderOpen", string(b))
	}
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
	d.listTheme.Refresh()
	d.syncTabs()
}

func (d *desk) refreshSidebarSoon() {
	if d.sideDirty {
		return
	}
	d.sideDirty = true
	time.AfterFunc(500*time.Millisecond, func() {
		d.do(func() {
			d.sideDirty = false
			d.refreshSidebar()
		})
	})
}

func (d *desk) buildMain() fyne.CanvasObject {
	d.title = widget.NewLabel("Quill")
	d.title.TextStyle = fyne.TextStyle{Bold: true}
	d.title.Truncation = fyne.TextTruncateEllipsis
	d.subtitle = mutedText("", fontMeta)
	d.note = widget.NewLabel("")
	d.note.Importance = widget.WarningImportance
	d.note.Truncation = fyne.TextTruncateEllipsis
	d.note.Hide()
	d.rcStatus = mutedText("", fontMeta)
	d.rcStatus.Hide()
	d.tasksBtn = newTasksButton(func() *deskSession { return d.current }, d.cmdTasks)
	d.acctBtn = widget.NewButtonWithIcon("Account", theme.AccountIcon(), d.accountMenu)
	d.headPad = canvas.NewRectangle(color.Transparent)
	header := container.NewBorder(nil, nil, container.NewHBox(d.headPad, container.NewCenter(d.layoutButtons()), padLeft(12)), container.NewHBox(container.NewCenter(d.updateButton()), container.NewCenter(d.rcStatus), container.NewCenter(d.phoneButton()), container.NewCenter(d.acctBtn)),
		layout.NewSpacer())
	d.bookRow = d.buildBookmarks()
	d.tabRow = d.buildTabs()
	d.usage = newUsagePanel(d)
	d.usageRow = d.usage.buildStrip()
	headerBox := container.NewVBox(container.NewPadded(header), d.note, d.buildPauseBanner(), d.bookRow, d.tabRow, d.usageRow, widget.NewSeparator())
	d.headerBox = headerBox

	d.input = newSendEntry(d.send)
	d.input.ac = d.newAutocorrector()
	d.input.known = func() func(string) bool { return d.knownWords(d.current) }
	d.input.onFix = func(msg string) { d.flashHint(d.folder, msg) }
	d.cmds = newCmdMenu(d)
	d.input.onKey = d.cmds.key
	d.input.menuOpen = d.cmds.visible
	d.input.OnChanged = func(t string) {
		d.lastTyped = time.Now()
		d.cmds.update(t)
	}
	d.input.onSendNow = d.sendNow
	d.input.onHistory = func(k *fyne.KeyEvent) bool { return d.historyKey(d.input, d.current, k) }
	d.input.onFocus = d.clearSelections
	d.input.onEscape = func() bool { return d.escape(d.current) }
	d.input.hint = "Message Claude"
	d.input.SetPlaceHolder(d.input.hint)
	if d.app.Preferences().Bool("centerInput") {
		d.input.Alignment = fyne.TextAlignCenter
	}
	d.modelSel = widget.NewSelect(modelChoices, func(v string) {
		if d.suppress || d.current == nil {
			return
		}
		d.app.Preferences().SetString("newModel", v)
		d.setModel(d.current, v)
	})
	d.modeSel = widget.NewSelect(modeChoices, func(v string) {
		if d.suppress || d.current == nil {
			return
		}
		d.app.Preferences().SetString("newMode", v)
		d.setMode(d.current, v)
	})
	d.backendBtn = widget.NewButton(backendOnline, func() {
		if d.current == nil {
			return
		}
		d.setBackend(d.current, otherBackend(d.current.model))
		d.updateChrome()
	})
	d.folder = mutedText("⏎ send · Ctrl+⏎ stop & send · Shift+⏎ new line · Ctrl+T new session · Ctrl+1-9 tabs · / commands", fontMeta)
	d.stats = mutedText("", fontMeta)
	d.stopBtn = widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		if d.current != nil {
			d.interrupt(d.current)
		}
	})
	d.stopBtn.Importance = widget.DangerImportance
	d.sendBtn = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.send(d.input.Text) })
	d.sendBtn.Importance = widget.HighImportance
	d.stt = newSpeech(d)
	openAll := widget.NewButtonWithIcon("Open chips", theme.MenuDropDownIcon(), func() { d.setAllChips(true) })
	openAll.Importance = widget.MediumImportance
	closeAll := widget.NewButtonWithIcon("Collapse chips", theme.MenuDropUpIcon(), func() { d.setAllChips(false) })
	closeAll.Importance = widget.MediumImportance
	d.chipBtns = [2]*widget.Button{openAll, closeAll}
	controls := container.NewBorder(nil, nil, container.NewHBox(d.backendBtn, boxed(d.modelSel), boxed(d.modeSel), d.stt.langBtn), container.NewHBox(openAll, closeAll), container.NewPadded(container.New(snugCenter{}, d.tasksBtn)))
	inputCard := bg(colInput, 12, container.NewBorder(nil, controls, nil, container.NewVBox(d.stopBtn, layout.NewSpacer(), d.stt.btn, d.sendBtn), d.input))
	statsRow := container.NewGridWithColumns(2, container.New(&clipLayout{}, d.stats), container.New(fitRight{}, d.folder))
	d.bottomBox = windowCentered(d.inputWidth, d.sidebarWidth, container.NewVBox(d.cmds.panel, inputCard, statsRow))
	toolbar := windowCentered(d.inputWidth, d.sidebarWidth, d.viewToolbar())
	d.toolbarBox = toolbar
	bottom := container.NewPadded(container.NewVBox(toolbar, d.bottomBox))
	d.chatDropHL = canvas.NewRectangle(fade(colAccent, 0.10))
	d.chatDropHL.CornerRadius = 14
	d.chatDropHL.StrokeColor = colAccent
	d.chatDropHL.StrokeWidth = 2
	d.chatDropHL.Hide()
	d.chatHost = container.NewStack(d.chat.root, d.chatDropHL)
	spare := canvas.NewRectangle(color.Transparent)
	spare.Hide()
	d.usageBox = container.New(usageLayout{d}, d.chatHost, d.usage.root, spare)
	d.mainPane = container.New(glideBorder{}, headerBox, bottom, d.usageBox)
	d.updateChrome()
	return d.mainPane
}

func (d *desk) updateChrome() {
	d.keepView()
	if d.note != nil {
		noteWas := d.note.Visible()
		defer func() {
			if d.note.Visible() != noteWas && d.mainPane != nil {
				d.headerBox.Refresh()
				relayout(d.mainPane)
			}
		}()
	}
	s := d.current
	d.suppress = true
	defer func() { d.suppress = false }()
	if s == nil {
		setLabel(d.title, "Quill")
		setText(d.subtitle, "")
		d.note.Hide()
		d.rcStatus.Hide()
		d.input.setSuggestion("")
		d.input.Disable()
		d.sendBtn.Disable()
		d.stopBtn.Disable()
		d.modelSel.Disable()
		d.modeSel.Disable()
		d.backendBtn.Disable()
		setText(d.stats, "")
		return
	}
	title, _, st := d.rowText(s.id)
	setLabel(d.title, title)
	sub := tildePath(s.cwd)
	if st != stNone {
		sub += "  ·  " + st.String()
	}
	if m := session.LocalModel(s.model); m != "" {
		sub += "  ·  Local: " + m
	}
	if d.subtitle.Text != sub {
		setText(d.subtitle, sub)
		d.headerBox.Refresh()
	}
	held := s.terminal != nil && !s.running()
	if s.takeover {
		setLabel(d.note, "Taking this session over from the terminal...")
		d.note.Show()
		d.input.Disable()
		d.sendBtn.Disable()
		d.rcStatus.Hide()
	} else if held {
		where := "a terminal"
		if s.terminal.Background() {
			where = "a background Claude process"
		}
		setLabel(d.note, fmt.Sprintf("This session is open in %s (pid %d). Sending a message here takes it over and closes it there.", where, s.terminal.PID))
		d.note.Show()
		d.input.Enable()
		d.sendBtn.Enable()
		d.modelSel.Enable()
		d.modeSel.Enable()
		d.backendBtn.Enable()
		d.rcStatus.Hide()
	} else {
		d.note.Hide()
		d.input.Enable()
		d.sendBtn.Enable()
		d.modelSel.Enable()
		d.modeSel.Enable()
		d.backendBtn.Enable()
		rcText, rcCol := "Remote Control off", color.Color(colMuted)
		if s.rcOn {
			rcText, rcCol = "● Remote Control on", colIdle
		} else if s.running() && d.autoRC {
			rcText = "Remote Control..."
		}
		if d.rcStatus.Text != rcText || d.rcStatus.Color != rcCol {
			d.rcStatus.Text, d.rcStatus.Color = rcText, rcCol
			d.rcStatus.Refresh()
			d.headerBox.Refresh()
		}
		if !d.rcStatus.Visible() {
			d.rcStatus.Show()
			d.headerBox.Refresh()
		}
	}
	if s.loading {
		d.sendBtn.Disable()
	}
	if held || s.takeover {
		d.input.setSuggestion("")
	} else {
		d.input.setSuggestion(d.shownSuggestion(s))
	}
	setOptions(d.modelSel, d.modelOptions(s))
	setOptions(d.modeSel, modeOptions(s))
	setBackendButton(d.backendBtn, s.model)
	if d.modelSel.Selected != s.model {
		d.modelSel.SetSelected(s.model)
	}
	if d.modeSel.Selected != s.mode {
		d.modeSel.SetSelected(s.mode)
	}
	if st == stRunning || st == stWaiting && !s.conv.AwaitsReply() || s.running() && s.conv.Background() > 0 {
		d.stopBtn.Enable()
	} else {
		d.stopBtn.Disable()
	}
	setText(d.stats, statsLine(s))
	if syncTasksButton(d.tasksBtn, s) && d.mainPane != nil {
		labels := [2]string{"Open chips", "Collapse chips"}
		for i, b := range d.chipBtns {
			if b == nil {
				continue
			}
			want := labels[i]
			if d.tasksBtn.Visible() {
				want = ""
			}
			b.SetText(want)
		}
		relayoutTree(d.mainPane)
	}
	if d.usage != nil {
		d.usage.update()
	}
	if d.chat != nil && (d.stt == nil || d.stt.state == sttIdle) {
		d.chat.setLive(liveText(d.current))
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
	if c := d.current; c != nil && c.id != id && c.qedit != nil {
		d.cancelQueueEdit(c)
	}
	d.flushUI()
	s := d.session(id)
	if s == nil {
		return
	}
	if p := d.paneFor(s); p != nil && p != d.focused {
		d.focusPane(p)
		return
	}
	d.current = s
	d.addTab(s.id)
	if s.persisted || d.infos[s.id].ID != "" {
		d.app.Preferences().SetString("lastTab", s.id)
	}
	defer d.syncPanes()
	if !d.rowShown(id) {
		d.rebuildRows()
	}
	info, has := d.infos[id]
	needLoad := has && !s.running() && (!s.loaded || !info.ModTime.Equal(s.loadedMod))
	if needLoad {
		path, mod := info.Path, info.ModTime
		s.loading = true
		if s.loaded {
			d.chat.show(s)
		} else {
			d.chat.show(nil)
		}
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
				s.limitSeen = ""
				d.copyLimit(s)
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
	cwd := home
	if d.current != nil && d.current.cwd != "" {
		cwd = d.current.cwd
	}
	prefs := d.app.Preferences()
	s := d.createSession(cwd, prefs.StringWithFallback("newModel", "default"), prefs.StringWithFallback("newMode", "auto"))
	name := d.nextSessionName()
	d.org.Titles[s.id] = name
	d.saveOrg()
	d.updateChrome()
	d.syncTabs()
	d.askNewName(s, name)
}

func (d *desk) nextSessionName() string {
	n := 0
	for _, t := range d.org.Titles {
		var k int
		if _, err := fmt.Sscanf(t, "New session %d", &k); err == nil && k > n {
			n = k
		}
	}
	return fmt.Sprintf("New session %d", n+1)
}

func (d *desk) createSession(cwd, model, mode string) *deskSession {
	return d.addSession(cwd, model, mode, true)
}

func (d *desk) addSession(cwd, model, mode string, show bool) *deskSession {
	id := session.NewUUID()
	d.created[id] = true
	ids := append(d.app.Preferences().StringList("createdSessions"), id)
	if len(ids) > 2000 {
		ids = ids[len(ids)-2000:]
	}
	d.app.Preferences().SetStringList("createdSessions", ids)
	if isLocal(model) && !contains(localModes, mode) {
		mode = "default"
	}
	s := &deskSession{id: id, cwd: cwd, model: model, mode: mode, conv: chat.New(), loaded: true, created: time.Now()}
	if d.app.Preferences().Bool("chromeNew") {
		s.chrome = "on"
	}
	d.sessions[id] = s
	if !show {
		d.addTab(id)
		d.rebuildRows()
		return s
	}
	d.current = s
	d.addTab(id)
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
	if err := d.checkOllama(s.model); err != nil {
		s.failed = err.Error()
		return err
	}
	opts := session.Options{Binary: d.cfg.ClaudeBin, Cwd: s.cwd, Model: s.model, Mode: s.mode, Chrome: s.chrome, AddDirs: s.addDirs, ResumeAt: s.resumeAt, Prompt: quillPromptHint,
		Env: append(d.localEnv(s.model), checkpointEnv)}
	d.warnInsecureOllama(s)
	if s.persisted {
		opts.Resume = s.id
	} else {
		opts.SessionID = s.id
	}
	s.failed = ""
	s.stopping = false
	s.rcOn, s.rcURL, s.rcName = false, "", ""
	p, err := session.Start(opts, func(ev *protocol.Event) {
		d.do(func() { d.handleEvent(s, ev) })
	})
	if err != nil {
		s.failed = err.Error()
		return err
	}
	s.conv.ClearBackground()
	s.resumeAt = ""
	d.adopt(s, p)
	d.autoRemote(s)
	return nil
}

func (d *desk) adopt(s *deskSession, p *session.Process) {
	s.proc = p
	s.persisted = true
	d.ownPIDs.Store(p.PID(), true)
	d.keepSession(s)
	go func() {
		<-p.Done()
		err := p.ExitError()
		d.do(func() { d.handleExit(s, p, err) })
	}()
}

func (d *desk) handleEvent(s *deskSession, ev *protocol.Event) {
	items := s.conv.Apply(ev)
	if ev.Type == "system" && ev.Subtype == "init" && ev.SessionID != "" && ev.SessionID != s.id {
		s.conv.AddNotice("Claude reported a different session id: " + ev.SessionID)
	}
	items = append(items, d.planGate(s, ev, items)...)
	if ev.Type == "result" {
		d.kmemAfterTurn(s)
		d.checkAutoHandoff(s)
		if s.clearAfterTurn {
			d.clearNow(s)
		}
		s.restarting = false
	}
	d.checkLimitMessage(s, items)
	d.attention(s, ev, items)
	switch ev.Type {
	case "control_request", "control_cancel_request", "result":
		d.keepSession(s)
	}
	if d.current == s {
		d.queueUI(s, items)
	} else if p := d.paneFor(s); p != nil {
		p.queue(items)
	}
	if ev.Type == "rate_limit_event" {
		d.setRateLimit(ev.RateLimit)
	}
	urgent := ev.Type == "result" || ev.Type == "control_request" || ev.Type == "prompt_suggestion" || ev.Type == "system" && ev.Subtype == "init"
	if urgent {
		if d.current == s {
			d.flushUI()
		}
		d.refreshSidebarSoon()
	}
	if ev.Type == "result" {
		d.flushQueue(s)
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
	d.unkeepSession(s.id)
	var items []*chat.Item
	items = append(items, s.conv.CancelPending()...)
	for _, it := range s.conv.Items {
		if it.Streaming {
			it.Streaming = false
			items = append(items, it)
		}
	}
	s.conv.Busy = false
	s.inTurn, s.planWait = false, false
	s.rcOn, s.rcURL, s.rcName = false, "", ""
	if err != nil && !s.stopping {
		msg := "Claude process exited: " + err.Error()
		if tail := strings.TrimSpace(p.Stderr()); tail != "" {
			msg += "\n" + lastLines(tail, 6)
		}
		s.failed = msg
		items = append(items, s.conv.AddError(msg))
		d.alert(s, "error", msg)
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
	if text == "" {
		return
	}
	if d.runLocalCommand(text) {
		return
	}
	if s == nil {
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
	if d.usagePaused && !strings.HasPrefix(text, "/") {
		text = pausedNote + text
	}
	if s.qedit != nil {
		idx, draft := s.qedit.idx, s.qedit.draft
		s.qedit = nil
		if s.compacting || s.running() && (s.conv.Busy || s.proc.Busy()) {
			idx = min(idx, len(s.queue))
			s.queue = append(append(append([]string{}, s.queue[:idx]...), text), s.queue[idx:]...)
			d.inputFor(s).SetText(draft)
			d.syncQueue(s)
			return
		}
		defer d.inputFor(s).SetText(draft)
	}
	if s.compacting || s.running() && (s.conv.Busy || s.proc.Busy()) {
		d.enqueue(s, text)
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
	s.coldStamp = time.Time{}
	if err := d.ensureProcess(s); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not start claude: " + err.Error())})
		d.updateChrome()
		return
	}
	if s.kmemNew {
		s.kmemNew = false
		if k := d.org.Kmem[s.id]; k != "" {
			text = kmemIntro(k) + text
		}
	}
	if s.forkOf != "" {
		text = forkIntro(s.forkOf) + text
		s.forkOf = ""
	}
	if d.planFirst {
		if approvesPlan(s, text) && text != planGoMessage {
			d.approveByText(s)
		} else if text != planGoMessage && !isRetry(text) && !s.inTurn {
			s.runAll, s.runAllNext = false, false
			d.chatFor(s, s.conv.CancelPlans())
		}
	}
	s.lastSent = text
	s.inTurn = true
	it := s.conv.AddUser(text)
	d.refreshChoiceCards(s, it)
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
	} else if p := d.paneFor(s); p != nil {
		p.view.update(items)
	}
}

func (d *desk) sendNow(text string) {
	s := d.current
	if s == nil {
		return
	}
	if len(s.queue) > 0 {
		parts := append([]string{}, s.queue...)
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
		s.queue = nil
		d.syncQueue(s)
		d.clearInputFor(s, text)
		text = strings.Join(parts, "\n\n")
	}
	if strings.TrimSpace(text) == "" {
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
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if it.PermState != chat.PermPending || !s.running() {
		return
	}
	if err := s.proc.Respond(it.RequestID, dec, it.Request); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not answer: " + err.Error())})
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
	d.keepSession(s)
}

func isQuestion(it *chat.Item) bool {
	return it.ToolName == "AskUserQuestion"
}

func (d *desk) answerQuestions(s *deskSession, it *chat.Item, answers map[string]string) {
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if it.PermState != chat.PermPending || !s.running() {
		return
	}
	if err := s.proc.Answer(it.RequestID, it.Request, answers); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not answer: " + err.Error())})
		return
	}
	var lines []string
	for _, q := range protocol.ParseQuestions(it.Input) {
		if a, ok := answers[q.Question]; ok {
			lines = append(lines, q.Question+" → "+a)
		}
	}
	it.Text = strings.Join(lines, "\n")
	it.PermState = chat.PermAllowed
	if d.current == s {
		d.chat.update([]*chat.Item{it})
		d.updateChrome()
	}
	d.refreshSidebar()
	d.keepSession(s)
}

func (d *desk) setModel(s *deskSession, model string) {
	switchBackend := (session.LocalModel(s.model) == "") != (session.LocalModel(model) == "")
	if switchBackend && s.running() && s.conv.Busy {
		d.notify(s, "Wait for the current turn to finish before switching between Claude and a local model.")
		return
	}
	s.model = model
	d.localModeGuard(s)
	if !s.running() {
		return
	}
	p := s.proc
	if switchBackend {
		s.proc = nil
		s.inTurn, s.planWait = false, false
		s.rcOn, s.rcURL, s.rcName = false, "", ""
		d.unkeepSession(s.id)
		go func() { _ = p.Stop(5 * time.Second) }()
		d.notify(s, "Model set to "+model+". The session restarts on the next message.")
		return
	}
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
	if !d.autoRC || s.proc == nil || isLocal(s.model) && !d.rcForLocal() {
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
			d.rememberRC(s.id, info.SessionURL)
			d.syncRCNames()
			if d.current == s {
				d.updateChrome()
			}
		})
	}()
}

func (d *desk) setRemoteControl(s *deskSession, on bool) {
	if on && isLocal(s.model) && !d.rcForLocal() {
		s.rcOn = false
		d.notify(s, "Remote Control is off for local sessions. Turn on “Remote Control for local sessions” in settings to use it here.")
		d.updateChrome()
		return
	}
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
					d.rememberRC(s.id, info.SessionURL)
				}
				s.rcName = ""
				d.syncRCNames()
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
			for k := range infos {
				infos[k].Title = d.unRCTitle(infos[k].ID, infos[k].Title)
				d.infos[infos[k].ID] = infos[k]
			}
			d.linkKmem(infos)
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
	d.do(d.reopenLastTab)
	go d.watchBinary(ctx)
	go d.glowLoop(ctx)
	go d.usageLoop(ctx)
	go d.watchUpdates(ctx)
	go d.chat.followGuard(ctx)
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
		if plan := d.acct.Plan(); plan != "" {
			label += "  ·  " + plan
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
	var dlgs []dialog.Dialog
	go func() {
		err := account.Login(ctx, d.cfg.ClaudeBin, func(u string) {
			d.do(func() {
				pu, perr := url.Parse(u)
				if perr != nil {
					return
				}
				link := widget.NewHyperlink("Open the sign-in page", pu)
				dlg := dialog.NewCustom("Sign in", "Close", container.NewVBox(
					widget.NewLabel("Finish signing in in your browser. If it did not open:"), link), d.win)
				dlgs = append(dlgs, dlg)
				dlg.Show()
			})
		})
		cancel()
		d.do(func() {
			d.signingIn = nil
			for _, dlg := range dlgs {
				dlg.Hide()
			}
			if err != nil && ctx.Err() == nil {
				dialog.ShowError(fmt.Errorf("sign-in failed: %w", err), d.win)
			}
		})
		d.refreshAccount()
	}()
}

func liveText(s *deskSession) string {
	if s == nil || !s.conv.Busy {
		return ""
	}
	if len(s.conv.PendingPermissions()) > 0 {
		return "Waiting for your answer above"
	}
	if s.conv.Thinking {
		return "✻ Thinking..."
	}
	if n := len(s.conv.Items); n > 0 {
		last := s.conv.Items[n-1]
		if last.Kind == chat.Assistant && last.Streaming {
			return ""
		}
		if last.Kind == chat.Tool && !last.HasResult {
			return "● " + oneLine(toolHeader(last), 90)
		}
	}
	return "✻ Working..."
}

func setLabel(l *widget.Label, text string) {
	if l.Text != text {
		l.SetText(text)
	}
}

func setText(t *canvas.Text, text string) {
	if t.Text != text {
		t.Text = text
		t.Refresh()
	}
}

func (d *desk) setInputCenter(on bool) {
	d.app.Preferences().SetBool("centerInput", on)
	if d.input == nil {
		return
	}
	d.input.Alignment = fyne.TextAlignLeading
	if on {
		d.input.Alignment = fyne.TextAlignCenter
	}
	d.input.Refresh()
}

func (d *desk) syncCenterAll() {
	if d.centerAll == nil {
		return
	}
	on := 0
	for _, c := range d.centerChecks {
		if c.Checked {
			on++
		}
	}
	all, some := on == len(d.centerChecks), on > 0 && on < len(d.centerChecks)
	if d.centerAll.Checked != all {
		d.suppressAlign = true
		d.centerAll.SetChecked(all)
		d.suppressAlign = false
	}
	if d.centerAll.Partial != some {
		d.centerAll.Partial = some
		d.centerAll.Refresh()
	}
}

func (d *desk) setStatusCenter(on bool) {
	d.app.Preferences().SetBool("centerStatus", on)
	for _, v := range d.allViews() {
		if v.live == nil {
			continue
		}
		v.live.Alignment = fyne.TextAlignLeading
		if on {
			v.live.Alignment = fyne.TextAlignCenter
		}
		v.live.Refresh()
	}
}

func (d *desk) allViews() []*chatView {
	var out []*chatView
	if d.single != nil {
		out = append(out, d.single)
	} else if d.chat != nil {
		out = append(out, d.chat)
	}
	for _, p := range d.panes {
		out = append(out, p.view)
	}
	return out
}

func (d *desk) setCollapseChips(on bool) {
	if d.collapseChips == on {
		return
	}
	if d.chat.stepOpen == nil {
		d.chat.stepOpen = map[*chat.Item]bool{}
	}
	for _, s := range d.sessions {
		for _, it := range s.conv.Items {
			if _, ok := d.chat.stepOpen[it]; !ok && it.Kind == chat.Tool {
				d.chat.stepOpen[it] = !d.collapseChips
			}
		}
	}
	d.collapseChips = on
	d.app.Preferences().SetBool("collapseChips", on)
}

func (d *desk) setAllChips(open bool) {
	if d.hideChips == open {
		d.hideChips = !open
		d.app.Preferences().SetBool("hideChips", d.hideChips)
		for _, v := range d.allViews() {
			v.cache = map[*deskSession]*sessView{}
			v.order = nil
			if vs := v.s; vs != nil && v != d.chat {
				v.s = nil
				v.show(vs)
			}
		}
	}
	s := d.current
	if s == nil {
		return
	}
	if d.chat.stepOpen == nil {
		d.chat.stepOpen = map[*chat.Item]bool{}
	}
	for _, it := range s.conv.Items {
		if it.Kind == chat.Tool {
			d.chat.stepOpen[it] = open
		}
	}
	d.chat.show(s)
}

func (d *desk) setCenter(on bool) {
	if d.center == on {
		return
	}
	d.center = on
	d.app.Preferences().SetBool("centerText", on)
	for _, v := range d.allViews() {
		v.cache = map[*deskSession]*sessView{}
		v.qcards = nil
		v.order = nil
		if s := v.s; s != nil {
			v.s = nil
			v.show(s)
		}
	}
}

const (
	planAskMessage = "Quill plan-first approval: nothing was run. Before running anything that needs approval, list everything you are going to do for this request (every command, file change and tool, in order, with a short reason), then stop and wait. The user will approve the whole plan at once."
	planGoMessage  = "Plan approved. Go ahead and carry out the whole plan; every tool call for this request is pre-approved."
)

func (d *desk) planGate(s *deskSession, ev *protocol.Event, items []*chat.Item) []*chat.Item {
	switch {
	case ev.Type == "control_request" && ev.Request != nil && ev.Request.Subtype == "can_use_tool":
		var it *chat.Item
		for _, x := range items {
			if x.Kind == chat.Permission && x.RequestID == ev.RequestID {
				it = x
			}
		}
		if it == nil || it.PermState != chat.PermPending || s.proc == nil || isQuestion(it) {
			return nil
		}
		if s.retryCmd != "" && blockedCommand(it) == s.retryCmd {
			rules, dest := s.retryRules, s.retryDest
			s.retryCmd, s.retryRules, s.retryDest = "", nil, ""
			if len(rules) > 0 {
				if s.proc.AllowWithRules(it.RequestID, it.Request, rules, dest) == nil {
					it.PermState = chat.PermRuleAllowed
				}
			} else if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermRetryAllowed
			}
			return nil
		}
		if targets := openerTargets(it, s.cwd); len(targets) > 0 {
			if s.proc.DenyWith(it.RequestID, it.Request, openerMessage) == nil {
				it.PermState = chat.PermDenied
				return []*chat.Item{s.conv.AddNotice("Shown here instead of opening a window: " + strings.Join(targets, " "))}
			}
			return nil
		}
		if s.runAll || d.kmemSaveAllowed(s, it.Request) {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
			}
			return nil
		}
		if !d.planFirst {
			return nil
		}
		if readOnlyRequest(it.Request, d.readOnlyExtra()) {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
			}
			return nil
		}
		if d.approvedInTranscript(s) {
			d.approveNow(s)
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
			}
			return nil
		}
		if s.proc.DenyWith(it.RequestID, it.Request, planAskMessage) == nil {
			it.PermState = chat.PermPlanAsked
			s.planWait = true
		}
	case ev.Type == "user" && d.planFirst:
		for _, it := range items {
			if it.Kind != chat.User {
				continue
			}
			if approvesPlan(s, it.Text) {
				d.approveNow(s)
			} else if !isRetry(it.Text) && !s.inTurn {
				s.runAll, s.runAllNext = false, false
			}
			s.inTurn = true
		}
	case ev.Type == "result":
		s.inTurn = false
		if s.restoreMode != "" {
			mode := s.restoreMode
			s.restoreMode = ""
			d.setMode(s, mode)
		}
		if s.runAllNext {
			s.runAll, s.runAllNext = true, false
		}
		if s.planWait || d.planFirst && !s.runAll && s.conv.AsksApproval() {
			s.planWait = false
			out := s.conv.CancelPlans()
			out = append(out, s.conv.MarkPlanText()...)
			return append(out, s.conv.AddPlan())
		}
	}
	return nil
}

func (d *desk) approveNow(s *deskSession) {
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind == chat.Plan {
			it.PermState = chat.PermAllowed
			d.chatFor(s, []*chat.Item{it})
		}
	}
	s.runAll, s.runAllNext = true, false
}

var approvalWords = map[string]bool{
	"yes": true, "y": true, "yeah": true, "yep": true, "yup": true, "ok": true, "okay": true, "sure": true,
	"approve": true, "approved": true, "go": true, "go ahead": true, "do it": true, "proceed": true,
	"continue": true, "lgtm": true, "run it": true, "sounds good": true, "yes please": true, "please do": true,
	"approve plan": true, "approve it": true, "go for it": true, "ship it": true,
}

var approvalStarts = []string{"yes", "yeah", "yep", "yup", "ok", "okay", "sure", "approve", "approved", "go ahead", "go for it", "do it", "proceed", "lgtm", "sounds good", "run it", "ship it", "please do"}

var notApproval = []string{"but", "don't", "dont", "do not", "not ", "no ", "wait", "instead", "hold", "stop", "except", "change", "first", "before", "?"}

const retryPrefix = "I approve the action that Claude Code's auto-mode check blocked: "

func approves(text string) bool {
	if isRetry(text) {
		return false
	}
	return isApproval(text) || text == planGoMessage
}

var stepList = regexp.MustCompile(`(?i)^(steps?\s*)?#?\d+(\s*(-|–|—|\bto\b|,|\band\b|&|\s)\s*#?\d+)+$`)

func isStepList(text string) bool {
	t := strings.TrimRight(strings.TrimSpace(text), ".! ")
	return len(t) <= 60 && stepList.MatchString(t)
}

var (
	stepItem = regexp.MustCompile(`(?i)#?(\d+)(?:\s*(?:-|–|—|\bto\b)\s*#?(\d+))?`)
	planStep = regexp.MustCompile(`(?m)^(?:\*\*)?\(?(\d+)[.)]`)
)

func stepNumbers(text string) map[int]bool {
	out := map[int]bool{}
	for _, m := range stepItem.FindAllStringSubmatch(text, -1) {
		a, _ := strconv.Atoi(m[1])
		b := a
		if m[2] != "" {
			b, _ = strconv.Atoi(m[2])
		}
		if b < a || b-a > 100 {
			return nil
		}
		for n := a; n <= b; n++ {
			out[n] = true
		}
	}
	return out
}

func planSteps(text string) int {
	n := 0
	for _, m := range planStep.FindAllStringSubmatch(text, -1) {
		if v, _ := strconv.Atoi(m[1]); v > n {
			n = v
		}
	}
	return n
}

func coversPlan(s *deskSession, text string) bool {
	nums := stepNumbers(strings.TrimRight(strings.TrimSpace(text), ".! "))
	found := false
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind != chat.Plan {
			continue
		}
		n := planSteps(s.conv.PlanText(it))
		if n == 0 {
			return false
		}
		for i := 1; i <= n; i++ {
			if !nums[i] {
				return false
			}
		}
		found = true
	}
	return found
}

func approvesPlan(s *deskSession, text string) bool {
	if text == planGoMessage {
		return true
	}
	if approves(text) {
		return hasPendingPlan(s) || s.conv.AsksApproval() || s.planWait
	}
	return isStepList(text) && coversPlan(s, text)
}

func isRetry(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), retryPrefix)
}

var strongApproval = []string{"yes approve", "yes, approve", "yes i approve", "yes, i approve", "i approve", "approve all", "yes approved", "yes, approved", "plan approved"}

var strongStop = []string{"but", "don't", "dont", "do not", "not ", "wait", "instead", "hold", "stop", "except", "cancel", "skip", "don’t"}

func isApproval(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimRight(t, ".! ")
	if t == "" || len(t) > 2000 {
		return false
	}
	for _, p := range strongApproval {
		if !strings.HasPrefix(t, p) {
			continue
		}
		rest := t[len(p):]
		if rest != "" && !strings.ContainsAny(rest[:1], " ,:;-—.!") {
			continue
		}
		if hasStopWord(rest, strongStop) {
			return false
		}
		return true
	}
	if len(t) > 200 {
		return false
	}
	if approvalWords[t] {
		return true
	}
	for _, w := range approvalStarts {
		if !strings.HasPrefix(t, w) {
			continue
		}
		rest := t[len(w):]
		if rest != "" && !strings.ContainsAny(rest[:1], " ,:;-—") {
			continue
		}
		if hasStopWord(rest, notApproval) {
			return false
		}
		return true
	}
	return false
}

var wordRun = regexp.MustCompile(`[a-z’']+|\?`)

func hasStopWord(text string, stops []string) bool {
	text = strings.ReplaceAll(strings.ToLower(text), "’", "'")
	words := wordRun.FindAllString(text, -1)
	joined := " " + strings.Join(words, " ") + " "
	for _, n := range stops {
		n = strings.TrimSpace(n)
		if n == "?" {
			if strings.Contains(text, "?") {
				return true
			}
			continue
		}
		if strings.Contains(joined, " "+strings.Join(wordRun.FindAllString(n, -1), " ")+" ") {
			return true
		}
	}
	return false
}

func (d *desk) approveByText(s *deskSession) {
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind == chat.Plan {
			it.PermState = chat.PermAllowed
			d.chatFor(s, []*chat.Item{it})
		}
	}
	s.runAll, s.runAllNext = true, false
}

func (d *desk) answerPlan(s *deskSession, it *chat.Item, ok bool) {
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if ok && it.PermState == chat.PermAllowed && !s.conv.Busy && !replied(s, it) {
		s.runAll = true
		d.sendTo(s, planGoMessage)
		return
	}
	if it.PermState != chat.PermPending {
		return
	}
	if !ok {
		it.PermState = chat.PermDenied
		d.chatFor(s, []*chat.Item{it})
		d.updateChrome()
		d.refreshSidebar()
		return
	}
	it.PermState = chat.PermAllowed
	s.runAll = true
	d.chatFor(s, []*chat.Item{it})
	d.sendTo(s, planGoMessage)
	if !s.running() {
		s.runAll = false
	}
}
