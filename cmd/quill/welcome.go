package main

import (
	"bufio"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Welcome (user 2026-11-21): questions on a new install, before the Setup
// checks. Online or local model, the speech-to-text language, plan first or
// not, the "New" feature questions (featureask.go: coordinate with subagents
// or one session, ...), kmem or Claude's auto memory, and Quill's default kmem rules (beginner) or none
// (advanced). Shown once;
// Settings → Setup → "Run welcome again" opens it later.

const (
	prefWelcomeSeen    = "welcomeSeen"
	prefSTTOff         = "sttOff"
	welcomeOnline      = "Online: Claude (needs a Claude sign-in)"
	welcomeLocal       = "Local: an open-weight model through Ollama, on this PC"
	welcomeRules       = "Default rules (recommended for beginners)"
	welcomeNoRules     = "No rules (advanced: write your own, or none)"
	welcomeVoiceOff    = "Off (no speech to text)"
	welcomeAsk         = "Ask me first: Claude shows a plan and waits for my OK (recommended for beginners)"
	welcomeLetWork     = "Let Claude work: no plan step (advanced)"
	welcomeKmem        = "kmem: shared memory, handoffs and tasks (recommended with the default rules)"
	welcomeAutoMem     = "Claude's auto memory: a MEMORY.md per folder"
	welcomeHomeOnly    = "No: only on my home network"
	welcomeAutocorrect = "Autocorrect (fixes misspelled words as you type)"
	welcomeAutoFormat  = "Auto formatting (capital letters, — dashes, lists with Shift+Enter)"
)

// welcomeChoices is what the user picked; applyWelcome turns it into settings.
type welcomeChoices struct {
	Local      bool
	LocalModel string
	Lang       string // "" = speech to text off
	Rules      bool
	PlanFirst  bool
	Features   map[string]int // feature question id → choice index
	KmemMemory bool
	NoKmem     bool     // kmem not installed: the memory choice stays as it is
	Remote     string   // "" or "skip", or a vpnOption id
	Typing     []string // the typing help picked (welcomeAutocorrect, welcomeAutoFormat)
}

// freshInstall: no tabs saved and Setup never shown, so Quill has not been
// used on this computer yet.
func (d *desk) freshInstall() bool {
	p := d.app.Preferences()
	return p.String("tabs") == "" && !p.Bool("setupSeen")
}

// memoryGB is this computer's memory in GB (0 = unknown).
var memoryGB = func() int {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.Atoi(f[1])
			return (kb + 512*1024) / (1024 * 1024)
		}
	}
	return 0
}

// suggestLocal: a model that fits this computer (gpt-oss:20b wants ~24 GB).
func suggestLocal(gb int) string {
	if gb >= 24 {
		return "ollama:gpt-oss:20b"
	}
	return "ollama:qwen3:8b"
}

// systemLang is the system's language if speech to text knows it, else "en".
func systemLang() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(k)
		if len(v) >= 2 {
			code := strings.ToLower(v[:2])
			for _, l := range languages {
				if l.code == code {
					return code
				}
			}
		}
	}
	return "en"
}

