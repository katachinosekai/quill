package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/codex"
	"github.com/katachinosekai/quill/internal/session"
)

type kmemGuardScope struct {
	Stage, Candidate string
	Since            float64
	Guard            int
}

func (s kmemGuardScope) sameRound(t kmemGuardScope) bool {
	return s.Candidate == t.Candidate && s.Stage == t.Stage && s.Since == t.Since
}

type kmemHookRollout struct {
	Rollout struct {
		Candidate string
		Stages    []struct{ Name string }
		Session   *kmemGuardScope `json:"session_guard"`
	}
}

const hookProbeText = "<scheduled-trigger source=\"quill-kmem-guard\">\n" +
	"Quill is verifying the kmem pre-tool hook for this staged session. Run exactly 'ls -d .' once using the shell tool. Report the result. Use no other tools and perform no other work.\n" +
	"</scheduled-trigger>"

func (p *kmemPanel) refreshHookReadiness() {
	if p.hookReading {
		return
	}
	d := p.d
	p.hookReading = true
	go func() {
		var r kmemHookRollout
		d.kmemJSONShared(&r, "rollout", "status")
		d.do(func() {
			stage := ""
			if stages := r.Rollout.Stages; r.Rollout.Candidate != "" && len(stages) > 0 {
				stage = stages[len(stages)-1].Name
			}
			s := d.hookReadinessSession(stage)
			if s == nil {
				p.hookReading = false
				p.hookStatus, p.hookProbeStatus = "", ""
				p.hookTarget = nil
				p.render()
				return
			}
			if p.hookTarget != s.proc {
				p.hookTarget, p.hookProbeStatus = s.proc, ""
			}
			go p.readHookReadiness(s, s.proc, d.kmemTopicFor(s), stage)
		})
	}()
}

func (d *desk) hookReadinessSession(stage string) *deskSession {
	usable := func(s *deskSession) bool {
		return s != nil && s.codex && s.remote == nil && s.running() && !s.stopping
	}
	if stage != "" {
		for _, s := range d.sessions {
			if usable(s) && d.kmemTopicFor(s) == stage {
				return s
			}
		}
	}
	if usable(d.current) {
		return d.current
	}
	if a := d.assistByRole(roleCodexKmem); a != nil && usable(a.s) {
		return a.s
	}
	return nil
}

func (p *kmemPanel) readHookReadiness(s *deskSession, proc *session.Process, topic, stage string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	raw, err := proc.Control(ctx, map[string]any{"subtype": "kmem_hooks"})
	var status codex.KmemHookStatus
	var scope *kmemGuardScope
	if err == nil {
		err = json.Unmarshal(raw, &status)
	}
	if err == nil && status.Guard == "ready" && stage != "" && stage == topic {
		scope = p.d.readGuardScope(stage, status.Thread)
	}
	p.d.do(func() {
		p.hookReading = false
		if p.hookTarget != proc || s.proc != proc || !s.running() {
			return
		}
		if err != nil || status.Thread == "" {
			p.hookStatus = "Codex hook status is unavailable in this session."
		} else {
			p.hookStatus = hookReadinessText(status)
			if stage == topic && status.Guard == "ready" && scope == nil {
				p.hookStatus += " Waiting for per-session evidence from staged kmem."
			}
		}
		p.render()
		if scope != nil && scope.Stage == topic && scope.Stage == stage {
			p.queueGuardProbe(s, proc, status, *scope, time.Now().Add(30*time.Second))
		}
	})
}

func hookReadinessText(status codex.KmemHookStatus) string {
	var parts []string
	for _, item := range []struct{ label, state string }{
		{"kmem-guard", status.Guard}, {"pzlock ensure", status.Lock}, {"kmem brief", status.Brief},
	} {
		parts = append(parts, item.label+" "+item.state)
	}
	text := "Codex hooks: " + strings.Join(parts, " · ") + "."
	if status.Guard == "review" || status.Lock == "review" || status.Brief == "review" {
		text += " Review the pending definitions in Codex /hooks."
	}
	return text
}

