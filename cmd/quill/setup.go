package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/session"
)

// Setup (user 2026-11-21, Quill Desktop only): everything a default Quill
// needs, with its state and one button each: Claude Code, sign-in, kmem
// (where to get it when missing; it is not part of Quill Desktop),
// python3 and hunspell (Linux), the device password and local models.
// Installers run in a visible terminal after asking; anything that needs
// root goes through pkexec, so the user approves the elevation.

type setupRow struct {
	title, state string
	ok, optional bool
	action       string
	do           func()
}

const (
	claudeInstallLinux   = "curl -fsSL https://claude.ai/install.sh | bash"
	claudeInstallWindows = "irm https://claude.ai/install.ps1 | iex"
)

// lookPath is exec.LookPath, also trying ~/.local/bin (where the Claude Code
// installer and kmem put themselves, often not on a desktop app's PATH).
var lookPath = func(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	p := filepath.Join(homeDir(), ".local", "bin", exe)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// pkgInstall: the command installing packages per package manager.
func pkgInstall(pkgs map[string]string) (string, bool) {
	for _, pm := range []struct{ bin, cmd string }{
		{"dnf", "pkexec dnf install -y "}, {"apt-get", "pkexec apt-get install -y "},
		{"pacman", "pkexec pacman -S --needed --noconfirm "}, {"zypper", "pkexec zypper --non-interactive install "},
	} {
		if p, ok := pkgs[pm.bin]; ok {
			if _, err := exec.LookPath(pm.bin); err == nil {
				return pm.cmd + p, true
			}
		}
	}
	return "", false
}

var hunspellPkgs = map[string]string{"dnf": "hunspell hunspell-en-US", "apt-get": "hunspell hunspell-en-us", "pacman": "hunspell hunspell-en_us", "zypper": "hunspell myspell-en_US"}
var pythonPkgs = map[string]string{"dnf": "python3", "apt-get": "python3", "pacman": "python", "zypper": "python3"}

func (d *desk) runInstaller(title, what, script string, after func()) {
	c := dialog.NewConfirm(title, what+"\n\nIt runs in a terminal window so you can watch it:\n"+script, func(ok bool) {
		if !ok {
			return
		}
		cmd, err := openTerminal(title, script)
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		go func() {
			_ = cmd.Wait()
			if after != nil {
				d.do(after)
			}
		}()
	}, d.win)
	c.SetConfirmText("Run it")
	c.Show()
}

func (d *desk) setupRows(refresh func()) []setupRow {
	var rows []setupRow
	win := runtime.GOOS == "windows"
	useCodex := d.app.Preferences().Bool(prefNewCodex)

	// Claude Code
	claude := lookPath(firstNonEmpty(d.cfg.ClaudeBin, "claude"))
	if filepath.IsAbs(d.cfg.ClaudeBin) {
		if _, err := os.Stat(d.cfg.ClaudeBin); err == nil {
			claude = d.cfg.ClaudeBin
		}
	}
	r := setupRow{title: "Claude Code", optional: useCodex}
	if claude != "" {
		r.ok, r.state = true, "installed: "+tildePath(claude)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if out, err := exec.CommandContext(ctx, claude, "--version").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
			r.state += " (" + strings.TrimSpace(string(out)) + ")"
		}
		cancel()
	} else {
		script := claudeInstallLinux
		if win {
			script = claudeInstallWindows
		}
		r.state, r.action = "not installed: used for Claude and Ollama sessions", "Install..."
		r.do = func() {
			d.runInstaller("Install Claude Code", "Quill will run Anthropic's official installer for Claude Code.", script, refresh)
		}
	}
	rows = append(rows, r)

	// Sign-in
	r = setupRow{title: "Claude account", optional: useCodex || isLocal(d.app.Preferences().String("newModel"))}
	switch {
	case claude == "":
		r.state = "install Claude Code first"
	case d.acct.LoggedIn:
		r.ok, r.state = true, "signed in"
		if p := d.acct.Plan(); p != "" {
			r.state += " (" + p + ")"
		}
	default:
		r.state, r.action, r.do = "not signed in", "Sign in...", d.signIn
	}
	rows = append(rows, r)

	codexPath, codexErr := codexBinary()
	r = setupRow{title: "Codex", optional: !useCodex}
	if codexErr != nil {
		r.state = "not installed; use an existing Codex installation or install the Codex CLI"
	} else {
		r.ok, r.state = true, "installed: "+tildePath(codexPath)
	}
	rows = append(rows, r)
	r = setupRow{title: "ChatGPT account for Codex", optional: !useCodex}
	status := d.codexAuth
	switch {
	case codexErr != nil:
		r.state = "install Codex first"
	case status.ready && (status.snapshot.Account != nil || !status.snapshot.RequiresOpenAIAuth):
		r.ok, r.state = true, "uses the existing Codex configuration"
	case !status.ready:
		r.state, r.action, r.do = "status unavailable", "Check sign-in", d.refreshCodexAccount
	default:
		r.state, r.action, r.do = "not signed in", "Sign in…", d.signInChatGPT
	}
	rows = append(rows, r)

	// kmem: not part of Quill Desktop; patrons get it (kmemsetup.go).
	r = setupRow{title: "kmem (shared memory)", optional: true}
	home := homeDir()
	kb := firstNonEmpty(kmemBin(), "kmem")
	switch {
	case !d.kmemAvailable() && kmemBin() != "" && !d.kmemOff:
		r.state = "installed but incompatible; supported versions: " + supportedKmemSeries
	case !d.kmemAvailable():
		r.state, r.action = kmemGetHint, "Get kmem..."
		r.do = d.openKmemPatreon
	case win && windowsPython() == "":
		r.state = "installed, but needs Python 3 (see the Python row)"
	case d.kmemSwitchWanted():
		r.state, r.action = "detected; preview the recommended Quill and kmem setup and shared import", "Review setup…"
		r.do = d.showKmemSwitch
	case kmemHooksMissing(home):
		r.state, r.action = "installed ("+tildePath(kb)+"), but its Claude Code hooks are missing: sessions don't get the kmem brief or the guards", "Add hooks..."
		r.do = func() { d.kmemHooksDialog(refresh) }
	default:
		r.ok, r.state = true, "installed: "+tildePath(kb)
	}
	rows = append(rows, r)

	if win {
		r = setupRow{title: "Python 3", optional: true}
		if py := windowsPython(); py != "" {
			r.ok, r.state = true, "installed ("+py+"; kmem needs it)"
		} else {
			r.state, r.action = "missing: kmem needs it", "Install..."
			r.do = func() {
				d.runInstaller("Install Python 3", "kmem runs on Python 3. This installs it with winget (Windows' package manager); restart Quill Desktop afterwards so it sees Python.",
					"winget install -e --id Python.Python.3.13 --accept-source-agreements --accept-package-agreements", refresh)
			}
		}
		rows = append(rows, r)
	}
	if !win {
		r = setupRow{title: "Python 3", optional: true}
		if lookPath("python3") != "" {
			r.ok, r.state = true, "installed (kmem needs it)"
		} else if cmd, ok := pkgInstall(pythonPkgs); ok {
			r.state, r.action = "missing: kmem needs it", "Install..."
			r.do = func() { d.runInstaller("Install Python 3", "kmem runs on Python 3.", cmd, refresh) }
		} else {
			r.state = "missing: install python3 with your package manager (kmem needs it)"
		}
		rows = append(rows, r)

		r = setupRow{title: "Spell checking (hunspell)", optional: true}
		if lookPath("hunspell") != "" {
			r.ok, r.state = true, "installed"
		} else if cmd, ok := pkgInstall(hunspellPkgs); ok {
			r.state, r.action = "missing: the message box can't autocorrect", "Install..."
			r.do = func() {
				d.runInstaller("Install hunspell", "The message box uses hunspell and an English dictionary for autocorrect.", cmd, refresh)
			}
		} else {
			r.state = "missing: install hunspell and an English dictionary with your package manager"
		}
		rows = append(rows, r)
	}

	rows = append(rows, d.remoteAccessRow(refresh))
	r = setupRow{title: "Device password", optional: true}
	if d.app.Preferences().String(prefPCPassword) != "" {
		r.ok, r.state = true, "set (phones and other PCs ask for it)"
	} else {
		r.state, r.action = "not set: no phone or other PC can connect", "Set..."
		r.do = func() { d.setDevicePasswordDialog("", refresh) }
	}
	rows = append(rows, r)

	r = setupRow{title: "Local models (open-weight)", optional: true, action: "Models...", do: d.localModelsDialog}
	n := len(d.localModels)
	switch {
	case n > 0:
		r.ok, r.state = true, plural(n, "local model")+" ready (Ollama or other local servers)"
	case ollamaReachable(session.OllamaHost()):
		r.state = "Ollama is running but has no tool-capable model yet"
	default:
		r.state = "none: optional; run open-weight models through Ollama or another local server"
	}
	rows = append(rows, r)
	return rows
}

