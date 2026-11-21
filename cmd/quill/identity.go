package main

import (
	"fmt"
	"time"

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
	if s == nil || !s.running() || s.kmemSave != nil {
		d.refreshSidebar()
		return
	}
	s.pendingEnd++
	tok := s.pendingEnd
	d.notify(s, fmt.Sprintf("Closed: this session ends in %d s and saves its kmem handoff first. Open it again before then to keep it running.", int(closeUndo/time.Second)))
	d.refreshSidebar()
	d.afterUI(closeUndo, func() {
		if s.pendingEnd != tok {
			return
		}
		s.pendingEnd = 0
		d.endWithSave(id, placeInactive)
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

func (d *desk) endWithSave(id, place string) {
	s := d.sessions[id]
	if s == nil || !s.running() || trivialSession(s) || d.ensureKmem(id) == "" {
		d.endSession(id)
		d.setPlacement(id, place)
		d.markEnded(id)
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
		d.notify(s, fmt.Sprintf("This session didn't save its kmem handoff “%s” within %d minutes, so it was not ended; it keeps running in %s. Close it again once it can save.", ks.name, int(kmemEndTimeout/time.Minute), d.folderTitle(place, "Inactive")))
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

func (d *desk) resumeFromHandoff(id string) {
	old := d.sessions[id]
	name := d.org.Kmem[id]
	if old == nil || name == "" {
		return
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
		d.do(func() { d.sendTo(ns, p) })
	}()
}

func (d *desk) renameSession(id, name string) {
	d.org.Titles[id] = name
	d.saveOrg()
	if old := d.org.Kmem[id]; old != "" && d.kmemAvailable() {
		next := kmemSlug(name, func(n string) bool { return n != old && d.kmemTaken(nil)(n) })
		if next != old {
			d.renameKmem(id, old, next)
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
