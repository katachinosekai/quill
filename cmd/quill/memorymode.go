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

// memoryModeUsesKmem: an explicit choice (Settings, /memory, Welcome, the
// switch or its undo) wins; with none, sessions use kmem while kmem is
// installed and Claude's memory otherwise, so removing kmem falls back on its
// own (user 2026-11-24, kmem f3f9c92). kmem needs to be installed either way.
func memoryModeUsesKmem(mode string, available bool) bool {
	return mode != memoryModeClaude && available
}

func (d *desk) kmemMemory() bool {
	return memoryModeUsesKmem(d.app.Preferences().String(memoryModeKey), d.kmemAvailable())
}

// kmemByDefault: sessions use kmem only because no mode was chosen, and no
// switch ever finished: Claude Code's notes were not imported and the switch
// did not add kmem's hooks or turn auto memory off.
func (d *desk) kmemByDefault() bool {
	p := d.app.Preferences()
	return p.String(memoryModeKey) == "" && p.String(prefKmemSwitchBackup) == "" && d.kmemAvailable()
}

// kmemSwitchWanted: "Switch to kmem" would change something: kmem is
// installed and sessions use Claude's memory, or kmem only by default.
func (d *desk) kmemSwitchWanted() bool {
	return d.kmemAvailable() && (!d.kmemMemory() || d.kmemByDefault())
}

// planFormatHint: how a plan's questions should look so Quill's plan card can
// turn each into a control (built in since the user retired shared rule 28,
// 2026-11-22).
const planFormatHint = "When a plan or proposal ends with questions for the user, number them, one per line (\"1. …?\", \"2. …?\"), put each question's options in its line (\"A or B?\", \"yes or no?\", \"pick any of a–d?\") and mark a recommended option \"(recommended)\"; Quill's plan card turns each into its own buttons, checkboxes or text field."

// buildGateHint: no build after every change (the user waited on one build per
// feature); ask with a plan card instead (user 2026-11-22). Settings → General
// "Ask before building" (prefBuildGate, on by default) turns it off.
const buildGateHint = "Don't build (compile, package, release or rebuild dist/) after each change until the user asks for a build. When a build would be next, end with a plan card question: add more features, debug, secure, or build? Mark the one you recommend."

// planApprovalHint: how Quill Desktop's approvals read and what one covers
// (planscope.go), told once at session start instead of in every approval
// (user 2026-11-23). Added with plan-first (Settings → General).
const planApprovalHint = "Quill Desktop approvals are short: \"Approved.\" may be followed by \"Choice: <option>.\", \"Answers: 2 → <option>; 3 → \\\"<text>\\\".\" (your question numbers, the picked option only) and \"Also: <changes from the user>\"; \"Not approved.\" means the plan is not to run. Quill's plan gate covers the tool calls within the approved request (kmem fact f3a1828). Honor any separate native permission prompts. Background commands and subagents you start for it stay approved until they finish, and so do the plan's remaining steps in the turn their completion notice starts, unless the user writes something else meanwhile: end your turn instead of waiting for them. While your question to the user is unanswered, reply to task notifications with one status line and don't repeat the question. If the user gives no answer, ask again later; never assume the recommended option." + " " + planSpecHint

// planSpecHint: plan format spec v1 (docs/plan-format.md, user 2026-11-23):
// what a plan looks like so the plan-first gate and the plan card read it,
// and what its approval covers, auto-approve included (autoapprove.go).
const planSpecHint = "Plan format: a numbered step list, one line per step saying what runs and why; a subagent step says what the subagent will run. End with the bold line \"**Can I run steps 1–N? Yes (recommended) or no?**\", optionally followed by numbered choice questions, each on its own line. Put any closing question last in the reply, after sources and links. Subagents started or resumed for the plan inherit its approval until they finish; a user message ends the approval for new work, not for them. Read-only checks need no plan. An approval with \"Auto-approve on\" lasts until the user turns it off, user messages included: write no plans and ask no approval in this session until a Quill Desktop note says auto-approve is off; commands that may run as root, and work asked for by a peer session's message or a scheduled trigger, still need a plan."

