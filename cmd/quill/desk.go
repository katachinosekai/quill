package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"regexp"
	"slices"
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
	"github.com/katachinosekai/quill/internal/codex"
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
	codexEffort, codexDefaultModel string
	pendingRevision                *approvalRevision
	codexWaiting, codexWorkers     bool
	claudeCoordination             string
	claudeCoordinationSet          bool
	codexUpgrading                 bool
	restartPending, restartActive  bool

	codex          bool
	agentHandoff   *agentHandoff
	id             string
	cwd            string
	title          string
	conv           *chat.Conversation
	loaded         bool
	loadedMod      time.Time
	persisted      bool
	hidden         bool   // a hidden session (kmem assistant, Quill operator): not in the session list, tabs or phone API (docs/design/kmem-window.md §4)
	role           string // its hidden role (hidden.go); "" with hidden = kmem
	proc           *session.Process
	model          string
	mode           string
	failed         string
	stopping       bool
	clearAfterTurn bool
	terminal       *live.Process
	planWait       bool
	cardGo         bool     // answerPlanMsg is sending the card's own approval
	approvalHint   bool     // the session's prompt has planApprovalHint (else planAsk adds a reminder)
	releaseTasks   []string // kmem task releases Quill asked for after a rewind (allowed for that turn)
	runAll         bool
	runAllNext     bool
	autoApprove    bool                  // auto-approve: no plan needed until the user turns it off (autoapprove.go)
	autoNote       string                // tells Claude with the next message that auto-approve is off
	autoPeer       bool                  // a peer, channel or scheduled message started or joined this turn: auto-approve doesn't cover it
	autoOwn        map[string]bool       // work started in turns auto-approve covers (tool_use, task and agent ids)
	autoStarting   bool                  // the message being sent turned auto-approve on (undone if it can't be sent)
	autoPrevNote   string                // autoNote before that message
	agentGrants    map[string]agentGrant // subagents an approved plan started, by tool_use_id, until they finish
	agentCalls     map[string]string     // tool_use_id of a granted subagent's call (nested agents too) -> its grant
	agentIDs       map[string]string     // agent id of a granted subagent (nested too) -> its grant
	planWork       map[string]string     // background work an approved plan started (tool_use, task and agent ids) -> its root call (planscope.go)
	planAgents     map[string]string     // agent id or name of a subagent a plan started -> its grant, for SendMessage resumes
	turnCovered    bool                  // the current turn is covered by the approved plan (planCovers)
	autoTurn       bool                  // a turn started by a message the user didn't write is running
	planRevoked    bool                  // the user stopped or refused since the last approval: no approval from the transcript
	grantRev       int                   // bumped whenever a grant is made or ends (gateMark)
	keptGate       gateMark              // the plan-scope state last sent to the keeper (keepGate)
	gateUnsure     bool                  // restored after a restart: no call is presumed a subagent's until the turn ends
	batchOf        map[string]string     // this turn's own tool_use ids -> their assistant message (gatebatch.go)
	batchRefused   map[string]bool       // assistant messages with a call plan-first refused this turn
	batchAgents    map[string]string     // this turn's own Agent calls -> their task (agent) id once known, "" before
	stopAgents     map[string]bool       // Agent calls of a refused batch, stopped once their task id is known
	refusedAgents  map[string]bool       // agent ids of subagents stopped with their refused batch: their calls are refused
	subRefused     map[string]bool       // subagents' assistant messages with a call plan-first refused
	planSends      map[string]bool       // SendMessage calls of a covered turn: a resumed subagent is granted (planscope.go)
	planShells     map[string]bool       // Bash calls of a covered turn: plan work if the CLI moves one to the background
	queuedResumes  map[string]string     // agent id of a granted subagent a covered SendMessage was queued to -> its grant (planscope.go)
	peerTurn       bool                  // a peer, channel or scheduled message started or joined this turn
	queue          []string
	lastSent       string
	inTurn         bool
	restoreMode    string
	retryCmd       string
	retryRules     []protocol.Rule
	retryDest      string
	kmemSave       *kmemSave
	endWait        *endWait // end-of-session check running (endcheck.go)
	kmemNew        bool
	remote         *remoteSess // a live session on a paired PC; nil = local
	resumeNext     string
	resumeAs       string
	rotateAsk      int
	rotateTried    int
	lastClaude     string
	compactFrom    string
	chrome         string
	addDirs        []string
	resumeAt       string
	forkOf         string
	pendingEnd     int
	qedit          *queueEdit
	coldStamp      time.Time
	rotateNo       bool
	limitSeen      string
	compacting     bool
	restarting     bool
	pickPending    bool
	takeover       bool
	loading        bool
	rcURL          string
	rcOn           bool
	rcName         string
	created        time.Time
	turnStart      time.Time // when the running turn began (status.go)
	lastAct        time.Time // last event from the session (status.go)
	touched        time.Time // last time the session was shown, read by the phone or busy (unload.go)
}

func (s *deskSession) running() bool {
	return s.proc != nil && !s.proc.Exited()
}

