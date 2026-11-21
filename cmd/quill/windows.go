package main

import "encoding/json"

func (d *desk) modelWindows() map[string]int {
	if d.windows == nil {
		d.windows = map[string]int{}
		_ = json.Unmarshal([]byte(d.app.Preferences().String("modelWindows")), &d.windows)
	}
	return d.windows
}

func (d *desk) noteWindow(s *deskSession) {
	if s == nil || s.conv == nil {
		return
	}
	st := &s.conv.Stats
	w := d.modelWindows()
	keys := []string{}
	for _, k := range []string{st.Model, s.model} {
		if k != "" && k != "default" {
			keys = append(keys, k)
		}
	}
	if st.ContextMax > 0 {
		changed := false
		for _, k := range keys {
			if w[k] != st.ContextMax {
				w[k] = st.ContextMax
				changed = true
			}
		}
		if changed {
			b, _ := json.Marshal(w)
			d.app.Preferences().SetString("modelWindows", string(b))
		}
		return
	}
	for _, k := range keys {
		if n := w[k]; n > 0 {
			st.ContextMax = n
			return
		}
	}
}

func (d *desk) staleTab(id string) bool {
	if _, ok := d.infos[id]; ok {
		return false
	}
	s := d.sessions[id]
	if s == nil {
		return true
	}
	if s.running() || s.loading || s.takeover || s.compacting || s.restarting || len(s.queue) > 0 {
		return false
	}
	if s.cwd != "" && s.cwd != "." {
		return false
	}
	return countMessages(s.conv) == 0
}

func (d *desk) pruneStaleTabs() {
	for _, id := range append([]string(nil), d.tabs...) {
		if d.staleTab(id) {
			d.closeTab(id)
		}
	}
}
