package main

import (
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

func (d *desk) memoryModeCheck() *widget.Check {
	chk := widget.NewCheck("Use kmem as memory for Quill sessions (turns off Claude's auto memory in them; Konsole sessions are not changed). Applies to sessions started or resumed after this.", func(on bool) {
		if on {
			d.app.Preferences().SetString(memoryModeKey, memoryModeKmem)
		} else {
			d.app.Preferences().SetString(memoryModeKey, memoryModeClaude)
		}
	})
	chk.Checked = d.kmemMemory()
	return chk
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
