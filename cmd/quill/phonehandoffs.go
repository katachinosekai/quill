package main

import (
	"net/http"
	"strings"

	"github.com/katachinosekai/quill/internal/folders"
)

type apiStart struct {
	Kmem  string `json:"kmem"`
	Name  string `json:"name"`
	Title string `json:"title"`
}

type apiOpenIn struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type apiHandoff struct {
	Name    string     `json:"name"`
	SavedMs int64      `json:"savedMs"`
	OpenIn  *apiOpenIn `json:"openIn"`
}

func (a *phoneAPI) handoffs(w http.ResponseWriter) {
	var hs []kmemHandoff
	avail := false
	a.ui(func() { avail = a.d.kmemAvailable() })
	if avail {
		hs = a.d.kmemHandoffs()
	}
	out := []apiHandoff{}
	a.ui(func() {
		for _, h := range hs {
			e := apiHandoff{Name: h.Name, SavedMs: h.Saved.UnixMilli()}
			if owner := a.d.handoffOwner(h.Name, &deskSession{}); owner != nil {
				title, _, _ := a.d.rowText(owner.id)
				e.OpenIn = &apiOpenIn{ID: owner.id, Title: title}
			}
			out = append(out, e)
		}
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "handoffs": out})
}

func (a *phoneAPI) handoffSlug(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	slug := ""
	a.ui(func() {
		if title == "" {
			return
		}
		a.d.hsMu.Lock()
		known := a.d.lastHandoffs
		a.d.hsMu.Unlock()
		slug = kmemSlug(title, a.d.kmemTaken(known))
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "slug": slug})
}

func (a *phoneAPI) checkStart(st *apiStart) *apiErr {
	switch st.Kmem {
	case "resume":
		if strings.TrimSpace(st.Name) == "" {
			return &apiErr{http.StatusUnprocessableEntity, "bad_request", "start.name is required to resume"}
		}
		if owner := a.d.handoffOwner(st.Name, &deskSession{}); owner != nil {
			return &apiErr{http.StatusConflict, "blocked", "that handoff is open in another session"}
		}
	case "new":
	default:
		return &apiErr{http.StatusUnprocessableEntity, "bad_request", "start.kmem is resume or new"}
	}
	return nil
}

func (a *phoneAPI) applyStart(s *deskSession, st *apiStart) {
	s.pickPending = true
	if st.Kmem == "resume" {
		a.d.pickResume(s, strings.TrimSpace(st.Name))
		return
	}
	a.d.pickNew(s, st.Title)
}
