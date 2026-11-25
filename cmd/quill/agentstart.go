package main

import "fyne.io/fyne/v2/widget"

const startAgentFirst = "start the agent first"

func (d *desk) startAgent(a *assistant) {
	if !d.agentCanStart(a) {
		return
	}
	if isCodexAssistant(a.role.id) {
		d.startCodexAssistant(a)
	} else {
		d.assistRestartNow(a)
	}
	d.syncAgentStart()
}

func (d *desk) agentCanStart(a *assistant) bool {
	if a == nil || a.s != nil && a.s.running() {
		return false
	}
	switch a.state {
	case asProbing, asStarting, asSaving, asStopping:
		return false
	}
	return true
}

func (d *desk) syncAgentStart() {
	update := func(button *widget.Button, a *assistant) {
		if button != nil {
			setShown(button, d.agentCanStart(a))
		}
	}
	if b := d.abox; b != nil && b.start != nil {
		var a *assistant
		switch b.role {
		case boxQuill:
			a = d.quillAssist()
		case roleCodexQuill:
			a = d.assistByRole(roleCodexQuill)
		}
		update(b.start, a)
	}
	if w := d.kmemWin; w != nil && w.agentStart != nil {
		a := w.selectedAgent()
		update(w.agentStart, a)
		if w.agent != nil {
			setShown(w.agent, (a.s == nil || a.chat == nil) &&
				d.main.role != roleKmem && d.main.role != roleCodexKmem)
		}
	}
}

func (w *kmemWindow) selectedAgent() *assistant {
	if w.codexShown {
		return w.d.codexAssistant(codexKmemRole)
	}
	return w.d.assist()
}
