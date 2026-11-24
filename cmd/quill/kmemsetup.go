package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

// kmem is not part of Quill Desktop (user 2026-11-24): it is distributed to
// Patreon patrons. Quill Desktop uses an installed kmem (kmemBin: PATH, else
// ~/.local/bin/kmem) and hides the kmem features without one; Settings →
// Setup, Settings → General → Memory (kmem) and the Welcome point to
// kmemPatreonURL.

// kmemPatreonURL is where patrons get kmem.
// TODO(user): the real Patreon page for kmem.
const kmemPatreonURL = "https://www.patreon.com/"

// kmemNoneText: Settings → General → Memory (kmem) without kmem.
const kmemNoneText = "kmem is not installed: Quill Desktop sessions use Claude Code's auto memory (MEMORY.md per project)."

// kmemGetHint is the short "Get kmem" text shown when kmem is not installed.
const kmemGetHint = "kmem is not installed. It gives sessions shared memory (handoffs, tasks, shared rules) and is available to Patreon patrons; Quill Desktop uses it once it is installed."

func userKmemPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "kmem")
}

func kmemBin() string {
	if p, err := exec.LookPath("kmem"); err == nil {
		return p
	}
	if st, err := os.Stat(userKmemPath()); err == nil && !st.IsDir() {
		return userKmemPath()
	}
	return ""
}

func shellQuote(p string) string {
	if !strings.ContainsAny(p, " \t'\"$`\\") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func kmemHooks(home string) map[string]map[string]any {
	return kmemHooksFor(runtime.GOOS, home, windowsPython())
}

// kmemHooksFor: on Windows (kmem 1.5.0+) the hooks run the scripts through
// Python with a quoted forward-slash path, which cmd, PowerShell and Git
// Bash all accept (PZ Lock Audit 2, kmem v1.5.0).
func kmemHooksFor(goos, home, python string) map[string]map[string]any {
	bin := filepath.Join(home, ".local", "bin")
	brief := shellQuote(filepath.Join(bin, "kmem")) + " brief 2>&1 || true"
	guard := shellQuote(filepath.Join(bin, "kmem-guard"))
	if goos == "windows" {
		fwd := func(p string) string { return `"` + strings.ReplaceAll(p, `\`, "/") + `"` }
		brief = python + " " + fwd(filepath.Join(bin, "kmem")) + " brief"
		guard = python + " " + fwd(filepath.Join(bin, "kmem-guard"))
	}
	return map[string]map[string]any{
		"SessionStart": {"hooks": []any{map[string]any{"type": "command", "command": brief, "timeout": 10}}},
		"PreToolUse": {"matcher": kmemGuardMatcher,
			"hooks": []any{map[string]any{"type": "command", "command": guard, "timeout": 10}}},
	}
}

// windowsPython: the Python command for kmem on Windows ("" when none works;
// the Microsoft Store's python.exe stub doesn't answer --version).
var windowsPython = func() string {
	if runtime.GOOS != "windows" {
		return "python3"
	}
	for _, c := range []string{"python", "py"} {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		out, err := exec.CommandContext(ctx, c, "--version").CombinedOutput()
		cancel()
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "Python 3") {
			return c
		}
	}
	return ""
}

// kmemCommand builds the command that runs kmem (through Python on Windows).
func kmemCommand(goos, python, path string, args ...string) (string, []string) {
	if goos == "windows" {
		return firstNonEmpty(python, "python"), append([]string{path}, args...)
	}
	return path, args
}

// kmemGuardMatcher: Claude Code on Windows runs commands with its PowerShell
// tool, which kmem-guard checks from kmem 1.5.2 on (PZ Lock Audit 2).
const kmemGuardMatcher = "Bash|PowerShell|Agent|Task|Write|Edit|MultiEdit|NotebookEdit"

// addPowerShellToGuard adds PowerShell to the matcher of existing kmem-guard
// groups that match Bash but not PowerShell (installs from before kmem 1.5.2).
func addPowerShellToGuard(groups []any) bool {
	changed := false
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		m, _ := gm["matcher"].(string)
		if gm == nil || !hasHookCommand([]any{gm}, "kmem-guard") {
			continue
		}
		parts := strings.Split(m, "|")
		hasBash, hasPS := false, false
		for _, p := range parts {
			hasBash = hasBash || p == "Bash"
			hasPS = hasPS || p == "PowerShell"
		}
		if !hasBash || hasPS {
			continue
		}
		out := make([]string, 0, len(parts)+1)
		for _, p := range parts {
			out = append(out, p)
			if p == "Bash" {
				out = append(out, "PowerShell")
			}
		}
		gm["matcher"] = strings.Join(out, "|")
		changed = true
	}
	return changed
}

func hasHookCommand(groups []any, needle string) bool {
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			// Windows hooks quote the path: python "C:/…/kmem" brief.
			if c, _ := hm["command"].(string); strings.Contains(strings.NewReplacer(`"`, "", "'", "").Replace(c), needle) {
				return true
			}
		}
	}
	return false
}