func (s *deskSession) status() status {
	if s.remote != nil {
		return remoteStatus(s.remote)
	}
	if s.terminal != nil && !s.running() {
		return stTerminal
	}
	restarting := s.restarting || s.restartActive || s.compacting || s.agentHandoff != nil || s.kmemSave != nil || s.endWait != nil
	if !s.running() {
		if s.failed != "" {
			return stError
		}
		if restarting {
			return stRestart
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
	CodexRoot    string
	SessionsDir  string
	ClaudeBin    string
}

type sideRow struct {
	header       string
	id           string
	parent       string
	key          string
	depth        int
	count        int
	open         bool
	ext          *extSession // a live session outside Quill Desktop: read-only row
	hasAgents    bool
	agentSession string
	agent        chat.AgentUse
}

type desk struct {
	peerMessages   *peerMessages // mutated on the UI thread
	statusRun      bool          // the status ticker runs (status.go)
	nameClose      func()
	limitMsg       string
	notifier       *notifier
	notifySink     func(sid, title, body string)
	foreground     bool
	compactor      func(ctx context.Context, model, text string) (string, error)
	kmemRun        func(prog string, args ...string) (string, error)
	kmemOff        bool
	kmemAdverts    []*fyne.Container // kmemoptional.go
	kmemOnlySet    *fyne.Container   // Settings' kmem-only controls (kmemsetup.go)
	kmemSwitch     *fyne.Container
	escClosers     map[fyne.CanvasObject]func()
	kmemVer        kmemVersion
	pcLock         *pcLock
	handedPw       map[string]handedPw // a live reload's lanMem.pwTok/pwConn, for the phone server
	pcPassRefresh  func()
	pcBox          *fyne.Container
	pcsBtn         *widget.Button
	pcNote         map[string]string
	hsMu           sync.Mutex
	lastHandoffs   []kmemHandoff
	kmemNoHandoff  map[string]bool // linked names kmem had no handoff for at the last rename check (kmemrename.go)
	policy         handoffPolicy
	policyAt       time.Time
	policyBusy     bool
	localModels    []string
	localBusy      bool
	fetchLocal     func(host string) []localInfo
	fetchServer    func(localServerConf) []string
	localAgain     bool
	after          func(time.Duration, func())
	toolbarRow     *fyne.Container
	chipsBtn       *widget.Button
	queueReflowing bool
	modelBtn       *widget.Button
	modeBtn        *widget.Button
	toolbarBox     *fyne.Container
	usagePaused    bool
	pauseUntil     time.Time
	pausedIDs      []string
	pauseBanner    *fyne.Container
	pauseText      *widget.Label
	quillMove      *quillMoveNotice // one-time move-in notice (quilloperator.go)
	// kmem button bubble: what kmem asks the user for (bin, audit, maint,
	// user tasks); kmembadge.go. The bin dialog: kmembin.go.
	kmemBubble    *bubble
	kmemRunDot    *canvas.Circle // the kmem assistant runs (kmembadge.go)
	kmemBtnBox    fyne.CanvasObject
	quillChat     *assistChat       // the Quill operator's chat in the box (quillwire.go)
	settingCards  []*settingCard    // the change cards in that chat (quillwire.go)
	quillBtnBox   fyne.CanvasObject // the Quill operator's button with its badge and dot (assistbox.go)
	kmemNeedsNow  kmemNeeds
	kmemCo        kmemCoalescer // read-only kmem commands: one load per event (kmemload.go)
	kmemGot       bool          // kmem has answered once (the badge may prune seen records)
	kmemBinSetBtn *widget.Button
	kmemBinNow    *binView
	verLine       *verLine               // kmem version under the message box; kmemverline.go
	saveGeom      func()                 // saves the main window's size and maximised state now
	kmemWin       *kmemWindow            // the kmem operations window, nil while closed; kmemwindow*.go
	hiddenHosts   map[string]*hiddenHost // hidden role -> where it shows (hidden.go); kmem defaults to kmemWin
	statsRow      *fyne.Container
	controlFn     func(s *deskSession, req map[string]any) (json.RawMessage, error)
	pingLocal     func(host string) bool
	localWindows  map[string]int
	warnedOllama  bool
	backendBtn    *widget.Button
	app           fyne.App
	win           fyne.Window
	cfg           config
	scanner       *transcript.Scanner
	finder        *live.Finder
	center        bool
	sessions      map[string]*deskSession
	infos         map[string]transcript.Info
	order         []transcript.Info
	terms         map[string]live.Process
	releaseFn     func(live.Process) error // tests: replaces finder.Release
	current       *deskSession
	ownPIDs       sync.Map

	codexQuillChat *assistChat

	// The "Switch to kmem" dialog when open (kmemswitchui.go), and the
	// refresh of Settings → General → Memory (kmem).
	kmemSwitchUI        *kmemSwitchView
	kmemSettingsRefresh func()

	rows              []sideRow
	openSidebarAgents map[string]bool
	rowCaps           map[string]string
	list              *widget.List
	search            *widget.Entry
	chat              *chatView
	title             *widget.Label
	subtitle          *canvas.Text
	note              *widget.Label
	rcStatus          *canvas.Text
	reloadBtn         *widget.Button // Reload for Other PCs sessions (remotereload.go)
	usage             *usagePanel
	rate              *protocol.RateLimit
	rateAt            time.Time
	input             *sendEntry
	modelSel          *widget.Select
	newSessSel        *widget.Select // Settings → New sessions
	newModeSel        *widget.Select // Settings → New sessions mode
	modeSel           *widget.Select
	folder            *canvas.Text
	stats             *canvas.Text
	stopBtn           *widget.Button
	sendBtn           *widget.Button
	acctBtn           *widget.Button
	acct              account.Status
	acctErr           string
	signingIn         context.CancelFunc
	codexAuth         codexAuthState
	codexCatalogs     map[string]*composerCatalog
	reasoning         *reasoningControl
	glowBoxes         map[*canvas.Rectangle]glow
	pulsing           atomic.Int32
	keeper            *keeperClient
	single            *chatView
	panes             []*pane
	focused           *pane
	chatHost          *fyne.Container
	mainRoot          *fyne.Container // the main area with the docked assistant panel (assistbox.go)
	abox              *assistBox      // the assistant box (assistbox.go)
	keepBox           bool            // a tab switch keeps the overlay open (the operator's move-in)
	main              mainTarget      // the main message box messages the overlay's assistant (mainbox.go)
	targetLbl         *widget.Label   // above the main message box: who it messages, when not the tab
	tipLayer          *fyne.Container // top layer of the window content for tooltips (agentstrip.go)
	chatDropHL        *canvas.Rectangle
	viewBtn           *widget.Button
	viewBox           *fyne.Container
	groups            []bookGroup
	tabs              []string
	tabBar            *fyne.Container
	bookBar           *fyne.Container
	tabSig            string
	tabChips          map[string]*tabChip
	strip             *tabStrip
	autoChk           *widget.Check
	autoSyncing       bool
	preAuto           string
	listTheme         *container.ThemeOverride
	usageBox          *fyne.Container
	resizeFreeze      float32
	resizeTimer       *time.Timer
	lastRootSize      fyne.Size
	tabDrag           string
	tabDragX          float32
	tabCenter         *fyne.Container
	bookCenter        *fyne.Container
	bookSig           string
	bookRow           fyne.CanvasObject
	tabRow            fyne.CanvasObject
	settingsBox       *fyne.Container
	setGeneral        *fyne.Container
	setSessions       *fyne.Container
	policyUI          *policyRow
	kmemPanelNow      *kmemPanel
	remotes           map[string]*remoteLink
	settingsDlg       dialog.Dialog
	autoBtn           *autoChip // "Auto-approve on" by the message box (autoapprove.go)
	keepNext          bool      // the approval being sent turns auto-approve on (autoapprove.go)
	phoneSync         *phoneSync
	buildChk          *widget.Check // Settings → General "Ask before building", kept in line by settingsChanged
	coordChk          *widget.Check // Settings → General "Coordinate with subagents"
	featureDlg        dialog.Dialog // the open feature questions dialog (featureask.go)
	phoneFileBtn      *widget.Button
	phoneFileOff      *widget.Button
	settingsSlot      *fyne.Container
	settingsBtn       *widget.Button
	kmemBtn           *widget.Button
	layoutBtn         *widget.Button
	viewMenu          *widget.PopUpMenu
	viewItems         []*fyne.MenuItem
	sideDirty         bool
	shares            map[string]float32
	bottomBox         *fyne.Container
	keptView          string
	keptHidden        bool
	lightboxClose     func()
	swapTo            string
	swapping          bool
	restore           *restoreState
	cmdList           []protocol.Command
	agentList         []protocol.Agent
	pending           []*chat.Item
	pendingSeen       map[*chat.Item]bool
	pendingChrome     bool
	pendingFor        *deskSession
	// The kmem window's per-frame chat batch (queueWinUI).
	winPending    []*chat.Item
	winPendingFor *deskSession
	winFlushTimer *time.Timer
	// kmemQuitFlow, when set (kmemassist.go), saves and stops the kmem
	// window's assistant before quitting and then calls done.
	kmemQuitFlow  func(done func())
	lastTyped     time.Time
	stt           *speech
	autoRC        bool
	groupProjects bool
	planFirst     bool
	collapseChips bool
	hideChips     bool
	sizesCard     fyne.CanvasObject
	usageRow      fyne.CanvasObject
	agentStrip    *agentStrip // subagent strip (agentstrip.go)
	updRelease    *updater.Release
	// update line (updline.go): never update or restart without the user
	updLine       *fyne.Container
	updText       *widget.Label
	updActs       *fyne.Container
	updating      bool
	swapOK        bool   // the user asked for the switch (Update / Restart into it)
	pendingBuild  string // a new build on disk waiting for the user
	rules         sortRules
	centerAll     *widget.Check
	centerChecks  []*widget.Check
	suppressAlign bool
	forceSwap     bool
	swapNow       bool        // "Reload now": switch without waiting for typing to pause
	swapRetry     *time.Timer // retries a switch held by typing
	swapNotice    bool        // the update line shows why a switch waits
	restartAsked  bool        // the tray's "Restart Quill Desktop" runs the switch (trayrestart.go)
	restartWait   string      // a clean restart waits for running turns; why the handover failed
	restartRetry  *time.Timer // re-checks the running turns for a waiting clean restart
	restarting    bool        // a clean restart stops the sessions and starts a new Quill Desktop
	restartBy     time.Time   // a waiting clean restart goes ahead then (restartTurnWait)
	restartKeeps  string      // a new build the tray restart keeps pending
	binMu         sync.Mutex
	bin           *binWatch // Quill's own program file (watchBinary), under binMu
	flushTimer    *time.Timer
	cmds          *cmdMenu
	sidebar       fyne.CanvasObject
	background    bool
	hidden        bool
	sideFrac      float32
	ac            *autocorrector
	acKnown       map[string]*wordSet
	hintPrev      map[*canvas.Text]string
	windows       map[string]int
	sideAnim      *fyne.Animation
	animate       bool
	root          *fyne.Container
	sideToggle    *tapRow
	headPad       *canvas.Rectangle
	chatFreeze    float32
	sideOff       bool
	sideBody      *fyne.Container
	folderOpen    map[string]bool
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
	kwin          *kwinRules // KDE window-position rules; nil = never touched (tests)
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
	d.pcLock = newPCLock()
	d.takeOverUnlocks() // before initPhone: the phone server starts with them
	d.applyLocalServers()
	d.pcNote = map[string]string{}
	d.loadOrg()
	d.loadRules()
	d.loadFolderOpen()
	for _, id := range a.Preferences().StringList("createdSessions") {
		d.created[id] = true
	}
	d.initPhone()
	d.startRemotePCs()
	d.wireKmemQuit()
	return d
}

func (d *desk) isAuto(id string) bool {
	info, ok := d.infos[id]
	return ok && strings.HasPrefix(info.Entrypoint, "sdk") && !d.created[id]
}

func (d *desk) buildSidebar() fyne.CanvasObject {
	var newBtn *widget.Button
	newBtn = widget.NewButtonWithIcon("New session", theme.ContentAddIcon(), func() { d.showNewSessionMenu(newBtn) })
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
			for _, s := range d.runningVisible() {
				if !s.rcOn {
					d.autoRemote(s)
				}
			}
		}
	})
	rcChk.Checked = d.autoRC
	rcLocalChk := widget.NewCheck("Remote Control for local sessions (relayed through claude.ai)", func(on bool) {
		d.app.Preferences().SetBool("rcLocal", on)
		for _, s := range d.runningVisible() {
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
	planChk := widget.NewCheck("Plan first, then approve all", func(on bool) {
		d.planFirst = on
		d.app.Preferences().SetBool("planFirst", on)
	})
	planChk.Checked = d.planFirst
	splitChk := widget.NewCheck("Allow reads and kmem notes without a plan", func(on bool) {
		d.app.Preferences().SetBool(prefGateSplit, on)
	})
	splitChk.Checked = d.gateSplitOn()
	splitNote := caption(gateSplitExplain)
	buildChk := widget.NewCheck("Ask before building (plan card: more features, debug, secure, or build)", func(on bool) {
		if on != d.buildGateOn() {
			d.app.Preferences().SetBool(prefBuildGate, on)
			d.settingsChanged()
		}
	})
	buildChk.Checked = d.buildGateOn()
	coordChk := widget.NewCheck(coordinatorTitle, func(on bool) {
		if on != prefBool(d.app.Preferences(), prefCoordinator) {
			setUserPref(d.app.Preferences(), prefCoordinator, on)
			d.settingsChanged()
		}
	})
	coordChk.Checked = prefBool(d.app.Preferences(), prefCoordinator)
	coordNote := caption(coordinatorExplain + "\n" + coordinatorOnline)
	d.buildChk, d.coordChk = buildChk, coordChk
	newSessions := container.NewVBox(d.newSessionModelSetting(), d.newSessionModeSetting())
	animChk := widget.NewCheck("Animate new text (replies appear character by character while they stream)", func(on bool) { d.app.Preferences().SetBool(prefAnimateText, on) })
	animChk.Checked = d.animateText()
	speedSel := widget.NewSelect(animSpeedNames, func(v string) { d.app.Preferences().SetString(prefAnimateSpeed, speedKey(v)) })
	speedSel.Selected = speedName(d.animateSpeed())
	speedRow := container.NewHBox(widget.NewLabel("Text animation speed"), speedSel)
	motionRows := d.boxSettings()
	chipChk := widget.NewCheck(collapseActivityChipsLabel, d.setCollapseChips)
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
	d.setGeneral = container.NewVBox(planChk, splitChk, splitNote, buildChk, coordChk, coordNote, animChk, speedRow, motionRows, chipChk, updChk, notifyChk, d.kwinSetting(), widget.NewSeparator(), heading("Alignment"), alignRow, widget.NewSeparator(), heading("Sizes"), sizes, widget.NewSeparator(), heading("New sessions"), newSessions, widget.NewSeparator(), heading("Message times"), d.timeSettings(), widget.NewSeparator(), heading("Voice input"), d.voiceSettings(), widget.NewSeparator(), heading("Typing"), d.autocorrectSettings(), widget.NewSeparator(), heading("Memory (kmem)"), d.kmemSettings())
	d.setGeneral.Add(widget.NewSeparator()) // quilloperator.go
	d.setGeneral.Add(heading("Quill operator"))
	d.setGeneral.Add(d.quillOpSettings())
	d.setSessions = container.NewVBox(rcChk, rcLocalChk, widget.NewSeparator(), d.pauseSettings(), widget.NewSeparator(), d.handoffSettings(), widget.NewSeparator(), heading("Devices"), d.buildPhoneRow(), widget.NewSeparator(), heading("Other PCs"), d.remotePCsRow())
	openSettings := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), d.showSettings)
	openSettings.Importance = widget.LowImportance
	openSettings.Alignment = widget.ButtonAlignLeading
	// Settings · Quill · kmem (docs/design/quill-operator.md §3.2), with the
	// toolbar's badges and dots mirrored (assistbox.go).
	openQuill := d.sideAssistButton(boxQuill, "Quill", appIcon)
	openKmem := d.sideAssistButton(boxKmem, "kmem", theme.StorageIcon())
	d.pcBox = container.NewVBox()
	d.refreshPCBox()
	d.settingsBox = container.NewVBox(padTop(6), widget.NewSeparator(), d.pcBox, openSettings, openQuill, openKmem, legend)
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
			caption := canvas.NewText("", colMuted)
			caption.TextSize = fontDense
			head := container.NewBorder(nil, nil, nil, container.NewCenter(caption), title)
			var item *sideItem
			disclosure := widget.NewButtonWithIcon("", theme.MenuExpandIcon(), func() {
				if row, ok := item.row(); ok && row.hasAgents {
					d.toggleSidebarAgents(row.id)
				}
			})
			disclosure.Importance = widget.LowImportance
			disclosure.Hide()
			row := container.NewBorder(nil, nil, container.NewHBox(sindent, container.NewCenter(sized(14, 14, statusMark(stNone)))), disclosure,
				container.New(&tightVBox{}, head, container.NewPadded(meta)))
			glow := canvas.NewRectangle(color.Transparent)
			glow.CornerRadius = 8
			glow.StrokeWidth = 1.5
			focus := canvas.NewRectangle(color.Transparent)
			focus.CornerRadius = 8
			focus.StrokeWidth = 2.5
			focus.StrokeColor = colText
			focus.Hide()
			item = d.newSideItem(rows, container.NewStack(glow, folder, row, focus))
			item.agentToggle = disclosure
			return item
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
			if r.hasAgents {
				icon := theme.MenuExpandIcon()
				if r.open {
					icon = theme.MenuDropDownIcon()
				}
				item.agentToggle.SetIcon(icon)
			}
			setShown(item.agentToggle, r.hasAgents)
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
			head := col.Objects[0].(*fyne.Container)
			title := head.Objects[0].(*widget.Label)
			caption := head.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			meta := col.Objects[1].(*fyne.Container).Objects[0].(*canvas.Text)
			mark := left.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*fyne.Container)
			if r.agentSession != "" {
				title.SetText(agentName(r.agent))
				state := stRunning
				if !r.agent.Running() {
					state = stIdle
					if r.agent.Status == "failed" || r.agent.Status == "error" {
						state = stError
					}
				}
				meta.Text = firstNonEmpty(r.agent.Status, "running")
				meta.Refresh()
				setText(caption, "")
				setStatusMark(mark, state)
				d.setGlow(glow, nil, 0)
				row.Refresh()
				return
			}
			if r.ext != nil { // read-only: running outside Quill Desktop
				title.SetText(r.ext.Name)
				meta.Text = "running elsewhere · tap to take over"
				meta.Refresh()
				if caption.Text != "" {
					caption.Text = ""
					caption.Refresh()
				}
				setStatusMark(mark, stTerminal)
				d.setGlow(glow, nil, 0)
				row.Refresh()
				return
			}
			t, m, st2 := d.rowText(r.id)
			title.SetText(t)
			meta.Text = m
			meta.Refresh()
			c := d.rowCaption(r.id)
			if d.rowCaps == nil {
				d.rowCaps = map[string]string{}
			}
			d.rowCaps[r.id] = c
			if caption.Text != c {
				caption.Text = c
				caption.Refresh()
				head.Refresh()
			}
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
		if r.agentSession != "" {
			list.Unselect(id) // an agent row opens its menu through its own tap
			return
		}
		if r.ext != nil {
			list.Unselect(id) // not Quill Desktop's: nothing to open, only Take over
			d.offerTakeOver(*r.ext, nil)
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
	if s != nil && s.remote != nil {
		title = s.remote.title
		last = time.Time{}
	}
	if title == "" {
		title = "New session"
	}
	title = d.topicRowTitle(id, title)
	meta := ago(last)
	if s != nil && s.remote != nil {
		meta = "on " + s.remote.link.conf.Name
	}
	if msgs > 0 {
		meta += fmt.Sprintf("  ·  %d msgs", msgs)
	}
	if st != stNone {
		meta += "  ·  " + st.String()
	}
	if s != nil && s.remote == nil && st == stRunning && !s.conv.Busy && !s.proc.Busy() {
		meta += fmt.Sprintf("  ·  %d in background", s.conv.Working())
	}
	if k := d.org.Kmem[id]; k != "" && d.topicOf(id) == "" {
		meta += "  ·  kmem " + k // a topic tab's kmem name is its title
	}
	if s != nil && s.pendingEnd != 0 {
		meta += "  ·  ending soon (open to keep)"
	} else if s != nil && s.kmemSave != nil && s.kmemSave.end {
		meta += "  ·  saving handoff, then ending"
	} else if s != nil && s.endWait != nil && s.endWait.why != endForCheckpoint {
		meta += "  ·  end-of-session check"
		if n := len(s.endWait.blockers); n > 0 {
			meta += fmt.Sprintf(": %d blocker(s)", n)
		}
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
	return localDate(t, dateMD)
}

func tildePath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

func (d *desk) rebuildRows() {
	q := foldKey(strings.TrimSpace(d.search.Text))
	seen := map[string]bool{}
	var entries []sideEntry
	add := func(id, cwd string, last time.Time) {
		if seen[id] || d.hiddenID(id, cwd) || !d.liveSession(id) {
			return
		}
		seen[id] = true
		if q != "" && !d.matches(id, cwd, q) {
			return
		}
		entries = append(entries, sideEntry{id, cwd, last})
	}
	for _, s := range d.sessions {
		if _, ok := d.infos[s.id]; !ok {
			add(s.id, s.cwd, s.created)
		}
	}
	for _, info := range d.order {
		add(info.ID, info.Cwd, info.Last)
	}
	groups := d.projectGroups(entries)
	for i := range groups {
		if q != "" { // search narrows external rows by name
			var keep []extSession
			for _, e := range groups[i].ext {
				if foldContains(e.Name+" "+groups[i].name, q) {
					keep = append(keep, e)
				}
			}
			groups[i].ext = keep
		}
	}
	if q != "" {
		var kept []projectGroup
		for _, g := range groups {
			if len(g.entries) > 0 || len(g.ext) > 0 {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	if q == "" {
		d.syncRCNames()
		d.rowPlace = make(map[string]string, len(entries))
		d.groups = d.groups[:0]
		for _, g := range groups {
			bg := bookGroup{key: projectPrefix + g.name, title: g.name}
			for _, e := range g.entries {
				bg.ids = append(bg.ids, e.id)
				d.rowPlace[e.id] = bg.key
			}
			d.groups = append(d.groups, bg)
		}
		d.syncBookmarks()
		d.syncTabs()
		d.phoneChanged()
	}
	var rows []sideRow
	for _, g := range groups {
		key := projectPrefix + g.name
		open := q != "" || d.isOpen(key)
		rows = append(rows, sideRow{header: g.name, key: key, count: len(g.entries) + len(g.ext), open: open})
		if open {
			for _, e := range g.entries {
				rows = append(rows, sideRow{id: e.id, depth: 1, parent: key})
			}
			for i := range g.ext {
				rows = append(rows, sideRow{depth: 1, parent: key, ext: &g.ext[i]})
			}
		}
	}
	d.drawSidebarRows(d.withSidebarAgents(rows))
}

func (d *desk) drawSidebarRows(rows []sideRow) {
	d.rows = rows
	d.suppress = true
	d.list.UnselectAll()
	for i, r := range rows {
		switch {
		case r.header != "" && r.depth == 0:
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
	return foldContains(t, q) || foldContains(cwd, q)
}

func (d *desk) refreshSidebar() {
	d.listTheme.Refresh()
	d.syncTabs()
	d.syncRowCaptions()
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
	d.autoBtn = newAutoChip(func() { d.setAutoApprove(d.current, false) })
	d.acctBtn = widget.NewButtonWithIcon("Manage sign-ins", theme.AccountIcon(), d.accountMenu)
	d.reloadBtn = d.newRemoteReloadButton("Reload", func() *deskSession { return d.current })
	d.headPad = canvas.NewRectangle(color.Transparent)
	header := container.NewBorder(nil, nil, container.NewHBox(d.headPad, container.NewCenter(d.layoutButtons()), padLeft(12)), container.NewHBox(container.NewCenter(d.reloadBtn), container.NewCenter(d.rcStatus), container.NewCenter(d.phoneButton()), container.NewCenter(d.acctBtn)),
		layout.NewSpacer())
	d.bookRow = d.buildBookmarks()
	d.tabRow = d.buildTabs()
	d.usage = newUsagePanel(d)
	d.usageRow = d.usage.buildStrip()
	d.agentStrip = newAgentStrip(d) // subagent strip under the tabs, both layouts (agentstrip.go)
	headerBox := container.NewVBox(container.NewPadded(header), d.note, d.buildPauseBanner(), d.buildQuillMoveBanner(), d.buildKmemSwitchBanner(), d.buildUpdLine(), d.bookRow, d.tabRow, d.agentStrip.root, d.usageRow, widget.NewSeparator())
	d.headerBox = headerBox

	d.input = newSendEntry(func(t string) {
		if !d.sendTargeted(d.input, t, false) {
			d.send(t)
		}
	})
	d.input.later = d.afterUI
	d.input.ac = d.newAutocorrector()
	d.input.autoFormat = d.autoFormatOn
	d.input.known = func() func(string) bool { return d.knownFor(d.input, d.current) }
	d.input.onFix = func(msg string) { d.flashHint(d.folder, msg) }
	d.cmds = newCmdMenu(d)
	d.input.onKey = d.cmds.key
	d.input.menuOpen = d.cmds.visible
	d.input.OnChanged = func(t string) {
		d.lastTyped = time.Now()
		d.noteActivity(d.current)
		if d.targeted(d.input) == "" && (d.current == nil || d.current.qedit == nil) {
			d.cmds.update(t)
		} else {
			d.cmds.hide()
		}
	}
	d.input.onSendNow = func(t string) {
		if !d.sendTargeted(d.input, t, true) {
			d.sendNow(t)
		}
	}
	d.input.onActivity = func() { d.noteActivity(d.current) }
	d.input.onHistory = func(k *fyne.KeyEvent) bool {
		if r := d.targeted(d.input); r != "" {
			return d.historyKey(d.input, d.targetSession(r), k)
		}
		return d.historyKey(d.input, d.current, k)
	}
	d.input.onFocus = d.clearSelections
	d.input.onEscape = func() bool {
		if d.targeted(d.input) != "" {
			return d.assistBoxEscape()
		}
		return d.escape(d.current)
	}
	d.input.hint = "Message Claude"
	d.input.SetPlaceHolder(d.input.hint)
	if d.app.Preferences().Bool("centerInput") {
		d.input.Alignment = fyne.TextAlignCenter
	}
	d.modelSel = widget.NewSelect(modelChoices, func(v string) {
		if s := d.messageSession(d.input, d.current); !d.suppress && s != nil {
			d.setModel(s, v)
		}
	})
	d.modeSel = widget.NewSelect(modeChoices, func(v string) {
		if s := d.messageSession(d.input, d.current); !d.suppress && s != nil {
			d.setMode(s, v)
		}
	})
	d.backendBtn = widget.NewButton(backendOnline, func() {
		d.showMessageBackend(d.input, d.current, d.backendBtn, d.updateChrome)
	})
	d.folder = mutedText("⏎ send · Ctrl+⏎ stop & send · Shift+⏎ new line · Ctrl+T new session · Ctrl+1-9 tabs · / commands · Ctrl+Shift+O Quill · Ctrl+Shift+K kmem", fontMeta)
	d.stats = mutedText("", fontMeta)
	d.stopBtn = widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		if s := d.messageSession(d.input, d.current); s != nil {
			d.interrupt(s)
		}
	})
	d.stopBtn.Importance = widget.DangerImportance
	d.sendBtn = widget.NewButtonWithIcon("", theme.MailSendIcon(), func() {
		if !d.sendTargeted(d.input, d.input.Text, false) {
			d.send(d.input.Text)
		}
	})
	d.sendBtn.Importance = widget.HighImportance
	d.stt = newSpeech(d)
	d.chipsBtn = menuButton(activityChipsLabel, theme.MenuDropUpIcon(), d.chipItems)
	d.modelBtn = menuButton("", nil, func() []*fyne.MenuItem {
		if s := d.messageSession(d.input, d.current); s != nil {
			return d.modelItems(s)
		}
		return nil
	})
	d.modeBtn = menuButton("", nil, func() []*fyne.MenuItem {
		if s := d.messageSession(d.input, d.current); s != nil {
			return d.modeItems(s)
		}
		return nil
	})
	d.reasoning = d.newReasoningControl()
	controls := container.NewBorder(nil, nil, container.NewHBox(d.backendBtn, d.modelBtn, d.modeBtn, d.reasoning.root, d.autoBtn), d.chipsBtn)
	d.targetLbl = caption("")
	d.targetLbl.Truncation = fyne.TextTruncateEllipsis
	d.targetLbl.Hide()
	cue := d.newCue(d.input, d.sendBtn)
	inputCard := bg(colInput, 12, container.NewBorder(d.targetLbl, controls, cue.chip, container.NewVBox(d.stopBtn, layout.NewSpacer(), d.stt.btn, cue.send), d.input))
	cue.attach(inputCard)
	d.verLine = d.newVerLine()
	d.statsRow = container.New(hintsLayout{}, d.verLine.slot, d.folder)
	statsRow := container.NewGridWithColumns(2, container.New(&clipLayout{}, d.stats), d.statsRow)
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
	// The assistant box covers the chat history only (all panes' in split
	// view); docked, it sits right of the main area (assistbox.go).
	box := d.box()
	box.area = container.New(&boxAreaLayout{d}, d.chatHost)
	d.usageBox = container.New(usageLayout{d}, box.area, d.usage.root, spare)
	d.mainPane = container.New(glideBorder{}, headerBox, bottom, d.usageBox)
	d.mainRoot = container.New(&assistRootLayout{d}, d.mainPane, box.panel, box.handle, box.layer, box.ghostL)
	d.updateChrome()
	return d.mainRoot
}

func (d *desk) updateChrome() {
	d.syncAgentStripTarget()
	d.keepView()
	defer d.syncMainOperatorControls()
	defer d.syncQuillMoveBanner()
	defer d.syncPlanMirrors()
	if d.usage != nil {
		defer d.usage.update()
	}
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
	d.syncReasoningControl(d.reasoning, d.messageSession(d.input, s))
	d.suppress = true
	defer func() { d.suppress = false }()
	if d.main.in != d.input || d.main.role == "" {
		updateEngineHint(d.input, s)
	}
	if syncRemoteReload(d.reloadBtn, s) && d.headerBox != nil {
		d.headerBox.Refresh()
	}
	if s == nil {
		setLabel(d.title, "Quill")
		setText(d.subtitle, "")
		d.note.Hide()
		d.rcStatus.Hide()
		d.suggest(d.input, "")
		d.input.Disable()
		d.sendBtn.Disable()
		d.stopBtn.Disable()
		d.modelSel.Disable()
		d.modeSel.Disable()
		d.backendBtn.Disable()
		d.modelBtn.Disable()
		d.modeBtn.Disable()
		d.syncAutoChip(d.autoBtn, nil)
		setText(d.stats, "")
		return
	}
	title, _, st := d.rowText(s.id)
	setLabel(d.title, title)
	sub := folderLabel(s.cwd)
	if st != stNone {
		sub += "  ·  " + st.String()
	}
	if s.codex {
		sub += "  ·  Codex"
	} else if m := session.LocalModel(s.model); m != "" {
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
	if s.codex {
		d.rcStatus.Hide()
		setCodexControls(s, d.modelSel, d.modeSel)
	}
	if s.loading {
		d.sendBtn.Disable()
	}
	if held || s.takeover {
		d.suggest(d.input, "")
	} else {
		d.suggest(d.input, d.shownSuggestion(s))
	}
	setOptions(d.modelSel, d.modelOptions(s))
	setOptions(d.modeSel, d.composerModeOptions(s))
	if setBackendButton(d.backendBtn, s) && d.mainPane != nil {
		relayoutTree(d.mainPane)
	}
	if d.modelSel.Selected != s.model {
		d.modelSel.SetSelected(s.model)
	}
	if d.modeSel.Selected != s.mode {
		d.modeSel.SetSelected(s.mode)
	}
	if s.running() && (s.conv.Busy || s.proc.Busy() || len(s.conv.PendingPermissions()) > 0 || s.conv.Background() > 0) {
		d.stopBtn.Enable()
	} else {
		d.stopBtn.Disable()
	}
	setText(d.stats, statsLine(s))
	mirror := func(b *widget.Button, sel *widget.Select) {
		if sel.Disabled() {
			b.Disable()
		} else {
			b.Enable()
		}
	}
	mirror(d.modelBtn, d.modelSel)
	mirror(d.modeBtn, d.modeSel)
	if d.syncBarButtons(s) && d.mainPane != nil {
		relayoutTree(d.mainPane)
	}
	if d.syncAutoChip(d.autoBtn, s) && d.mainPane != nil {
		relayoutTree(d.mainPane)
	}
	if d.chat != nil && (d.stt == nil || d.stt.state == sttIdle) {
		d.chat.setStatus(d.current)
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
	s := &deskSession{codex: info.Entrypoint == codex.Entrypoint, id: id, cwd: info.Cwd, title: info.Title, persisted: true, model: "default", mode: "default", conv: chat.New()}
	if p, ok := d.terms[id]; ok {
		pp := p
		s.terminal = &pp
	}
	d.sessions[id] = s
	return s
}

func (d *desk) open(id string) {
	d.noteActivity(d.sessions[id])
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
	if d.hiddenID(id, s.cwd) {
		// A hidden assistant never gets a tab: its role's host (the box) opens.
		d.openHiddenRole(d.hiddenRoleOf(id, s.cwd))
		return
	}
	d.closeAssistOverlay() // a tab or sidebar row closes the box first, then switches (Q16)
	if r := s.remote; r != nil && r.fetched != r.rev {
		defer r.link.fetchItems(s)
	}
	d.cancelPendingEnd(s)
	if d.offerResume(id) {
		return
	}
	if p := d.paneFor(s); p != nil && p != d.focused {
		d.focusPane(p)
		return
	}
	d.current = s
	s.touched = time.Now()
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
				d.noteWindow(s)
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

func (d *desk) newSessionCwd() string {
	home, _ := os.UserHomeDir()
	cwd := home
	if d.current != nil && d.current.cwd != "" {
		cwd = d.current.cwd
	}
	return cwd
}

func (d *desk) newSessionDialog() {
	if d.app.Preferences().Bool(prefNewCodex) {
		d.newCodexSessionIn(d.newSessionCwd())
		return
	}
	d.newClaudeSession()
}

func (d *desk) newClaudeSession() {
	prefs := d.app.Preferences()
	s := d.createSession(d.newSessionCwd(), prefs.StringWithFallback("newModel", "default"), prefs.StringWithFallback(prefNewMode, "auto"))
	name := d.nextSessionName()
	d.org.Titles[s.id] = name
	d.saveOrg()
	d.updateChrome()
	d.syncTabs()
	d.promptNewSessionName(s, name)
}

func (d *desk) promptNewSessionName(s *deskSession, name string) {
	if d.kmemMemory() {
		s.pickPending = true
		d.chat.rebuild()
		return
	}
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
	return d.addSessionWithBackend(cwd, model, mode, show, false)
}

func (d *desk) addSessionWithBackend(cwd, model, mode string, show, useCodex bool) *deskSession {
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
	s := &deskSession{codex: useCodex, id: id, cwd: cwd, model: model, mode: mode, conv: chat.New(), loaded: true, created: time.Now()}
	d.sessions[id] = s
	if !show {
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
	return s != nil && s.running() && s.pendingRevision == nil
}

func (d *desk) ensureProcess(s *deskSession) error {
	if s.remote != nil {
		return errors.New("this session runs on " + s.remote.link.conf.Name)
	}
	replacingBridge := s.running()
	if replacingBridge && (s.agentHandoff != nil || !d.codexBridgeNeedsRefresh(s)) {
		return nil
	}
	if s.terminal != nil {
		return fmt.Errorf("this session is open in a terminal")
	}
	if name := d.kmemTopicFor(s); name != "" && d.kmemAvailable() {
		if owner := d.runningKmemOwner(name, s); owner != nil {
			return fmt.Errorf("kmem topic %q is already in use by another %s session", name, owner.engineName())
		}
		if _, external := d.topicExternal(name); external {
			return fmt.Errorf("kmem topic %q is already in use outside Quill Desktop", name)
		}
	}
	if err := d.checkOllama(s.model); err != nil {
		s.failed = err.Error()
		return err
	}
	var opts session.Options
	if s.codex {
		var err error
		opts, err = d.codexProcessOptions(s)
		if err != nil {
			s.failed = err.Error()
			return err
		}
	} else {
		opts = d.processOptions(s)
		d.warnInsecureOllama(s)
	}
	if replacingBridge {
		d.unkeepSession(s.id)
		s.stopping = true
		if err := s.proc.Stop(time.Second); err != nil && !s.proc.Exited() {
			s.stopping = false
			return err
		}
	}
	s.failed = ""
	s.stopping = false
	s.rcOn, s.rcURL, s.rcName = false, "", ""
	var p *session.Process
	p, err := session.Start(opts, func(ev *protocol.Event) {
		d.do(func() {
			if s.proc == p {
				d.handleEvent(s, ev)
			}
		})
	})
	if err != nil {
		s.failed = err.Error()
		return err
	}
	s.conv.ClearBackground()
	s.claudeCoordinationSet = false
	s.resumeAt = ""
	s.codexUpgrading = replacingBridge && s.codex
	d.adopt(s, p)
	if !s.codex {
		d.autoRemote(s)
	}
	return nil
}

// processOptions: how ensureProcess starts s's Claude process.
func (d *desk) processOptions(s *deskSession) session.Options {
	kmemMem := d.kmemMemory() || hiddenRole(s) == roleKmem && d.kmemAvailable()
	opts := session.Options{Binary: d.cfg.ClaudeBin, Cwd: s.cwd, Model: s.model, Mode: s.mode, Chrome: s.chrome, AddDirs: d.sessionDirs(s), ResumeAt: s.resumeAt, Prompt: d.sessionPromptFor(s, kmemMem),
		Env: append(append(d.localEnv(s.model), checkpointEnv), sessionMemoryEnv(kmemMem)...)}
	if s.persisted {
		opts.Resume = s.id
	} else {
		opts.SessionID = s.id
	}
	opts.Prompt += operatorRouteHint
	if isHiddenSession(s) {
		// Other sessions find a hidden assistant by this name (ListAgents,
		// SendMessage), not by its folder ("kmem-window-51").
		opts.Name = kmemPeerName
		if hiddenRole(s) == roleQuill {
			opts.Name = quillOpName
		}
	}
	return opts
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
	if s.codex && ev.Type == "system" && ev.Subtype == "init" {
		s.codexDefaultModel = ev.Model
	}
	bridgeReady := s.codexUpgrading && ev.Type == "system" && ev.Subtype == "init"
	if bridgeReady {
		s.codexUpgrading = false
	}
	waitStarted := s.codex && ev.Type == "system" && ev.Subtype == protocol.CodexAgentWaitStarted
	if s.codex {
		switch {
		case waitStarted:
			s.codexWaiting = true
		case ev.Type == "system" && ev.Subtype == protocol.CodexWorkersStarted:
			s.codexWorkers = true
		case ev.Type == "system" && ev.Subtype == protocol.CodexWorkersEnded:
			s.codexWorkers = false
		case ev.Type == "result" && ev.Subtype != protocol.ResultContinuing || ev.Type == "system" && ev.Subtype == protocol.CodexAgentWaitEnded:
			s.codexWaiting = false
		case ev.Type == "system" && ev.Subtype == protocol.CodexParentTurnStarted:
			s.inTurn, s.codexWaiting = true, false
		}
	}
	finishedCodexPlan := s.codex && d.planFirst && ev.Type == "result" &&
		!turnFailed(ev) && !strings.HasPrefix(ev.TerminalReason, "aborted") &&
		s.pendingRevision == nil && s.agentHandoff == nil && !codexNativeApprovalPending(s)
	items := s.conv.Apply(ev)
	if ev.Type == "result" && ev.Subtype == protocol.ResultContinuing {
		s.conv.Busy = true
	}
	s.noteActivity(statusNow())
	d.syncTargetSuggestion(s) // the main message box messages this assistant (mainbox.go)
	if ev.Type == "system" && ev.Subtype == "init" && ev.SessionID != "" && ev.SessionID != s.id {
		s.conv.AddNotice("Claude reported a different session id: " + ev.SessionID)
	}
	if !s.codex {
		items = append(items, d.planGate(s, ev, items)...)
	} else if finishedCodexPlan {
		items = append(items, addCodexPlanCard(s)...)
	}
	if ev.Type == "result" || ev.Type == "assistant" {
		d.noteWindow(s)
	}
	if ev.Type == "result" {
		if !s.codex && s.agentHandoff == nil {
			d.kmemAfterTurn(s)
			d.checkAutoHandoff(s)
		} else if s.codex {
			s.inTurn = s.conv.Busy
		}
		if ev.Subtype != protocol.ResultContinuing {
			d.agentHandoffResult(s, ev)
		}
		if s.clearAfterTurn {
			d.clearNow(s)
		}
		s.restarting = false
	}
	d.checkLimitMessage(s, items)
	d.attention(s, ev, items)
	if ev.Type == "control_request" || ev.Type == "control_cancel_request" || ev.Type == "result" {
		for _, it := range items {
			items = append(items, codexChoiceBeforeQuestion(s, it)...)
		}
	}
	switch ev.Type {
	case "control_request", "control_cancel_request", "result":
		d.keepSession(s)
	default:
		d.keepGate(s)
	}
	if d.current == s {
		d.queueUI(s, items)
	} else if p := d.paneFor(s); p != nil {
		p.queue(items)
	} else if v := d.viewFor(s); v != nil {
		d.queueWinUI(s, items)
	}
	if ev.Type == "rate_limit_event" {
		d.setRateLimit(ev.RateLimit)
	}
	if s.codex && ev.Type == "codex_token_usage" {
		d.noteWindow(s)
		if d.current == s || d.targetSession(d.targeted(d.input)) == s {
			d.updateChrome()
		}
		if pane := d.paneFor(s); pane != nil {
			d.syncPaneControls(pane)
		}
	}
	codexStatus := s.codex && ev.Type == "system" && (waitStarted || ev.Subtype == protocol.CodexAgentWaitEnded ||
		ev.Subtype == protocol.CodexParentTurnStarted)
	urgent := codexStatus || ev.Type == "result" || ev.Type == "control_request" || ev.Type == "prompt_suggestion" || ev.Type == "system" && ev.Subtype == "init"
	if urgent {
		if d.current == s {
			d.flushUI()
		} else if d.kmemWin.session() == s {
			d.flushWinUI()
		}
		d.refreshSidebarSoon()
	}
	if urgent || ev.Type == "user" || ev.Type == "control_cancel_request" {
		// Any assistant's badge and button (not only kmem's): a question or
		// plan came, or was answered, while its box is closed (t98af12).
		d.assistEvent(s)
		d.syncPlanMirrors() // another session's plan card came or was answered (planmirror.go)
	}
	if bridgeReady || waitStarted || s.codex && ev.Type == "control_cancel_request" {
		d.flushQueue(s)
	}
	if bridgeReady {
		d.loadComposerCatalog(s, false)
		d.refreshComposerFor(s)
		d.trySwap()
	}
	if ev.Type == "result" && ev.Subtype != protocol.ResultContinuing {
		if !d.finishApprovalInterrupt(s) {
			d.refreshLegacyCodexBridge(s)
			d.flushQueue(s)
		}
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

// queueWinUI batches the kmem window's chat updates per frame, like queueUI
// does for the main chat.
func (d *desk) queueWinUI(s *deskSession, items []*chat.Item) {
	if d.winPendingFor != s {
		d.flushWinUI()
		d.winPendingFor = s
	}
	d.winPending = append(d.winPending, items...)
	if d.winFlushTimer == nil {
		d.winFlushTimer = time.AfterFunc(uiFrame, func() { d.do(d.flushWinUI) })
	}
}

func (d *desk) flushWinUI() {
	if d.winFlushTimer != nil {
		d.winFlushTimer.Stop()
		d.winFlushTimer = nil
	}
	items, owner := d.winPending, d.winPendingFor
	d.winPending, d.winPendingFor = nil, nil
	if owner == nil || len(items) == 0 {
		return
	}
	if v := d.viewFor(owner); v != nil && v != d.chat && v.s == owner {
		v.update(dedupItems(items))
		v.setStatus(owner)
	}
}

// dedupItems drops repeats, keeping the first position of each item.
func dedupItems(items []*chat.Item) []*chat.Item {
	seen := make(map[*chat.Item]bool, len(items))
	out := items[:0:0]
	for _, it := range items {
		if !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}

func (d *desk) handleExit(s *deskSession, p *session.Process, err error) {
	d.ownPIDs.Delete(p.PID())
	if s.proc != p {
		return
	}
	d.unkeepSession(s.id)
	if h := s.agentHandoff; h != nil && !s.stopping && h.phase != handoffVerifying {
		d.failAgentHandoff(s, h, errors.New("the source agent stopped"))
	}
	s.restarting = false
	if r := s.pendingRevision; r != nil && r.proc == p {
		d.failApprovalRevision(s, r)
	}
	defer func() {
		if isHiddenSession(s) {
			d.assistantsExited(s)
		}
	}()
	var items []*chat.Item
	items = append(items, s.conv.CancelPending()...)
	for _, it := range s.conv.Items {
		if it.Streaming {
			it.Streaming = false
			items = append(items, it)
		}
	}
	s.conv.Busy, s.codexWaiting, s.codexWorkers, s.codexUpgrading = false, false, false, false
	s.inTurn, s.planWait = false, false
	s.agentGrants, s.agentCalls, s.agentIDs = nil, nil, nil // the process and its subagents are gone
	s.planWork, s.planAgents, s.turnCovered, s.autoTurn = nil, nil, false, false
	s.queuedResumes, s.peerTurn = nil, false
	s.refusedAgents, s.subRefused = nil, nil
	if s.autoApprove {
		endAutoApprove(s) // the session ended or restarts (autoapprove.go)
		defer d.syncAutoApprove()
	}
	s.rcOn, s.rcURL, s.rcName = false, "", ""
	if err != nil && !s.stopping {
		msg := s.engineName() + " process exited: " + err.Error()
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
	} else if v := d.viewFor(s); v != nil {
		v.update(items)
	}
	d.refreshSidebar()
	if s.restartActive {
		s.proc = nil
	}
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
	if d.saveQueueEdit(s, d.inputFor(s), text) {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" || pendingRevisionMatches(s, text) {
		return
	}
	if s != nil && s.remote != nil {
		// Everything typed in a remote tab, /commands included, goes to that
		// PC; its Quill answers or refuses (desktop-only commands) there.
		d.clearInputFor(s, text)
		d.sendTo(s, text)
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
	if d.reviseTypedApproval(s, d.inputFor(s), text) {
		return
	}
	if !s.codex && d.usagePaused && !strings.HasPrefix(text, "/") {
		text = pausedNote + text
	}
	if s.shouldQueue() {
		d.enqueue(s, text)
		return
	}
	d.sendTo(s, text)
}

func (d *desk) takeOver(s *deskSession, t live.Process, text string) {
	d.takeOverThen(s, t, text, nil)
}

// releaseProc ends the terminal's Claude process (a test hook; never a real
// process in tests).
func (d *desk) releaseProc(t live.Process) error {
	if d.releaseFn != nil {
		return d.releaseFn(t)
	}
	return d.finder.Release(t, d.cfg.ClaudeBin, 5*time.Second)
}

// takeOverThen: takeOver with a callback (UI thread) when it ended. Empty
// text takes the session over without sending anything: the process just
// resumes (the topic Take over, docs/design/topic-tabs.md §3.2).
func (d *desk) takeOverThen(s *deskSession, t live.Process, text string, after func(error)) {
	s.takeover = true
	if text != "" {
		d.input.SetText("")
	}
	d.updateChrome()
	path := ""
	if info, ok := d.infos[s.id]; ok {
		path = info.Path
	}
	go func() {
		err := d.releaseProc(t)
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
				if text != "" && d.current == s && d.input.Text == "" {
					d.input.SetText(text)
				}
				d.updateChrome()
				if after != nil {
					after(err)
				}
				return
			}
			s.terminal = nil
			delete(d.terms, s.id)
			if conv != nil {
				s.conv = conv
				d.noteWindow(s)
				s.loaded = true
				s.loadedMod = mod
				if d.current == s {
					d.chat.show(s)
				}
			}
			if text == "" {
				err := d.ensureProcess(s)
				if err != nil {
					d.chatFor(s, []*chat.Item{s.conv.AddError("Could not start " + s.engineName() + ": " + err.Error())})
				}
				d.updateChrome()
				if after != nil {
					after(err)
				}
				return
			}
			d.sendTo(s, text)
			if after != nil {
				after(nil)
			}
		})
	}()
}

func (d *desk) sendTo(s *deskSession, text string) {
	if s.agentHandoff != nil || s.restartActive || s.pendingRevision != nil || s.codexUpgrading {
		d.enqueue(s, text)
		return
	}
	d.deliverTurn(s, text, s.steerCodexMessage())
}

func (d *desk) sendTurn(s *deskSession, text string) bool {
	return d.deliverTurn(s, text, false)
}

func (d *desk) deliverTurn(s *deskSession, text string, steer bool) bool {
	return d.deliverTurnWithOrigin(s, text, steer, false)
}

func (d *desk) deliverTurnWithOrigin(s *deskSession, text string, steer, peer bool) bool {
	typed := text
	cardGo := !peer && s.cardGo // the plan card's own Approve (answerPlanMsg)
	if !peer {
		s.cardGo = false
		defer d.autoStartFailed(s) // auto-approve this message turned on, if it never went out
	}
	if s.remote != nil {
		d.remoteWrite(s, "send", map[string]string{"text": text})
		return true
	}
	s.coldStamp = time.Time{}
	if err := d.ensureProcess(s); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not start " + s.engineName() + ": " + err.Error())})
		d.updateChrome()
		return false
	}
	if s.codexUpgrading {
		d.enqueue(s, text)
		return true
	}
	if s.kmemNew {
		s.kmemNew = false
		if k := d.org.Kmem[s.id]; k != "" {
			text = kmemIntro(k) + text
		}
	}
	if s.forkOf != "" {
		text = forkIntro(s.forkOf, d.org.Kmem[s.id]) + text
		s.forkOf = ""
	}
	if !peer && !s.codex && d.planFirst {
		// "Approved." is the card's own text: it keeps the plan going when the
		// card sent it, or when a plan or approval question waits for it; typed
		// with nothing pending it is an ordinary message (user 2026-11-23).
		planGo := isPlanGo(text) && (cardGo || approvesPlan(s, text))
		nearMiss := !approvesPlan(s, text) && !isPlanGo(typed) && !isRetry(typed) && nearApproval(typed) &&
			(hasPendingPlan(s) || s.conv.AsksApproval() || s.planWait)
		if approvesPlan(s, text) && !isPlanGo(text) {
			d.approveByText(s)
		} else if !planGo && !isRetry(text) && !s.inTurn {
			endPlan(s)
			s.planWait = false // an approval question kept past a blocked call is answered otherwise
			d.chatFor(s, s.conv.CancelPlans())
		}
		if nearMiss {
			d.notify(s, notCountedNotice)
		}
		if !isRetry(text) {
			s.planRevoked = false // the user's newest message decides from here
		} else if s.runAll && !s.inTurn {
			s.turnCovered = true // the user's retry continues the approved plan
		}
	}
	// The auto-approve-off note goes to Claude with the message, not into
	// its bubble, and stays pending until a send succeeds.
	note := autoNoteFor(s, text)
	guide, coordinationChanged := d.coordinationUpdate(s)
	if coordinationChanged {
		update := guide
		if update == "" {
			update = "Coordinate with subagents is off for new work. Continue already approved worker tasks."
		}
		note += "\n\n[Quill session setting update] " + update
	}
	if !s.codex && s.resumeNext == "" {
		s.resumeNext = d.firstTurnKmemName(s, text)
	}
	if s.resumeNext != "" || !isRetry(text) {
		s.resumeAs, s.resumeNext = s.resumeNext, ""
	}
	s.lastSent = text + note
	if peer {
		notUserMessage(s, text)
	} else {
		s.autoPeer = false
	}
	s.inTurn = true
	defer d.keepGate(s) // the message may have ended or started the plan's approval
	send := s.proc.Send
	if steer {
		send = s.proc.Steer
	}
	var err error
	if s.codex && s.codexComposerAvailable() {
		err = s.proc.SendCodex(text+note, steer, d.codexTurnSettings(s))
	} else {
		err = send(text + note)
	}
	if err != nil && (peer || s.codex && cardGo) {
		s.inTurn = false
		if peer {
			turnEnded(s)
		}
		d.chatFor(s, []*chat.Item{s.conv.AddError("Send failed: " + err.Error())})
		return false
	}
	it := s.conv.AddUser(text)
	d.refreshChoiceCards(s, it)
	if s.title == "" {
		s.title = oneLine(text, 60)
	}
	if err != nil {
		d.chatFor(s, []*chat.Item{it, s.conv.AddError("Send failed: " + err.Error())})
		return false
	}
	s.autoStarting = false
	if coordinationChanged {
		s.claudeCoordination, s.claudeCoordinationSet = guide, true
	}
	if note != "" {
		s.autoNote = ""
	}
	s.conv.Busy = true
	if d.current == s && strings.TrimSpace(d.input.Text) == text {
		d.input.SetText("")
	}
	items := []*chat.Item{it}
	if s.codex && !cardGo && !notFromUser(typed) {
		items = append(s.conv.CancelPlans(), it)
	}
	d.chatFor(s, items)
	if d.current == s {
		d.chat.scrollBottom()
	}
	d.updateChrome()
	d.rebuildRows()
	return true
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func (d *desk) chatFor(s *deskSession, items []*chat.Item) {
	defer d.syncPlanMirrors()
	if d.current == s {
		d.chat.update(items)
	} else if p := d.paneFor(s); p != nil {
		p.view.update(items)
	} else if v := d.viewFor(s); v != nil {
		v.update(items)
	}
}

func (d *desk) sendNow(text string) {
	s := d.current
	if s == nil {
		return
	}
	if s.agentHandoff != nil {
		d.send(text)
		return
	}
	if d.saveQueueEdit(s, d.input, text) || d.reviseApproval(s, d.input, text) {
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

// interrupt is Stop / Esc / stop & send: it ends the current turn only and
// leaves the session's running background subagents and tasks alone (the
// interrupt carries spare_background_tasks, see protocol.Interrupt).
func (d *desk) interrupt(s *deskSession) {
	if s == nil {
		return
	}
	if s.remote != nil {
		d.remoteWrite(s, "interrupt", map[string]any{})
		return
	}
	if r := s.pendingRevision; r != nil {
		r.text, r.input = "", nil
		return
	}
	hadPrompt := len(s.conv.PendingPermissions()) > 0
	d.dismissApprovalProposal(s)
	p := s.proc
	if p == nil || p.Exited() || !s.conv.Busy && !p.Busy() && len(s.conv.PendingPermissions()) == 0 {
		return
	}
	if hadPrompt {
		r := &approvalRevision{proc: p}
		s.pendingRevision = r
		d.requestApprovalInterrupt(s, r)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Interrupt(ctx)
	}()
}

// interruptAll ends the turn and the session's background subagents and
// tasks ("Stop everything" in the background tasks menu). Local sessions only.
func (d *desk) interruptAll(s *deskSession) {
	if s == nil || s.remote != nil || s.proc == nil || s.proc.Exited() {
		return
	}
	revokePlan(s)
	d.keepGate(s)
	p := s.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.InterruptAll(ctx)
	}()
}

func (d *desk) answer(s *deskSession, it *chat.Item, dec protocol.Decision) {
	if s != nil && s.remote != nil {
		d.remoteDecision(s, it, dec)
		return
	}
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if it.PermState != chat.PermPending || !d.canAnswer(s) {
		return
	}
	if dec == protocol.AllowAlways && !nativeSessionApprovalAvailable(it) {
		d.notify(s, "This request does not offer permission for the rest of the session.")
		return
	}
	if err := s.proc.Respond(it.RequestID, dec, it.Request); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not answer: " + err.Error())})
		return
	}
	if isPermissionDeny(it, dec) {
		revokePlan(s) // the user refused: the approval and its grants end
	}
	switch dec {
	case protocol.Allow:
		it.PermState = chat.PermAllowed
	case protocol.AllowAlways:
		it.PermState = chat.PermAllowedAlways
	default:
		it.PermState = chat.PermDenied
	}
	d.chatFor(s, append([]*chat.Item{it}, codexChoiceBeforeQuestion(s, it)...))
	if d.current == s {
		d.updateChrome()
	}
	d.assistEvent(s)
	d.refreshSidebar()
	d.keepSession(s)
	if s.codex {
		d.flushQueue(s)
	}
}

func isQuestion(it *chat.Item) bool {
	return it.ToolName == "AskUserQuestion"
}

func (d *desk) answerQuestions(s *deskSession, it *chat.Item, answers map[string]string) {
	if s != nil && s.remote != nil {
		d.remoteAnswers(s, it, answers)
		return
	}
	if s = d.cardOwner(s, it); s == nil {
		return
	}
	if it.PermState != chat.PermPending || !d.canAnswer(s) {
		return
	}
	if err := s.proc.Answer(it.RequestID, it.Request, answers); err != nil {
		d.chatFor(s, []*chat.Item{s.conv.AddError("Could not answer: " + err.Error())})
		return
	}
	d.dialogGate(s, it, answers)
	var lines []string
	for _, q := range protocol.ParseQuestions(it.Input) {
		if a, ok := answers[q.Question]; ok {
			lines = append(lines, q.Question+" → "+a)
		}
	}
	it.Text = strings.Join(lines, "\n")
	it.PermState = chat.PermAllowed
	d.chatFor(s, append([]*chat.Item{it}, codexChoiceBeforeQuestion(s, it)...))
	if d.current == s {
		d.updateChrome()
	}
	d.assistEvent(s)
	d.refreshSidebar()
	d.keepSession(s)
	if s.codex {
		d.flushQueue(s)
	}
}

func (d *desk) setModel(s *deskSession, model string) {
	if s.codex {
		if !s.codexComposerAvailable() || !contains(d.modelOptions(s), model) {
			return
		}
		s.model = model
		if !contains(d.codexEfforts(s), s.codexEffort) {
			s.codexEffort = ""
		}
		if !contains(d.composerModeOptions(s), s.mode) {
			s.mode = "default"
		}
		return
	}
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
		endAutoApprove(s)
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
	if s.codex {
		if s.codexComposerAvailable() && contains(d.composerModeOptions(s), mode) {
			s.mode = mode
		}
		return
	}
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
	} else if v := d.viewFor(s); v != nil {
		v.update([]*chat.Item{it})
	}
}

func (d *desk) autoRemote(s *deskSession) {
	if s.codex || isHiddenSession(s) {
		return // the kmem window's assistant never gets Remote Control
	}
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
	if s.codex {
		return
	}
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

// runningVisible: running sessions other than the kmem window's assistant
// (docs/design/kmem-window.md §4 #4).
func (d *desk) runningVisible() []*deskSession {
	var out []*deskSession
	for _, s := range d.runningSessions() {
		if !isHiddenSession(s) && !d.org.Hidden[s.id] {
			out = append(out, s)
		}
	}
	return out
}

// hiddenID and markHidden are in hidden.go.

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
	d.do(d.pruneStaleTabs)
	d.do(d.reopenLastTab)
	d.do(d.assistantsBoot)
	d.do(func() { d.pollPeerMessages(time.Now()) })
	go d.watchBinary(ctx)
	go d.glowLoop(ctx)
	go d.usageLoop(ctx)
	go d.watchUpdates(ctx)
	go d.watchKmemNeeds(ctx)
	go watchMemoryLimit(ctx)
	go d.followGuard(ctx)
	go d.fetchCommands()
	d.do(d.refreshCodexAccount)
	d.refreshAccount()
	d.do(func() { d.reconcileKmemNames(true, nil) }) // kmem handoffs renamed while Quill Desktop was closed
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.refresh()
			d.do(func() {
				now := time.Now()
				d.unloadIdle(now)
				d.pollSessionRestarts()
				d.pollPeerMessages(now)
			})
			if n++; n%12 == 0 {
				d.do(d.refreshCodexAccount)
				d.refreshAccount()
				d.do(func() { d.reconcileKmemNames(false, nil) })
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
	d.rememberGeom()
	running := d.runningSessions()
	if len(running) == 0 {
		d.rememberTopicsAtQuit()
		d.app.Quit()
		return
	}
	checks := d.quitCheckSessions()
	msg := quitMessage(len(d.runningVisible()), len(running)-len(d.runningVisible()), len(checks))
	dialog.ShowCustomConfirm("Stop running sessions?", "Stop and quit", "Cancel", widget.NewLabel(msg), func(ok bool) {
		if !ok {
			return
		}
		d.quitConfirmed(checks)
	}, d.win)
}

// quitConfirmed: the topic sessions in checks run their end-of-session check
// at once (endcheck.go); a topic that does not pass is listed and needs a
// second confirm (decision 22). Then the kmem assistant saves its handoff and
// everything stops.
func (d *desk) quitConfirmed(checks []*deskSession) {
	d.rememberTopicsAtQuit()
	prog := dialog.NewCustomWithoutButtons("Stopping sessions...", widget.NewProgressBarInfinite(), d.win)
	prog.Show()
	stop := func() {
		go func() {
			d.stopAll(d.runningSessions())
			d.do(func() {
				prog.Hide()
				d.app.Quit()
			})
		}()
	}
	stopAll := func() {
		if len(d.runningSessions()) > len(d.runningVisible()) {
			// The assistants stop first (kmem saves its handoff, bounded by its 60 s cap).
			d.assistantsQuit(func() { d.do(stop) })
			return
		}
		stop()
	}
	if len(checks) == 0 {
		stopAll()
		return
	}
	d.quitEndChecks(checks, func(failed []quitFail) {
		if len(failed) == 0 {
			stopAll()
			return
		}
		prog.Hide()
		d.confirmQuitAnyway(failed, func() {
			prog.Show()
			stopAll()
		})
	})
}

// quitMessage: the quit confirmation for visible running sessions, the
// kmem window's assistant (hidden, 0 or 1) and the topic sessions among the
// visible ones that run the end-of-session check (checked).
func quitMessage(visible, hidden, checked int) string {
	var msg string
	if visible > 0 {
		msg = fmt.Sprintf("%d session(s) are still running in Quill Desktop. Closing stops them (their conversations are saved and can be resumed).", visible)
	}
	if checked > 0 {
		msg += " Each topic runs its end-of-session check first; a topic that does not pass is listed before anything stops."
	}
	if hidden > 0 {
		if msg != "" {
			msg += "\n"
		}
		msg += "The kmem assistant is running too; it will save its handoff first."
	}
	return msg
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
	if d.input != nil {
		d.alignInput(d.input)
	}
	if a := d.assist(); a.input != nil {
		d.alignInput(a.input)
	}
}

// alignInput gives a message box the "Typing" alignment preference.
func (d *desk) alignInput(e *sendEntry) {
	e.Alignment = fyne.TextAlignLeading
	if d.app.Preferences().Bool("centerInput") {
		e.Alignment = fyne.TextAlignCenter
	}
	e.Refresh()
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
	// The kmem assistant chat keeps its view while its window is closed.
	if a := d.assist(); a.view != nil {
		out = append(out, a.view)
	} else if w := d.kmemWin; w != nil && w.view != nil {
		out = append(out, w.view)
	}
	for _, v := range d.hostViews() {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func (d *desk) setCollapseChips(on bool) {
	if d.collapseChips == on {
		return
	}
	for _, v := range d.allViews() {
		if v == d.chat {
			for _, s := range d.sessions {
				if !isHiddenSession(s) {
					v.keepChipDefaults(s)
				}
			}
		}
		v.keepChipDefaults(v.s)
		for s := range v.cache {
			v.keepChipDefaults(s)
		}
	}
	d.collapseChips = on
	d.app.Preferences().SetBool("collapseChips", on)
}

func (v *chatView) keepChipDefaults(s *deskSession) {
	if s == nil {
		return
	}
	for _, it := range s.conv.Items {
		if _, ok := v.stepOpen[it]; !ok && it.Kind == chat.Tool {
			if v.stepOpen == nil {
				v.stepOpen = map[*chat.Item]bool{}
			}
			v.stepOpen[it] = !v.d.collapseChips
		}
	}
}

func (d *desk) setAllChips(open bool) {
	views := []*chatView{d.chat}
	role := d.assistBoxRole()
	if role == boxKmem && d.kmemWin != nil && d.kmemWin.codexShown {
		role = roleCodexKmem
	}
	if v := d.hostView(d.targetSession(role)); v != nil && v != d.chat {
		if d.assistBoxDocked() {
			views = append(views, v)
		} else {
			views = []*chatView{v}
		}
	}
	for _, v := range views {
		if v == nil || v.s == nil {
			continue
		}
		if v.stepOpen == nil {
			v.stepOpen = map[*chat.Item]bool{}
		}
		for _, it := range v.s.conv.Items {
			if it.Kind == chat.Tool {
				v.stepOpen[it] = open
			}
		}
	}
	if d.hideChips == open {
		d.setShowChips(open)
		return
	}
	for _, v := range views {
		if v != nil && v.s != nil {
			v.show(v.s)
		}
	}
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
	planAskMessage = "Quill plan-first approval: nothing was run. Before running anything that needs approval, list everything you are going to do for this request (every command, file change and tool, in order, with a short reason), then stop and wait. The user will approve the whole plan at once. If you have questions, end the plan with them numbered, one per line, options in each line and a recommended option marked \"(recommended)\"."
	// planGoMessage: the approval Quill sends, short on purpose (user
	// 2026-11-23): " Choice: …", " Answers: 2 → …; 3 → …." and " Also: …"
	// may follow. What an approval covers (planscope.go) is in the session
	// hint (planApprovalHint), not repeated per approval.
	planGoMessage = "Approved."
	// planGoLegacy: the approval text of earlier builds (still in
	// transcripts and logs); the gate counts it as before.
	planGoLegacy = "Plan approved. Go ahead and carry out the whole plan; every tool call for this request is pre-approved."
)

// planAsk: the plan-first refusal text; without the session hint (plan-first
// turned on mid-session) it carries a one-line reminder of what an approval covers.
func planAsk(s *deskSession) string {
	msg := planAskMessage
	if s != nil && s.autoApprove {
		msg = autoApproveRootAsk + msg
	}
	if s != nil && !s.approvalHint {
		msg += planGoReminder
	}
	if s != nil && s.autoNote != "" {
		msg += " " + strings.TrimSpace(s.autoNote) // auto-approve went off: tell any turn, not only the user's next message
	}
	return msg
}

func (d *desk) planGate(s *deskSession, ev *protocol.Event, items []*chat.Item) []*chat.Item {
	trackAgentCalls(s, ev)
	trackAutoWork(s, ev)
	out := noteBatch(s, ev)
	out = append(out, noteAgentTask(s, ev)...)
	return append(out, d.planGateEvent(s, ev, items)...)
}

func (d *desk) planGateEvent(s *deskSession, ev *protocol.Event, items []*chat.Item) []*chat.Item {
	switch {
	case ev.Type == "control_request" && ev.Request != nil && ev.Request.Subtype == "can_use_tool":
		var it *chat.Item
		for _, x := range items {
			if x.Kind == chat.Permission && x.RequestID == ev.RequestID {
				it = x
			}
		}
		if it == nil || it.PermState != chat.PermPending || !d.canAnswer(s) || isQuestion(it) {
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
		// A subagent stopped with its refused batch gets nothing more
		// (gatebatch.go).
		if r := it.Request; r.AgentID != "" && s.refusedAgents[r.AgentID] {
			if s.proc.DenyWith(it.RequestID, r, stoppedAgentMessage) == nil {
				it.PermState = chat.PermDenied
			}
			return nil
		}
		// A batch is decided as a whole: once plan-first refused one of its
		// calls, its other gated calls are refused too (ta43580), a
		// subagent's batch too.
		if d.planFirst && d.inRefusedBatch(s, it.Request) && !readOnlyRequest(it.Request, d.readOnlyExtraFor(s)) {
			if s.proc.DenyWith(it.RequestID, it.Request, planAsk(s)) == nil {
				it.PermState = chat.PermPlanAsked
				s.planWait = true
			}
			return nil
		}
		// Auto-approve (autoapprove.go): no plan needed until the user turns
		// it off, except for commands that run as root.
		if d.planFirst && autoCovers(s, it.Request) {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
			}
			return nil
		}
		// The approved plan covers this turn's calls (planscope.go has the
		// whole rule).
		if planCovers(s) || d.kmemSaveAllowed(s, it.Request) || d.kmemResumeAllowed(s, it.Request) || kmemReleaseAllowed(s, it.Request) {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
				if r := it.Request; r.AgentID == "" {
					planCall(s, r.ToolName, r.ToolUseID, r.Input)
				}
			}
			return nil
		}
		// An approved plan covers the subagents it started (user 2026-11-22),
		// tracked by subagent: a call made by one of them (or by an agent one
		// of them started) goes ahead until it finishes, even while the user
		// talks to this session meanwhile. The main session's own calls
		// follow the gate below.
		// A background subagent's events don't reach this stream, so its
		// calls are known by the agent_id its requests carry (agent gap,
		// 2026-11-22).
		regrantQueued(s, it.Request)
		if g, ok := grantedRequest(s, it.Request); ok {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
				if isAgentTool(it.Request) {
					addAgentCall(s, it.Request.ToolUseID, g)
				}
			}
			return nil
		}
		// Fallback for a call whose assistant event hasn't been seen yet (the
		// stream sends the tool_use before its can_use_tool request, but
		// don't count on it): while this session's own turn is over, a call
		// can only be a subagent's. A turn a peer message, a scheduled
		// trigger or a notification started is a turn too (autoTurn).
		// Not right after a restart either (gateUnsure): events lost in the
		// crash may have started a turn.
		// Never for a root command (ta43580).
		if _, known := s.agentCalls[it.Request.ToolUseID]; !known && !s.inTurn && !s.autoTurn && !s.gateUnsure && len(s.agentGrants) > 0 && !privilegedRequest(it.Request) {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
			}
			return nil
		}
		if !d.planFirst {
			return nil
		}
		// Reads, and with the gate split on (gatesplit.go) kmem bookkeeping
		// under the session's own name and Zomboid launches kmem-guard owns:
		// allowed, so they never refuse their batch and never stop a
		// subagent started next to them.
		if c := requestClass(it.Request, d.readOnlyExtraFor(s)); c != classGated {
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
			}
			return nil
		}
		// An approval Quill didn't see go out (the user's last message in
		// the transcript) counts in a turn the user started, never in one a
		// peer message, trigger or notification started, nor after a Stop or
		// refusal. Not once a call of this turn was refused either: that
		// message came before the refusal, so it doesn't answer it (ta43580:
		// the refused Write's planWait turned an earlier "yes" into approval
		// of the pkexec call next to it). Never for a root command.
		if !s.autoTurn && !s.planRevoked && !s.planWait && !privilegedRequest(it.Request) && d.approvedInTranscript(s) {
			d.approveNow(s)
			if s.proc.Respond(it.RequestID, protocol.Allow, it.Request) == nil {
				it.PermState = chat.PermAutoAllowed
				if r := it.Request; r.AgentID == "" {
					planCall(s, r.ToolName, r.ToolUseID, r.Input)
				}
			}
			return nil
		}
		if s.proc.DenyWith(it.RequestID, it.Request, planAsk(s)) == nil {
			it.PermState = chat.PermPlanAsked
			s.planWait = true
			return d.refuseBatch(s, it.Request)
		}
	case ev.Type == "user":
		// A subagent's own messages, peer messages and background-task
		// notifications are not the user: they neither approve nor end an
		// approved plan (Kmem Lock Audit, 2026-11-22: they re-armed the gate
		// between steps). A finished subagent's grant ends; what the turn
		// they start may do is notUserMessage's.
		if ev.ParentToolUseID != nil {
			return nil
		}
		for _, it := range items {
			if it.Kind != chat.User {
				continue
			}
			if notFromUser(it.Text) {
				notUserMessage(s, it.Text)
				continue
			}
			if !d.planFirst {
				continue
			}
			if approvesPlan(s, it.Text) {
				d.approveNow(s)
			} else if !isRetry(it.Text) && !s.inTurn {
				endPlan(s)
				s.planWait = false
				d.chatFor(s, s.conv.CancelPlans()) // the session moved on: the plan card is stale
			}
			s.inTurn = true
		}
	case ev.Type == "result":
		s.inTurn = false
		turnEnded(s)
		endBatches(s)
		s.releaseTasks = nil
		if s.restoreMode != "" {
			mode := s.restoreMode
			s.restoreMode = ""
			d.setMode(s, mode)
		}
		if s.runAllNext {
			s.runAll, s.runAllNext = true, false
		}
		// A turn that ends asking for approval gets the card even while an
		// earlier approval is still in force: Claude is asking about new work,
		// so that approval ends here (user 2026-11-21: no card and no autofill
		// after an "Approve this plan?" that followed an approved card).
		// A blocked call whose turn ends with another question (which of two
		// things, what was meant) gets no card: the question stays, with its
		// option buttons, and the plan comes after the answer (user
		// 2026-11-21: "Approve plan & run all" under a "which did you mean"
		// question).
		// An approval question asked earlier in the turn, before the blocked
		// call, still waits: the blocked call doesn't clear it, so the user's
		// pick of its Yes still approves (td0cabd, user 2026-11-24).
		if s.pendingRevision != nil || strings.HasPrefix(ev.TerminalReason, "aborted") {
			s.planWait = false
			return nil
		}
		asks := d.planFirst && s.conv.AsksApproval()
		if s.planWait && !asks && s.conv.AsksQuestion() {
			s.planWait = turnAskedApproval(s.conv)
			return nil
		}
		// A turn that failed (API errors such as ECONNREFUSED after retries)
		// or wrote no text has no plan to approve: no card (t3ff30a, the kmem
		// window showed "Approve this plan?" after a failed turn).
		if s.planWait && (turnFailed(ev) || !turnHasText(s.conv)) {
			s.planWait = false
			return nil
		}
		if s.planWait || asks {
			s.planWait = false
			// A status reply (no new question) leaves an unanswered card live,
			// even when a call in its turn was blocked (user 2026-11-23).
			// The same for an unanswered choice question (tc3712f).
			if !asks && (livePlan(s.conv) != nil || statusAfterQuestion(s.conv)) {
				return nil
			}
			if keptPlan(s.conv, lastTurnText(s.conv)) != nil {
				return nil // same questions as the unanswered card: it stays
			}
			endPlan(s)
			out := s.conv.CancelPlans()
			out = append(out, s.conv.MarkPlanText()...)
			return append(out, s.conv.AddPlan())
		}
	}
	return nil
}

// turnAskedApproval: a text of the current turn before its last one asked
// for approval (chat.TextAsksApproval), and no user message came since.
func turnAskedApproval(c *chat.Conversation) bool {
	last := true
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == chat.User || it.Kind == chat.Plan {
			return false
		}
		if it.Kind != chat.Assistant || strings.TrimSpace(it.Text) == "" {
			continue
		}
		if last {
			last = false // the closing text: AsksApproval's
			continue
		}
		if chat.TextAsksApproval(it.Text) {
			return true
		}
	}
	return false
}

// turnFailed: the turn's result reports an error (not a user interrupt).
func turnFailed(ev *protocol.Event) bool {
	if strings.HasPrefix(ev.TerminalReason, "aborted") {
		return false
	}
	return ev.IsError || (ev.Subtype != "" && ev.Subtype != "success")
}

// turnHasText: the current turn wrote assistant text other than an
// "API Error: …" message.
func turnHasText(c *chat.Conversation) bool {
	for i := len(c.Items) - 1; i >= 0; i-- {
		it := c.Items[i]
		if it.Kind == chat.User || it.Kind == chat.Plan {
			return false
		}
		if t := strings.TrimSpace(it.Text); it.Kind == chat.Assistant && t != "" && !strings.HasPrefix(t, "API Error") {
			return true
		}
	}
	return false
}

func (d *desk) approveNow(s *deskSession) {
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind == chat.Plan {
			it.PermState = chat.PermAllowed
			d.chatFor(s, []*chat.Item{it})
		}
	}
	startPlan(s)
	s.planWait = false // approved: no card for it at the turn's end
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
	return isApproval(text) || isPlanGo(text)
}

// isPlanGo: the plan card's approval, alone or with the answer to the
// approval question's choice appended (planGoWith).
func isPlanGo(text string) bool {
	if strings.HasPrefix(text, planGoLegacy) {
		return true
	}
	rest, ok := strings.CutPrefix(text, planGoMessage)
	if !ok {
		return false
	}
	// Exactly the approval, or followed by what Quill appends; "Approved.
	// but not that" typed by hand is no approval.
	return rest == "" || strings.HasPrefix(rest, " Choice: ") || strings.HasPrefix(rest, " Answers: ") || strings.HasPrefix(rest, " Also: ") || strings.HasPrefix(rest, autoApproveOnText)
}

// planGoWith: the approval text carrying the chosen option.
func planGoWith(choice string) string {
	if choice == "" {
		return planGoMessage
	}
	return planGoMessage + " Choice: " + choice + "."
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
	if notFromUser(text) {
		return false // peer, channel and scheduled messages never approve
	}
	if isPlanGo(text) {
		// Only while a plan or approval question waits; the card's own
		// Approve path does not come through here (sendTo's cardGo).
		return planPending(s)
	}
	// A quick-choice pick of the approval question's Yes ("I pick the first
	// option: Yes."; user 2026-11-22: picking Yes didn't count).
	for _, opt := range choicePicks(text) {
		if isApproval(opt) {
			return planPending(s)
		}
	}
	if !typedApprovalCounts() {
		return false
	}
	if approves(text) {
		return planPending(s)
	}
	return isStepList(text) && coversPlan(s, text)
}

// planPending: a plan card, an approval question or a blocked call's plan
// waits for the user's answer.
func planPending(s *deskSession) bool {
	return hasPendingPlan(s) || s.conv.AsksApproval() || s.planWait
}

// typedApprovalCounts: whether a go-ahead the user types ("yes", "go ahead",
// "approve the plan", a step list "1-3") approves a waiting plan, as the
// card's buttons, a quick-choice pick, the dialog and the card's "Approved."
// do. Undecided (td0cabd, user 2026-11-24): it counts, as before; return
// false here to make only buttons, picks and the dialog approve.
func typedApprovalCounts() bool { return true }

func isRetry(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), retryPrefix)
}

// strongApproval: an approval at the start counts however long the message
// goes on, unless the rest has a strongStop word ("Approve the plan with
// the recommended options, also look at …" did not count, user 2026-11-22).
var strongApproval = []string{"yes approve", "yes, approve", "yes i approve", "yes, i approve", "i approve", "approve all", "yes approved", "yes, approved", "plan approved",
	"approve the plan", "approve this plan", "approve plan", "approved", "plan is approved", "yes, go ahead", "go ahead with the plan"}

var strongStop = []string{"but", "don't", "dont", "do not", "not ", "wait", "instead", "hold", "stop", "except", "cancel", "skip", "don’t"}

// choicePrefix: a short pick in front of an approval ("C1, go ahead",
// "option B - approved", "2: yes"); the rest decides (user 2026-11-21).
var choicePrefix = regexp.MustCompile(`(?i)^\s*(?:option |choice |pick )?[a-z]?\d{0,3}[a-z]?\s*[,;:–—-]\s*(.+)$`)

func isApproval(text string) bool {
	if m := choicePrefix.FindStringSubmatch(text); m != nil && strings.TrimSpace(m[1]) != "" && len(text)-len(m[1]) <= 14 && strings.TrimSpace(text[:len(text)-len(m[1])]) != "" {
		head := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text[:len(text)-len(m[1])]), ",;:–—-"))
		if head != "" && !approvalWords[strings.ToLower(head)] {
			return isApproval(m[1])
		}
	}
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
	startPlan(s)
	s.planWait = false // approved: no card for it at the turn's end
}

