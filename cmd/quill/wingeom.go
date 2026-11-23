package main

import (
	"time"

	"fyne.io/fyne/v2"
)

// Window geometry that survives restarts, new builds and crashes. Sizes are
// stored in Fyne's logical units (the canvas size), so a changed display
// scale does not change them, under keys with no version or build id in them.
// Wayland-native windows cannot set their position, so only size and the
// maximised/fullscreen state are kept; the position is left to the compositor.

const (
	geomMain = "main"
	geomKmem = "kmem"

	appIDKmem = appID + ".kmem" // Wayland app_id / X11 class of the kmem window

	geomMinSide  = 200
	geomPoll     = time.Second
	geomMaxRetry = 400 * time.Millisecond
)

type winGeom struct {
	W, H      float32
	Max, Full bool
}

func (g winGeom) valid() bool { return g.W > geomMinSide && g.H > geomMinSide }

func geomKeys(name string) (w, h, max, full string) {
	p := "win." + name + "."
	return p + "w", p + "h", p + "max", p + "full"
}

// loadGeom reads the saved geometry of the window called name.
func loadGeom(p fyne.Preferences, name string) winGeom {
	kw, kh, km, kf := geomKeys(name)
	g := winGeom{
		W:    float32(p.Float(kw)),
		H:    float32(p.Float(kh)),
		Max:  p.Bool(km),
		Full: p.Bool(kf),
	}
	return g
}

func saveGeom(p fyne.Preferences, name string, g winGeom) {
	kw, kh, km, kf := geomKeys(name)
	if g.valid() {
		p.SetFloat(kw, float64(g.W))
		p.SetFloat(kh, float64(g.H))
	}
	p.SetBool(km, g.Max)
	p.SetBool(kf, g.Full)
}

// currentGeom reads the window now. While it is maximised or fullscreen the
// canvas holds the screen size, so the last normal size in prev is kept.
func currentGeom(w fyne.Window, prev winGeom) winGeom {
	g := prev
	g.Full = w.FullScreen()
	g.Max = false
	if pw, ok := w.(placedWindow); ok {
		g.Max = pw.Maximized()
	}
	if sz := w.Canvas().Size(); !g.Max && !g.Full && sz.Width > geomMinSide && sz.Height > geomMinSide {
		g.W, g.H = sz.Width, sz.Height
	}
	return g
}

// applyGeom sets the saved geometry before the window is shown. The size
// goes first (it is the size the window returns to when un-maximised); the
// maximise is asked again shortly after the show if the compositor did not
// take it while the window was still hidden.
func applyGeom(w fyne.Window, g winGeom) {
	if g.valid() {
		w.Resize(fyne.NewSize(g.W, g.H))
	}
	if g.Full {
		w.SetFullScreen(true)
		return
	}
	if !g.Max {
		return
	}
	pw, ok := w.(placedWindow)
	if !ok {
		return
	}
	pw.Maximize()
	time.AfterFunc(geomMaxRetry, func() {
		fyne.Do(func() {
			if !pw.Maximized() {
				pw.Maximize()
			}
		})
	})
}

// geomOr: the geometry to open with: from first when it has a size, else
// from the saved preferences, else def.
func geomOr(first *winGeom, saved, def winGeom) winGeom {
	switch {
	case first != nil && first.valid():
		return *first
	case saved.valid():
		return saved
	}
	saved.W, saved.H = def.W, def.H
	return saved
}

// trackGeom saves the window's geometry whenever it changed and stayed the
// same for one poll (so a drag is saved once, when it stops). It runs until
// done() reports true. It returns the function that saves right now, for
// close and quit.
func trackGeom(w fyne.Window, p fyne.Preferences, name string, start winGeom, done func() bool) (saveNow func()) {
	last := start
	saveNow = func() {
		g := currentGeom(w, last)
		if g != last {
			saveGeom(p, name, g)
			last = g
		}
	}
	go func() {
		var seen winGeom
		for {
			time.Sleep(geomPoll)
			stop := false
			fyne.DoAndWait(func() {
				if done() {
					stop = true
					return
				}
				g := currentGeom(w, last)
				if g == seen && g != last {
					saveGeom(p, name, g)
					last = g
				}
				seen = g
			})
			if stop {
				return
			}
		}
	}()
	return saveNow
}

// setAppID gives the window its own Wayland app_id / X11 class, which must
// happen before it is shown. Windows that cannot do it keep the app's ID.
func setAppID(w fyne.Window, id string) {
	if s, ok := w.(interface{ SetAppID(string) }); ok {
		s.SetAppID(id)
	}
}

// rememberGeom saves the main window's geometry now (quit, hide, handover).
func (d *desk) rememberGeom() {
	if d.saveGeom != nil {
		d.saveGeom()
	}
}
