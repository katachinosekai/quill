package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	kmemTimeout    = 20 * time.Second
	kmemRecentSave = 10 * time.Minute
	kmemNewChoice  = "New kmem"
	kmemNoChoice   = "No kmem"
	kmemResumeOf   = "Resume "
)

type kmemHandoff struct {
	Name  string
	Saved time.Time
}

type kmemSave struct {
	name   string
	asked  time.Time
	spare  int
	prompt string
	end    bool
	place  string
	keep   bool // save only (from /export): the session goes on
}

var handoffLine = regexp.MustCompile(`^(\S+)\s+\d+ items?, saved (\S+)`)

func parseHandoffList(out string) []kmemHandoff {
	var hs []kmemHandoff
	for _, line := range strings.Split(out, "\n") {
		m := handoffLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, m[2])
		if err != nil {
			continue
		}
		hs = append(hs, kmemHandoff{Name: m[1], Saved: t})
	}
	return hs
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

func kmemSlug(title string, taken func(string) bool) string {
	base := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(base) > 40 {
		base = strings.TrimRight(base[:40], "-")
	}
	if base == "" {
		base = "session"
	}
	name := base
	for n := 2; taken(name); n++ {
		name = fmt.Sprintf("%s-%d", base, n)
	}
	return name
}

func (d *desk) kmemAvailable() bool {
	if d.kmemOff {
		return false
	}
	if d.kmemRun != nil {
		return true
	}
	return kmemBin() != ""
}

func (d *desk) runKmem(prog string, args ...string) (string, error) {
	if d.kmemRun != nil {
		return d.kmemRun(prog, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), kmemTimeout)
	defer cancel()
	if prog == "kmem" {
		if p := kmemBin(); p != "" {
			prog = p
		}
	}
	cmd := exec.CommandContext(ctx, prog, args...)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func (d *desk) kmemHandoffs() []kmemHandoff {
	hs, err := d.kmemHandoffList()
	if err != nil {
		return nil
	}
	d.hsMu.Lock()
	d.lastHandoffs = hs
	d.hsMu.Unlock()
	return hs
}

func (d *desk) kmemTaken(hs []kmemHandoff) func(string) bool {
	return func(name string) bool {
		for _, h := range hs {
			if h.Name == name {
				return true
			}
		}
		for _, k := range d.org.Kmem {
			if k == name {
				return true
			}
		}
		return false
	}
}

func (d *desk) resumePrompt(name string) string {
	if out, err := d.runKmem("kmem-resume", name, "--print"); err == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out)
	}
	return fmt.Sprintf("You are the session for kmem handoff %q; use kmem name %q (kmem --as %q ...). "+
		"Start by running `kmem brief` and `kmem handoff get %s`, read the pointers you need (kmem show / kmem get), "+
		"then continue from the handoff state. Follow the shared rules (`kmem rules`).", name, name, name, name)
}

func kmemIntro(name string) string {
	return fmt.Sprintf("(Quill: this session's kmem name is %q. Keep notes in kmem, not in files; when Quill restarts the session it asks you to run `kmem handoff save %s`.)\n\n", name, name)
}

func kmemSaveMessage(name string) string {
	return fmt.Sprintf("Quill is restarting this session as a fresh one that resumes from kmem, and this session will then be deleted. "+
		"Save your kmem handoff now: run `kmem handoff save %s` with --pointer and --state lines for everything the next session needs; run it again even if you saved it recently, Quill checks the save time. "+
		"It refuses until your claims, pzlock turn, background jobs and uncommitted files are recorded, so record those first. "+
		"Don't start other work. Reply with one line once it is saved.", name)
}

func kmemChoices(hs []kmemHandoff) []string {
	out := []string{kmemNewChoice}
	for _, h := range hs {
		out = append(out, kmemResumeOf+h.Name+" (saved "+h.Saved.Local().Format("Jan 2 15:04")+")")
	}
	return out
}

