package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"fyne.io/fyne/v2/dialog"
)

// Following kmem handoff renames made outside Quill Desktop (bug 2026-11-22):
// after `kmem handoff rename quill quill-desktop` in a terminal, the live tab
// still linked to "quill", so `quill --resume quill` (and the kmem window's
// Resume of a NEW "quill" handoff) opened the old tab. A renamed handoff's
// facts move away, so its old name drops out of `kmem handoff list`; Quill
// asks `kmem handoff resolve` about the linked names that dropped out and
// moves each link to the name kmem gives. This runs at start, every minute,
// on a kmem window reload and before a resume matches a name to a tab.
//
// Limit: once someone saves a new handoff under the old name (`kmem handoff
// save quill --new`), resolve answers "quill" again, so a tab that did not
// follow before that keeps the old link.

// renamedLinks: the tab links (id -> kmem name) that renames (old -> new)
// move, as id -> new name.
func renamedLinks(links, renames map[string]string) map[string]string {
	out := map[string]string{}
	for id, k := range links {
		if n := renames[k]; k != "" && n != "" && n != k {
			out[id] = n
		}
	}
	return out
}

// kmemRenameCandidates: the linked names to ask kmem about, sorted: those not
// in the handoff list (a listed name is current), minus skip.
func kmemRenameCandidates(links map[string]string, listed []kmemHandoff, skip map[string]bool) []string {
	in := map[string]bool{}
	for _, h := range listed {
		in[h.Name] = true
	}
	var out []string
	for _, k := range links {
		if k != "" && !in[k] && !skip[k] && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// kmemResolve: the current name of handoff name, following renames; false
// when kmem has no handoff by that name (or no answer).
func (d *desk) kmemResolve(name string) (string, bool) {
	var r struct {
		Name string `json:"name"`
	}
	if d.kmemAPILevel() == kmemAPI {
		ok := d.kmemJSON(&r, "handoff", "resolve", name)
		return r.Name, ok && plainKmemName(r.Name)
	}
	out, err := d.runKmem("kmem", "handoff", "resolve", name)
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	cur := strings.TrimSpace(lines[len(lines)-1]) // after any kmem NOTICE lines
	return cur, plainKmemName(cur)
}

// plainKmemName: what kmem accepts as a handoff name (letters, digits, . _ -).
func plainKmemName(s string) bool { return kmemHandoffNameRe.MatchString(s) }

var kmemHandoffNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// liveKmemLinks: the kmem links of open tabs, running sessions and the kmem
// window's session (the ones a name can open; ended sessions in Inactive can
// be hundreds, and each resolve runs kmem).
func (d *desk) liveKmemLinks() map[string]string {
	out := map[string]string{}
	for id, k := range d.org.Kmem {
		s := d.sessions[id]
		if k != "" && (slices.Contains(d.tabs, id) || s != nil && (s.running() || s == d.kmemWin.session())) {
			out[id] = k
		}
	}
	return out
}

type kmemRenameScan struct {
	listed  []kmemHandoff
	renames map[string]string // old -> current
	none    []string          // asked, but kmem has no handoff by that name
	listOK  bool
}

// scanKmemRenames runs kmem (off the UI thread): the handoff list, then a
// resolve for each linked name missing from it.
func (d *desk) scanKmemRenames(links map[string]string, skip map[string]bool) kmemRenameScan {
	sc := kmemRenameScan{renames: map[string]string{}}
	hs, err := d.kmemHandoffList()
	if err != nil {
		return sc
	}
	sc.listed, sc.listOK = hs, true
	d.hsMu.Lock()
	d.lastHandoffs = hs
	d.hsMu.Unlock()
	for _, n := range kmemRenameCandidates(links, hs, skip) {
		if cur, ok := d.kmemResolve(n); !ok {
			sc.none = append(sc.none, n)
		} else if cur != n {
			sc.renames[n] = cur
		}
	}
	return sc
}

// applyKmemRenames (UI thread) moves tab links (and a title that is the old
// name, and the project keyed by it) to the renamed handoffs. Returns the
// tab ids it moved.
func (d *desk) applyKmemRenames(sc kmemRenameScan) []string {
	if d.kmemNoHandoff == nil {
		d.kmemNoHandoff = map[string]bool{}
	}
	for _, h := range sc.listed {
		delete(d.kmemNoHandoff, h.Name)
	}
	for _, n := range sc.none {
		d.kmemNoHandoff[n] = true
	}
	moved := renamedLinks(d.org.Kmem, sc.renames)
	if len(moved) == 0 {
		return nil
	}
	never := func(string) bool { return false }
	ids := slices.Sorted(maps.Keys(moved))
	for _, id := range ids {
		old, next := d.org.Kmem[id], moved[id]
		title, _, _ := d.rowText(id)
		d.org.Kmem[id] = next
		if kmemSlug(title, never) == old {
			d.org.Titles[id] = next // tab, session and kmem name are one identity
		}
		d.moveKmemProject(old, next)
		if s := d.sessions[id]; s != nil {
			d.notify(s, fmt.Sprintf("The kmem handoff “%s” was renamed to “%s” outside Quill Desktop; this tab now uses the kmem name “%s”.", old, next, next))
		}
	}
	resetKmemSessions()
	d.saveOrg()
	d.tabSig = ""
	d.syncTabs()
	d.rebuildRows()
	d.updateChrome()
	return ids
}

// moveKmemProject: the project set in Quill is keyed by kmem name.
func (d *desk) moveKmemProject(old, next string) {
	if p, ok := d.org.Projects[old]; ok && d.org.Projects[next] == "" && !d.kmemNameInUse(old) {
		d.org.Projects[next] = p
		delete(d.org.Projects, old)
	}
}

// reconcileKmemNames (UI thread) follows renames made outside Quill Desktop,
// then calls done (if any) on the UI thread with the scan. all=false skips
// names kmem had no handoff for last time (the minute-by-minute check).
func (d *desk) reconcileKmemNames(all bool, done func(kmemRenameScan)) {
	if !d.kmemAvailable() {
		if done != nil {
			done(kmemRenameScan{})
		}
		return
	}
	links := d.liveKmemLinks()
	var skip map[string]bool
	if !all {
		skip = maps.Clone(d.kmemNoHandoff)
	}
	go func() {
		sc := d.scanKmemRenames(links, skip)
		d.do(func() {
			d.applyKmemRenames(sc)
			if done != nil {
				done(sc)
			}
		})
	}()
}

// resumeNamedFromCLI: `quill --resume NAME` (user 2026-11-21): bring the
// window up and open the tab whose current kmem name is NAME, or start one
// from its handoff (following a rename), and say which in the window.
func (d *desk) resumeNamedFromCLI(name string) {
	if name == kmemWinOperator && d.kmemAvailable() {
		d.openKmemWindow()
		return
	}
	d.showWindow()
	if !d.kmemAvailable() {
		d.cliNotice("quill --resume " + name + ": kmem is not available, so Quill Desktop cannot open a kmem session.")
		return
	}
	links := d.liveKmemLinks()
	go func() {
		sc := d.scanKmemRenames(links, nil)
		target, renamed := name, false
		if sc.listOK && !slices.ContainsFunc(sc.listed, func(h kmemHandoff) bool { return h.Name == name }) {
			if cur, ok := d.kmemResolve(name); ok && cur != name {
				target, renamed = cur, true
			}
		}
		d.do(func() {
			d.applyKmemRenames(sc)
			d.resumeResolved(name, target, renamed, sc)
		})
	}()
}

// resumeResolved (UI thread, after the reconcile): open or start target.
func (d *desk) resumeResolved(asked, target string, renamed bool, sc kmemRenameScan) {
	via := ""
	if renamed {
		via = fmt.Sprintf(" (kmem renamed “%s” to “%s”)", asked, target)
	}
	if owner := d.handoffOwner(target, &deskSession{}); owner != nil {
		s := d.resumeHandoffNamed(target)
		if s != nil && s.id != "" {
			title, _, _ := d.rowText(s.id)
			d.notify(s, fmt.Sprintf("quill --resume %s: opened the tab “%s”, which has the kmem name “%s”%s.", asked, title, target, via))
		}
		return
	}
	known := slices.ContainsFunc(sc.listed, func(h kmemHandoff) bool { return h.Name == target })
	if !known {
		d.cliNotice("quill --resume: there is no kmem handoff or live session called “" + asked + "”.")
		return
	}
	if s := d.resumeHandoffNamed(target); s != nil && s.id != "" {
		d.notify(s, fmt.Sprintf("quill --resume %s: started a fresh session from the kmem handoff “%s”%s.", asked, target, via))
	}
}

// cliNotice shows a message from a CLI request in the window: as a notice in
// the current tab, else in a dialog.
func (d *desk) cliNotice(msg string) {
	if s := d.current; s != nil {
		d.notify(s, msg)
		return
	}
	dialog.ShowInformation("Quill Desktop", msg, d.win)
}
