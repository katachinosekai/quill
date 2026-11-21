package main

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// The desktop session menu's actions for paired phones (rename, resume, end,
// delete, project) and kmem handoffs (resume, delete). A tab, its Claude session and its kmem name are one
// identity, so rename also renames the kmem name and there is no separate
// kmem action.

var menuActions = []string{"rename", "resume", "end", "delete", "project"}

const apiTitleMax = 200

func (a *phoneAPI) projects() []string {
	out := []string{}
	for _, g := range a.d.groups {
		if strings.HasPrefix(g.key, projectPrefix) {
			out = append(out, g.title)
		}
	}
	return out
}

func (a *phoneAPI) sessionOut(id string) map[string]any {
	_, group := a.sessionIDs()
	return map[string]any{"session": a.session(id, firstNonEmpty(group[id], a.d.projectOf(id)))}
}

func (a *phoneAPI) handoffAction(name, action string) (map[string]any, *apiErr) {
	d := a.d
	if !d.kmemAvailable() {
		return nil, &apiErr{http.StatusConflict, "blocked", "kmem is not available"}
	}
	known := false
	for _, h := range d.kmemHandoffs() {
		known = known || h.Name == name
	}
	if !known {
		return nil, notFound("no handoff " + name)
	}
	owner := d.handoffOwner(name, &deskSession{})
	switch action {
	case "resume":
		ns := d.resumeHandoffNamed(name)
		return a.sessionOut(ns.id), nil
	case "delete":
		if owner != nil {
			return nil, &apiErr{http.StatusConflict, "blocked", "that handoff belongs to a live session; end or delete the session instead"}
		}
		d.trashHandoffNamed(name, "Quill: the user deleted handoff "+name+" from the phone", nil)
		return map[string]any{"removed": name}, nil
	}
	return nil, notFound("unknown path")
}

func (a *phoneAPI) menuAction(s *deskSession, action string, in writeBody) (map[string]any, *apiErr) {
	d := a.d
	id := s.id
	switch action {
	case "rename":
		if in.Title == nil {
			return nil, badReq("title is required (empty restores the original name)")
		}
		title := strings.TrimSpace(*in.Title)
		if utf8.RuneCountInString(title) > apiTitleMax {
			return nil, badReq("title is longer than 200 characters")
		}
		if title == "" {
			d.restoreTitle(id)
		} else {
			d.renameSession(id, title)
		}
		return a.sessionOut(id), nil
	case "project":
		if in.Project == nil {
			return nil, badReq("project is required (empty = automatic)")
		}
		p := strings.TrimSpace(*in.Project)
		if utf8.RuneCountInString(p) > apiTitleMax {
			return nil, badReq("project is longer than 200 characters")
		}
		d.setProject(id, p)
		return a.sessionOut(id), nil
	case "resume":
		if s.running() {
			return a.sessionOut(id), nil
		}
		if s.takeover || s.terminal != nil {
			return nil, &apiErr{http.StatusConflict, "blocked", "the session is open in a terminal or being taken over"}
		}
		if d.org.Ended[id] && d.org.Kmem[id] != "" && d.kmemAvailable() && !in.Old {
			ns := d.resumeFromHandoff(id)
			if ns == nil {
				return nil, &apiErr{http.StatusInternalServerError, "internal", "could not resume from the handoff"}
			}
			return a.sessionOut(ns.id), nil
		}
		if d.org.Ended[id] {
			delete(d.org.Ended, id)
			d.saveOrg()
		}
		d.addTab(id)
		if err := d.ensureProcess(s); err != nil {
			return nil, &apiErr{http.StatusInternalServerError, "internal", "could not start claude: " + err.Error()}
		}
		d.refreshSidebar()
		return a.sessionOut(id), nil
	case "end":
		d.closeTab(id)
		d.endWithSave(id, placeInactive)
		return map[string]any{"saving": s.kmemSave != nil}, nil
	case "delete":
		if s.terminal != nil && !s.running() {
			return nil, &apiErr{http.StatusConflict, "blocked", "the session is open in a terminal; end it there first"}
		}
		path := ""
		if info, ok := d.infos[id]; ok {
			path = info.Path
		}
		if _, err := sessionFiles(d.cfg.ProjectsRoot, id, path); err != nil {
			return nil, &apiErr{http.StatusInternalServerError, "internal", err.Error()}
		}
		d.deleteSession(id)
		return map[string]any{"removed": id}, nil
	}
	return nil, notFound("unknown path")
}