func (d *desk) answerPlan(s *deskSession, it *chat.Item, ok bool) {
	d.answerPlanWith(s, it, ok, "", "")
}

// answerPlanWith approves (or not) and, with choice, answers the choice the
// approval question offered (user 2026-11-21).
func (d *desk) answerPlanWith(s *deskSession, it *chat.Item, ok bool, choice, changes string) {
	if planSuperseded(d.ownerOf(s, it), it) {
		return
	}
	if s != nil && s.remote != nil {
		if s = d.cardOwner(s, it); s != nil {
			d.remotePlan(s, it, ok, choice, nil, changes)
		}
		return
	}
	d.answerPlanMsg(s, it, ok, planGoChanges(planGoWith(choice), changes))
}

// answerPlanAnswers approves with every plan question's answer (plan card
// with questions; planquestions.go).
func (d *desk) answerPlanAnswers(s *deskSession, it *chat.Item, qs []planQ, ans map[int]planAnswer) {
	if planSuperseded(d.ownerOf(s, it), it) {
		return
	}
	if s != nil && s.remote != nil {
		if s = d.cardOwner(s, it); s != nil {
			d.remotePlan(s, it, true, "", planAnswersJSON(qs, ans), "")
		}
		return
	}
	d.answerPlanMsg(s, it, true, planGoAnswers(qs, ans))
}

