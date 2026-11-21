package main

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// The desktop session menu's actions for paired phones (rename, resume, end,
// delete, place). A tab, its Claude session and its kmem name are one
// identity, so rename also renames the kmem name and there is no separate
// kmem action.

var menuActions = []string{"rename", "resume", "end", "delete", "place"}

const apiTitleMax = 200

type apiPlace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (a *phoneAPI) places() []apiPlace {
	out := []apiPlace{}
	for _, f := range a.d.org.Folders {
		out = append(out, apiPlace{ID: f.ID, Name: f.Name})
	}
	return out
}

// apiPlaceOf turns a stored placement into the API form ("folder:<id>").
func apiPlaceOf(p string) string {
	if id, ok := strings.CutPrefix(p, folderPrefix); ok {
		return "folder:" + id
	}
	return p
}

func (a *phoneAPI) placeFromAPI(p string) (string, *apiErr) {
	switch p {
	case "", placeActive, placeInactive, placeArchive, placeAuto:
		return p, nil
	}
	if id, ok := strings.CutPrefix(p, "folder:"); ok {
		if _, found := a.d.folderByID(id); found {
			return folderPrefix + id, nil
		}
		return "", notFound("no folder " + id)
	}
	return "", badReq("place is active, inactive, archive, auto, folder:<id> or empty")
}

func (a *phoneAPI) sessionOut(id string) map[string]any {
	_, group := a.sessionIDs()
	return map[string]any{"session": a.session(id, firstNonEmpty(group[id], "Open"))}
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
	case "place":
		if in.Place == nil {
			return nil, badReq("place is required (empty = Automatic placement)")
		}
		p, fail := a.placeFromAPI(*in.Place)
		if fail != nil {
			return nil, fail
		}
		d.placeSession(id, p)
		d.refreshSidebar()
		return map[string]any{"place": *in.Place, "saving": s.kmemSave != nil}, nil
	}
	return nil, notFound("unknown path")
}