func choiceHandoff(choice string) string {
	if !strings.HasPrefix(choice, kmemResumeOf) {
		return ""
	}
	name := strings.TrimPrefix(choice, kmemResumeOf)
	if i := strings.Index(name, " ("); i >= 0 {
		name = name[:i]
	}
	return name
}

func (d *desk) kmemSelect(apply func(*widget.Select)) *widget.Select {
	sel := widget.NewSelect([]string{kmemNewChoice}, nil)
	sel.SetSelected(kmemNewChoice)
	go func() {
		hs := d.kmemHandoffs()
		d.do(func() {
			sel.Options = kmemChoices(hs)
			sel.Refresh()
			if apply != nil {
				apply(sel)
			}
		})
	}()
	return sel
}

func (d *desk) applyKmemChoice(s *deskSession, choice, title string) {
	if name := choiceHandoff(choice); name != "" {
		d.org.Kmem[s.id] = name
		d.saveOrg()
		go func() {
			p := d.resumePrompt(name)
			d.do(func() { d.sendResume(s, name, p) })
		}()
		return
	}
	if choice != kmemNewChoice {
		return
	}
	delete(d.org.Kmem, s.id)
	d.hsMu.Lock()
	known := d.lastHandoffs
	d.hsMu.Unlock()
	d.org.Kmem[s.id] = kmemSlug(title, d.kmemTaken(known))
	s.kmemNew = true
	d.saveOrg()
	go func() {
		hs := d.kmemHandoffs()
		d.do(func() {
			cur := d.org.Kmem[s.id]
			if !s.kmemNew || cur == "" {
				return
			}
			delete(d.org.Kmem, s.id)
			taken := d.kmemTaken(hs)
			if !taken(cur) {
				d.org.Kmem[s.id] = cur
				return
			}
			d.org.Kmem[s.id] = kmemSlug(title, taken)
			d.saveOrg()
		})
	}()
}

func (d *desk) linkKmem(infos []transcript.Info) {
	changed := false
	for _, info := range infos {
		if info.Kmem == "" {
			continue
		}
		if _, set := d.org.Kmem[info.ID]; set {
			continue
		}
		if d.kmemTaken(nil)(info.Kmem) {
			continue
		}
		d.org.Kmem[info.ID] = info.Kmem
		changed = true
	}
	if changed {
		d.saveOrg()
	}
}

func (d *desk) askKmemName(id string) {
	e := widget.NewSelectEntry(nil)
	e.SetText(d.org.Kmem[id])
	if e.Text == "" {
		title, _, _ := d.rowText(id)
		e.SetPlaceHolder(kmemSlug(title, func(string) bool { return false }))
	}
	go func() {
		hs := d.kmemHandoffs()
		d.do(func() {
			var names []string
			for _, h := range hs {
				names = append(names, h.Name)
			}
			e.SetOptions(names)
		})
	}()
	note := widget.NewLabel("The kmem handoff this session saves to and resumes from. Restart as new session then saves the handoff, starts a fresh session from it and deletes this one. Leave empty to unlink.")
	note.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewForm("kmem name", "Save", "Cancel", []*widget.FormItem{widget.NewFormItem("Name", e), widget.NewFormItem("", note)}, func(ok bool) {
		if !ok {
			return
		}
		if name := strings.TrimSpace(e.Text); name == "" {
			d.org.Kmem[id] = ""
		} else {
			d.org.Kmem[id] = name
			defer d.syncTitleToKmem(id)
		}
		d.saveOrg()
	}, d.win)
	dlg.Resize(fyne.NewSize(480, 260))
	dlg.Show()
}