func mergeKmemHooks(settingsPath, home string, now time.Time) (bool, string, error) {
	if real, err := filepath.EvalSymlinks(settingsPath); err == nil {
		settingsPath = real
	}
	settings := map[string]any{}
	raw, err := os.ReadFile(settingsPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return false, "", fmt.Errorf("%s is not valid JSON; not changed: %w", settingsPath, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return false, "", err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	for event, group := range kmemHooks(home) {
		groups, isList := hooks[event].([]any)
		if hooks[event] != nil && !isList {
			return false, "", fmt.Errorf("%s has an unexpected %q hook entry; not changed", settingsPath, event)
		}
		needle := "kmem brief"
		if event == "PreToolUse" {
			needle = "kmem-guard"
		}
		if hasHookCommand(groups, needle) {
			if event == "PreToolUse" && addPowerShellToGuard(groups) {
				changed = true
			}
			continue
		}
		hooks[event] = append(groups, group)
		changed = true
	}
	if !changed {
		return false, "", nil
	}
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return false, "", err
	}
	backup := ""
	if raw != nil {
		backup = settingsPath + ".bak-quill-" + now.Format("20060102-150405")
		if err := os.WriteFile(backup, raw, 0o600); err != nil {
			return false, "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return false, backup, err
	}
	tmp := settingsPath + ".quill-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return false, backup, err
	}
	return true, backup, os.Rename(tmp, settingsPath)
}

func kmemHooksPresent(home string) bool {
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return false
	}
	var s struct {
		Hooks map[string][]any `json:"hooks"`
	}
	return json.Unmarshal(raw, &s) == nil && hasHookCommand(s.Hooks["SessionStart"], "kmem brief") && hasHookCommand(s.Hooks["PreToolUse"], "kmem-guard")
}

func kmemSetupComplete(home string) bool {
	for _, f := range []string{"kmem", "kmem-guard"} {
		if _, err := os.Stat(filepath.Join(home, ".local", "bin", f)); err != nil {
			return false
		}
	}
	return kmemHooksPresent(home)
}

// kmemGuardInstalled: kmem-guard sits next to kmem in ~/.local/bin, where the
// hooks Quill Desktop adds point (kmemHooksFor).
func kmemGuardInstalled(home string) bool {
	for _, f := range []string{"kmem", "kmem-guard"} {
		if st, err := os.Stat(filepath.Join(home, ".local", "bin", f)); err != nil || st.IsDir() {
			return false
		}
	}
	return true
}

// kmemHooksMissing: an installed kmem in ~/.local/bin whose Claude Code
// hooks Quill Desktop can still add.
func kmemHooksMissing(home string) bool {
	return kmemGuardInstalled(home) && !kmemHooksPresent(home)
}

// addKmemHooks adds the kmem hooks of an installed kmem to
// ~/.claude/settings.json. Quill Desktop never installs kmem itself.
func addKmemHooks(home string, now time.Time) ([]string, error) {
	if !kmemGuardInstalled(home) {
		return nil, errors.New("kmem and kmem-guard are not in ~/.local/bin; install kmem first")
	}
	changed, backup, err := mergeKmemHooks(filepath.Join(home, ".claude", "settings.json"), home, now)
	if err != nil {
		return nil, err
	}
	if !changed {
		return []string{"the kmem hooks were already in ~/.claude/settings.json"}, nil
	}
	line := "added the kmem brief (SessionStart) and kmem-guard (PreToolUse) hooks to ~/.claude/settings.json"
	if backup != "" {
		line += "; backup " + tildePath(backup)
	}
	return []string{line}, nil
}

// kmemStatus: what Settings → General → Memory (kmem) says, and whether it
// offers to add the hooks.
func kmemStatus(home, bin string) (text string, offerHooks bool) {
	switch {
	case bin == "":
		return kmemNoneText, false
	case kmemHooksMissing(home):
		return "kmem is installed (" + tildePath(bin) + "), but its Claude Code hooks are missing: sessions don't get the kmem brief or the guards.", true
	}
	return "kmem is installed: " + tildePath(bin), false
}

func (d *desk) kmemSettings() fyne.CanvasObject {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	card := d.kmemStatusCard(status)
	btn := widget.NewButton("Add the kmem hooks...", nil)
	btn.Importance = widget.LowImportance
	refresh := func() {
		bin := ""
		if d.kmemAvailable() {
			bin = kmemBin()
		}
		text, offer := kmemStatus(homeDir(), bin)
		status.SetText(text)
		d.syncKmemAdverts()
		btn.Hidden = !offer
		btn.Refresh()
		d.syncKmemButton()
	}
	btn.OnTapped = func() { d.kmemHooksDialog(refresh) }
	refresh()
	d.kmemBinSetBtn = widget.NewButtonWithIcon(binButtonText(d.kmemNeedsNow.Bin), theme.DeleteIcon(), d.showBinDialog)
	d.kmemBinSetBtn.Importance = widget.LowImportance
	d.kmemBinSetBtn.Alignment = widget.ButtonAlignLeading
	if !d.kmemAvailable() {
		d.kmemBinSetBtn.Disable()
	}
	// Everything below the card does nothing without kmem: hidden until kmem
	// is installed (syncKmemOnlySettings).
	d.kmemOnlySet = container.NewVBox(d.memoryModeRadio(nil), widget.NewSeparator(), d.kmemAssistSetting(), d.kmemBadgeSetting(), widget.NewSeparator(), d.defaultRulesSettings(),
		widget.NewSeparator(), caption("Facts and tasks sessions moved to kmem's recycle bin: review, restore, or delete them for good."), d.kmemBinSetBtn)
	d.kmemOnlySet.Hidden = !d.kmemAvailable()
	return container.NewVBox(card, btn, d.kmemOnlySet)
}

