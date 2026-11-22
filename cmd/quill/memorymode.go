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

// planFormatHint: how a plan's questions should look so Quill's plan card can
// turn each into a control (built in since the user retired shared rule 28,
// 2026-11-22).
const planFormatHint = "When a plan or proposal ends with questions for the user, number them, one per line (\"1. …?\", \"2. …?\"), put each question's options in its line (\"A or B?\", \"yes or no?\", \"pick any of a–d?\") and mark a recommended option \"(recommended)\"; Quill's plan card turns each into its own buttons, checkboxes or text field."

// buildGateHint: no build after every change (the user waited on one build per
// feature); ask with a plan card instead (user 2026-11-22). Settings → General
// "Ask before building" (prefBuildGate, on by default) turns it off.
const buildGateHint = "Don't build (compile, package, release or rebuild dist/) after each change until the user asks for a build. When a build would be next, end with a plan card question: add more features, debug, secure, or build? Mark the one you recommend."

const prefBuildGate = "buildGate"

func (d *desk) buildGateOn() bool {
	return d.app.Preferences().BoolWithFallback(prefBuildGate, true)
}

// coordinatorHint: the session the user chats with coordinates; feature work
// goes to subagents (user 2026-11-22). Online (Claude) sessions only: local
// models rarely run subagents well. Subagents never see Quill Desktop's
// appended prompt, so they don't re-delegate. Settings → General
// "Coordinate with subagents" (prefCoordinator, on by default) turns it off.
const coordinatorHint = "You coordinate; subagents do the feature work. Stay free to answer the user at once. For each feature, fix or improvement the user asks for, design the split, then give the design and implementation to a subagent (one per feature, each in its own git worktree and branch, with the kmem briefing when kmem is on), running in the background. Relay every question you or a subagent has to the user as numbered plan card questions, and pass the answers back. Review, merge and test each result yourself. Small lookups and answers you do directly."

const prefCoordinator = "coordinator"

func (d *desk) coordinatorOn(model string) bool {
	return !isLocal(model) && d.app.Preferences().BoolWithFallback(prefCoordinator, true)
}

func sessionPrompt(kmem, buildGate, coordinator bool) string {
	p := quillPromptHint + " " + planFormatHint
	if buildGate {
		p += " " + buildGateHint
	}
	if coordinator {
		p += " " + coordinatorHint
	}
	if kmem {
		return p + " " + kmemMemoryHint
	}
	return p
}

// kmemAssistantHint: the kmem window's operator (docs/design/kmem-window.md §6,
// final text, at most 900 characters).
const kmemAssistantHint = `You are kmem-lock-audit, the kmem operator in Quill Desktop's kmem window; use kmem --as kmem-lock-audit. Start with kmem brief, kmem handoff get kmem-lock-audit, then the open tasks titled "kmem issue:" (topic kmem). Your job: explain audit and maint warnings, review recycle bin items (advise; only the user empties it), suggest topic splits (rule 29: split downward, dry run first), tidy tasks, check handoffs, debug kmem state other sessions report, explain pzlock status. Reads are free; before any kmem write show the plan and wait for approval; one fact per kmem add. Never: kmem trash empty, repair history by hand, edit kmem data or backups, pzlock clear, edit Projects/kmem, install, or change the RULES file; that is kmem-dev's: file a "kmem issue: ..." task instead. No background jobs or subagent writes. When Quill Desktop asks, release or transfer claims and save your handoff.`

// sessionPromptFor: the prompt Quill Desktop appends for s. The kmem window's
// hidden assistant gets the operator hint and always the kmem memory hint,
// without the coordinator and build-gate hints (it edits no code).
func (d *desk) sessionPromptFor(s *deskSession, kmemMem bool) string {
	if isHiddenSession(s) {
		return sessionPrompt(true, false, false) + " " + kmemAssistantHint
	}
	return sessionPrompt(kmemMem, d.buildGateOn(), d.coordinatorOn(s.model))
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
		d.refreshKmemVer()
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
