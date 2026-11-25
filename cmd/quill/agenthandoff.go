package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

type handoffPhase int

const (
	handoffWaiting handoffPhase = iota
	handoffPreparing
	handoffSaving
	handoffVerifying
)

type agentHandoff struct {
	phase    handoffPhase
	toCodex  bool
	model    string
	target   *assistant
	activate func()
	name     string
	previous string
	fromItem int
}

func (d *desk) selectTabAgent(s *deskSession, codex bool) {
	d.selectTabAgentWithModel(s, codex, "")
}

func (d *desk) selectTabAgentWithModel(s *deskSession, codex bool, model string) {
	if s == nil || s.codex == codex {
		return
	}
	if !s.persisted && !s.running() && !s.loading && countMessages(s.conv) == 0 {
		s.codex = codex
		s.model, s.mode = "default", "default"
		if !codex {
			p := d.app.Preferences()
			s.model, s.mode = p.StringWithFallback("newModel", "default"), p.StringWithFallback(prefNewMode, "auto")
		}
		if model != "" {
			s.model = model
			d.localModeGuard(s)
		}
		d.updateChrome()
		if pane := d.paneFor(s); pane != nil {
			d.syncPaneControls(pane)
		}
		return
	}
	d.handoffAgent(s, &agentHandoff{toCodex: codex, model: model})
}

func (d *desk) selectOperatorAgent(current, target *assistant, activate func()) {
	if current == target {
		return
	}
	if current.s == nil || !current.s.running() {
		if current.s == nil && current.state == asProbing {
			current.run++
			current.state, current.pending = asClosed, nil
		}
		activate()
		return
	}
	if target.s != nil && target.s.running() && (target.s.conv.Busy || target.s.proc.Busy() || target.s.agentHandoff != nil) {
		d.notify(current.s, "The selected agent is working. You can switch when its turn finishes.")
		return
	}
	d.handoffAgent(current.s, &agentHandoff{
		toCodex: isCodexAssistant(target.role.id), target: target, activate: activate,
	})
}

func (d *desk) handoffAgent(s *deskSession, h *agentHandoff) {
	if s.agentHandoff != nil {
		return
	}
	switch {
	case !d.kmemAvailable():
		d.notify(s, "Switching agents for this session requires kmem.")
		return
	case s.loading || s.takeover || s.terminal != nil || s.compacting || s.kmemSave != nil || s.endWait != nil || s.stopping:
		d.notify(s, "This session is busy with another operation. Try switching when it finishes.")
		return
	}
	if h.toCodex {
		if _, err := codexBinary(); err != nil {
			d.notify(s, err.Error())
			return
		}
	}
	h.name = d.kmemTopicFor(s)
	if h.name == "" {
		if s.codex {
			h.name = d.codexTopic(s)
		} else {
			h.name = d.ensureKmem(s.id)
		}
	}
	if !validKmemName(h.name) {
		d.notify(s, "Could not determine a valid kmem handoff name.")
		return
	}
	if owner := d.runningKmemOwner(h.name, s); owner != nil {
		d.refuseSecondSession(h.name, "switch agents", owner)
		return
	}
	s.agentHandoff = h
	d.cancelQueueEdit(s)
	d.notify(s, fmt.Sprintf("Switching agents through kmem handoff %q.", h.name))
	if s.running() && (s.proc.Busy() || s.conv.Busy) {
		return
	}
	d.prepareAgentHandoff(s, h)
}

func (d *desk) prepareAgentHandoff(s *deskSession, h *agentHandoff) {
	h.phase = handoffPreparing
	go func() {
		previous, _ := d.runKmem("kmem", "handoff", "get", h.name)
		d.do(func() {
			if s.agentHandoff != h || d.sessions[s.id] != s {
				return
			}
			h.previous = strings.TrimSpace(previous)
			if s.codex {
				d.sendHandoffPrompt(s, h, "Write a concise handoff for the agent taking over this work. Include the user’s goal, completed work, working folder and files, pending decisions, and the next step. Record any known uncommitted work and running jobs. Return the brief as your reply, without using tools or starting new work.")
				return
			}
			prompt := fmt.Sprintf("The user selected another agent to continue this session. Save a fresh kmem handoff for %q now using `kmem --as %s handoff save %s`. Include the goal, relevant pointers, current state, and next steps. Account for any claims, shared lock, jobs, and uncommitted work using kmem’s normal safe-point checks. Do not start new work. Reply briefly after the handoff saves.", h.name, h.name, h.name)
			d.sendHandoffPrompt(s, h, prompt)
		})
	}()
}

