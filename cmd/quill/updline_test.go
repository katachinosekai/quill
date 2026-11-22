package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/updater"
)

func updButtons(h *harness) []string {
	var out []string
	for _, o := range h.d.updActs.Objects {
		if b, ok := o.(*widget.Button); ok {
			out = append(out, b.Text)
		}
	}
	return out
}

// Updates never happen on their own: a release or a new build only shows one
// quiet line; no dialog opens and nothing switches until the user picks it.
func TestUpdateLineAsksFirst(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetString(prefUpdLater, "")
		r := &updater.Release{Tag: "v9.9.9", Page: "https://github.com/katachinosekai/quill/releases/tag/v9.9.9"}
		h.d.offerRelease(r)
		if !h.d.updLine.Visible() || !strings.Contains(h.d.updText.Text, "Quill Desktop 9.9.9 is available") {
			t.Fatalf("a release shows the line: %q", h.d.updText.Text)
		}
		if got := strings.Join(updButtons(h), "|"); got != "Update|What's new|Later" {
			t.Fatalf("line actions: %s", got)
		}
		if n := len(h.w.Canvas().Overlays().List()); n != 0 || h.d.updating || h.d.swapOK {
			t.Fatalf("no dialog, no download, no switch until Update: overlays %d", n)
		}
		test_tap(h.d.updActs.Objects[2])
		if h.d.updLine.Visible() || p.String(prefUpdLater) != "v9.9.9" {
			t.Fatal("Later hides the line and remembers the version")
		}
		h.d.offerRelease(r)
		if h.d.updLine.Visible() {
			t.Fatal("the same version doesn't come back after Later")
		}
		h.d.offerRelease(&updater.Release{Tag: "v9.9.10"})
		if !h.d.updLine.Visible() {
			t.Fatal("a newer version shows again")
		}

		// A new build on disk: no switch, the line asks.
		h.d.swapOK, h.d.swapTo = false, ""
		h.d.binaryChanged("/nonexistent/quill")
		if h.d.swapTo != "" || h.d.swapping || !strings.Contains(h.d.updText.Text, "new Quill Desktop build") {
			t.Fatalf("a new build waits for the user: swapTo %q", h.d.swapTo)
		}
		if got := strings.Join(updButtons(h), "|"); got != "Restart into it|Later" {
			t.Fatalf("build line actions: %s", got)
		}
		h.d.lastTyped = time.Now() // keeps trySwap from going ahead in the test
		reloadPending(h.d)
		if !h.d.swapOK || h.d.swapTo != "/nonexistent/quill" {
			t.Fatal("quill --reload takes the waiting build")
		}
		h.d.swapOK, h.d.swapTo = false, ""
		h.d.acceptBuild()
		if !h.d.swapOK || h.d.swapTo != "/nonexistent/quill" {
			t.Fatal("Restart into it asks for the switch")
		}
		h.d.swapTo, h.d.swapOK = "", false
		h.d.setUpdLine("")
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() {
			h.w.Resize(fyne.NewSize(1280, 860))
			h.d.app.Preferences().SetString(prefUpdLater, "")
			h.d.offerRelease(&updater.Release{Tag: "v0.1.20", Page: "https://github.com/katachinosekai/quill/releases/tag/v0.1.20"})
		})
		h.ui(func() { shot(t, h.w, dir, "update-line.png") })
		h.ui(func() { h.d.setUpdLine("") })
	}
}

func test_tap(o fyne.CanvasObject) {
	if b, ok := o.(*widget.Button); ok && b.OnTapped != nil {
		b.OnTapped()
	}
}

// reloadPending does what the "reload" command does before trySwap.
func reloadPending(d *desk) {
	if d.swapTo == "" && d.pendingBuild != "" {
		d.swapOK, d.swapTo = true, d.pendingBuild
	}
}
