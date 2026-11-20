package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const rcSep = " · "

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

func (d *desk) rcFolderLabel(id string) string {
	key, ok := d.rowPlace[id]
	if !ok {
		return ""
	}
	switch key {
	case placeActive:
		return d.folderTitle(placeActive, "Active")
	case placeInactive:
		return d.folderTitle(placeInactive, "Inactive")
	case placeArchive:
		return d.folderTitle(placeArchive, "Archive")
	case placeAuto:
		return d.folderTitle(placeAuto, "Created by Claude")
	}
	if strings.HasPrefix(key, folderPrefix) {
		return d.folderTitle(key, "")
	}
	return ""
}

func (d *desk) rcName(id string) (name, base string) {
	label := d.rcFolderLabel(id)
	if label == "" {
		return "", ""
	}
	if info, ok := d.infos[id]; (!ok || !info.Named) && d.org.Titles[id] == "" {
		return "", ""
	}
	base, _, _ = d.rowText(id)
	base = d.unRCTitle(id, base)
	return label + rcSep + base, base
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