func (d *desk) sendHandoffPrompt(s *deskSession, h *agentHandoff, prompt string) {
	if err := d.ensureProcess(s); err != nil {
		d.failAgentHandoff(s, h, err)
		return
	}
	h.phase, h.fromItem = handoffSaving, len(s.conv.Items)
	d.sendTurn(s, prompt)
	if !s.proc.Busy() && !s.conv.Busy {
		d.failAgentHandoff(s, h, errors.New("the source agent could not receive the handoff request"))
	}
}

func (d *desk) agentHandoffResult(s *deskSession, ev *protocol.Event) {
	h := s.agentHandoff
	if h == nil {
		return
	}
	switch h.phase {
	case handoffWaiting:
		d.prepareAgentHandoff(s, h)
	case handoffSaving:
		if ev.IsError || strings.HasPrefix(ev.TerminalReason, "aborted") {
			d.failAgentHandoff(s, h, errors.New("the handoff turn did not finish"))
			return
		}
		h.phase = handoffVerifying
		brief := ""
		if s.codex {
			for _, item := range s.conv.Items[min(h.fromItem, len(s.conv.Items)):] {
				if item.Kind == chat.Assistant && strings.TrimSpace(item.Text) != "" {
					brief = clip(item.Text, 8000)
				}
			}
			if brief == "" {
				d.failAgentHandoff(s, h, errors.New("Codex did not return a handoff brief"))
				return
			}
		}
		go d.verifyAgentHandoff(s, h, brief)
	}
}

func (d *desk) verifyAgentHandoff(s *deskSession, h *agentHandoff, brief string) {
	if brief != "" {
		out, err := d.runKmem("kmem", "--as", h.name, "handoff", "save", h.name, "--same", "--state", brief)
		if err != nil {
			d.do(func() { d.failAgentHandoff(s, h, kmemError(out, err)) })
			return
		}
	}
	out, err := d.runKmem("kmem", "handoff", "get", h.name)
	out = strings.TrimSpace(out)
	if err == nil && !freshAgentHandoff(s, h, out) {
		err = errors.New("kmem did not return a fresh handoff from this agent")
		out = ""
	}
	if err == nil {
		if status, briefErr := d.runKmem("kmem", "brief"); briefErr == nil && strings.TrimSpace(status) != "" {
			out += "\n\nCurrent kmem brief:\n" + clip(status, 2800)
		}
	}
	d.do(func() {
		if s.agentHandoff != h || d.sessions[s.id] != s {
			return
		}
		if err != nil {
			d.failAgentHandoff(s, h, kmemError(out, err))
			return
		}
		d.finishAgentHandoff(s, h, out)
	})
}

func freshAgentHandoff(s *deskSession, h *agentHandoff, current string) bool {
	if current == "" || current == h.previous {
		return false
	}
	artifact := " history: Handoff artifact " + h.name + ": saved by "
	header := func(snapshot string) string {
		for _, line := range strings.Split(snapshot, "\n") {
			if strings.HasPrefix(line, "[") && strings.Contains(line, "]"+artifact) {
				return line
			}
		}
		return ""
	}
	before, after := header(h.previous), header(current)
	if before == "" && after == "" { // older stores without artifact headers
		return true
	}
	return after != "" && after != before && (s.codex || strings.Contains(after, "(session cs:"+s.id+")"))
}

func kmemError(out string, err error) error {
	if out = strings.TrimSpace(out); out != "" {
		return errors.New(clip(out, 1500))
	}
	return err
}

func (d *desk) failAgentHandoff(s *deskSession, h *agentHandoff, err error) {
	if s.agentHandoff != h {
		return
	}
	s.agentHandoff = nil
	d.notify(s, "Agent switch cancelled: "+err.Error())
	d.updateChrome()
	d.flushQueue(s)
	d.trySwap()
}

