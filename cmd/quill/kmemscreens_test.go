package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/test"
)

// kmemScreensDir: where TestKmemAdvertScreens saves its captures, only with
// QUILL_SCREENS=1 (a normal test run writes nothing).
const kmemScreensDir = "/tmp/quill-kmem-ads/v2"

// shownIn: o is in root's visible tree.
func shownIn(root, o fyne.CanvasObject) bool {
	found := false
	deepWalk(root, func(x fyne.CanvasObject) {
		if x == o {
			found = true
		}
	})
	return found
}

// TestKmemSettingsWithoutKmem: without kmem, Settings → General → Memory
// (kmem) is one card, the status then the advert, and no kmem-only control;
// the dismiss hides only the advert line. With kmem the controls come back.
func TestKmemSettingsWithoutKmem(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	h := newHarness(t)
	h.ui(func() {
		h.d.kmemAdverts = nil
		set := h.d.kmemSettings()
		text := visibleText(set)
		if !strings.Contains(text, kmemNoneText) || !strings.Contains(text, kmemAdvertText) {
			t.Fatalf("status and advert: %q", text)
		}
		for _, gone := range []string{"Badge on the kmem button", "Install Quill's default kmem rules", "kmem assistant", "Memory for Quill sessions", "recycle bin"} {
			if strings.Contains(text, gone) {
				t.Fatalf("%q shows without kmem", gone)
			}
		}
		for _, b := range []string{"Show rules", binButtonText(0)} {
			if btn := findButton(set, b); btn == nil || shownIn(set, btn) {
				t.Fatalf("%q exists but is hidden without kmem", b)
			}
		}
		for _, c := range []string{"Badge on the kmem button", "Install Quill's default kmem rules (recommended for beginners)"} {
			if chk := findCheck([]fyne.CanvasObject{set}, c); chk == nil || shownIn(set, chk) {
				t.Fatalf("%q exists but is hidden without kmem", c)
			}
		}
		if why := h.d.settingUnavailable(mustSetting(t, "kmemBadge")); why == "" {
			t.Fatal("the operator can't set the kmem badge without kmem")
		}
		if len(h.d.kmemAdverts) != 1 {
			t.Fatalf("one advert: %d", len(h.d.kmemAdverts))
		}
		card := set.(*fyne.Container).Objects[0]
		v := visibleText(card)
		if i, j := strings.Index(v, kmemNoneText), strings.Index(v, kmemAdvertText); i < 0 || j < i {
			t.Fatalf("one card, the status first: %q", v)
		}
		if findButton(card, "Learn more") == nil {
			t.Fatal("the card has Learn more")
		}
		test.Tap(findButton(card, "")) // the dismiss
		v = visibleText(card)
		if !strings.Contains(v, kmemNoneText) || strings.Contains(v, kmemAdvertText) {
			t.Fatalf("the dismiss hides only the advert: %q", v)
		}

		// kmem installed: the controls show, the advert doesn't.
		h.d.app.Preferences().SetBool(prefKmemAdvertOff, false)
		h.d.kmemOff = false
		h.d.syncKmemButton()
		text = visibleText(set)
		if strings.Contains(text, kmemAdvertText) || !strings.Contains(text, "kmem assistant") {
			t.Fatalf("with kmem: %q", text)
		}
		if btn := findButton(set, "Show rules"); !shownIn(set, btn) {
			t.Fatal("Show rules shows with kmem")
		}
		h.d.kmemOff = true
		h.d.syncKmemButton()
		if strings.Contains(visibleText(set), "kmem assistant") {
			t.Fatal("hidden again when kmem goes away")
		}
	})
}

func mustSetting(t *testing.T, id string) settingDef {
	s, ok := settingByID(id)
	if !ok {
		t.Fatalf("no setting %q", id)
	}
	return s
}

// TestKmemAdvertScreens: the four placements of the advert, the Welcome
// scrolled to the memory question with its advert line in view. With
// QUILL_SCREENS=1 it saves a PNG of each in kmemScreensDir.
func TestKmemAdvertScreens(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KMEM_CONFIG", "")
	save := os.Getenv("QUILL_SCREENS") == "1"
	if save {
		if err := os.MkdirAll(kmemScreensDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t)
	capture := func(name string) {
		if save {
			h.ui(func() { shot(t, h.w, kmemScreensDir, name) })
		}
	}
	cwd := filepath.Join(t.TempDir(), "placeholder-project")
	for _, c := range []struct {
		name, title string
		body        func() fyne.CanvasObject
		size        fyne.Size
	}{
		{"1-settings-memory.png", "Settings → General → Memory (kmem)", func() fyne.CanvasObject { return h.d.kmemSettings() }, fyne.NewSize(760, 300)},
		{"2-memory-tab.png", "Settings → Memory", func() fyne.CanvasObject { return h.d.memoryPane(nil, cwd) }, fyne.NewSize(760, 420)},
		{"3-memory-panel.png", "Memory (Claude's auto memory)", func() fyne.CanvasObject {
			return h.d.claudeMemoryPane(cwd, func() fyne.Window { return h.w })
		}, fyne.NewSize(720, 520)},
	} {
		var dlg dialog.Dialog
		h.ui(func() {
			h.d.kmemAdverts = nil
			body := c.body()
			dlg = dialog.NewCustom(c.title, "Close", body, h.w)
			dlg.Resize(c.size)
			dlg.Show()
			if len(h.d.kmemAdverts) == 0 || !shownIn(body, h.d.kmemAdverts[0]) {
				t.Fatalf("%s: the advert shows", c.name)
			}
		})
		capture(c.name)
		h.ui(dlg.Hide)
	}

	h.ui(func() {
		h.d.kmemAdverts = nil
		h.d.showWelcome(nil)
		ad := h.d.kmemAdverts[0]
		var sc *container.Scroll
		deepWalk(h.w.Canvas().Overlays().Top(), func(x fyne.CanvasObject) {
			if s, ok := x.(*container.Scroll); ok && sc == nil {
				sc = s
			}
		})
		if sc == nil {
			t.Fatal("the Welcome scrolls")
		}
		drv := fyne.CurrentApp().Driver()
		y := drv.AbsolutePositionForObject(ad).Y - drv.AbsolutePositionForObject(sc.Content).Y
		sc.ScrollToOffset(fyne.NewPos(0, y-sc.Size().Height/2))
		top := drv.AbsolutePositionForObject(sc).Y
		at := drv.AbsolutePositionForObject(ad).Y
		if at < top || at+ad.Size().Height > top+sc.Size().Height {
			t.Fatalf("the advert line is in view: at %v, view %v+%v", at, top, sc.Size().Height)
		}
		if !strings.Contains(visibleText(ad), kmemAdvertText) {
			t.Fatal("the advert text shows")
		}
	})
	capture("4-welcome-advert.png")
}