// planGoReminder: the short form, for a session whose prompt lacks the hint.
const planGoReminder = " After \"Approved.\", background commands and subagents you start stay approved until they finish; end your turn instead of waiting for them."

const prefBuildGate = "buildGate"

func (d *desk) buildGateOn() bool {
	return d.app.Preferences().BoolWithFallback(prefBuildGate, true)
}

// coordinatorHint: the session the user chats with coordinates; feature work
// goes to subagents (user 2026-11-22). Online (Claude) sessions only: local
// models rarely run subagents well. Subagents never see Quill Desktop's
// appended prompt, so they don't re-delegate. Settings → General
// "Coordinate with subagents" (prefCoordinator, on by default) turns it off.
const coordinatorHint = "You coordinate; subagents do the feature work. Stay free to answer the user at once. For each feature, fix or improvement the user asks for, design the split, then give the design and implementation to a subagent (one per feature, each in its own git worktree and branch, with the kmem briefing when kmem is on), running in the background. Relay every question you or a subagent has to the user as numbered plan card questions, and pass the answers back. Review and integrate each result. If Plan first requires approval, include the workers and their steps on the same plan card. Pass the user's follow-ups to affected workers and ask for approval if they expand the plan. Long-running work (test suites, merges, builds, release steps) goes to background subagents too. Use Sonnet for such mechanical jobs. Use a subagent when a task is independent and takes more than a few minutes; do small changes, or ones that depend on details from the conversation, directly. Small lookups and answers you do directly too."

const prefCoordinator = "coordinator"

func (d *desk) coordinatorOn(model string) bool {
	return !isLocal(model) && d.app.Preferences().BoolWithFallback(prefCoordinator, true)
}

func sessionPrompt(kmem, buildGate, coordinator bool) string {
	return sessionPromptWith(kmem, buildGate, coordinator, topicHints{})
}

// topicHints: what a topic session's prompt teaches (docs/design/topic-tabs.md
// §3.8, §3.10, §6). Zero value = today's hints, for non-topic sessions and a
// kmem without the "topic-context" feature.
type topicHints struct {
	topic    string // the session's kmem topic ("" = not a topic session)
	endCheck bool   // kmem has "end-check": mention the end-of-session check
}

// topicMemoryHint: the kmem hint of a topic session (kmem 1.10.0 topic-context).
func topicMemoryHint(t topicHints) string {
	h := "Project memory is kmem, not Claude memory files. Topic `" + t.topic + "` (`kmem --as " + t.topic + "`): start with `kmem brief` and `kmem context --topic " + t.topic + "`, save one fact per `kmem add`, record progress in `kmem task checkpoint`, keep the one fact `" + t.topic + "/status` current with `--replaces`."
	if t.endCheck {
		h += " Before the session ends, the end-of-session check must pass."
	}
	return h + " Pinned kmem facts hold the user's preferences; follow them. Do not write MEMORY.md or other Claude memory files."
}

// topicCoordinatorHint: coordinatorHint with the subagent briefing of §3.10.
func topicCoordinatorHint(topic string) string {
	return strings.Replace(coordinatorHint, "with the kmem briefing when kmem is on", "briefed with `kmem context \"<task>\" --topic "+topic+"`, stable part first, ending \"checkpoint your task\"", 1) + " Only you write `" + topic + "/status`."
}

// coordinationUpdate conveys the current setting to a retained Claude
// process on user input without restarting its workers.
func (d *desk) coordinationUpdate(s *deskSession) (string, bool) {
	if s.codex || hiddenRole(s) == roleKmem || isLocal(s.model) {
		return "", false
	}
	want := ""
	if d.coordinatorOn(s.model) && (hiddenRole(s) != roleQuill || d.quillSource() != "") {
		want = coordinatorHint
		if d.kmemMemory() {
			if hiddenRole(s) == roleQuill {
				want = topicCoordinatorHint(quillOpName)
			} else if d.kmemFeatureCached("topic-context") {
				if topic := d.topicOf(s.id); topic != "" {
					want = topicCoordinatorHint(topic)
				}
			}
		}
	}
	prompt := s.proc.Options().Prompt
	if s.claudeCoordinationSet {
		if s.claudeCoordination == want {
			return "", false
		}
	} else if want != "" && strings.Contains(prompt, want) ||
		want == "" && !strings.Contains(prompt, "You coordinate; subagents do the feature work.") {
		return "", false
	}
	return want, true
}