func (d *desk) finishAgentHandoff(old *deskSession, h *agentHandoff, saved string) {
	name := h.name
	model, mode := "default", "default"
	if !h.toCodex {
		p := d.app.Preferences()
		model, mode = p.StringWithFallback("newModel", "default"), p.StringWithFallback(prefNewMode, "auto")
		if h.target != nil {
			model, mode = h.target.role.model(d), "default"
			if model == assistOff {
				d.failAgentHandoff(old, h, errors.New("the selected Claude Code assistant is off in Settings"))
				return
			}
		}
	}
	if h.model != "" {
		model = h.model
		if isLocal(model) && !contains(localModes, mode) {
			mode = "default"
		}
	}
	var next *deskSession
	if h.target != nil {
		next = h.target.s
		if next != nil && next.running() && (next.conv.Busy || next.proc.Busy() || next.agentHandoff != nil) {
			d.failAgentHandoff(old, h, errors.New("the selected agent began another turn before the handoff finished"))
			return
		}
	}
	if next == nil || !next.running() {
		dir := old.cwd
		if h.target != nil {
			if h.toCodex {
				var err error
				dir, err = d.codexAssistantDir(h.target)
				if err != nil {
					d.failAgentHandoff(old, h, err)
					return
				}
			} else if h.target.role.dirFor != nil {
				dir = h.target.role.dirFor(d)
			} else {
				dir = h.target.role.dir()
			}
		}
		if h.target != nil {
			if err := os.MkdirAll(dir, 0700); err != nil {
				d.failAgentHandoff(old, h, err)
				return
			}
		}
		next = d.addSessionWithBackend(dir, model, mode, false, h.toCodex)
		next.addDirs = append([]string(nil), old.addDirs...)
		if h.target != nil {
			d.markHiddenAs(next, h.target.role.hidden)
		}
	}
	oldName, previousName := d.org.Kmem[old.id], d.org.Kmem[next.id]
	d.stopThen(old, func() {
		if old.agentHandoff != h || d.sessions[old.id] != old {
			return
		}
		old.agentHandoff = nil
		delete(d.org.Kmem, old.id)
		d.org.Kmem[next.id] = name
		d.saveOrg()
		if err := d.ensureProcess(next); err != nil {
			delete(d.org.Kmem, next.id)
			if previousName != "" {
				d.org.Kmem[next.id] = previousName
			}
			if oldName != "" {
				d.org.Kmem[old.id] = oldName
			}
			d.saveOrg()
			d.notify(old, "Handoff saved, but the selected agent could not start: "+err.Error())
			return
		}
		if h.target == nil && !next.codex && d.org.CodexTopics[name] != "" {
			delete(d.org.CodexTopics, name)
			d.saveOrgQuiet()
		}
		next.queue = append(next.queue, old.queue...)
		old.queue = nil
		if h.target == nil {
			title, _, _ := d.rowText(old.id)
			draft := d.strip.draft(old.id)
			if old == d.current || d.paneFor(old) != nil {
				draft = d.boxDraft(old, d.inputFor(old))
			}
			d.strip.keepDraft(next.id, draft)
			d.replaceTabSession(old, next, title)
		} else {
			a := h.target
			a.s, a.state = next, asLive
			if h.toCodex {
				d.app.Preferences().SetString(a.role.id+"Session", next.id)
				a.view.show(next)
			} else {
				d.assistAttach(a)
			}
			h.activate()
		}
		d.notify(next, fmt.Sprintf("Resuming from kmem handoff %q.", h.name))
		prompt := fmt.Sprintf("The user switched agents to continue the same work. Here is the saved kmem handoff %q. Use it as prior context, preserve the user’s constraints, and resume from its next step. Existing approvals still apply.\n\n%s", h.name, saved)
		if !h.toCodex {
			prompt = fmt.Sprintf("Use your kmem name %q. ", name) + prompt
		}
		d.sendResume(next, name, prompt)
		d.trySwap()
	})
}
