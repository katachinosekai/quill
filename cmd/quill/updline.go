package main

import (
	"net/url"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/updater"
)

// The update line (user 2026-11-21): Quill Desktop never updates or restarts
// on its own. A verified release or a new build on disk shows one quiet line
// under the header, like Quill Android's; nothing happens until the user
// picks Update / Restart. No dialog, no popup.

const prefUpdLater = "updateLater" // the release tag the user put off with Later

func (d *desk) buildUpdLine() fyne.CanvasObject {
	d.updText = widget.NewLabel("")
	d.updText.Truncation = fyne.TextTruncateEllipsis
	d.updActs = container.NewHBox()
	d.updLine = container.NewBorder(nil, nil, nil, d.updActs, d.updText)
	d.updLine.Hide()
	return d.updLine
}

func (d *desk) setUpdLine(text string, acts ...fyne.CanvasObject) {
	if d.updLine == nil {
		return
	}
	was := d.updLine.Visible()
	d.updText.SetText(text)
	d.updActs.Objects = acts
	d.updActs.Refresh()
	if text == "" {
		d.updLine.Hide()
	} else {
		d.updLine.Show()
	}
	if was != d.updLine.Visible() && d.headerBox != nil {
		d.headerBox.Refresh()
		if d.mainPane != nil {
			relayout(d.mainPane)
		}
	}
}

func lineBtn(label string, f func()) *widget.Button {
	b := widget.NewButton(label, f)
	b.Importance = widget.LowImportance
	return b
}

// offerRelease shows a verified newer release (UI thread).
func (d *desk) offerRelease(r *updater.Release) {
	d.updRelease = r
	if d.app.Preferences().String(prefUpdLater) == r.Tag || d.updating {
		return
	}
	v := strings.TrimPrefix(r.Tag, "v")
	acts := []fyne.CanvasObject{lineBtn("Update", func() { d.startUpdate(r) })}
	if u, err := url.Parse(r.Page); err == nil && u.Scheme == "https" && u.Host == "github.com" {
		acts = append(acts, lineBtn("What's new", func() { d.app.OpenURL(u) }))
	}
	acts = append(acts, lineBtn("Later", func() {
		d.app.Preferences().SetString(prefUpdLater, r.Tag)
		d.setUpdLine("")
	}))
	d.setUpdLine("Quill Desktop "+v+" is available (you have "+version+").", acts...)
}

// startUpdate: the user picked Update. Progress stays in the line; once the
// signed package is installed, Quill may switch to it (swapOK).
func (d *desk) startUpdate(r *updater.Release) {
	if d.updating {
		return
	}
	d.updating = true
	d.swapOK = true
	kind, appImage := d.updateKind()
	d.setUpdLine("Checking the release signature…")
	go func() {
		err := d.applyUpdate(r, kind, appImage,
			func(f float64) {
				d.do(func() {
					d.setUpdLine("Downloading Quill Desktop " + strings.TrimPrefix(r.Tag, "v") + "… " + strconv.Itoa(int(f*100)) + "%")
				})
			},
			func(s string) { d.do(func() { d.setUpdLine(s) }) })
		d.do(func() {
			d.updating = false
			if err != nil {
				d.swapOK = false
				d.setUpdLine("Not updated: "+oneLine(err.Error(), 120), lineBtn("Try again", func() { d.startUpdate(r) }), lineBtn("Later", func() { d.setUpdLine("") }))
			}
		})
	}()
}

// offerBuild: Quill's program file changed on disk (a package update or a new
// local build). Switching to it waits for the user.
func (d *desk) offerBuild(path string) {
	d.pendingBuild = path
	d.setUpdLine("A new Quill Desktop build is ready on this computer.",
		lineBtn("Restart into it", func() { d.acceptBuild() }),
		lineBtn("Later", func() { d.setUpdLine("") }))
}

func (d *desk) acceptBuild() {
	if d.pendingBuild == "" {
		return
	}
	d.swapOK = true
	d.swapTo = d.pendingBuild
	d.setUpdLine("Restarting into the new build…")
	d.trySwap()
}