func (d *desk) applyWelcome(c welcomeChoices) []string {
	p := d.app.Preferences()
	var done []string
	if c.Local {
		p.SetString("newModel", c.LocalModel)
		done = append(done, "New sessions use the local model "+strings.TrimPrefix(c.LocalModel, "ollama:")+".")
	} else {
		p.SetString("newModel", "default")
		done = append(done, "New sessions use Claude online.")
	}
	if c.Lang == "" {
		p.SetBool(prefSTTOff, true)
		done = append(done, "Speech to text is off.")
	} else {
		p.SetBool(prefSTTOff, false)
		p.SetString("sttLang", c.Lang)
		done = append(done, "Speech to text listens for "+languageName(c.Lang)+" (its model downloads the first time you use the mic).")
	}
	if d.stt != nil {
		d.stt.syncOff()
	}
	if c.Rules {
		path, err := installDefaultRules(homeDir())
		switch {
		case errors.Is(err, errRulesExist):
			done = append(done, "You already have kmem rules ("+tildePath(path)+"); they stay as they are.")
		case err != nil:
			done = append(done, "Could not install the default rules: "+err.Error())
		default:
			done = append(done, "Installed Quill's default kmem rules at "+tildePath(path)+".")
		}
	}
	d.planFirst = c.PlanFirst
	p.SetBool("planFirst", c.PlanFirst)
	if c.PlanFirst {
		done = append(done, "Claude asks before running anything (plan first).")
	} else {
		done = append(done, "Claude works without a plan step.")
	}
	for _, q := range featureQuestions {
		if i, ok := c.Features[q.ID]; ok && d.answerFeatureQuestion(q.ID, i) {
			done = append(done, q.Question+" "+strings.TrimSuffix(q.Choices[i].Label, " (recommended)")+".")
		}
	}
	d.markFeatureQuestionsAnswered() // new users are never asked them again
	d.settingsChanged()
	switch {
	case c.NoKmem:
		// Without kmem sessions use Claude's auto memory; the saved
		// choice stays, so an installed kmem is used once it is set up.
		done = append(done, "Sessions use Claude's auto memory (kmem is not installed).")
	case c.KmemMemory && d.kmemByDefault():
		// kmem is already the default (no mode chosen); the switch
		// (welcomeMemoryNext) imports Claude's notes and adds the hooks.
		done = append(done, "kmem memory: \"Switch to kmem\" opens next to import Claude's notes and add kmem's hooks.")
	case c.KmemMemory && !d.kmemMemory():
		// The mode changes only through the switch (welcomeMemoryNext
		// opens it after the Welcome; user 2026-11-24).
		p.SetString(memoryModeKey, memoryModeClaude)
		done = append(done, "kmem memory: \"Switch to kmem\" opens next; sessions use Claude's auto memory until you switch.")
	case c.KmemMemory:
		p.SetString(memoryModeKey, memoryModeKmem)
		done = append(done, "Sessions use kmem memory.")
	case d.kmemMemory() && d.kmemSwitchUndoDir() != "":
		done = append(done, "Claude's auto memory: \"Switch back to Claude memory\" opens next.")
	default:
		p.SetString(memoryModeKey, memoryModeClaude)
		done = append(done, "Sessions use Claude's auto memory.")
	}
	if c.Typing != nil {
		ac, af := contains(c.Typing, welcomeAutocorrect), contains(c.Typing, welcomeAutoFormat)
		p.SetBool("autocorrect", ac)
		p.SetBool(prefAutoFormat, af)
		done = append(done, map[bool]string{true: "Autocorrect on.", false: "Autocorrect off."}[ac], map[bool]string{true: "Auto formatting on.", false: "Auto formatting off."}[af])
	}
	if o, ok := vpnByID(c.Remote); ok {
		p.SetString(prefRemoteAccess, o.ID)
		done = append(done, "Remote access: "+o.Name+" (Setup installs and connects it).")
	} else {
		p.SetString(prefRemoteAccess, "skip")
		done = append(done, "Phones and other PCs connect on your home network only.")
	}
	p.SetBool(prefWelcomeSeen, true)
	return done
}

// welcomeMemoryNext: the Welcome's memory choice needs the switch (or its
// undo) dialog, which opens after the Welcome (requestMemoryMode).
func (d *desk) welcomeMemoryNext(c welcomeChoices) bool {
	if c.NoKmem || !d.kmemAvailable() {
		return false
	}
	if c.KmemMemory {
		return d.kmemSwitchWanted()
	}
	return d.kmemMemory() && d.kmemSwitchUndoDir() != ""
}