func sessionPromptWith(kmem, buildGate, coordinator bool, t topicHints) string {
	p := quillPromptHint + " " + planFormatHint
	if buildGate {
		p += " " + buildGateHint
	}
	topical := kmem && t.topic != ""
	if coordinator {
		if topical {
			p += " " + topicCoordinatorHint(t.topic)
		} else {
			p += " " + coordinatorHint
		}
	}
	if topical {
		return p + " " + topicMemoryHint(t)
	}
	if kmem {
		return p + " " + kmemMemoryHint
	}
	return p
}

// kmemAssistantHint: the always-running kmem assistant, kmem's developer and
// operator in one session (docs/design/kmem-window.md §6, user 2026-11-22,
// kmem fact fc6b73c; at most 900 characters).
const kmemAssistantHint = `You are kmem-lock-audit, Quill Desktop's kmem developer and operator; use kmem --as kmem-lock-audit. Start: kmem brief, kmem handoff get kmem-lock-audit, open "kmem issue:" tasks (topic kmem). Operator: explain audit/maint warnings, review the recycle bin (only the user empties it), suggest splits (rule 29: split downward, dry run first), tidy tasks, check handoffs, debug reports. Developer: Projects/kmem, install.sh, launcher, kmem-guard, releases, rollouts, RULES file, pzlock code, backlog. Reads are free; show the plan and get the user's approval before any write or release; one fact per kmem add. Never: kmem trash empty, hand repair of history/kmem data/backups, pzlock clear. You always run in the background (background jobs, reviewer subagents ok); sessions reach you by SendMessage. Save at safe points; when Quill Desktop asks, release or transfer claims and save your handoff.`

// sessionPromptFor: the prompt Quill Desktop appends for s. The hidden kmem
// assistant gets its own hint and always the kmem memory hint, without the
// coordinator and build-gate hints (its hint asks before every write). The
// hidden Quill operator gets the topic hints and its own (quilloperator.go).
func (d *desk) sessionPromptFor(s *deskSession, kmemMem bool) string {
	switch hiddenRole(s) {
	case roleKmem:
		return sessionPrompt(true, false, false) + " " + kmemAssistantHint
	case roleQuill:
		return d.quillOperatorPrompt(s)
	}
	var t topicHints
	if kmemMem && d.kmemFeatureCached("topic-context") {
		t = topicHints{topic: d.topicOf(s.id), endCheck: d.kmemFeatureCached("end-check")}
	}
	p := sessionPromptWith(kmemMem, d.buildGateOn(), d.coordinatorOn(s.model), t)
	s.approvalHint = d.planFirst
	if d.planFirst {
		p += " " + planApprovalHint
	}
	return p
}

func sessionMemoryEnv(kmem bool) []string {
	if kmem {
		return []string{autoMemoryOffEnv}
	}
	return nil
}

// memoryModeRadio: which memory Quill sessions use (Settings → General →
// Memory (kmem) and /memory). A choice goes through requestMemoryMode: the
// radio stays on the mode in use until the switch (or its undo) completes.
func (d *desk) memoryModeRadio(changed func()) fyne.CanvasObject {
	optKmem := "kmem (shared memory: handoffs, tasks, rules)"
	optClaude := "Claude's auto memory (MEMORY.md per folder)"
	r := widget.NewRadioGroup([]string{optKmem, optClaude}, nil)
	r.Required = true
	syncing := false
	sync := func() {
		syncing = true
		if d.kmemMemory() {
			r.SetSelected(optKmem)
		} else {
			r.SetSelected(optClaude)
		}
		syncing = false
	}
	sync()
	r.OnChanged = func(v string) {
		if syncing || (v != optKmem && v != optClaude) {
			return
		}
		sync() // back to the mode in use until the choice is settled
		d.requestMemoryMode(v == optKmem, func() {
			sync()
			if changed != nil {
				changed()
			}
		})
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
