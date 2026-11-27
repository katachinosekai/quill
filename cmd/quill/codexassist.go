package main

import (
	"errors"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

const (
	roleCodexQuill = "codex-quill"
	roleCodexKmem  = "codex-kmem"
	codexAgent     = "Codex"
	claudeAgent    = "Claude Code"
)

var (
	codexQuillRole = &assistRole{id: roleCodexQuill, hidden: roleCodexQuill, label: "Quill operator"}
	codexKmemRole  = &assistRole{id: roleCodexKmem, hidden: roleCodexKmem, label: "kmem assistant"}
)

func isCodexAssistant(role string) bool {
	return role == roleCodexQuill || role == roleCodexKmem
}

func (d *desk) codexAssistant(role *assistRole) *assistant {
	a := d.assistFor(role)
	if a.view != nil {
		return a
	}
	c := d.attachAssistBox(role, role.label, "Message Codex")
	if role == codexQuillRole {
		d.codexQuillChat = c
	}
	a.chat, a.input, a.view = c.root, c.input, c.view
	id := d.app.Preferences().String(role.id + "Session")
	if s := d.sessions[id]; s != nil && s.codex && hiddenRole(s) == role.id {
		a.s, a.state = s, asLive
		c.view.show(s)
		d.syncReasoningControl(a.reasoning, s)
	}
	if role == codexKmemRole {
		a.input.onEscape = d.kmemComposerEscape
		d.setHiddenHost(roleCodexKmem, &hiddenHost{
			session: func() *deskSession { return a.s },
			view:    func() *chatView { return a.view },
			shown: func() bool {
				w := d.kmemWin
				return w.assistantSelected() && w.codexShown && (w.win != nil || d.assistBoxShowing(boxKmem))
			},
			open: d.openKmemCodex,
		})
	}
	return a
}

func (d *desk) startCodexAssistant(a *assistant) {
	fresh := a.role == codexQuillRole
	s := a.s
	if s == nil || fresh {
		dir, err := d.codexAssistantDir(a)
		if err != nil {
			d.assistReason(a, err.Error())
			return
		}
		s = d.addSessionWithBackend(dir, "default", "default", false, true)
		d.markHiddenAs(s, a.role.hidden)
		s.title = a.role.label
	}
	err := d.ensureProcess(s)
	if err == nil || a.s == nil {
		if old := a.s; old != nil && s != old {
			s.queue, old.queue = old.queue, nil
		}
		a.s, a.state = s, asLive
		d.app.Preferences().SetString(a.role.id+"Session", s.id)
		a.view.show(s)
		d.syncReasoningControl(a.reasoning, s)
	}
	if err != nil {
		d.chatFor(a.s, []*chat.Item{a.s.conv.AddError("Could not start Codex: " + err.Error())})
		return
	}
	if fresh {
		prompt := "Start as the Quill operator. Wait for the user's request."
		if d.kmemMemory() {
			prompt = "Start as the Quill operator. Read the included kmem brief and operator handoff. Use them as context and wait for the user's request."
		}
		d.sendTurn(s, prompt)
	}
	d.refreshBoxHeader()
}

func (d *desk) codexAssistantDir(a *assistant) (string, error) {
	if d.cfg.CodexRoot == "" {
		return "", errors.New("could not locate Quill’s Codex session directory")
	}
	dir := filepath.Join(d.cfg.CodexRoot, "assistants", a.role.id)
	if a.role == codexQuillRole {
		if src := d.quillSource(); src != "" {
			dir = src
		}
	}
	return dir, os.MkdirAll(dir, 0700)
}

func (d *desk) chooseQuillAgent(codex bool) {
	want := boxQuill
	target := d.quillAssist()
	if codex {
		want = roleCodexQuill
		target = d.codexAssistant(codexQuillRole)
	}
	b := d.box()
	current := d.assistByRole(b.role)
	if current == nil || current == target {
		return
	}
	b.agent.Selected = claudeAgent
	if b.role == roleCodexQuill {
		b.agent.Selected = codexAgent
	}
	b.agent.Refresh()
	d.selectOperatorAgent(current, target, func() {
		d.app.Preferences().SetBool("quillAgentCodex", codex)
		if b.role == boxQuill || b.role == roleCodexQuill {
			d.openAssistBox(want)
		}
	})
}

func (d *desk) openQuillCodex() {
	d.codexAssistant(codexQuillRole)
	d.openAssistBox(roleCodexQuill)
}

func (d *desk) openKmemCodex() {
	d.openKmemAssistant()
	if w := d.kmemWin; w != nil && w.agent != nil {
		w.showAgent(true)
	}
}

func (w *kmemWindow) agentChat() fyne.CanvasObject {
	d := w.d
	w.chatSlot = container.NewStack(w.right)
	w.agent = widget.NewSelect([]string{claudeAgent, codexAgent}, func(engine string) {
		w.chooseAgent(engine == codexAgent)
	})
	w.agent.Selected = claudeAgent
	w.agentStart = widget.NewButton("Start", func() { d.startAgent(w.selectedAgent()) })
	return container.NewBorder(container.NewHBox(w.agent, w.agentStart), nil, nil, nil, w.chatSlot)
}

func (w *kmemWindow) chooseAgent(codex bool) {
	if w.codexShown == codex {
		return
	}
	d := w.d
	current, target := w.selectedAgent(), d.assist()
	w.agent.Selected = claudeAgent
	if codex {
		target = d.codexAssistant(codexKmemRole)
	} else {
		w.agent.Selected = codexAgent
	}
	w.agent.Refresh()
	d.selectOperatorAgent(current, target, func() {
		d.app.Preferences().SetBool("kmemAgentCodex", codex)
		if win := d.kmemWin; win != nil && !win.closed {
			win.showAgent(codex)
		}
	})
}

func (w *kmemWindow) showAgent(codex bool) {
	d := w.d
	if w.codexShown != codex {
		d.cancelQueueEdit(w.selectedAgent().s)
	}
	w.codexShown = codex
	w.agent.Selected = claudeAgent
	w.chatSlot.Objects = []fyne.CanvasObject{w.right}
	if codex {
		w.agent.Selected = codexAgent
		w.chatSlot.Objects = []fyne.CanvasObject{d.codexAssistant(codexKmemRole).chat}
	}
	w.agent.Refresh()
	w.chatSlot.Refresh()
	d.syncMainTarget()
	w.panel.render()
	d.refreshBoxHeader()
	if !w.assistantSelected() {
		return
	}
	if w.win == nil && d.assistBoxShowing(boxKmem) {
		d.focusBox()
	} else if w.win != nil && codex {
		w.win.Canvas().Focus(d.codexAssistant(codexKmemRole).input)
	}
}

func (w *kmemWindow) composer() *sendEntry {
	if w.win == nil && operatorButtonRole(w.d.main.role) == boxKmem {
		return w.d.main.in
	}
	return w.selectedAgent().input
}

func (w *kmemWindow) fillComposer(text string) {
	if input := w.composer(); input != nil {
		input.SetText(text)
		input.SetCursorTextOffset(len([]rune(text)))
		w.panel.parent().Canvas().Focus(input)
	}
}