func (d *desk) relaunchViaKmem(id, name string) {
	old := d.session(id)
	if old == nil {
		return
	}
	spare := 0
	if old.inTurn {
		spare = 1
	}
	old.kmemSave = &kmemSave{name: name, asked: time.Now().Add(-2 * time.Second), spare: spare}
	d.notify(old, fmt.Sprintf("Restarting from kmem: waiting for this session to save its handoff “%s”.", name))
	d.sendTo(old, kmemSaveMessage(name))
	if !old.running() {
		old.kmemSave = nil
		d.notify(old, "The restart was cancelled: this session's Claude process could not start, so it couldn't save its handoff.")
	}
}

var kmemSaveExtra = [][]string{{"kmem"}, {"pzlock", "status"}}

// kmemReleaseAllowed lets through exactly the `kmem task release <id>` calls
// Quill asked for after a rewind, during that turn.
func kmemReleaseAllowed(s *deskSession, r *protocol.ControlRequest) bool {
	if len(s.releaseTasks) == 0 {
		return false
	}
	var extra [][]string
	for _, id := range s.releaseTasks {
		extra = append(extra, []string{"kmem", "task", "release", id})
	}
	return readOnlyRequest(r, extra)
}

func (d *desk) kmemSaveAllowed(s *deskSession, r *protocol.ControlRequest) bool {
	if s.kmemSave == nil {
		return false
	}
	return readOnlyRequest(r, append(d.readOnlyExtra(), kmemSaveExtra...))
}

// sendResume sends Quill's own resume-from-handoff prompt. Until the next
// message is sent, plan-first lets the session's read-only kmem startup
// commands through even when they carry `--as <its kmem name>`, as the
// prompt asks. New tabs and messages typed by the user are not affected.
func (d *desk) sendResume(s *deskSession, name, prompt string) {
	s.resumeNext = name
	d.sendTo(s, prompt)
}

func (d *desk) kmemResumeAllowed(s *deskSession, r *protocol.ControlRequest) bool {
	if s.resumeAs == "" || !d.planFirst {
		return false
	}
	extra := d.readOnlyExtra()
	for _, e := range extra {
		if len(e) > 1 && e[0] == "kmem" {
			extra = append(extra,
				append([]string{"kmem", "--as", s.resumeAs}, e[1:]...),
				append([]string{"kmem", "--as=" + s.resumeAs}, e[1:]...))
		}
	}
	return readOnlyRequest(r, extra)
}

func (d *desk) kmemAfterTurn(s *deskSession) {
	ks := s.kmemSave
	if ks == nil || ks.prompt != "" {
		return
	}
	ks.prompt = "checking"
	go func() {
		saved, recent := false, false
		for _, h := range d.kmemHandoffs() {
			if h.Name != ks.name {
				continue
			}
			if !h.Saved.Before(ks.asked.Truncate(time.Second)) {
				saved = true
			} else if ks.asked.Sub(h.Saved) <= kmemRecentSave {
				recent = true
			}
		}
		if !saved && recent {
			saved = strings.Contains(d.kmemHandoffText(ks.name), "session cs:"+s.id+")")
		}
		prompt := ""
		if saved {
			prompt = d.resumePrompt(ks.name)
		}
		d.do(func() {
			if s.kmemSave != ks {
				return
			}
			if !saved {
				ks.prompt = ""
				if hasPendingPlan(s) || s.conv.AsksApproval() {
					d.notify(s, fmt.Sprintf("Waiting for you: approve the plan so this session can save its kmem handoff “%s”; the restart continues after that.", ks.name))
					return
				}
				if ks.spare > 0 {
					ks.spare--
					return
				}
				s.kmemSave = nil
				what := "restart"
				if ks.end {
					what = "end"
				}
				if ks.keep {
					d.notify(s, fmt.Sprintf("The kmem handoff “%s” wasn't saved by this session.", ks.name))
					return
				}
				d.notify(s, fmt.Sprintf("The kmem handoff “%s” wasn't saved by this session, so the %s was cancelled and nothing was stopped or deleted. Try again once it can save.", ks.name, what))
				return
			}
			s.kmemSave = nil
			if ks.keep {
				d.notify(s, fmt.Sprintf("Saved the kmem handoff “%s”; a new session can continue from it (New session → resume from kmem).", ks.name))
				return
			}
			if ks.end {
				d.finishKmemEnd(s, ks.name, ks.place)
				return
			}
			d.finishKmemRelaunch(s, ks.name, prompt)
		})
	}()
}

