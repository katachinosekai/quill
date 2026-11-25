package main

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// The main message box while the assistant box is open over the chat
// (td1e074, user 2026-11-23): the overlay has no message box of its own; the
// main one (the focused pane's in split view) belongs to the box's assistant
// until the box closes or docks. Its placeholder and label say who it
// messages. Drafts are kept per target: the tab's draft comes back when the
// box closes, the assistant's when it opens again. Docked, the box keeps its
// own message box and the main one stays with the tab.

// mainTarget: who the main message box messages while it isn't the tab.
type mainTarget struct {
	role string     // the box role it messages ("" = the tab, as always)
	in   *sendEntry // the entry it took over
	key  string     // the tab draft to put back (the tab the entry belongs to)
}

// assistDraftKey: the draft key of role's main-box draft (never a session id).
func assistDraftKey(role string) string { return "\x00assist:" + role }

// tabDraftKey: tab id's draft key; no tab keeps its draft too.
func tabDraftKey(id string) string {
	if id == "" {
		return "\x00notab"
	}
	return id
}

// targetLabel: "Message the Quill operator" for role.
func targetLabel(role string) string {
	if role == boxKmem {
		return "Message the kmem assistant"
	}
	if isCodexAssistant(role) {
		return "Message " + roleDef(role).title
	}
	return "Message the " + roleDef(role).title
}

// mainEntry: the main message box (the focused pane's in split view).
func (d *desk) mainEntry() (*sendEntry, string) {
	if d.split() {
		if p := d.focused; p != nil && p.input != nil {
			id := ""
			if p.view.s != nil {
				id = p.view.s.id
			}
			return p.input, id
		}
		return nil, ""
	}
	id := ""
	if d.strip != nil {
		id = d.strip.lastCur
	} else if d.current != nil {
		id = d.current.id
	}
	return d.input, id
}

// syncMainTarget hands the main message box to the overlay's assistant, or
// back to the tab, keeping each one's draft. Called when the box opens,
// closes, docks or undocks.
func (d *desk) syncMainTarget() {
	want := ""
	if d.assistOverlayOpen() {
		want = d.abox.role
		if w := d.kmemWin; want == boxKmem && w != nil && w.codexShown {
			want = roleCodexKmem
		}
	}
	in, key := d.mainEntry()
	if want == d.main.role && (want == "" || d.main.in == in) {
		d.syncBoxInputs()
		return
	}
	// Give the entry back first.
	if old := d.main; old.role != "" && old.in != nil && d.strip != nil {
		if s := d.targetSession(old.role); s != nil && s.qedit != nil && s.qedit.in == old.in {
			d.finishQueueEdit(s, s.qedit.text)
		}
		d.strip.keepBox(assistDraftKey(old.role), old.in)
		d.strip.fillBox(tabDraftKey(old.key), old.in)
		s := d.entrySession(old.in)
		setHint(old.in, s.messageHint(), d.tabSuggestion(s), false)
		if old.in == d.input && d.strip != nil {
			d.strip.lastCur = old.key
		}
	}
	d.main = mainTarget{}
	if want != "" && in != nil && d.strip != nil {
		if s := d.targetSession(want); s != nil && s.qedit != nil && s.qedit.in != in {
			d.finishQueueEdit(s, s.qedit.text)
		}
		if s := d.entrySession(in); s != nil && s.qedit != nil && s.qedit.in == in {
			d.finishQueueEdit(s, s.qedit.text)
		}
		d.strip.keepBox(tabDraftKey(key), in)
		d.strip.fillBox(assistDraftKey(want), in)
		d.main = mainTarget{role: want, in: in, key: key}
		setHint(in, targetLabel(want), d.targetSuggestion(want), true)
		if d.cmds != nil {
			d.cmds.hide()
		}
	}
	d.syncBoxInputs()
	if d.backendBtn != nil {
		d.updateChrome()
		for _, p := range d.panes {
			d.syncPaneControls(p)
		}
	}
}

// syncBoxInputs: an assistant's own message box shows unless the main one
// messages it (the overlay); the main box's label says who it messages.
func (d *desk) syncBoxInputs() {
	defer d.syncCues()
	if b := d.abox; b != nil {
		for role, v := range b.views {
			if v.inRow == nil {
				continue
			}
			if o := v.inRow(); o != nil {
				setShown(o, d.main.role != role)
			}
		}
	}
	if d.targetLbl != nil {
		if d.main.role != "" && d.main.in == d.input {
			setLabel(d.targetLbl, strings.Replace(targetLabel(d.main.role), "Message", "To", 1)+" · Esc closes the box and gives this box back to the tab")
			d.targetLbl.Show()
		} else {
			d.targetLbl.Hide()
		}
	}
}

// Suggestions are per target too (te68cb1): while the main message box
// messages an assistant it shows that assistant's prompt suggestion (ghost
// text, Tab takes it), none if it has none, never the tab's; closing or
// docking the box brings the tab's back. Autocorrect learns from the
// target's conversation the same way.

// setHint sets in's placeholder to hint and its suggestion to sug; named
// puts hint before the suggestion too, so a split pane still says whom it
// messages ("Message the Quill operator: <suggestion>   (Tab)").
func setHint(in *sendEntry, hint, sug string, named bool) {
	in.hint = hint
	in.sugName = ""
	if named {
		in.sugName = hint
	}
	in.suggestion = ""
	in.SetPlaceHolder(hint)
	in.setSuggestion(sug)
}

// targetSuggestion: the prompt suggestion of role's assistant session, ""
// when it has none. /clear and /compact are the tab's business: never here.
func (d *desk) targetSuggestion(role string) string {
	s := d.targetSession(role)
	if s == nil || s.conv == nil || isClearSuggestion(s.conv.Suggestion) {
		return ""
	}
	return s.conv.Suggestion
}

