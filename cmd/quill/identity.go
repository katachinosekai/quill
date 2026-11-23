package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

const (
	closeUndo      = 10 * time.Second
	kmemEndTimeout = 5 * time.Minute
	trivialMsgs    = 4
)

func (d *desk) afterUI(t time.Duration, f func()) {
	after := d.after
	if after == nil {
		after = func(t time.Duration, f func()) { time.AfterFunc(t, f) }
	}
	after(t, func() { d.do(f) })
}

func (d *desk) ensureKmem(id string) string {
	if k := d.org.Kmem[id]; k != "" {
		return k
	}
	if !d.kmemAvailable() {
		return ""
	}
	title, _, _ := d.rowText(id)
	d.hsMu.Lock()
	known := d.lastHandoffs
	d.hsMu.Unlock()
	name := kmemSlug(title, d.kmemTaken(known))
	d.org.Kmem[id] = name
	d.rememberKmemTitle(name, title)
	d.saveOrg()
	return name
}

func trivialSession(s *deskSession) bool {
	if s == nil || s.conv == nil {
		return true
	}
	if countMessages(s.conv) > trivialMsgs {
		return false
	}
	for _, it := range s.conv.Items {
		if it.ToolName != "" {
			return false
		}
	}
	return true
}

func (d *desk) waitingOnUser(s *deskSession) bool {
	return s != nil && s.running() && (hasPendingPlan(s) || s.conv.AsksApproval())
}

func (d *desk) closeSessionTabs(ids []string) {
	for _, id := range ids {
		d.closeSessionTab(id)
	}
}

func (d *desk) closeSessionTab(id string) {
	s := d.sessions[id]
	if s != nil && s.remote != nil {
		d.closeTab(id)
		return
	}
	if d.waitingOnUser(s) {
		title, _, _ := d.rowText(id)
		dialog.ShowConfirm("Close and end this session?",
			fmt.Sprintf("“%s” is waiting for your answer. Closing ends it: it saves its kmem handoff first (answer its prompt if it asks), then stops and moves to Inactive.", title),
			func(ok bool) {
				if ok {
					d.closeAndEnd(id)
				}
			}, d.win)
		return
	}
	d.closeAndEnd(id)
}

func (d *desk) closeAndEnd(id string) {
	s := d.sessions[id]
	d.closeTab(id)
	if d.placement(id) != placeArchive {
		d.setPlacement(id, placeInactive)
	}
	if s == nil || !s.running() || s.kmemSave != nil || s.endWait != nil {
		d.refreshSidebar()
		return
	}
	s.pendingEnd++
	tok := s.pendingEnd
	first := "saves its kmem handoff first"
	if d.endCheckFor(s) {
		first = "runs its end-of-session check first"
	}
	d.notify(s, fmt.Sprintf("Closed: this session ends in %d s and %s. Open it again before then to keep it running.", int(closeUndo/time.Second), first))
	d.refreshSidebar()
	d.afterUI(closeUndo, func() {
		if s.pendingEnd != tok {
			return
		}
		s.pendingEnd = 0
		d.endWithSave(id, d.endPlace())
	})
}

func (d *desk) cancelPendingEnd(s *deskSession) {
	if s == nil || s.pendingEnd == 0 {
		return
	}
	s.pendingEnd = 0
	d.setPlacement(s.id, placeActive)
	d.notify(s, "Kept running: the end was cancelled because you opened this session again.")
	d.refreshSidebar()
}

// endWithSave ends a session: a topic session through the end-of-session
// check (endcheck.go) when kmem has it, else through a handoff save. Trivial
// sessions end at once.
func (d *desk) endWithSave(id, place string) {
	s := d.sessions[id]
	if s != nil && s.running() && !trivialSession(s) && d.endCheckFor(s) {
		d.endTopic(id, place)
		return
	}
	d.endWithHandoff(id, place)
}