// showWelcome asks the questions; after runs when it closes.
func (d *desk) showWelcome(after func()) {
	model := widget.NewRadioGroup([]string{welcomeOnline, welcomeLocal}, nil)
	model.SetSelected(welcomeOnline)
	if isLocal(d.app.Preferences().String("newModel")) {
		model.SetSelected(welcomeLocal)
	}
	gb := memoryGB()
	local := suggestLocal(gb)
	localNote := caption("")
	localNote.Wrapping = fyne.TextWrapWord
	syncLocal := func() {
		if model.Selected != welcomeLocal {
			localNote.SetText("You can switch any session between online and local later with the Online/Local button under the message box.")
			return
		}
		text := "Suggested for this computer: " + strings.TrimPrefix(local, "ollama:")
		if gb > 0 {
			text += " (" + strconv.Itoa(gb) + " GB memory)"
		}
		text += ". Needs Ollama; Settings → Local models installs it and pulls models."
		localNote.SetText(text)
	}
	model.OnChanged = func(string) { syncLocal() }
	syncLocal()

	var names []string
	byName := map[string]string{welcomeVoiceOff: ""}
	for _, l := range languages {
		n := l.name + " (" + l.code + ")"
		names = append(names, n)
		byName[n] = l.code
	}
	names = append(names, welcomeVoiceOff)
	voice := widget.NewSelect(names, nil)
	want := d.app.Preferences().StringWithFallback("sttLang", autoLang)
	if d.app.Preferences().Bool(prefSTTOff) {
		voice.SetSelected(welcomeVoiceOff)
	} else {
		voice.SetSelected(languageName(want) + " (" + want + ")")
	}

	plan := widget.NewRadioGroup([]string{welcomeAsk, welcomeLetWork}, nil)
	plan.SetSelected(welcomeLetWork)
	if d.planFirst {
		plan.SetSelected(welcomeAsk)
	}

	n := 3 // questions 1-3 come first, then the "New" feature questions
	num := func(t string) *widget.Label { n++; return boldLabel(strconv.Itoa(n) + ". " + t) }
	var featureParts []fyne.CanvasObject
	featureRadios := map[string]*widget.RadioGroup{}
	for _, q := range featureQuestions {
		if q.Kind != featureNew || q.Retired || len(q.Choices) == 0 {
			continue
		}
		title := num(q.Question)
		q.Question = ""
		r, objs := featureQuestionForm(q)
		featureRadios[q.ID] = r
		featureParts = append(append(append(featureParts, title), objs...), widget.NewSeparator())
	}
	memTitle := num("Memory for your sessions:")
	memNum := n
	rulesTitle := num("Shared rules for your sessions (kmem)")
	typingTitle := num("Typing help in the message box")
	remoteTitle := num("Reach Quill from outside your home network (phone, other PCs)?")

	mem := widget.NewRadioGroup([]string{welcomeKmem, welcomeAutoMem}, nil)
	mem.SetSelected(welcomeAutoMem)
	if d.kmemMemory() || d.app.Preferences().String(memoryModeKey) != memoryModeClaude && d.kmemAvailable() {
		mem.SetSelected(welcomeKmem)
	}
	memNote := caption("")
	memNote.Wrapping = fyne.TextWrapWord
	memLink := d.kmemAdvert() // one line, only without kmem and until dismissed
	noKmem := !d.kmemAvailable()
	if noKmem {
		mem.Disable() // Claude's auto memory until kmem is installed
		memNote.SetText("kmem isn't installed: sessions use Claude's auto memory.")
	}

	remoteNames := []string{welcomeHomeOnly}
	for _, o := range vpnOptions {
		remoteNames = append(remoteNames, o.Name)
	}
	remote := widget.NewSelect(remoteNames, nil)
	remote.SetSelected(welcomeHomeOnly)
	if o, ok := vpnByID(d.app.Preferences().String(prefRemoteAccess)); ok {
		remote.SetSelected(o.Name)
	}

	typing := widget.NewCheckGroup([]string{welcomeAutocorrect, welcomeAutoFormat}, nil)
	if d.autocorrectOn() {
		typing.Selected = append(typing.Selected, welcomeAutocorrect)
	}
	if d.autoFormatOn() {
		typing.Selected = append(typing.Selected, welcomeAutoFormat)
	}

	rules := widget.NewRadioGroup([]string{welcomeRules, welcomeNoRules}, nil)
	rules.SetSelected(welcomeRules)
	rulesNote := caption("Shared rules every Claude session follows: start with kmem brief, claim tasks, save handoffs, git safety in shared folders.")
	rulesNote.Wrapping = fyne.TextWrapWord
	baseRulesNote := rulesNote.Text
	if p := kmemRulesPath(homeDir()); fileExists(p) {
		rules.SetSelected(welcomeNoRules)
		rules.Disable()
		baseRulesNote = "You already have kmem rules (" + tildePath(p) + "); Quill leaves them as they are."
	}
	syncRulesNote := func() {
		text := baseRulesNote
		if mem.Selected == welcomeAutoMem && !rules.Disabled() && rules.Selected == welcomeRules {
			text += " The rules are kmem's: they work best with kmem memory (question " + strconv.Itoa(memNum) + ")."
		}
		rulesNote.SetText(text)
	}
	mem.OnChanged = func(string) { syncRulesNote() }
	rules.OnChanged = func(string) { syncRulesNote() }
	syncRulesNote()
	showRules := widget.NewButton("Show rules", d.showDefaultRules)
	showRules.Importance = widget.LowImportance

	body := container.NewVBox(
		caption(strconv.Itoa(n)+" quick choices. All of them can be changed later in Settings."),
		widget.NewSeparator(),
		boldLabel("1. Which model should new sessions use?"), model, localNote,
		widget.NewSeparator(),
		boldLabel("2. Which language do you speak for speech to text?"), voice,
		widget.NewSeparator(),
		boldLabel("3. Before Claude changes files or runs commands:"), plan,
		widget.NewSeparator(),
	)
	for _, o := range featureParts {
		body.Add(o)
	}
	for _, o := range []fyne.CanvasObject{
		memTitle, mem, memNote, memLink,
		widget.NewSeparator(),
		container.NewBorder(nil, nil, nil, showRules, rulesTitle), rules, rulesNote,
		widget.NewSeparator(),
		typingTitle, typing,
		widget.NewSeparator(),
		remoteTitle, remote,
		caption("Optional. A VPN lets Quill for Android reach this PC from anywhere; Setup installs and connects it next."),
	} {
		body.Add(o)
	}
	dlg := dialog.NewCustomConfirm("Welcome to Quill Desktop", "Done", "Skip", container.NewVScroll(body), func(ok bool) {
		if !ok {
			d.app.Preferences().SetBool(prefWelcomeSeen, true)
		} else {
			c := welcomeChoices{
				Local: model.Selected == welcomeLocal, LocalModel: local,
				Lang:       byName[voice.Selected],
				Rules:      rules.Selected == welcomeRules && !rules.Disabled(),
				PlanFirst:  plan.Selected == welcomeAsk,
				Features:   featureAnswers(featureRadios),
				KmemMemory: mem.Selected == welcomeKmem,
				NoKmem:     noKmem,
				Remote:     remoteID(remote.Selected),
				Typing:     append([]string{}, typing.Selected...),
			}
			memNext := d.welcomeMemoryNext(c) // before applyWelcome sets the mode
			done := d.applyWelcome(c)
			d.notifyWelcome(done)
			if remoteID(remote.Selected) != "" {
				inner := after
				after = func() {
					d.remoteAccessDialog(nil)
					if inner != nil {
						inner()
					}
				}
			}
			if memNext {
				inner := after
				after = func() { d.requestMemoryMode(c.KmemMemory, inner) }
			}
		}
		if after != nil {
			after()
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(720, 600))
	dlg.Show()
}

func (d *desk) notifyWelcome(done []string) {
	if d.current != nil {
		d.notify(d.current, strings.Join(done, " "))
		return
	}
	d.flashHint(d.folder, strings.Join(done, " "))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func remoteID(name string) string {
	if o, ok := vpnByOptionName(name); ok {
		return o.ID
	}
	return ""
}

// newSessionModelSetting: Settings → New sessions: online or one of the
// local models (the same setting the Welcome's first question sets).
func (d *desk) newSessionModelSetting() fyne.CanvasObject {
	sel := widget.NewSelect(nil, nil)
	d.newSessSel = sel
	d.syncNewSessionSetting()
	sel.OnChanged = func(v string) {
		if v == optNewOnline {
			if isLocal(d.app.Preferences().String("newModel")) {
				d.app.Preferences().SetString("newModel", "default")
			}
			return
		}
		name := strings.TrimPrefix(v, "Local: ")
		for _, m := range d.localModelList() {
			if strings.TrimPrefix(m, "ollama:") == name {
				d.app.Preferences().SetString("newModel", m)
				return
			}
		}
		d.app.Preferences().SetString("newModel", "ollama:"+name)
	}
	return container.NewVBox(container.NewBorder(nil, nil, widget.NewLabel("New sessions use"), nil, sel),
		caption("Each session can still switch with the Online/Local button under the message box."))
}

const (
	optNewOnline = "Online: Claude"
	prefNewMode  = "newMode"
)

// newSessionModeSetting: Settings → New sessions: the permission mode new
// sessions start in. Only this control changes it; picking a mode for one
// session leaves it alone.
func (d *desk) newSessionModeSetting() fyne.CanvasObject {
	sel := widget.NewSelect(modeChoices, nil)
	d.newModeSel = sel
	d.syncNewSessionSetting()
	sel.OnChanged = func(v string) { d.app.Preferences().SetString(prefNewMode, v) }
	return container.NewBorder(nil, nil, widget.NewLabel("New sessions start in mode"), nil, sel)
}

// syncNewSessionSetting shows the live newModel in Settings → New sessions
// (called when Settings opens; the box itself is built once at startup).
func (d *desk) syncNewSessionSetting() {
	if m := d.newModeSel; m != nil {
		m.Selected = d.app.Preferences().StringWithFallback(prefNewMode, "auto") // no OnChanged
		m.Refresh()
	}
	sel := d.newSessSel
	if sel == nil {
		return
	}
	opts := []string{optNewOnline}
	for _, m := range d.localModelList() {
		opts = append(opts, "Local: "+strings.TrimPrefix(m, "ollama:"))
	}
	cur := d.app.Preferences().StringWithFallback("newModel", "default")
	want := optNewOnline
	if isLocal(cur) {
		want = "Local: " + strings.TrimPrefix(cur, "ollama:")
		if !contains(opts, want) {
			opts = append(opts, want)
		}
	}
	sel.Options, sel.Selected = opts, want // no OnChanged: only showing the setting
	sel.Refresh()
}

func featureAnswers(radios map[string]*widget.RadioGroup) map[string]int {
	out := map[string]int{}
	for id, r := range radios {
		out[id] = indexOf(r.Options, r.Selected)
	}
	return out
}
