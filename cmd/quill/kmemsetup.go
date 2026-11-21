package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func bundledKmemDir() string {
	var dirs []string
	if v := os.Getenv("QUILL_KMEM_DIR"); v != "" {
		dirs = append(dirs, v)
	}
	if v := os.Getenv("APPDIR"); v != "" {
		dirs = append(dirs, filepath.Join(v, "usr", "share", "quill", "kmem"))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "..", "..", "share", "quill", "kmem"), filepath.Join(filepath.Dir(exe), "..", "share", "quill", "kmem"))
	}
	dirs = append(dirs, "/usr/share/quill/kmem")
	for _, d := range dirs {
		if st, err := os.Stat(filepath.Join(d, "kmem")); err == nil && !st.IsDir() {
			return filepath.Clean(d)
		}
	}
	return ""
}

func bundledKmemVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
}

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
	bin := filepath.Join(home, ".local", "bin")
	return map[string]map[string]any{
		"SessionStart": {"hooks": []any{map[string]any{"type": "command", "command": shellQuote(filepath.Join(bin, "kmem")) + " brief 2>&1 || true", "timeout": 10}}},
		"PreToolUse": {"matcher": "Bash|Agent|Task|Write|Edit|MultiEdit|NotebookEdit",
			"hooks": []any{map[string]any{"type": "command", "command": shellQuote(filepath.Join(bin, "kmem-guard")), "timeout": 10}}},
	}
}

func hasHookCommand(groups []any, needle string) bool {
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if c, _ := hm["command"].(string); strings.Contains(c, needle) {
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

func copyExec(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".quill-tmp"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
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

func setupKmem(home, bundled string, now time.Time) ([]string, error) {
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, err
	}
	var done []string
	for _, f := range []string{"kmem", "kmem-guard"} {
		dst := filepath.Join(bin, f)
		if _, err := os.Stat(dst); err == nil {
			done = append(done, "kept the existing "+dst+" (Quill updates it only when it is an older kmem release)")
			continue
		}
		if err := copyExec(filepath.Join(bundled, f), dst); err != nil {
			return done, err
		}
		done = append(done, "installed "+dst)
	}
	changed, backup, err := mergeKmemHooks(filepath.Join(home, ".claude", "settings.json"), home, now)
	if err != nil {
		return done, err
	}
	if changed {
		line := "added the kmem brief (SessionStart) and kmem-guard (PreToolUse) hooks to ~/.claude/settings.json"
		if backup != "" {
			line += "; backup " + tildePath(backup)
		}
		done = append(done, line)
	} else {
		done = append(done, "the kmem hooks were already in ~/.claude/settings.json")
	}
	return done, nil
}

func (d *desk) kmemSettings() fyne.CanvasObject {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	btn := widget.NewButton("Set up kmem...", nil)
	btn.Importance = widget.LowImportance
	btn.Hide()
	refresh := func() {
		bundled := bundledKmemDir()
		bv := bundledKmemVersion(bundled)
		home, _ := os.UserHomeDir()
		if p := kmemBin(); p != "" && (bundled == "" || p != userKmemPath() || kmemSetupComplete(home)) {
			text := "kmem is installed: " + tildePath(p)
			if bv != "" {
				text += " (Quill Desktop comes with kmem " + bv + " and updates an older kmem in ~/.local/bin to it when it starts)"
			}
			status.SetText(text)
			btn.Hide()
			return
		}
		if bundled == "" {
			status.SetText("kmem is not installed, and this build of Quill doesn't bundle it.")
			btn.Hide()
			return
		}
		status.SetText("kmem " + bv + " comes with Quill but isn't set up yet. kmem gives sessions shared memory: handoffs, tasks and rules.")
		if kmemBin() != "" {
			status.SetText("kmem setup is incomplete (kmem-guard or the Claude hooks are missing). Finish it to turn on the guards and the session brief.")
			btn.SetText("Finish kmem setup...")
		}
		btn.Show()
	}
	btn.OnTapped = func() { d.kmemSetupDialog(refresh) }
	refresh()
	return container.NewVBox(status, btn, d.memoryModeRadio(nil))
}

// kmemSetupDialog copies the bundled kmem and adds its hooks (after asking).
func (d *desk) kmemSetupDialog(after func()) {
	home, _ := os.UserHomeDir()
	bundled := bundledKmemDir()
	steps := widget.NewLabel("Quill will:\n" +
		"• copy kmem and kmem-guard to ~/.local/bin\n" +
		"• add two hooks to ~/.claude/settings.json (a backup is made first; your other hooks stay):\n" +
		"    SessionStart: kmem brief\n" +
		"    PreToolUse (Bash, Agent, Task, Write, Edit): kmem-guard\n" +
		"kmem keeps its store in ~/.local/share/kmem and needs python3. Settings for paths and backups go in ~/.config/kmem/config.json (optional).")
	steps.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustomConfirm("Set up kmem", "Set up", "Cancel", container.NewVScroll(steps), func(ok bool) {
		if !ok {
			return
		}
		done, err := setupKmem(home, bundled, time.Now())
		msg := strings.Join(done, "\n")
		if err != nil {
			msg = strings.TrimSpace(msg + "\nStopped: " + err.Error())
		}
		d.kmemVer.reset()
		if after != nil {
			after()
		}
		dialog.ShowInformation("kmem setup", msg, d.win)
	}, d.win)
	dlg.Resize(fyne.NewSize(560, 320))
	dlg.Show()
}
