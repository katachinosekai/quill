//go:build go1.24

package container_test

import (
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// sizedTheme is the default theme with its own text size and foreground.
type sizedTheme struct {
	fyne.Theme
	text float32
	fg   color.NRGBA
}

func newSizedTheme(text float32, fg color.NRGBA) sizedTheme {
	return sizedTheme{Theme: theme.DefaultTheme(), text: text, fg: fg}
}

func (t sizedTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameText {
		return t.text
	}
	return t.Theme.Size(n)
}

func (t sizedTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameForeground {
		return t.fg
	}
	return t.Theme.Color(n, v)
}

// probeCached reports whether a text-size cache entry stored before is still there.
func probeCached() bool {
	_, base := cache.GetFontMetrics("probe", 13, fyne.TextStyle{}, nil)
	return base != 0
}

// Applying an override again with the same theme keeps the SVG and text-size
// caches (Quill patch: every apply cleared them, i.e. every chat redraw in
// Quill); replacing the override's theme still clears them.
func TestThemeOverrideKeepsCaches(t *testing.T) {
	test.NewTempApp(t)
	red := color.NRGBA{R: 0xff, A: 0xff}
	cache.SetFontMetrics("probe", 13, fyne.TextStyle{}, nil, fyne.NewSize(30, 15), 12)

	o := container.NewThemeOverride(container.NewVBox(widget.NewLabel("text"), widget.NewIcon(theme.HomeIcon())), newSizedTheme(13, red))
	w := test.NewWindow(o)
	defer w.Close()
	o.Refresh()
	o.Theme = newSizedTheme(13, red) // an equal value is the same theme
	o.Refresh()
	_ = container.NewThemeOverride(widget.NewLabel("another"), newSizedTheme(13, red))
	if !probeCached() {
		t.Fatal("applying an override with an unchanged theme cleared the text-size cache")
	}

	o.Theme = newSizedTheme(20, red)
	o.Refresh()
	if probeCached() {
		t.Fatal("replacing the override's theme kept the text-size cache")
	}
}

// countNear counts the pixels of img close to c.
func countNear(img image.Image, c color.NRGBA) (n int) {
	near := func(a uint32, b uint8) bool {
		d := int(a>>8) - int(b)
		return d > -40 && d < 40
	}
	r := img.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if near(cr, c.R) && near(cg, c.G) && near(cb, c.B) {
				n++
			}
		}
	}
	return n
}

// A real theme change still reaches the content: text sizes and the colours
// of SVG icons follow the override's new theme, and the app theme.
// (widget.Icon is left out: upstream it only redraws when its resource
// changes, with or without this patch.)
func TestThemeOverrideChangeUpdatesSizesAndIcons(t *testing.T) {
	a := test.NewTempApp(t)
	red := color.NRGBA{R: 0xff, A: 0xff}
	blue := color.NRGBA{B: 0xff, A: 0xff}
	label := widget.NewLabel("Some text")
	icon := canvas.NewImageFromResource(theme.HomeIcon())
	button := widget.NewButtonWithIcon("", theme.SearchIcon(), nil)
	o := container.NewThemeOverride(container.NewVBox(label, button, container.NewGridWrap(fyne.NewSize(64, 64), icon)), newSizedTheme(10, red))
	w := test.NewWindow(o)
	defer w.Close()
	w.Resize(fyne.NewSize(300, 200))

	small := label.MinSize()
	if img := w.Canvas().Capture(); countNear(img, red) == 0 {
		t.Fatal("no red text or icon under the first theme")
	}

	o.Theme = newSizedTheme(20, blue)
	o.Refresh()
	if big := label.MinSize(); big.Height <= small.Height || big.Width <= small.Width {
		t.Errorf("text size did not follow the theme: %v -> %v", small, big)
	}
	img := w.Canvas().Capture()
	if countNear(img, blue) == 0 || countNear(img, red) != 0 {
		t.Errorf("icon/text colours did not follow the theme: blue %d, red %d", countNear(img, blue), countNear(img, red))
	}

	// Redraws with the unchanged theme keep it.
	for range 3 {
		o.Refresh()
	}
	if img := w.Canvas().Capture(); countNear(img, blue) == 0 || countNear(img, red) != 0 {
		t.Error("colours changed on a redraw with the same theme")
	}

	// The app theme still reaches objects outside the override.
	plain := canvas.NewImageFromResource(theme.HomeIcon())
	w.SetContent(container.NewGridWrap(fyne.NewSize(64, 64), plain))
	a.Settings().SetTheme(newSizedTheme(14, red))
	if img := w.Canvas().Capture(); countNear(img, red) == 0 {
		t.Error("an app theme change did not recolour the icon")
	}
	a.Settings().SetTheme(newSizedTheme(14, blue))
	if img := w.Canvas().Capture(); countNear(img, blue) == 0 || countNear(img, red) != 0 {
		t.Error("a second app theme change did not recolour the icon")
	}
}