// answerPlanMsg approves (sending msg) or turns the plan down.
func (d *desk) answerPlanMsg(s *deskSession, it *chat.Item, ok bool, msg string) {
	if s = d.cardOwner(s, it); s == nil || s.pendingRevision != nil || planSuperseded(s, it) {
		return
	}
	if s.codex {
		d.answerCodexPlan(s, it, ok, msg)
		return
	}
	if ok && it.PermState == chat.PermAllowed && !s.conv.Busy && !replied(s, it) {
		startPlan(s)
		s.cardGo = true
		d.sendTo(s, d.keepApproving(s, msg))
		return
	}
	if it.PermState != chat.PermPending {
		return
	}
	if !ok {
		// "Don't run": the user refused; any approval still in force ends.
		revokePlan(s)
		d.keepGate(s)
		it.PermState = chat.PermDenied
		d.chatFor(s, []*chat.Item{it})
		d.updateChrome()
		d.refreshSidebar()
		return
	}
	it.PermState = chat.PermAllowed
	startPlan(s)
	d.chatFor(s, []*chat.Item{it})
	s.cardGo = true
	d.sendTo(s, d.keepApproving(s, msg))
	if !s.running() {
		endPlan(s)
	}
}

var (
	choicePickRe = regexp.MustCompile(`^(?:Question \d+: )?I pick the \w+ option: (.+?)\.?$`)
	toolUseIDRe  = regexp.MustCompile(`<tool-use-id>([^<\s]+)</tool-use-id>`)
)

