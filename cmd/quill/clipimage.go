package main

// Copy image: right-click an image in the chat (preview cards for inline
// images, screenshots, SendUserFile files) or in the image viewer to put the
// picture itself on the clipboard as image/png. Fyne's clipboard is text
// only, so the PNG goes through wl-copy (Wayland) or xclip (X11); "Copy
// path" / "Copy link" stays a separate item.

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

const svgMaxSide = 4096

// toPNG converts image bytes (PNG, JPEG, GIF, BMP, WebP, SVG) to PNG.
// PNG input is returned as is.
func toPNG(data []byte, name string) ([]byte, error) {
	if bytes.HasPrefix(data, pngMagic) {
		return data, nil
	}
	var img image.Image
	if isSVG(data, name) {
		var err error
		if img, err = rasterSVG(data); err != nil {
			return nil, err
		}
	} else {
		var err error
		if img, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("unsupported image: %w", err)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func isSVG(data []byte, name string) bool {
	if strings.EqualFold(filepath.Ext(name), ".svg") {
		return true
	}
	head := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 256)])))
	return strings.HasPrefix(head, "<svg") || (strings.HasPrefix(head, "<?xml") || strings.HasPrefix(head, "<!doctype")) && strings.Contains(head, "<svg")
}

func rasterSVG(data []byte) (img image.Image, err error) {
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unsupported SVG: %w", err)
	}
	w, h := icon.ViewBox.W, icon.ViewBox.H
	if w < 1 || h < 1 {
		w, h = 512, 512
	}
	if s := float64(svgMaxSide) / max(w, h); s < 1 {
		w, h = w*s, h*s
	}
	iw, ih := max(1, int(w+0.5)), max(1, int(h+0.5))
	icon.SetTarget(0, 0, float64(iw), float64(ih))
	rgba := image.NewRGBA(image.Rect(0, 0, iw, ih))
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, errors.New("could not draw the SVG")
		}
	}()
	icon.Draw(rasterx.NewDasher(iw, ih, rasterx.NewScannerGV(iw, ih, rgba, rgba.Bounds())), 1)
	return rgba, nil
}

// clipEnv is what the clipboard tool choice looks at (tests inject it).
type clipEnv struct {
	getenv   func(string) string
	lookPath func(string) (string, error)
}

var sysClipEnv = clipEnv{getenv: os.Getenv, lookPath: exec.LookPath}

var errNoImageClip = errors.New("Copy image needs wl-copy (Wayland, package wl-clipboard) or xclip (X11), and neither is installed")

// imageClipCmd picks the tool that takes PNG data on stdin and owns the
// clipboard selection: wl-copy in a Wayland session, xclip on X11 (also the
// fallback under Wayland when only xclip is there, through XWayland).
func imageClipCmd(e clipEnv) ([]string, error) {
	wayland := e.getenv("WAYLAND_DISPLAY") != "" || e.getenv("XDG_SESSION_TYPE") == "wayland"
	x11 := e.getenv("DISPLAY") != ""
	if wayland {
		if p, err := e.lookPath("wl-copy"); err == nil {
			return []string{p, "--type", "image/png"}, nil
		}
	}
	if x11 || !wayland {
		if p, err := e.lookPath("xclip"); err == nil {
			return []string{p, "-selection", "clipboard", "-t", "image/png", "-i"}, nil
		}
	}
	return nil, errNoImageClip
}

// readImage returns the bytes of a chat image: local files from disk, web
// images through the in-memory preview cache.
func readImage(src string) ([]byte, error) {
	m := mediaRef{src: src}
	if m.remote() {
		type res struct {
			r   fyne.Resource
			err error
		}
		ch := make(chan res, 1)
		fetchImage(src, func(r fyne.Resource, err error) { ch <- res{r, err} })
		got := <-ch
		if got.err != nil {
			return nil, got.err
		}
		return got.r.Content(), nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("larger than %d MB", maxImageBytes>>20)
	}
	return data, nil
}

// copyImageTo is swapped in tests: converts and hands the PNG to the tool.
var copyImageTo = func(src string) error {
	if runtime.GOOS == "windows" {
		return errors.New("not available on Windows yet; use Copy path")
	}
	data, err := readImage(src)
	if err != nil {
		return err
	}
	pngData, err := toPNG(data, mediaRef{src: src}.name())
	if err != nil {
		return err
	}
	argv, err := imageClipCmd(sysClipEnv)
	if err != nil {
		return err
	}
	return runClipTool(argv, pngData)
}

// copyImage runs off the UI thread and reports in the session's chat, like
// "Copy message".
func (d *desk) copyImage(s *deskSession, m mediaRef) {
	go func() {
		err := copyImageTo(m.src)
		d.do(func() {
			if s == nil {
				return
			}
			if errors.Is(err, errNoImageClip) {
				d.notify(s, err.Error()+".")
				return
			}
			if err != nil {
				d.notify(s, "Could not copy the image: "+err.Error())
				return
			}
			d.notify(s, "Copied the image.")
		})
	}()
}

// mediaMenuItems: the right-click menu of a chat image or video.
func (d *desk) mediaMenuItems(s *deskSession, m mediaRef) []*fyne.MenuItem {
	var items []*fyne.MenuItem
	if m.kind == mediaImage {
		items = append(items, fyne.NewMenuItem("Copy image", func() { d.copyImage(s, m) }))
	}
	label, done := "Copy path", "Copied the path."
	if m.remote() {
		label, done = "Copy link", "Copied the link."
	}
	items = append(items, fyne.NewMenuItem(label, func() {
		d.app.Clipboard().SetContent(m.src)
		if s != nil {
			d.notify(s, done)
		}
	}))
	return items
}

func (d *desk) mediaMenu(s *deskSession, m mediaRef, at fyne.Position) {
	widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", d.mediaMenuItems(s, m)...), d.win.Canvas(), at)
}

// mediaTap: a tap opens, a right-click (long-press on touch) shows the menu.
type mediaTap struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
	onMenu  func(fyne.Position)
}

func newMediaTap(c fyne.CanvasObject, onTap func(), onMenu func(fyne.Position)) *mediaTap {
	t := &mediaTap{content: c, onTap: onTap, onMenu: onMenu}
	t.ExtendBaseWidget(t)
	return t
}

func (t *mediaTap) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.content) }

func (t *mediaTap) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
}

func (t *mediaTap) TappedSecondary(e *fyne.PointEvent) {
	if t.onMenu != nil {
		t.onMenu(e.AbsolutePosition)
	}
}