func (d *desk) finishKmemRelaunch(old *deskSession, name, prompt string) {
	title, _, _ := d.rowText(old.id)
	ns := d.replaceWith(old, title)
	ns.restarting = true
	delete(d.org.Kmem, old.id)
	d.org.Kmem[ns.id] = name
	d.saveOrg()
	d.notify(ns, fmt.Sprintf("Resuming from kmem handoff “%s”; the previous session was deleted.", name))
	d.sendResume(ns, name, prompt)
	d.deleteSession(old.id)
}

func kmemEndMessage(name string) string {
	return fmt.Sprintf("Quill is ending this session; it will be stopped and archived once its kmem handoff is saved. "+
		"Save your kmem handoff now: run `kmem handoff save %s` with --pointer and --state lines for everything the next session needs; run it again even if you saved it recently, Quill checks the save time. "+
		"It refuses until your claims, shared lock turn, background jobs and uncommitted files are recorded, so record or release those first. "+
		"Don't start other work. Reply with one line once it is saved.", name)
}

// saveHandoffNow asks the session to save its kmem handoff and go on (/export
// "Save as kmem handoff").
func (d *desk) saveHandoffNow(s *deskSession) {
	name := d.org.Kmem[s.id]
	if name == "" || !d.kmemAvailable() || !s.running() || s.kmemSave != nil {
		return
	}
	spare := 0
	if s.inTurn {
		spare = 1
	}
	s.kmemSave = &kmemSave{name: name, asked: time.Now().Add(-2 * time.Second), spare: spare, keep: true}
	d.notify(s, fmt.Sprintf("Asked this session to save its kmem handoff “%s”.", name))
	d.sendTo(s, fmt.Sprintf("Save your kmem handoff now: run `kmem handoff save %s` with --pointer and --state lines for everything a new session would need to continue this work "+
		"(it refuses until your claims, shared lock turn, background jobs and uncommitted files are recorded, so record or release those first). "+
		"Then reply with one line and wait; this session goes on afterwards.", name))
}

func (d *desk) endAndSave(id string) {
	d.endAndSaveTo(id, placeArchive)
}

func (d *desk) endAndSaveTo(id, place string) {
	s := d.session(id)
	name := d.org.Kmem[id]
	if s == nil {
		return
	}
	if name == "" || !d.kmemAvailable() || !s.running() {
		d.endSession(id)
		d.setPlacement(id, place)
		return
	}
	spare := 0
	if s.inTurn {
		spare = 1
	}
	s.kmemSave = &kmemSave{name: name, asked: time.Now().Add(-2 * time.Second), spare: spare, end: true, place: place}
	d.notify(s, fmt.Sprintf("Ending: waiting for this session to save its kmem handoff “%s”; it is stopped and moved to %s after that.", name, d.folderTitle(place, place)))
	d.sendTo(s, kmemEndMessage(name))
}

func (d *desk) finishKmemEnd(s *deskSession, name, place string) {
	if place == "" {
		place = placeArchive
	}
	d.notify(s, fmt.Sprintf("Saved kmem handoff “%s”; this session is stopped and moved to %s (nothing deleted).", name, d.folderTitle(place, place)))
	if s.running() {
		p := s.proc
		s.stopping = true
		go func() { _ = p.Stop(5 * time.Second) }()
	}
	d.org.Place[s.id] = place
	d.markEnded(s.id)
	d.saveOrg()
	d.closeTab(s.id)
	d.refreshSidebar()
}

func hasPendingPlan(s *deskSession) bool {
	for _, it := range s.conv.PendingPermissions() {
		if it.Kind == chat.Plan {
			return true
		}
	}
	return false
}
