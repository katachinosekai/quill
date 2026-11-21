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

func kmemHooks(home string) map[string]map[string]any {
	bin := filepath.Join(home, ".local", "bin")
	return map[string]map[string]any{
		"SessionStart": {"hooks": []any{map[string]any{"type": "command", "command": filepath.Join(bin, "kmem") + " brief 2>&1 || true", "timeout": 10}}},
		"PreToolUse": {"matcher": "Bash|Agent|Task|Write|Edit|MultiEdit|NotebookEdit",
			"hooks": []any{map[string]any{"type": "command", "command": filepath.Join(bin, "kmem-guard"), "timeout": 10}}},
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
		groups, _ := hooks[event].([]any)
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

func setupKmem(home, bundled string, now time.Time) ([]string, error) {
	bin := filepath.Join(home, ".local", "bin")
	for _, f := range []string{"kmem", "kmem-guard"} {
		if _, err := os.Stat(filepath.Join(bin, f)); err == nil {
			return nil, fmt.Errorf("%s already exists; Quill never replaces an installed kmem", filepath.Join(bin, f))
		}
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return nil, err
	}
	var done []string
	for _, f := range []string{"kmem", "kmem-guard"} {
		if err := copyExec(filepath.Join(bundled, f), filepath.Join(bin, f)); err != nil {
			return done, err
		}
		done = append(done, "installed "+filepath.Join(bin, f))
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
		if p := kmemBin(); p != "" {
			text := "kmem is installed: " + tildePath(p)
			if bv != "" {
				text += " (Quill bundles kmem " + bv + "; your installed copy is never replaced)"
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
		btn.Show()
	}
	btn.OnTapped = func() {
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
			d.kmemVer = kmemVersion{}
			refresh()
			dialog.ShowInformation("kmem setup", msg, d.win)
		}, d.win)
		dlg.Resize(fyne.NewSize(560, 320))
		dlg.Show()
	}
	refresh()
	return container.NewVBox(status, btn)
}
