package main

import "fmt"

// rowCaption is the muted caption right of a session's title in the
// portrait sidebar: "12.3k tok · ctx 42%". Context only when known
// (loaded sessions), nothing without usage, nothing in landscape.
func (d *desk) rowCaption(id string) string {
	if d.layoutMode() != layoutVertical {
		return ""
	}
	total := d.infos[id].Usage.Total()
	if total <= 0 {
		return ""
	}
	c := tok(total) + " tok"
	if s := d.sessions[id]; s != nil && s.conv != nil {
		if st := s.conv.Stats; st.ContextUsed > 0 && st.ContextMax > 0 {
			c += fmt.Sprintf(" · ctx %d%%", st.ContextUsed*100/st.ContextMax)
		}
	}
	return c
}

// syncRowCaptions refreshes only the sidebar rows whose caption changed
// since they were last drawn.
func (d *desk) syncRowCaptions() {
	if d.list == nil {
		return
	}
	for i, r := range d.rows {
		if r.header != "" || r.id == "" {
			continue
		}
		if d.rowCaption(r.id) != d.rowCaps[r.id] {
			d.list.RefreshItem(i)
		}
	}
}