// kmemHooksDialog adds the hooks of an installed kmem (after asking).
func (d *desk) kmemHooksDialog(after func()) {
	home := homeDir()
	steps := widget.NewLabel("Quill will add two hooks to ~/.claude/settings.json (a backup is made first; your other hooks stay):\n" +
		"    SessionStart: kmem brief\n" +
		"    PreToolUse (Bash, PowerShell, Agent, Task, Write, Edit): kmem-guard\n" +
		"They run the kmem and kmem-guard in ~/.local/bin.")
	steps.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustomConfirm("kmem hooks", "Add", "Cancel", container.NewVScroll(steps), func(ok bool) {
		if !ok {
			return
		}
		done, err := addKmemHooks(home, time.Now())
		msg := strings.Join(done, "\n")
		if err != nil {
			msg = strings.TrimSpace(msg + "\nStopped: " + err.Error())
		}
		d.kmemVer.reset()
		if after != nil {
			after()
		}
		dialog.ShowInformation("kmem hooks", msg, d.win)
	}, d.win)
	dlg.Resize(fyne.NewSize(560, 240))
	dlg.Show()
}

// The model of the kmem window's assistant (docs/design/kmem-window.md §9):
// "" is Online Claude (the New sessions model if that is a Claude model, never
// a local one), "off" leaves the window panels-only.
const (
	prefKmemAssistModel = "kmemAssistModel"
	kmemAssistOff       = "off"
	optAssistSame       = "Online Claude (default)"
	optAssistOff        = "Off"
)

// kmemAssistModel: the model the assistant starts with, or kmemAssistOff.
func (d *desk) kmemAssistModel() string {
	p := d.app.Preferences()
	if v := p.String(prefKmemAssistModel); v != "" {
		return v
	}
	// A local model only when chosen here explicitly: a local New sessions
	// model may not be running (ECONNREFUSED) or able to use tools.
	if m := p.StringWithFallback("newModel", "default"); !isLocal(m) {
		return m
	}
	return "default"
}

// kmemAssistOptions: Online Claude (default), Off, the Claude models and the
// local models that are listed (plus the saved choice if no longer listed).
func (d *desk) kmemAssistOptions() []string {
	opts := append([]string{optAssistSame, optAssistOff}, claudeModels...)
	opts = append(opts, d.localModels...)
	if cur := d.app.Preferences().String(prefKmemAssistModel); cur != "" && cur != kmemAssistOff && !contains(opts, cur) {
		opts = append(opts, cur)
	}
	return opts
}

func (d *desk) kmemAssistSetting() fyne.CanvasObject {
	prefs := d.app.Preferences()
	sel := widget.NewSelect(d.kmemAssistOptions(), nil)
	switch cur := prefs.String(prefKmemAssistModel); cur {
	case "":
		sel.SetSelected(optAssistSame)
	case kmemAssistOff:
		sel.SetSelected(optAssistOff)
	default:
		sel.SetSelected(cur)
	}
	sel.OnChanged = func(v string) {
		switch v {
		case optAssistSame:
			prefs.SetString(prefKmemAssistModel, "")
		case optAssistOff:
			prefs.SetString(prefKmemAssistModel, kmemAssistOff)
		case "":
			return
		default:
			prefs.SetString(prefKmemAssistModel, v)
		}
	}
	if !d.kmemAvailable() {
		sel.Disable()
	}
	return container.NewVBox(boldLabel("kmem assistant"), sel,
		caption("The model of kmem-lock-audit, Quill Desktop's kmem assistant (always running in the background). By default it runs on Online Claude, even when new sessions start on a local model; a local model runs it only when picked here and must be able to use tools. Off: it doesn't run and the kmem window shows its panels only. A change applies the next time it starts."))
}

// kmemBadgeSetting: the switch for the badge on the kmem button (kmemseen.go).
func (d *desk) kmemBadgeSetting() fyne.CanvasObject {
	chk := widget.NewCheck("Badge on the kmem button", func(on bool) {
		d.app.Preferences().SetBool(prefKmemBadge, on)
		d.updateKmemBadge()
	})
	chk.SetChecked(d.kmemBadgeOn())
	return container.NewVBox(widget.NewSeparator(), chk,
		caption("The badge shows only what needs you: a question or plan from the kmem assistant, a paused rollout, new maintenance warnings and open \"kmem issue:\" tasks. Each shows once after it is listed in the kmem window; it comes back when it changes."))
}
