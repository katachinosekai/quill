package main

import (
	"context"
	"encoding/json"
	"time"
)

type rcTitle struct {
	Sent string `json:"sent"`
	Base string `json:"base"`
}

func (d *desk) unRCTitle(id, title string) string {
	if rc, ok := d.org.RC[id]; ok && title == rc.Sent {
		return rc.Base
	}
	return title
}

func (d *desk) rcName(id string) (name, base string) {
	if info, ok := d.infos[id]; (!ok || !info.Named) && d.org.Titles[id] == "" {
		return "", ""
	}
	base, _, _ = d.rowText(id)
	base = d.unRCTitle(id, base)
	return base, base
}

func (d *desk) syncRCNames() {
	for _, s := range d.sessions {
		if !s.rcOn || s.proc == nil || !s.running() {
			continue
		}
		name, base := d.rcName(s.id)
		if name == "" || name == s.rcName {
			continue
		}
		s.rcName = name
		if d.org.RC == nil {
			d.org.RC = map[string]rcTitle{}
		}
		d.org.RC[s.id] = rcTitle{Sent: name, Base: base}
		d.saveOrgQuiet()
		p := s.proc
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = p.RenameSession(ctx, name)
		}()
	}
}

func (d *desk) saveOrgQuiet() {
	if b, err := json.Marshal(d.org); err == nil {
		d.app.Preferences().SetString("sidebarOrg", string(b))
	}
}
