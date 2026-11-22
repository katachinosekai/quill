package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Quill Desktop's default kmem rules: generic shared rules for people who run
// several sessions on the same projects. Optional (user 2026-11-21: the
// recommended choice for beginners; no rules is the advanced one): installed
// only when the user picks it, only when there is no rules file yet, and
// never over an existing one.
//
//go:embed defaultrules.md
var defaultKmemRules string

// kmemRulesPath is where kmem reads its rules: "rules_file" in kmem's config
// (KMEM_CONFIG, else ~/.config/kmem/config.json), else
// ~/.local/share/kmem/RULES.md.
func kmemRulesPath(home string) string {
	cfg := os.Getenv("KMEM_CONFIG")
	if cfg == "" {
		cfg = filepath.Join(home, ".config", "kmem", "config.json")
	}
	if b, err := os.ReadFile(cfg); err == nil {
		var c struct {
			RulesFile string `json:"rules_file"`
		}
		if json.Unmarshal(b, &c) == nil && c.RulesFile != "" {
			p := c.RulesFile
			if p == "~" || len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == '\\') {
				p = filepath.Join(home, p[1:])
			}
			return filepath.Clean(p)
		}
	}
	return filepath.Join(home, ".local", "share", "kmem", "RULES.md")
}

var errRulesExist = errors.New("a rules file already exists")

// installDefaultRules writes the default rules where kmem reads them, only
// when no file is there (an existing one is never touched).
func installDefaultRules(home string) (string, error) {
	p := kmemRulesPath(home)
	if _, err := os.Stat(p); err == nil {
		return p, errRulesExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return p, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return p, errRulesExist
		}
		return p, err
	}
	if _, err := f.WriteString(defaultKmemRules); err != nil {
		f.Close()
		return p, err
	}
	return p, f.Close()
}

// defaultRulesSettings: the optional row under Memory (kmem).
func (d *desk) defaultRulesSettings() fyne.CanvasObject {
	home := homeDir()
	note := caption("Shared rules every Claude session follows: start with kmem brief, claim tasks, save handoffs, git safety in shared folders. " +
		"Recommended if you are new to running several sessions; leave it off to write your own rules (advanced). Installed only when you have no rules file; yours is never replaced.")
	var chk *widget.Check
	sync := func() {
		p := kmemRulesPath(home)
		if _, err := os.Stat(p); err == nil {
			// Checked is set directly: SetChecked would run OnChanged and try to install.
			chk.Checked = true
			chk.Disable()
			chk.Text = "kmem rules are set up: " + tildePath(p)
			chk.Refresh()
		}
	}
	chk = widget.NewCheck("Install Quill's default kmem rules (recommended for beginners)", func(on bool) {
		if !on || chk.Disabled() {
			return
		}
		p, err := installDefaultRules(home)
		switch {
		case errors.Is(err, errRulesExist):
			dialog.ShowInformation("kmem rules", "You already have a rules file ("+tildePath(p)+"); Quill left it as it is.", d.win)
		case err != nil:
			chk.SetChecked(false)
			dialog.ShowError(fmt.Errorf("could not install the default rules: %w", err), d.win)
			return
		default:
			dialog.ShowInformation("kmem rules", "Installed Quill's default rules at "+tildePath(p)+". Sessions read them with kmem rules; edit the file to make them yours.", d.win)
		}
		sync()
	})
	show := widget.NewButton("Show rules", func() { d.showDefaultRules() })
	show.Importance = widget.LowImportance
	sync()
	return container.NewVBox(container.NewBorder(nil, nil, nil, show, chk), note)
}

func (d *desk) showDefaultRules() {
	l := widget.NewLabel(defaultKmemRules)
	l.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustom("Quill's default kmem rules", "Close", container.NewVScroll(l), d.win)
	dlg.Resize(fyne.NewSize(760, 560))
	dlg.Show()
}