// choicePick: the option of a quick-choice reply (choiceReply).
func choicePick(text string) (string, bool) {
	m := choicePickRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// choicePicks: the options of a reply made only of quick-choice lines (one
// per question after "Send answers"), or nil.
func choicePicks(text string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		o, ok := choicePick(l)
		if !ok {
			return nil
		}
		out = append(out, o)
	}
	return out
}

// notFromUser: text Claude Code puts in a user turn that the user didn't
// write (peer messages, channel messages, scheduled triggers, background-task
// notifications, system reminders).
func notFromUser(text string) bool {
	t := strings.TrimSpace(text)
	return chat.Automated(t) || strings.HasPrefix(t, "<task-notification") || strings.HasPrefix(t, "<system-reminder")
}

func isAgentTool(r *protocol.ControlRequest) bool {
	return r != nil && (r.ToolName == "Agent" || r.ToolName == "Task")
}

// agentGrant: a subagent an approved plan started. A background one ends
// with its task notification, a foreground one with its tool result.
type agentGrant struct {
	Background bool      `json:"background,omitempty"`
	At         time.Time `json:"at,omitzero"` // when it was made (or renewed by a resume); a restart's stale check
}

// maxAgentCalls caps the calls remembered for live grants; past it, new
// calls fall to the gate (asking is the safe side).
const maxAgentCalls = 4096

