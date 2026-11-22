package main

import (
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

// The kmem version under the message box, next to the key hints (user
// 2026-11-22): muted "kmem 1.7.0"; when the kmem that comes with Quill
// Desktop is newer than the installed one, "kmem 1.7.0 · update" with
// "update" in the accent colour, and tapping it (or the first time it is
// seen) shows what to do in the hint line. Hidden when kmem is missing or
// Quill Desktop sessions use Claude's own memory. A staged kmem launcher
// does not count: only the bundled kmem is offered.

type kmemVerState struct {
	installed string // `kmem version --json` ("" if unknown)
	bundled   string // KMEM_VERSION of the kmem that comes with Quill Desktop
	plainFile bool   // ~/.local/bin/kmem is a regular file Quill Desktop may replace
}

func (s kmemVerState) update() bool {
	return s.installed != "" && s.bundled != "" && versionLess(s.installed, s.bundled)
}

func (s kmemVerState) label() string {
	if s.installed == "" {
		return ""
	}
	return "kmem " + s.installed
}

func (s kmemVerState) hint() string {
	if !s.update() {
		return ""
	}
	if s.plainFile {
		return "kmem " + s.bundled + " is ready: restart Quill Desktop to update"
	}
	return "kmem " + s.bundled + " is out: update your kmem yourself"
}

// verLine is the small "kmem 1.7.0 · update" text.
type verLine struct {
	slot   *fyne.Container // what the row lays out; holds tap
	box    *fyne.Container
	tap    *tapRow
	ver    *canvas.Text
	upd    *canvas.Text
	state  kmemVerState
	hinted bool
}

func (d *desk) newVerLine() *verLine {
	l := &verLine{ver: mutedText("", fontMeta), upd: canvas.NewText(" · update", colAccent)}
	l.upd.TextSize = fontMeta
	l.upd.Hide()
	l.box = container.NewHBox(l.ver, l.upd)
	l.tap = newTapRow(l.box, func() {
		if h := l.state.hint(); h != "" {
			d.flashHint(d.folder, h)
		}
	})
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
	if st.update() {
		l.upd.Show()
		if !l.hinted {
			l.hinted = true
			d.flashHint(d.folder, st.hint())
		}
	} else {
		l.upd.Hide()
	}
	l.tap.Show()
	l.tap.Refresh()
	if d.statsRow != nil {
		d.statsRow.Refresh()
	}
}

// readKmemVer (not on the UI thread).
func (d *desk) readKmemVer() kmemVerState {
	st := kmemVerState{installed: d.kmemInstalledVersion()}
	if b := bundledKmemDir(); b != "" {
		st.bundled = kmemScriptVersion(filepath.Join(b, "kmem"))
	}
	if fi, err := os.Lstat(userKmemPath()); err == nil && fi.Mode().IsRegular() {
		st.plainFile = true
	}
	return st
}

// refreshKmemVer reads the versions in the background and updates the line.
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
// hint such as the update one), else the version stays.
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
