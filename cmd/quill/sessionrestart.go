package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2/dialog"
)

const codexRestartContext = "Codex starts a new native thread using a fresh kmem handoff. The Quill tab, transcript and draft stay in place."

type sessionRestartRequest struct {
	Target      string `json:"target"`
	Cancel      bool   `json:"cancel,omitempty"`
	FromHandoff bool   `json:"from_handoff,omitempty"`
}

type sessionRestartReply struct {
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

func sessionRestartCLI(sock string, args []string, out, errOut io.Writer) int {
	var req sessionRestartRequest
	if len(args) == 2 {
		switch args[0] {
		case "--cancel":
			req.Cancel = true
		case "--from-handoff":
			req.FromHandoff = true
		}
		if req.Cancel || req.FromHandoff {
			args = args[1:]
		}
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(errOut, "usage: quill restart [--from-handoff|--cancel] NAME|id:SESSION_ID")
		return 2
	}
	req.Target = args[0]
	body, _ := json.Marshal(req)
	lines, ok := commandReply(sock, "session-restart "+string(body))
	var reply sessionRestartReply
	if !ok || len(lines) == 0 {
		fmt.Fprintln(errOut, "no running Quill Desktop answered")
		return 1
	}
	if json.Unmarshal([]byte(lines[0]), &reply) != nil || reply.Status == "" && reply.Error == "" {
		fmt.Fprintln(errOut, "invalid session restart receipt")
		return 1
	}
	if reply.Error != "" {
		fmt.Fprintln(errOut, reply.Error)
		return 1
	}
	fmt.Fprintln(out, reply.Status)
	return 0
}

func (d *desk) restartSessionReply(r sessionRestartRequest) sessionRestartReply {
	out := make(chan sessionRestartReply, 1)
	d.do(func() {
		s, problem := d.peerRecipient(canonicalPeerName(r.Target))
		if problem != "" {
			out <- sessionRestartReply{Error: problem}
			return
		}
		out <- d.requestSessionRestart(s, r)
	})
	select {
	case reply := <-out:
		return reply
	case <-time.After(1500 * time.Millisecond):
		return sessionRestartReply{Error: "restart receipt timed out; the request may already be pending"}
	}
}

func (d *desk) askSessionRestart(s *deskSession) {
	run := func() {
		reply := d.requestSessionRestart(s, sessionRestartRequest{FromHandoff: true})
		if reply.Error != "" {
			d.notify(s, reply.Error)
		}
	}
	if s.codex {
		dialog.ShowConfirm("Restart session?", codexRestartContext, func(ok bool) {
			if ok {
				run()
			}
		}, d.win)
		return
	}
	run()
}

func (d *desk) requestSessionRestart(s *deskSession, r sessionRestartRequest) sessionRestartReply {
	if s == nil || s.remote != nil || isHiddenSession(s) {
		return sessionRestartReply{Error: "choose a local session tab"}
	}
	if r.Cancel {
		if !s.restartPending {
			return sessionRestartReply{Error: "no waiting restart to cancel"}
		}
		s.restartPending = false
		d.notify(s, "Session restart cancelled.")
		return sessionRestartReply{Status: "Restart cancelled."}
	}
	if !s.running() || s.stopping {
		return sessionRestartReply{Error: "the session is not running"}
	}
	if s.restartPending {
		return sessionRestartReply{Status: "Restart already waiting."}
	}
	if s.restartActive || s.agentHandoff != nil {
		return sessionRestartReply{Error: "this session is already changing its agent process"}
	}
	if s.codex {
		if !r.FromHandoff {
			return sessionRestartReply{Error: codexRestartContext + " Use --from-handoff to confirm."}
		}
		if !d.kmemAvailable() {
			return sessionRestartReply{Error: "restarting a Codex session requires kmem for its handoff"}
		}
	}
	if reason := sessionRestartBlocker(s); reason != "" {
		s.restartPending = true
		message := "Restart waiting for " + reason + "."
		d.notify(s, message)
		return sessionRestartReply{Status: message}
	}
	d.beginSessionRestart(s)
	return sessionRestartReply{Status: "Restart requested."}
}

func sessionRestartBlocker(s *deskSession) string {
	switch {
	case len(s.conv.PendingPermissions()) != 0 || s.planWait || s.pendingRevision != nil || s.conv.AwaitsReply():
		return "your approval or reply"
	case s.stopping || s.loading || s.takeover || s.terminal != nil || s.kmemSave != nil || s.endWait != nil ||
		s.pendingEnd != 0 || s.restarting || s.queueBlocked():
		return "the current session operation to finish"
	case s.conv.Background() != 0 || s.codexWorkers || len(s.agentGrants) != 0 || len(s.planWork) != 0:
		return "background work to finish"
	case s.inTurn || s.proc.Busy() || s.conv.Busy || s.conv.Working() > 0:
		return "the current turn to finish"
	case len(s.queue) != 0 || s.qedit != nil:
		return "the message queue to clear"
	}
	return ""
}

func (d *desk) pollSessionRestarts() {
	for _, s := range d.sessions {
		if !s.restartPending {
			continue
		}
		if !s.running() {
			s.restartPending = false
			d.notify(s, "Restart cancelled because the session stopped.")
		} else if sessionRestartBlocker(s) == "" {
			s.restartPending = false
			reply := d.requestSessionRestart(s, sessionRestartRequest{FromHandoff: true})
			if reply.Error != "" {
				d.notify(s, reply.Error)
			}
		}
	}
}

func (d *desk) beginSessionRestart(s *deskSession) {
	if s.codex {
		d.handoffAgent(s, &agentHandoff{toCodex: true, restart: true})
		return
	}
	d.replaceSessionProcess(s, nil, "")
}

func (d *desk) checkSessionRestartHandoff(s *deskSession, h *agentHandoff, saved string) {
	if !s.running() {
		d.failAgentHandoff(s, h, fmt.Errorf("the previous Codex process already stopped"))
		return
	}
	if !d.kmemFeatureCached(endCheckFeature) {
		d.replaceSessionProcess(s, h, saved)
		return
	}
	p := s.proc
	go func() {
		result, problem := d.endCheck(h.name, s.id, p.PID())
		blockers := slices.DeleteFunc(result.Blockers, func(b endBlocker) bool {
			return b.Kind == "snapshot" || b.Kind == "status" // a fresh handoff was verified above
		})
		d.do(func() {
			if d.sessions[s.id] != s || s.agentHandoff != h {
				return
			}
			switch {
			case problem != "":
				d.failAgentHandoff(s, h, fmt.Errorf("kmem could not check the session: %s", problem))
			case result.Name != h.name || result.Session != s.id || s.proc != p || !s.running():
				d.failAgentHandoff(s, h, fmt.Errorf("the session changed while kmem checked it"))
			case len(blockers) > 0:
				d.failAgentHandoff(s, h, fmt.Errorf("resolve these before restarting: %s", blockerList(blockers, ", ")))
			default:
				d.replaceSessionProcess(s, h, saved)
			}
		})
	}()
}

func (d *desk) replaceSessionProcess(s *deskSession, h *agentHandoff, saved string) {
	s.restartActive = true
	d.notify(s, "Restarting the session with the current provider settings.")
	d.stopThen(s, func() {
		if d.sessions[s.id] != s || h != nil && s.agentHandoff != h {
			s.restartActive = false
			return
		}
		if s.running() {
			s.restartActive = false
			s.stopping = false
			s.agentHandoff = nil
			d.notify(s, "Restart failed because the old process could not stop.")
			d.flushQueue(s)
			return
		}
		if p := s.proc; p != nil {
			d.handleExit(s, p, nil)
		}
		s.restartActive = false
		s.agentHandoff = nil
		if err := d.ensureProcess(s); err != nil {
			d.notify(s, "The session stopped, but could not restart: "+err.Error())
			return
		}
		d.notify(s, "Session restarted.")
		if h != nil {
			d.sendResume(s, h.name, "Quill restarted this session. Treat the following saved handoff as prior context. Summarize the saved state and wait for the user's next instruction.\n\n"+saved)
		} else {
			d.flushQueue(s)
		}
		d.updateChrome()
	})
}