// setupMissing: whether a required item (Claude Code, sign-in) is missing.
func (d *desk) setupMissing() bool {
	for _, r := range d.setupRows(nil) {
		if !r.ok && !r.optional {
			return true
		}
	}
	return false
}

func (d *desk) setupPane() fyne.CanvasObject {
	box := container.NewVBox()
	var fill func()
	fill = func() {
		box.Objects = nil
		d.syncKmemButton() // kmem may have been installed meanwhile
		box.Add(caption("Everything Quill Desktop needs, and what it can add. Quill for Android needs none of it: it only connects to this PC."))
		for _, r := range d.setupRows(func() { d.do(fill) }) {
			r := r
			icon := widget.NewIcon(theme.ConfirmIcon())
			if !r.ok {
				icon.SetResource(theme.WarningIcon())
				if r.optional {
					icon.SetResource(theme.InfoIcon())
				}
			}
			state := caption(r.state)
			state.Wrapping = fyne.TextWrapWord
			var btn fyne.CanvasObject = widget.NewLabel("")
			if r.action != "" && r.do != nil {
				b := widget.NewButton(r.action, r.do)
				if !r.ok && !r.optional {
					b.Importance = widget.HighImportance
				}
				btn = b
			}
			box.Add(container.NewBorder(nil, nil, container.NewCenter(icon), container.NewCenter(btn), container.NewVBox(boldLabel(r.title), state)))
			box.Add(widget.NewSeparator())
		}
		again := widget.NewButtonWithIcon("Check again", theme.ViewRefreshIcon(), fill)
		again.Importance = widget.LowImportance
		welcome := widget.NewButton("Run welcome again", func() { d.showWelcome(func() { d.do(fill) }) })
		welcome.Importance = widget.LowImportance
		box.Add(container.NewHBox(again, welcome))
		box.Refresh()
	}
	fill()
	return container.NewVScroll(box)
}

func (d *desk) showSetup() {
	dlg := dialog.NewCustom("Setup", "Close", d.setupPane(), d.win)
	dlg.Resize(fyne.NewSize(760, 560))
	dlg.Show()
}

// setupAtStart: on a new install the Welcome first, then Setup once when
// Claude Code or the sign-in is missing.
func (d *desk) setupAtStart() {
	fresh := d.freshInstall()
	if !fresh {
		d.app.Preferences().SetBool(prefWelcomeSeen, true) // existing installs skip the Welcome
	}
	go func() {
		time.Sleep(3 * time.Second) // the account check runs first
		d.do(func() {
			if fresh && !d.app.Preferences().Bool(prefWelcomeSeen) {
				d.showWelcome(func() { d.setupIfMissing(); d.askKwinWhenReady() })
				return
			}
			d.setupIfMissing()
			d.askFeatureQuestionsWhenReady()
			d.syncKwinRules()
			d.askKwinWhenReady()
		})
	}()
}

func (d *desk) setupIfMissing() {
	if d.app.Preferences().Bool("setupSeen") || !d.setupMissing() {
		return
	}
	d.app.Preferences().SetBool("setupSeen", true)
	d.showSetup()
}
