package main

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/codex"
	"github.com/katachinosekai/quill/internal/protocol"
)

type composerCatalog struct {
	codex.Catalog
	loading bool
}

func (s *deskSession) codexComposerAvailable() bool {
	if s == nil || !s.codex || s.remote != nil {
		return false
	}
	if !s.running() {
		return true
	}
	args := s.proc.Options().BridgeArgs
	var opts codex.Options
	return len(args) == 2 && args[0] == "codex-bridge" && json.Unmarshal([]byte(args[1]), &opts) == nil && opts.ComposerSettings
}

func (d *desk) loadComposerCatalog(s *deskSession, refresh bool) {
	if !s.codexComposerAvailable() {
		return
	}
	if d.codexCatalogs == nil {
		d.codexCatalogs = map[string]*composerCatalog{}
	}
	cwd := s.cwd
	entry := d.codexCatalogs[cwd]
	if entry != nil && (entry.loading || !refresh) {
		return
	}
	bin, err := codexBinary()
	if err != nil {
		return
	}
	entry = &composerCatalog{loading: true}
	d.codexCatalogs[cwd] = entry
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		catalog, _ := codex.ReadCatalog(ctx, bin, cwd)
		d.do(func() {
			entry.Catalog, entry.loading = catalog, false
			d.refreshComposerFor(s)
			for _, pane := range d.panes {
				if pane.view.s != nil && pane.view.s.cwd == cwd {
					d.syncPaneControls(pane)
				}
			}
		})
	}()
}

func (d *desk) refreshComposerFor(s *deskSession) {
	if s == nil {
		return
	}
	if s == d.current || s == d.targetSession(d.main.role) {
		d.updateChrome()
	}
	if p := d.paneFor(s); p != nil {
		d.syncPaneControls(p)
	}
	if a := d.assistOf(s); a != nil {
		d.syncReasoningControl(a.reasoning, s)
	}
}

func (d *desk) selectedCodexModel(s *deskSession) *codex.ModelInfo {
	catalog := d.codexCatalogs[s.cwd]
	if catalog == nil {
		return nil
	}
	active := s.model
	if active == "default" {
		active = s.codexDefaultModel
	}
	for i := range catalog.Models {
		model := &catalog.Models[i]
		if !model.Hidden && (model.Model == active && active != "" || model.IsDefault && active == "") {
			return model
		}
	}
	return nil
}

func (d *desk) codexEfforts(s *deskSession) []string {
	model := d.selectedCodexModel(s)
	if model == nil {
		return nil
	}
	var supported []string
	for _, option := range model.SupportedReasoningEfforts {
		if option.ReasoningEffort != "" && !contains(supported, option.ReasoningEffort) {
			supported = append(supported, option.ReasoningEffort)
		}
	}
	return supported
}

func (d *desk) codexMode(s *deskSession, want string) *codex.ModeInfo {
	catalog, model := d.codexCatalogs[s.cwd], d.selectedCodexModel(s)
	if catalog == nil || model == nil {
		return nil
	}
	for i := range catalog.Modes {
		mode := &catalog.Modes[i]
		if mode.Mode == want && (mode.Model == "" || mode.Model == model.Model) &&
			(mode.ReasoningEffort == "" || contains(d.codexEfforts(s), mode.ReasoningEffort)) {
			return mode
		}
	}
	return nil
}

func (d *desk) composerModeOptions(s *deskSession) []string {
	if !s.codex {
		return modeOptions(s)
	}
	if d.codexMode(s, "plan") != nil {
		return []string{"default", "plan"}
	}
	return []string{"default"}
}

func (d *desk) codexTurnSettings(s *deskSession) protocol.CodexTurnSettings {
	settings := protocol.CodexTurnSettings{Model: s.model, Mode: "default"}
	if model := d.selectedCodexModel(s); model != nil {
		switch {
		case contains(d.codexEfforts(s), s.codexEffort):
			settings.Effort = s.codexEffort
		case s.model != "default":
			settings.Effort = model.DefaultReasoningEffort
		}
	}
	if value := d.codexMode(s, s.mode); value != nil && s.mode == "plan" {
		settings.Mode = value.Mode
		settings.ModeInstructions = value.DeveloperInstructions
		if settings.Effort == "" {
			settings.Effort = value.ReasoningEffort
		}
	}
	return settings
}

func (d *desk) codexModelItems(s *deskSession) []*fyne.MenuItem {
	d.loadComposerCatalog(s, false)
	names := map[string]string{"default": "Default"}
	if entry := d.codexCatalogs[s.cwd]; entry != nil {
		for _, model := range entry.Models {
			names[model.Model] = model.DisplayName
		}
	}
	var items []*fyne.MenuItem
	for _, model := range d.modelOptions(s) {
		item := fyne.NewMenuItem(firstNonEmpty(names[model], model), func() {
			d.setModel(s, model)
			d.refreshComposerFor(s)
		})
		item.Checked = model == s.model
		items = append(items, item)
	}
	label := "Refresh models"
	if entry := d.codexCatalogs[s.cwd]; entry != nil && entry.loading {
		label = "Loading models…"
	}
	refresh := fyne.NewMenuItem(label, func() { d.loadComposerCatalog(s, true) })
	refresh.Disabled = label != "Refresh models"
	return append(items, fyne.NewMenuItemSeparator(), refresh)
}

type reasoningControl struct {
	root    *fyne.Container
	label   *widget.Label
	slider  *widget.Slider
	session *deskSession
	choices []string
	current string
	syncing bool
}

func (d *desk) newReasoningControl() *reasoningControl {
	c := &reasoningControl{
		label:  widget.NewLabel("Reasoning effort"),
		slider: widget.NewSlider(0, 1),
	}
	c.label.SizeName = theme.SizeNameCaptionText
	c.slider.Step = 1
	c.root = container.NewHBox(c.label, container.NewCenter(container.NewGridWrap(fyne.NewSize(68, 24), c.slider)))
	c.root.Hide()
	c.slider.OnChanged = func(value float64) {
		i := int(value)
		if i >= 0 && i < len(c.choices) {
			c.label.SetText("Reasoning effort · " + firstNonEmpty(c.choices[i], "Default"))
		}
	}
	c.slider.OnChangeEnded = func(value float64) {
		i, session := int(value), c.session
		if c.syncing || session == nil || i < 0 || i >= len(c.choices) || !session.codexComposerAvailable() {
			return
		}
		effort := c.choices[i]
		if effort == session.codexEffort || effort != "" && !contains(d.codexEfforts(session), effort) {
			return
		}
		session.codexEffort = effort
		d.refreshComposerFor(session)
	}
	return c
}

func (d *desk) syncReasoningControl(c *reasoningControl, session *deskSession) {
	if c == nil {
		return
	}
	if !session.codexComposerAvailable() {
		c.session = nil
		c.root.Hide()
		return
	}
	d.loadComposerCatalog(session, false)
	levels := d.codexEfforts(session)
	if len(levels) == 0 {
		c.session = nil
		c.root.Hide()
		return
	}
	choices := append([]string{""}, levels...)
	if c.session == session && c.current == session.codexEffort && slices.Equal(c.choices, choices) {
		return
	}
	c.session, c.current, c.choices = session, session.codexEffort, choices
	c.syncing = true
	c.slider.Max = float64(len(choices) - 1)
	i := max(0, slices.Index(choices, c.current))
	c.slider.SetValue(float64(i))
	c.slider.OnChanged(float64(i))
	c.slider.Refresh()
	c.syncing = false
	c.root.Show()
}
