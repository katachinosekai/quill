package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

type fakeMax struct {
	fyne.Window
	max bool
}

func (f *fakeMax) Maximized() bool          { return f.max }
func (f *fakeMax) Maximize()                { f.max = true }
func (f *fakeMax) Position() (int, int)     { return 0, 0 }
func (f *fakeMax) RequestPosition(int, int) {}

func TestGeomSavedAndLoaded(t *testing.T) {
	p := test.NewApp().Preferences()
	saveGeom(p, geomMain, winGeom{W: 900, H: 700, Max: true})
	g := loadGeom(p, geomMain)
	if g.W != 900 || g.H != 700 || !g.Max || g.Full {
		t.Fatalf("round trip: %+v", g)
	}
	// A tiny or empty size is never stored over a good one.
	saveGeom(p, geomMain, winGeom{W: 5, H: 5})
	if g := loadGeom(p, geomMain); g.W != 900 || g.Max {
		t.Fatalf("bad size kept the old one, max cleared: %+v", g)
	}
}

func TestGeomKeysHaveNoVersion(t *testing.T) {
	old := version
	defer func() { version = old }()
	version = "1.2.3"
	for _, n := range []string{geomMain, geomKmem} {
		w, h, m, f := geomKeys(n)
		for _, k := range []string{w, h, m, f} {
			if strings.Contains(k, "1.2.3") || strings.Contains(k, version) {
				t.Fatalf("key %q has the version", k)
			}
		}
	}
	version = "9.9.9"
	w, _, _, _ := geomKeys(geomMain)
	if w != "win.main.w" {
		t.Fatalf("key changed with the version: %q", w)
	}
}

func TestGeomOrPrefersHandoverThenSavedThenDefault(t *testing.T) {
	def := winGeom{W: 1280, H: 860}
	saved := winGeom{W: 1000, H: 600, Max: true}
	if g := geomOr(&winGeom{W: 800, H: 500}, saved, def); g.W != 800 {
		t.Fatalf("handover wins: %+v", g)
	}
	if g := geomOr(&winGeom{}, saved, def); g.W != 1000 || !g.Max {
		t.Fatalf("empty handover falls to saved: %+v", g)
	}
	if g := geomOr(nil, winGeom{Max: true}, def); g.W != 1280 || g.H != 860 || !g.Max {
		t.Fatalf("default size, saved state kept: %+v", g)
	}
}

func TestApplyGeomResizesAndMaximises(t *testing.T) {
	test.NewApp()
	w := &fakeMax{Window: test.NewWindow(nil)}
	applyGeom(w, winGeom{W: 900, H: 640, Max: true})
	if sz := w.Canvas().Size(); sz.Width != 900 || sz.Height != 640 {
		t.Fatalf("size %v", sz)
	}
	if !w.max {
		t.Fatal("maximised state applied")
	}
}

func TestCurrentGeomKeepsNormalSizeWhileMaximised(t *testing.T) {
	test.NewApp()
	w := &fakeMax{Window: test.NewWindow(nil)}
	w.Resize(fyne.NewSize(1900, 1000))
	w.max = true
	g := currentGeom(w, winGeom{W: 900, H: 640})
	if !g.Max || g.W != 900 || g.H != 640 {
		t.Fatalf("maximised keeps the normal size: %+v", g)
	}
	w.max = false
	if g := currentGeom(w, g); g.Max || g.W != 1900 {
		t.Fatalf("normal again takes the size: %+v", g)
	}
}

func TestTrackGeomSaveNow(t *testing.T) {
	a := test.NewApp()
	w := test.NewWindow(nil)
	p := a.Preferences()
	save := trackGeom(w, p, "t", winGeom{}, func() bool { return true })
	w.Resize(fyne.NewSize(777, 555))
	save()
	if g := loadGeom(p, "t"); g.W != 777 || g.H != 555 {
		t.Fatalf("saved on demand: %+v", g)
	}
}

func TestAppIDs(t *testing.T) {
	if appID != "io.github.katachinosekai.quill" || appIDKmem != "io.github.katachinosekai.quill.kmem" {
		t.Fatalf("app ids changed: %q %q", appID, appIDKmem)
	}
	if kmemWinTitle != "kmem — Quill Desktop" {
		t.Fatalf("kmem title changed: %q", kmemWinTitle)
	}
}