// tabSuggestion: the suggestion a tab's message box shows for s.
func (d *desk) tabSuggestion(s *deskSession) string {
	if s == nil || s.conv == nil || s.takeover || s.terminal != nil && !s.running() {
		return ""
	}
	return d.shownSuggestion(s)
}

// entrySession: the tab session in belongs to (the main one's or a pane's).
func (d *desk) entrySession(in *sendEntry) *deskSession {
	if in == d.input {
		return d.current
	}
	for _, p := range d.panes {
		if p.input == in {
			return p.view.s
		}
	}
	return nil
}

// suggest sets in's suggestion: tab's, or its assistant's while in messages one.
func (d *desk) suggest(in *sendEntry, tab string) {
	if r := d.targeted(in); r != "" {
		tab = d.targetSuggestion(r)
	}
	in.setSuggestion(tab)
}

// syncTargetSuggestion follows the targeted assistant's suggestion after s's
// events (a new suggestion, or the one cleared by a sent message).
func (d *desk) syncTargetSuggestion(s *deskSession) {
	if d.main.role != "" && d.main.in != nil && s != nil && d.targetSession(d.main.role) == s {
		d.main.in.setSuggestion(d.targetSuggestion(d.main.role))
	}
}

// knownFor: the words autocorrect keeps for in: its target's conversation.
func (d *desk) knownFor(in *sendEntry, tab *deskSession) func(string) bool {
	if r := d.targeted(in); r != "" {
		return d.knownWords(d.targetSession(r))
	}
	return d.knownWords(tab)
}

func setShown(o fyne.CanvasObject, on bool) {
	if o.Visible() == on {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}

// targeted: in messages an assistant now (its role), "" when it is the tab's.
func (d *desk) targeted(in *sendEntry) string {
	if in != nil && d.main.in == in {
		return d.main.role
	}
	return ""
}

// targetSession: the session the targeted assistant runs, or nil.
func (d *desk) targetSession(role string) *deskSession {
	if a := d.assistByRole(role); a != nil {
		return a.s
	}
	return nil
}

// sendTargeted sends in's text to the assistant it messages; false when in
// is the tab's (the caller sends as usual).
func (d *desk) sendTargeted(in *sendEntry, text string, now bool) bool {
	role := d.targeted(in)
	if role == "" {
		return false
	}
	if strings.TrimSpace(text) == "" {
		return true
	}
	a := d.assistByRole(role)
	if a == nil || d.saveQueueEdit(a.s, in, text) || pendingRevisionMatches(a.s, text) {
		return true
	}
	if now && d.reviseApproval(a.s, in, text) || !now && d.reviseTypedApproval(a.s, in, text) {
		return true
	}
	if now {
		if s := a.s; s != nil && s.running() {
			d.interrupt(s)
		}
	}
	if d.assistDeliver(a, text) == startAgentFirst {
		return true
	}
	in.SetText("")
	in.setSuggestion(d.targetSuggestion(role))
	d.strip.keepDraft(assistDraftKey(role), "")
	if role == boxQuill {
		if c := d.quillChat; c != nil && a.s != nil && c.view.s != a.s {
			c.view.show(a.s)
		}
	}
	d.assistRefresh(a)
	return true
}

func (d *desk) messageSession(in *sendEntry, tab *deskSession) *deskSession {
	if role := d.targeted(in); role != "" {
		return d.targetSession(role)
	}
	return tab
}

func operatorButtonRole(role string) string {
	switch role {
	case roleCodexQuill:
		return boxQuill
	case roleCodexKmem:
		return boxKmem
	}
	return role
}

func (d *desk) showMessageBackend(in *sendEntry, tab *deskSession, b *widget.Button, after func()) {
	role := d.targeted(in)
	if role == "" {
		d.showBackendMenu(tab, b, after)
		return
	}
	menuAbove(b, d.operatorAgentItems(role))
}

func (d *desk) operatorAgentItems(role string) []*fyne.MenuItem {
	current := claudeAgent
	if isCodexAssistant(role) {
		current = codexAgent
	}
	return pickItems([]string{claudeAgent, codexAgent}, current, func(v string) {
		codex := v == codexAgent
		if role == roleKmem || role == roleCodexKmem {
			if w := d.kmemWin; w != nil {
				w.chooseAgent(codex)
			}
		} else {
			d.chooseQuillAgent(codex)
		}
	})
}

func (d *desk) syncMainOperatorControls() {
	role := d.targeted(d.input)
	if role == "" || d.backendBtn == nil {
		return
	}
	d.backendBtn.SetText("Agent")
	d.backendBtn.Enable()
	d.input.Enable()
	d.sendBtn.Enable()
	d.modelBtn.Disable()
	d.modeBtn.Disable()
	d.stopBtn.Disable()
	d.syncAutoChip(d.autoBtn, nil)
	s := d.targetSession(role)
	d.syncReasoningControl(d.reasoning, s)
	if s == nil {
		setText(d.stats, "")
		return
	}
	setText(d.stats, statsLine(s))
	suppress := d.suppress
	d.suppress = true
	setOptions(d.modelSel, d.modelOptions(s))
	setOptions(d.modeSel, d.composerModeOptions(s))
	d.modelSel.SetSelected(s.model)
	d.modeSel.SetSelected(s.mode)
	d.suppress = suppress
	d.syncBarButtons(s)
	if s.codex && s.codexComposerAvailable() || !s.codex && s.running() {
		d.modelBtn.Enable()
		d.modeBtn.Enable()
	}
	if s.running() && (s.conv.Busy || s.proc.Busy() || hasPendingPlan(s)) {
		d.stopBtn.Enable()
	}
}