func grantAgentCall(s *deskSession, id string, input json.RawMessage) {
	if id == "" {
		return
	}
	var in struct {
		Background flexBool `json:"run_in_background"`
	}
	json.Unmarshal(input, &in)
	if s.agentGrants == nil {
		s.agentGrants = map[string]agentGrant{}
	}
	s.agentGrants[id] = agentGrant{Background: bool(in.Background), At: gateNow()}
	s.grantRev++
}

func addAgentCall(s *deskSession, id, g string) {
	if id == "" || len(s.agentCalls) >= maxAgentCalls {
		return
	}
	if s.agentCalls == nil {
		s.agentCalls = map[string]string{}
	}
	s.agentCalls[id] = g
}

func addAgentID(s *deskSession, agent, g string) {
	if agent == "" || len(s.agentIDs) >= maxAgentCalls {
		return
	}
	if s.agentIDs == nil {
		s.agentIDs = map[string]string{}
	}
	s.agentIDs[agent] = g
	addPlanAgent(s, agent, g) // for a SendMessage resume later
}

// grantedRequest: the request was made by a subagent under a live grant,
// known by its call (seen in the stream) or by the agent_id it carries. The
// session's own requests carry no agent_id.
func grantedRequest(s *deskSession, r *protocol.ControlRequest) (string, bool) {
	if g, ok := s.agentCalls[r.ToolUseID]; ok && r.ToolUseID != "" {
		if _, live := s.agentGrants[g]; live {
			return g, true
		}
	}
	if g, ok := s.agentIDs[r.AgentID]; ok && r.AgentID != "" {
		if _, live := s.agentGrants[g]; live {
			return g, true
		}
	}
	return "", false
}

