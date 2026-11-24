package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestToPNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 6, 3))
	src.Set(1, 1, color.RGBA{200, 10, 10, 255})
	var pb, jb bytes.Buffer
	png.Encode(&pb, src)
	jpeg.Encode(&jb, src, nil)

	got, err := toPNG(pb.Bytes(), "a.png")
	if err != nil || !bytes.Equal(got, pb.Bytes()) {
		t.Fatalf("PNG should pass through unchanged: %v", err)
	}
	got, err = toPNG(jb.Bytes(), "a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(got))
	if err != nil || img.Bounds().Dx() != 6 || img.Bounds().Dy() != 3 {
		t.Fatalf("JPEG -> PNG: %v %v", err, img)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 20" width="40" height="20"><rect width="40" height="20" fill="#f00"/></svg>`
	got, err = toPNG([]byte(svg), "pic.svg")
	if err != nil {
		t.Fatal(err)
	}
	img, err = png.Decode(bytes.NewReader(got))
	if err != nil || img.Bounds().Dx() != 40 || img.Bounds().Dy() != 20 {
		t.Fatalf("SVG -> PNG: %v %v", err, img)
	}
	if _, _, _, a := img.At(20, 10).RGBA(); a == 0 {
		t.Fatal("SVG should be drawn, not blank")
	}
	if _, err := toPNG([]byte("not an image"), "x.png"); err == nil {
		t.Fatal("garbage should fail")
	}
}

func TestImageClipCmd(t *testing.T) {
	env := func(vars map[string]string, tools ...string) clipEnv {
		return clipEnv{
			getenv: func(k string) string { return vars[k] },
			lookPath: func(name string) (string, error) {
				for _, tl := range tools {
					if tl == name {
						return "/usr/bin/" + name, nil
					}
				}
				return "", errors.New("not found")
			},
		}
	}
	wl := map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0", "XDG_SESSION_TYPE": "wayland"}
	x11 := map[string]string{"DISPLAY": ":0", "XDG_SESSION_TYPE": "x11"}
	for _, c := range []struct {
		name string
		env  clipEnv
		want string
	}{
		{"wayland", env(wl, "wl-copy", "xclip"), "/usr/bin/wl-copy --type image/png"},
		{"wayland, only xclip (XWayland)", env(wl, "xclip"), "/usr/bin/xclip -selection clipboard -t image/png -i"},
		{"x11", env(x11, "wl-copy", "xclip"), "/usr/bin/xclip -selection clipboard -t image/png -i"},
		{"x11 without xclip", env(x11, "wl-copy"), ""},
		{"none", env(wl), ""},
	} {
		argv, err := imageClipCmd(c.env)
		if c.want == "" {
			if !errors.Is(err, errNoImageClip) {
				t.Errorf("%s: want errNoImageClip, got %v %v", c.name, argv, err)
			}
			continue
		}
		if err != nil || strings.Join(argv, " ") != c.want {
			t.Errorf("%s: got %q %v, want %q", c.name, argv, err, c.want)
		}
	}
	if !strings.Contains(errNoImageClip.Error(), "wl-copy") || !strings.Contains(errNoImageClip.Error(), "xclip") {
		t.Fatal("the error should name both tools")
	}
}

func TestMediaMenuCopyImage(t *testing.T) {
	h := newHarness(t)
	pic := filepath.Join(t.TempDir(), "shot.png")
	writePNG(t, pic)
	var copied []string
	var fail error
	orig := copyImageTo
	copyImageTo = func(src string) error { copied = append(copied, src); return fail }
	t.Cleanup(func() { copyImageTo = orig })

	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "default", "default")
		h.d.chat.update([]*chat.Item{s.conv.AddUser("x")})
		got := strings.Join(menuLabels(h.d.mediaMenuItems(s, mediaRef{src: pic, kind: mediaImage})), "|")
		if got != "Copy image|Copy path" {
			t.Fatalf("local image menu: %s", got)
		}
		got = strings.Join(menuLabels(h.d.mediaMenuItems(s, mediaRef{src: "https://example.com/p.png", kind: mediaImage})), "|")
		if got != "Copy image|Copy link" {
			t.Fatalf("web image menu: %s", got)
		}
		got = strings.Join(menuLabels(h.d.mediaMenuItems(s, mediaRef{src: "/tmp/v.mp4", kind: mediaVideo})), "|")
		if got != "Copy path" {
			t.Fatalf("video menu: %s", got)
		}

		// the preview card answers a right-click with the menu
		card := h.d.chat.mediaCard(mediaRef{src: pic, kind: mediaImage})
		mt, ok := card.(*mediaTap)
		if !ok {
			t.Fatalf("card is %T, want a right-clickable mediaTap", card)
		}
		before := len(h.w.Canvas().Overlays().List())
		mt.TappedSecondary(&fyne.PointEvent{})
		if len(h.w.Canvas().Overlays().List()) != before+1 {
			t.Fatal("right-click should open the image menu")
		}
		h.w.Canvas().Overlays().Top().Hide()
		h.w.Canvas().Overlays().Remove(h.w.Canvas().Overlays().Top())

		// "Copy image" copies off the UI thread and says so in the chat
		h.d.mediaMenuItems(s, mediaRef{src: pic, kind: mediaImage})[0].Action()
	})
	lastNotice := func() string { // called under the UI lock (eventually)
		var txt string
		for _, it := range s.conv.Items {
			if it.Kind == chat.Notice {
				txt = it.Text
			}
		}
		return txt
	}
	h.eventually("copied notice", func() bool { return lastNotice() == "Copied the image." })
	if len(copied) != 1 || copied[0] != pic {
		t.Fatalf("copied: %v", copied)
	}

	fail = errNoImageClip
	h.ui(func() { h.d.mediaMenuItems(s, mediaRef{src: pic, kind: mediaImage})[0].Action() })
	h.eventually("no-tool notice", func() bool {
		n := lastNotice()
		return strings.HasPrefix(n, "Copy image needs wl-copy") && strings.HasSuffix(n, ".")
	})

	// the viewer's image has the same menu
	h.ui(func() {
		h.d.viewMedia(mediaRef{src: pic, kind: mediaImage}, nil)
		var found *mediaTap
		var walk func(o fyne.CanvasObject)
		walk = func(o fyne.CanvasObject) {
			switch x := o.(type) {
			case *mediaTap:
				found = x
			case *fyne.Container:
				for _, c := range x.Objects {
					walk(c)
				}
			case *lightbox:
				walk(x.content)
			case *absorb:
				walk(x.content)
			case fyne.Widget:
				for _, c := range test.WidgetRenderer(x).Objects() {
					walk(c)
				}
			}
		}
		walk(h.w.Canvas().Overlays().Top())
		if found == nil {
			t.Fatal("viewer image should be right-clickable")
		}
		h.d.lightboxClose()
	})
}