func (d *desk) endWithHandoff(id, place string) {
	s := d.sessions[id]
	if s == nil || !s.running() || trivialSession(s) || d.ensureKmem(id) == "" {
		d.endSession(id)
		d.carryDirsToTopic(id)
		d.setPlacement(id, place)
		d.refreshSidebar()
		return
	}
	d.endAndSaveTo(id, place)
	ks := s.kmemSave
	if ks == nil {
		return
	}
	d.afterUI(kmemEndTimeout, func() {
		if s.kmemSave != ks {
			return
		}
		s.kmemSave = nil
		d.notify(s, fmt.Sprintf("This session didn't save its kmem handoff “%s” within %d minutes, so it was not ended; it keeps running. Close it again once it can save.", ks.name, int(kmemEndTimeout/time.Minute)))
		d.refreshSidebar()
	})
}

func (d *desk) placeSession(id, place string) {
	s := d.sessions[id]
	if (place == placeInactive || place == placeArchive) && s != nil && s.running() {
		d.closeTab(id)
		d.endWithSave(id, place)
		return
	}
	d.setPlacement(id, place)
}

func (d *desk) markEnded(id string) {
	if d.org.Kmem[id] == "" {
		return
	}
	if d.org.Ended == nil {
		d.org.Ended = map[string]bool{}
	}
	d.org.Ended[id] = true
	d.carryDirsToTopic(id)
	d.saveOrg()
}

func (d *desk) offerResume(id string) bool {
	name := d.org.Kmem[id]
	s := d.sessions[id]
	if !d.org.Ended[id] || name == "" || !d.kmemAvailable() || s == nil || s.running() {
		return false
	}
	title, _, _ := d.rowText(id)
	dlg := dialog.NewConfirm("Resume “"+title+"”?",
		fmt.Sprintf("This session was ended with its kmem handoff “%s” saved. Resume from the handoff starts a fresh session with the same name, folder, model and mode (the old conversation stays in Inactive). Open old conversation continues the old one instead.", name),
		func(fresh bool) {
			if fresh {
				d.resumeFromHandoff(id)
				return
			}
			delete(d.org.Ended, id)
			d.saveOrg()
			d.open(id)
		}, d.win)
	dlg.SetConfirmText("Resume from handoff")
	dlg.SetDismissText("Open old conversation")
	dlg.Show()
	return true
}

func (d *desk) resumeFromHandoff(id string) *deskSession {
	old := d.sessions[id]
	name := d.org.Kmem[id]
	if old == nil || name == "" {
		return nil
	}
	title, _, _ := d.rowText(id)
	ns := d.createSession(old.cwd, old.model, old.mode)
	ns.chrome, ns.addDirs = old.chrome, append([]string(nil), old.addDirs...)
	d.org.Titles[ns.id] = title
	delete(d.org.Kmem, id)
	delete(d.org.Ended, id)
	d.org.Kmem[ns.id] = name
	d.saveOrg()
	d.open(ns.id)
	d.notify(ns, fmt.Sprintf("Resuming from kmem handoff “%s”; the previous conversation stays in Inactive.", name))
	go func() {
		p := d.resumePrompt(name)
		d.do(func() { d.sendResume(ns, name, p) })
	}()
	return ns
}

func (d *desk) renameSession(id, name string) {
	if s := d.sessions[id]; s != nil && s.remote != nil || strings.HasPrefix(id, remotePrefix) {
		return // another PC's session: memory only, renamed there
	}
	d.org.Titles[id] = name
	d.retitled(id)
}

// kmemNamePreview: the line the rename dialog shows under the typed title.
func (d *desk) kmemNamePreview(id, typed string) string {
	typed = strings.TrimSpace(typed)
	if typed == "" || !d.kmemAvailable() {
		return ""
	}
	old := d.org.Kmem[id]
	next := kmemNameFor(typed, old, func(n string) bool { return n != old && d.kmemTaken(nil)(n) })
	switch {
	case old == "":
		return "kmem name: " + next
	case next == old && slugless(typed):
		return "kmem name stays " + old + " (kmem names use a-z, 0-9 and -); the title is shown as typed."
	case next == old:
		return "kmem name stays " + old
	}
	return "kmem name: " + old + " becomes " + next
}