// grantFor: the live grant covering events whose parent is id: the grant
// itself, or the grant a nested agent's call belongs to.
func grantFor(s *deskSession, id string) (string, bool) {
	if _, ok := s.agentGrants[id]; ok {
		return id, true
	}
	if g, ok := s.agentCalls[id]; ok {
		if _, live := s.agentGrants[g]; live {
			return g, true
		}
	}
	return "", false
}

// trackAgentCalls learns from the stream which calls belong to a granted
// subagent: its assistant events carry the Agent call's tool_use_id as
// parent, and their tool_use blocks are its calls (a nested agent's events
// name the nested Agent call, which maps to the root grant). A foreground
// grant ends when the main session gets its Agent call's result.
//
// A background subagent's own events are not streamed to this session, so
// its calls are learnt by agent id instead: the Agent call's launch result
// (tool_use_result.agentId, or "agentId: …" in its text) and the
// task_started event (task_id) name it, and its can_use_tool requests carry
// it as agent_id.
//
// An Agent call the session makes while an approved plan is in force is
// granted from its assistant event too: the CLI need not ask for it.
func trackAgentCalls(s *deskSession, ev *protocol.Event) {
	if ev.Type == "assistant" && !ev.IsSubagent() && ev.Message != nil {
		for _, b := range ev.Message.Blocks() {
			if b.Type == "tool_use" && b.Name == "SendMessage" {
				var in struct {
					To string `json:"to"`
				}
				json.Unmarshal(b.Input, &in)
				forgetQueued(s, in.To)
			}
		}
	}
	if ev.Type == "assistant" && !ev.IsSubagent() && planCovers(s) && ev.Message != nil {
		for _, b := range ev.Message.Blocks() {
			if b.Type == "tool_use" {
				planCall(s, b.Name, b.ID, b.Input)
			}
		}
	}
	learnPlanWork(s, ev)
	learnResumes(s, ev)
	if len(s.agentGrants) == 0 {
		return
	}
	if ev.Type == "system" && ev.Subtype == "task_started" {
		if g, ok := grantFor(s, ev.ToolUseID); ok {
			addAgentID(s, ev.TaskID, g)
		}
		return
	}
	if ev.Message == nil {
		return
	}
	switch ev.Type {
	case "assistant":
		if !ev.IsSubagent() {
			return
		}
		g, ok := grantFor(s, *ev.ParentToolUseID)
		if !ok {
			return
		}
		addAgentID(s, ev.AgentID, g)
		for _, b := range ev.Message.Blocks() {
			if b.Type == "tool_use" {
				addAgentCall(s, b.ID, g)
			}
		}
	case "user":
		var results []protocol.Block
		for _, b := range ev.Message.Blocks() {
			if b.Type == "tool_result" {
				results = append(results, b)
			}
		}
		for _, b := range results {
			g, ok := grantFor(s, b.ToolUseID)
			if !ok {
				continue
			}
			// The launch result names the agent (a nested agent's too, when
			// its parent's results are streamed).
			agent := ""
			if m := agentIDRe.FindSubmatch(b.Content); m != nil {
				agent = string(m[1])
			} else if len(results) == 1 {
				var r struct {
					AgentID string `json:"agentId"`
				}
				json.Unmarshal(ev.ToolUseResult, &r)
				agent = r.AgentID
			}
			addAgentID(s, agent, g)
			if ev.IsSubagent() {
				continue
			}
			if gr, ok := s.agentGrants[b.ToolUseID]; ok && !gr.Background {
				if asyncLaunch(b, ev, len(results) == 1) {
					// The CLI ran it in the background although the call
					// didn't say so: it goes on until its notification, and
					// that notification is the plan's.
					gr.Background = true
					s.agentGrants[b.ToolUseID] = gr
					s.grantRev++
					if s.runAll {
						addPlanWork(s, b.ToolUseID, b.ToolUseID)
						addPlanWork(s, agent, b.ToolUseID)
					}
					continue
				}
				endAgentGrant(s, b.ToolUseID)
			}
		}
	}
}