func (d *desk) readGuardScope(stage, thread string) *kmemGuardScope {
	if !validKmemName(stage) || !sessionIDRe.MatchString(thread) {
		return nil
	}
	var r kmemHookRollout
	if !d.kmemJSON(&r, "--as", stage, "rollout", "status", "--session", thread) || r.Rollout.Session == nil {
		return nil
	}
	scope := r.Rollout.Session
	if scope.Candidate == "" || scope.Stage == "" || scope.Since <= 0 {
		return nil
	}
	return scope
}

func probeKey(thread string, scope kmemGuardScope) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%s/%.6f", thread, scope.Stage, scope.Candidate, scope.Since))))
}

func (p *kmemPanel) guardStatus(proc *session.Process, text string) {
	if p.hookTarget == proc {
		p.hookProbeStatus = text
		p.render()
	}
}

func (p *kmemPanel) canProbe(s *deskSession, proc *session.Process, scope kmemGuardScope) bool {
	return p.hookTarget == proc && s.proc == proc && s.running() && !s.stopping &&
		s.remote == nil && p.d.kmemTopicFor(s) == scope.Stage
}

func (d *desk) guardProbeIdle(s *deskSession) bool {
	return !s.loading && !s.inTurn && !s.conv.Busy && !s.proc.Busy() && !s.queueBlocked() && s.qedit == nil &&
		!s.takeover && s.terminal == nil && s.kmemSave == nil && s.endWait == nil &&
		!s.planWait && !hasPendingPlan(s) && !s.codexUpgrading && len(s.queue) == 0 && len(s.conv.PendingPermissions()) == 0 && !s.codexWorkers &&
		s.conv.Background() == 0 && d.codexTurnSettings(s).Mode != "plan"
}

func (p *kmemPanel) queueGuardProbe(s *deskSession, proc *session.Process, status codex.KmemHookStatus, scope kmemGuardScope, until time.Time) {
	d := p.d
	if !p.canProbe(s, proc, scope) || !time.Now().Before(until) {
		return
	}
	if scope.Guard > 0 {
		p.guardStatus(proc, "kmem recorded a successful guard evaluation for this staged session.")
		return
	}
	key := "kmemGuardProbe." + probeKey(status.Thread, scope)
	prefs := d.app.Preferences()
	if prefs.Bool(key) {
		return
	}
	retry := func() { p.queueGuardProbe(s, proc, status, scope, until) }
	if !d.guardProbeIdle(s) {
		d.afterUI(time.Second, retry)
		return
	}

	// Re-read the round immediately before dispatch. Waiting for an idle tab
	// must not produce a probe in a candidate that has since been replaced.
	go func() {
		fresh := d.readGuardScope(scope.Stage, status.Thread)
		d.do(func() {
			if !p.canProbe(s, proc, scope) || prefs.Bool(key) || !time.Now().Before(until) {
				return
			}
			if fresh == nil || !fresh.sameRound(scope) {
				p.guardStatus(proc, "The rollout changed before the guard probe could run.")
				return
			}
			if fresh.Guard > scope.Guard {
				p.guardStatus(proc, "kmem recorded a successful guard evaluation for this staged session.")
				return
			}
			if !d.guardProbeIdle(s) {
				d.afterUI(time.Second, retry)
				return
			}
			prefs.SetBool(key, true)
			if !d.deliverTurnWithOrigin(s, hookProbeText, false, true) {
				p.guardStatus(proc, "The staged guard probe could not be sent.")
				return
			}
			p.guardStatus(proc, "Waiting for kmem to record the staged guard probe.")
			go p.verifyGuardProbe(proc, status.Thread, scope)
		})
	}()
}

func (p *kmemPanel) verifyGuardProbe(proc *session.Process, thread string, scope kmemGuardScope) {
	d := p.d
	for range 25 {
		time.Sleep(time.Second)
		fresh := d.readGuardScope(scope.Stage, thread)
		if fresh == nil || !fresh.sameRound(scope) {
			d.do(func() { p.guardStatus(proc, "The rollout changed before the guard probe was verified.") })
			return
		}
		if fresh.Guard > scope.Guard {
			d.do(func() { p.guardStatus(proc, "kmem recorded a successful guard evaluation for this staged session.") })
			return
		}
	}
	d.do(func() { p.guardStatus(proc, "kmem has not recorded a successful evaluation from this guard probe.") })
}
