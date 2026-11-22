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

// Welcome (user 2026-11-21): six questions on a new install, before the
// Setup checks. Online or local model, the speech-to-text language, plan
// first or not, kmem or Claude's auto memory, and Quill's default kmem rules
// (beginner) or none (advanced). Shown once;
// Settings → Setup → "Run welcome again" opens it later.

const (
	prefWelcomeSeen = "welcomeSeen"
	prefSTTOff      = "sttOff"
	welcomeOnline   = "Online: Claude (needs a Claude sign-in)"
	welcomeLocal    = "Local: an open-weight model through Ollama, on this PC"
	welcomeRules    = "Default rules (recommended for beginners)"
	welcomeNoRules  = "No rules (advanced: write your own, or none)"
	welcomeVoiceOff = "Off (no speech to text)"
	welcomeAsk      = "Ask me first: Claude shows a plan and waits for my OK (recommended for beginners)"
	welcomeLetWork  = "Let Claude work: no plan step (advanced)"
	welcomeKmem     = "kmem: shared memory, handoffs and tasks (recommended with the default rules)"
	welcomeAutoMem  = "Claude's auto memory: a MEMORY.md per folder"
	welcomeHomeOnly = "No: only on my home network"
)

// welcomeChoices is what the user picked; applyWelcome turns it into settings.
type welcomeChoices struct {
	Local      bool
	LocalModel string
	Lang       string // "" = speech to text off
	Rules      bool
	PlanFirst  bool
	KmemMemory bool
	Remote     string // "" or "skip", or a vpnOption id
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
	if c.KmemMemory {
		p.SetString(memoryModeKey, memoryModeKmem)
		done = append(done, "Sessions use kmem memory.")
	} else {
		p.SetString(memoryModeKey, memoryModeClaude)
		done = append(done, "Sessions use Claude's auto memory.")
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

// showWelcome asks the three questions; after runs when it closes.
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
	want := d.app.Preferences().StringWithFallback("sttLang", systemLang())
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

	mem := widget.NewRadioGroup([]string{welcomeKmem, welcomeAutoMem}, nil)
	mem.SetSelected(welcomeAutoMem)
	if d.kmemMemory() || d.app.Preferences().String(memoryModeKey) != memoryModeClaude && bundledKmemDir() != "" {
		mem.SetSelected(welcomeKmem)
	}
	memNote := caption("")
	memNote.Wrapping = fyne.TextWrapWord
	if !d.kmemAvailable() {
		memNote.SetText("kmem isn't set up yet; Setup offers it right after this.")
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
			text += " The rules are kmem's: they work best with kmem memory (question 4)."
		}
		rulesNote.SetText(text)
	}
	mem.OnChanged = func(string) { syncRulesNote() }
	rules.OnChanged = func(string) { syncRulesNote() }
	syncRulesNote()
	showRules := widget.NewButton("Show rules", d.showDefaultRules)
	showRules.Importance = widget.LowImportance

	body := container.NewVBox(
		caption("Six quick choices. All of them can be changed later in Settings."),
		widget.NewSeparator(),
		boldLabel("1. Which model should new sessions use?"), model, localNote,
		widget.NewSeparator(),
		boldLabel("2. Which language do you speak for speech to text?"), voice,
		widget.NewSeparator(),
		boldLabel("3. Before Claude changes files or runs commands:"), plan,
		widget.NewSeparator(),
		boldLabel("4. Memory for your sessions:"), mem, memNote,
		widget.NewSeparator(),
		container.NewBorder(nil, nil, nil, showRules, boldLabel("5. Shared rules for your sessions (kmem)")), rules, rulesNote,
		widget.NewSeparator(),
		boldLabel("6. Reach Quill from outside your home network (phone, other PCs)?"), remote,
		caption("Optional. A VPN lets Quill for Android reach this PC from anywhere; Setup installs and connects it next."),
	)
	dlg := dialog.NewCustomConfirm("Welcome to Quill Desktop", "Done", "Skip", container.NewVScroll(body), func(ok bool) {
		if !ok {
			d.app.Preferences().SetBool(prefWelcomeSeen, true)
		} else {
			done := d.applyWelcome(welcomeChoices{
				Local: model.Selected == welcomeLocal, LocalModel: local,
				Lang:       byName[voice.Selected],
				Rules:      rules.Selected == welcomeRules && !rules.Disabled(),
				PlanFirst:  plan.Selected == welcomeAsk,
				KmemMemory: mem.Selected == welcomeKmem,
				Remote:     remoteID(remote.Selected),
			})
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