// asyncLaunch: b, an Agent call's result, says the subagent was launched
// in the background (its result comes with the task notification).
func asyncLaunch(b protocol.Block, ev *protocol.Event, single bool) bool {
	if single {
		var r struct {
			Status  string `json:"status"`
			IsAsync bool   `json:"isAsync"`
		}
		if json.Unmarshal(ev.ToolUseResult, &r) == nil && (r.Status == "async_launched" || r.IsAsync) {
			return true
		}
	}
	return strings.HasPrefix(strings.TrimSpace(protocol.ContentText(b.Content)), "Async agent launched")
}

// agentIDRe: the agent id in an Agent call's launch result.
var agentIDRe = regexp.MustCompile(`agentId: ([A-Za-z0-9_-]+)`)

// endAgentGrant ends a grant and forgets the calls made under it.
func endAgentGrant(s *deskSession, id string) {
	delete(s.agentGrants, id)
	s.grantRev++
	for c, g := range s.agentCalls {
		if g == id {
			delete(s.agentCalls, c)
		}
	}
	for a, g := range s.agentIDs {
		if g == id {
			delete(s.agentIDs, a)
		}
	}
	if len(s.agentGrants) == 0 {
		s.agentCalls, s.agentIDs = nil, nil
	}
}

// endAgentGrants: a task notification names the subagent that finished.
func endAgentGrants(s *deskSession, text string) {
	for _, m := range toolUseIDRe.FindAllStringSubmatch(text, -1) {
		if _, ok := s.agentGrants[m[1]]; ok {
			endAgentGrant(s, m[1])
		}
	}
}
