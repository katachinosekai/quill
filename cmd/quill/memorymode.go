package main

import (
	"encoding/json"
	"os"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

const kmemMemoryHint = "Project memory is kmem, not Claude memory files: start with `kmem brief`, read with `kmem get`, `kmem find` or `kmem context \"<task>\"`, and save one fact per `kmem add`. Pinned kmem facts hold the user's preferences; follow them. Do not write MEMORY.md or other Claude memory files."

const autoMemoryOffEnv = "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"

const (
	memoryModeKey    = "memoryMode"
	memoryModeKmem   = "kmem"
	memoryModeClaude = "claude"
)

func memoryModeUsesKmem(mode string, setupComplete, available bool) bool {
	switch mode {
	case memoryModeClaude:
		return false
	case memoryModeKmem:
		return available
	}
	return setupComplete && available
}

func (d *desk) kmemMemory() bool {
	mode := d.app.Preferences().String(memoryModeKey)
	if mode == memoryModeClaude {
		return false
	}
	return memoryModeUsesKmem(mode, kmemSetupComplete(homeDir()), d.kmemAvailable())
}

func sessionPrompt(kmem bool) string {
	if kmem {
		return quillPromptHint + " " + kmemMemoryHint
	}
	return quillPromptHint
}

func sessionMemoryEnv(kmem bool) []string {
	if kmem {
		return []string{autoMemoryOffEnv}
	}
	return nil
}

// memoryModeRadio: which memory Quill sessions use (the same setting as the
// checkbox under Settings → General → Memory (kmem)).
func (d *desk) memoryModeRadio(changed func()) fyne.CanvasObject {
	optKmem := "kmem (shared memory: handoffs, tasks, rules)"
	optClaude := "Claude's auto memory (MEMORY.md per folder)"
	r := widget.NewRadioGroup([]string{optKmem, optClaude}, nil)
	if d.kmemMemory() {
		r.SetSelected(optKmem)
	} else {
		r.SetSelected(optClaude)
	}
	r.OnChanged = func(v string) {
		switch v {
		case optKmem:
			d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		case optClaude:
			d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
		default:
			return
		}
		if changed != nil {
			changed()
		}
	}
	if !d.kmemAvailable() {
		r.Disable()
	}
	return container.NewVBox(boldLabel("Memory for Quill sessions"), r,
		caption("Applies to Quill sessions started or resumed after a change."))
}

// kmemRulesFile: the shared rules file from kmem's config ("" if unknown).
func kmemRulesFile() string {
	b, err := os.ReadFile(kmemCfgPath())
	if err != nil {
		return ""
	}
	var c struct {
		RulesFile   string `json:"rules_file"`
		ProjectsDir string `json:"projects_dir"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return expandHome(c.RulesFile)
}

// kmemProjectsDir: kmem's projects_dir ("" if unknown).
func kmemProjectsDir() string {
	b, err := os.ReadFile(kmemCfgPath())
	if err != nil {
		return ""
	}
	var c struct {
		ProjectsDir string `json:"projects_dir"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return expandHome(c.ProjectsDir)
}

func (d *desk) kmemMemoryRow() fyne.CanvasObject {
	name := widget.NewLabel("Shared memory (kmem)")
	name.TextStyle = fyne.TextStyle{Bold: true}
	caption := "not in use: Quill sessions use Claude's auto memory"
	if d.kmemMemory() {
		caption = "in use: Quill sessions read kmem; Claude's auto memory is off in them"
	}
	sub := widget.NewLabel(caption)
	sub.SizeName = theme.SizeNameCaptionText
	sub.Wrapping = fyne.TextWrapWord
	btn := widget.NewButtonWithIcon("Show", theme.InfoIcon(), func() { d.showKmemMemory() })
	btn.Importance = widget.MediumImportance
	if !d.kmemAvailable() {
		btn.Disable()
	}
	return container.NewBorder(nil, nil, nil, container.NewCenter(btn), container.New(&tightVBox{}, name, sub))
}

func (d *desk) showKmemMemory() {
	var brief struct {
		Brief string `json:"brief"`
	}
	var ctx struct {
		Context string `json:"context"`
	}
	parts := []string{}
	if d.kmemJSON(&brief, "brief") && strings.TrimSpace(brief.Brief) != "" {
		parts = append(parts, strings.TrimSpace(brief.Brief))
	}
	if d.kmemJSON(&ctx, "context", "user preferences and how to work") {
		if p := pinnedSection(ctx.Context); p != "" {
			parts = append(parts, p)
		}
	}
	text := strings.Join(parts, "\n\n")
	if text == "" {
		text = "kmem didn't answer (it needs kmem with API 1)."
	}
	lbl := widget.NewLabel(text)
	lbl.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustom("Shared memory (kmem)", "Close", container.NewVScroll(lbl), d.win)
	dlg.Resize(fyne.NewSize(720, 460))
	dlg.Show()
}

func pinnedSection(context string) string {
	var out []string
	in := false
	for _, line := range strings.Split(context, "\n") {
		if strings.HasPrefix(line, "pinned:") {
			in = true
			out = append(out, line)
			continue
		}
		if in {
			if !strings.HasPrefix(line, " ") {
				break
			}
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
