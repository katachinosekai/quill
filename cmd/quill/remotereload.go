package main

import (
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Reload for Other PCs sessions: drops the shown window of a remote session
// and loads it again from that PC (like Quill Android's ⟳). The button only
// shows while a remote session is shown, and is off while its reload runs.

func (d *desk) newRemoteReloadButton(label string, shown func() *deskSession) *widget.Button {
	b := widget.NewButtonWithIcon(label, theme.ViewRefreshIcon(), nil)
	b.Importance = widget.LowImportance
	b.OnTapped = func() { d.remoteReload(shown()) }
	b.Hide()
	return b
}

// syncRemoteReload shows the button for remote sessions only; reports whether
// its visibility changed (the caller relayouts then).
func syncRemoteReload(b *widget.Button, s *deskSession) bool {
	if b == nil {
		return false
	}
	want := s != nil && s.remote != nil
	changed := b.Visible() != want
	if want {
		b.Show()
		if s.remote.reloading {
			b.Disable()
		} else {
			b.Enable()
		}
	} else {
		b.Hide()
	}
	return changed
}

func (d *desk) remoteReload(s *deskSession) {
	if s == nil || s.remote == nil || s.remote.reloading {
		return
	}
	r := s.remote
	r.link.userActive()
	r.reloading = true
	r.fetched = -1
	r.link.fetchWindow(s, false)
	d.updateChrome()
	d.syncPanes()
}