func (d *desk) askSessionName(id, current string) {
	d.askNameFor("Rename session", current, func(t string) string { return d.kmemNamePreview(id, t) },
		func(name string) { d.renameSession(id, name) })
}

// restoreTitle drops the custom title. The kmem name follows the restored
// title, because a tab, its session and its kmem name are one identity.
func (d *desk) restoreTitle(id string) {
	if _, ok := d.org.Titles[id]; !ok {
		return
	}
	delete(d.org.Titles, id)
	d.retitled(id)
}

func (d *desk) retitled(id string) {
	d.saveOrg()
	name, _, _ := d.rowText(id)
	if old := d.org.Kmem[id]; old != "" && d.kmemAvailable() {
		next := kmemNameFor(name, old, func(n string) bool { return n != old && d.kmemTaken(nil)(n) })
		if next != old {
			d.renameKmem(id, old, next)
		} else {
			d.rememberKmemTitle(old, name)
		}
	} else {
		d.ensureKmem(id)
	}
	if d.current != nil && d.current.id == id {
		d.updateChrome()
	}
	d.refreshSidebar()
}

func (d *desk) trashHandoff(id string) {
	name := d.org.Kmem[id]
	if name == "" || !d.kmemAvailable() {
		return
	}
	for other, k := range d.org.Kmem {
		if other != id && k == name {
			return
		}
	}
	go func() {
		var r struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if !d.kmemJSON(&r, "handoff", "get", name) {
			return
		}
		for _, it := range r.Items {
			_, _ = d.runKmem("kmem", "trash", it.ID, "Quill: session deleted, its handoff "+name+" goes to the recycle bin")
		}
	}()
}

func (d *desk) resumableIn(key string) []string {
	var cands []string
	for _, g := range d.groups {
		if g.key == key {
			cands = append(cands, g.ids...)
		}
	}
	var placed []string
	for id, p := range d.org.Place {
		if p == key {
			placed = append(placed, id)
		}
	}
	slices.Sort(placed)
	cands = append(cands, placed...)
	saved := map[string]bool{}
	for _, h := range d.kmemHandoffs() {
		saved[h.Name] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range cands {
		if seen[id] || !d.org.Ended[id] || !saved[d.org.Kmem[id]] {
			continue
		}
		seen[id] = true
		if s := d.sessions[id]; s != nil && s.running() {
			continue
		}
		if d.handoffOwner(d.org.Kmem[id], &deskSession{id: id}) != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

func (d *desk) resumeAllMenuItem(key string) *fyne.MenuItem {
	if !d.kmemAvailable() {
		return nil
	}
	ids := d.resumableIn(key)
	if len(ids) == 0 {
		return nil
	}
	return fyne.NewMenuItem(fmt.Sprintf("Resume all from handoffs (%d)", len(ids)), func() {
		var names []string
		for _, id := range ids {
			title, _, _ := d.rowText(id)
			names = append(names, "• "+title+"  (kmem "+d.org.Kmem[id]+")")
		}
		dialog.ShowConfirm("Resume all from handoffs?",
			"Each of these starts a fresh session that resumes from its kmem handoff (same name, folder, model and mode); the old conversations stay here:\n\n"+strings.Join(names, "\n"),
			func(ok bool) {
				if ok {
					d.resumeAll(ids)
				}
			}, d.win)
	})
}

func (d *desk) resumeAll(ids []string) {
	for _, id := range ids {
		if d.org.Ended[id] && d.org.Kmem[id] != "" {
			d.resumeFromHandoff(id)
		}
	}
}
