package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

// The kmem version under the message box, next to the key hints (user
// 2026-11-22): muted "kmem 1.7.0". Hidden when kmem is missing or Quill
// Desktop sessions use Claude's own memory. kmem is not part of Quill Desktop
// (user 2026-11-24), so there is no bundled version to offer as an update.

type kmemVerState struct {
	installed string // `kmem version --json` ("" if unknown)
}

func (s kmemVerState) label() string {
	if s.installed == "" {
		return ""
	}
	return "kmem " + s.installed
}

// verLine is the small "kmem 1.7.0" text.
type verLine struct {
	slot  *fyne.Container // what the row lays out; holds tap
	box   *fyne.Container
	tap   *tapRow
	ver   *canvas.Text
	state kmemVerState
}

func (d *desk) newVerLine() *verLine {
	l := &verLine{ver: mutedText("", fontMeta)}
	l.box = container.NewHBox(l.ver)
	l.tap = newTapRow(l.box, func() {})
	l.tap.Hide()
	l.slot = container.NewStack(l.tap)
	return l
}

// setKmemVer shows the state (UI thread).
func (d *desk) setKmemVer(st kmemVerState) {
	l := d.verLine
	if l == nil {
		return
	}
	l.state = st
	if st.label() == "" || !d.kmemMemory() {
		l.tap.Hide()
		return
	}
	l.ver.Text = st.label()
	l.ver.Refresh()
	l.tap.Show()
	l.tap.Refresh()
	if d.statsRow != nil {
		d.statsRow.Refresh()
	}
}

// readKmemVer (not on the UI thread).
func (d *desk) readKmemVer() kmemVerState {
	return kmemVerState{installed: d.kmemInstalledVersion()}
}

// refreshKmemVer reads the version in the background and updates the line.
func (d *desk) refreshKmemVer() {
	if !d.kmemAvailable() {
		d.do(func() { d.setKmemVer(kmemVerState{}) })
		return
	}
	go func() {
		st := d.readKmemVer()
		d.do(func() { d.setKmemVer(st) })
	}()
}

// hintsLayout: the key hints right-aligned with the kmem version just left of
// them. When both don't fit, the hints win if they fit alone (a short flashed
// hint), else the version stays.
type hintsLayout struct{}

func (hintsLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(10, h)
}

func (hintsLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	ver, hints := objs[0], objs[1] // ver: a slot holding the tappable version
	pad := theme.Padding()
	gap := 3 * pad
	vm, hm := ver.MinSize(), hints.MinSize()
	withVer := vm.Width > 0
	if c, ok := ver.(*fyne.Container); ok && len(c.Objects) > 0 {
		withVer = withVer && c.Objects[0].Visible()
	}
	hintsFit := hm.Width+2*pad <= size.Width
	bothFit := hm.Width+vm.Width+gap+2*pad <= size.Width
	showHints := hintsFit
	showVer := withVer && (bothFit || !hintsFit) && vm.Width+2*pad <= size.Width
	right := size.Width - pad
	if showHints {
		hints.Show()
		hints.Move(fyne.NewPos(right-hm.Width, (size.Height-hm.Height)/2))
		hints.Resize(hm)
		right -= hm.Width + gap
	} else {
		hints.Hide()
	}
	if !showVer {
		ver.Hide()
		return
	}
	ver.Show()
	ver.Move(fyne.NewPos(right-vm.Width, (size.Height-vm.Height)/2))
	ver.Resize(vm)
}
