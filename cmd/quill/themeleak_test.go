package main

import (
	"fmt"
	"image/color"
	"runtime"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// themeLeakReply is a ~12 KB placeholder reply with paragraphs, lists and
// inline formatting, like a long answer from the model.
func themeLeakReply() string {
	var b strings.Builder
	for i := 0; b.Len() < 12000; i++ {
		fmt.Fprintf(&b, "Paragraph %d has **bold** words, `inline code` and a few plain sentences about the change being streamed. ", i)
		b.WriteString(strings.Repeat("More text keeps arriving from the model in small bursts, with `code` and **bold** now and then. ", 24))
		b.WriteString("\n\n")
		if i%3 == 0 {
			b.WriteString("- one item\n- another item with `code`\n\n")
		}
	}
	return b.String()
}

// heapAfterGC returns the live heap after a full collection (cleanups run
// in the background after a GC, so collect twice).
func heapAfterGC() uint64 {
	var ms runtime.MemStats
	for range 3 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// TestThemeCacheBounded: Fyne's theme override cache kept every text and
// rectangle inside each message's ThemeOverride forever (RAM review
// 2026-11-23: 243,000 entries, ~0.9 MB per streaming redraw). Streaming a
// long reply, switching tabs and closing them must leave the cache and the
// live heap where they started once the paint loop's clean has run.
func TestThemeCacheBounded(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		h.d.app.Preferences().SetBool(prefAnimateText, false)
		h.d.open(editsID)
	})
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	clean := func() {
		h.ui(func() { test.CleanCaches(h.w.Canvas(), 2*time.Minute) })
	}
	// settle: expire what is not drawn, collect it, and let the next clean
	// drop the theme scopes of the overrides that were collected.
	settle := func() (int, uint64) {
		clean()
		heapAfterGC()
		clean()
		return test.ThemeOverrideEntries(), heapAfterGC()
	}
	entries0, heap0 := settle()

	full := themeLeakReply()
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind, it.Streaming = chat.Assistant, true
		h.d.chat.show(s)
	})
	const redraws = 300
	step := len(full)/redraws + 1
	for n := step; ; n += step {
		m := min(n, len(full))
		h.ui(func() {
			it.Text = full[:m]
			h.d.chat.update([]*chat.Item{it})
		})
		if m == len(full) {
			break
		}
		if n/step%50 == 0 {
			clean()
		}
	}
	h.ui(func() {
		it.Streaming = false
		h.d.chat.update([]*chat.Item{it})
	})
	entriesStream, heapStream := settle()

	// Switching tabs shows the cached views again (their renderers expired
	// meanwhile): the cache must not grow with each switch.
	var cycles []int
	for range 20 {
		h.ui(func() { h.d.open(smokeID) })
		h.eventually("smoke history", func() bool { return h.d.sessions[smokeID].loaded })
		h.ui(func() { h.d.open(editsID) })
		e, _ := settle()
		cycles = append(cycles, e)
	}
	h.ui(func() {
		h.d.closeSessionTab(smokeID)
		h.d.closeSessionTab(editsID)
	})
	entriesEnd, heapEnd := settle()

	t.Logf("theme cache entries: start %d, after streaming %d, tab switches %d..%d, after closing tabs %d",
		entries0, entriesStream, cycles[0], cycles[len(cycles)-1], entriesEnd)
	t.Logf("live heap: start %.1f MB, after streaming %.1f MB, after closing tabs %.1f MB",
		float64(heap0)/1e6, float64(heapStream)/1e6, float64(heapEnd)/1e6)
	// What stays is what Quill still holds: the finished reply and the
	// session views it caches (cachedViews), a few thousand objects. Before
	// the fix every redraw left its objects behind (~24,000 entries here).
	if entriesStream > entries0+5000 {
		t.Errorf("theme cache grew from %d to %d entries while streaming", entries0, entriesStream)
	}
	if grow := cycles[len(cycles)-1] - cycles[0]; grow > 50 {
		t.Errorf("theme cache grew by %d entries over %d tab switches", grow, len(cycles)-1)
	}
	if entriesEnd > entriesStream+1000 {
		t.Errorf("theme cache holds %d entries after closing the tabs (%d after streaming)", entriesEnd, entriesStream)
	}
	// Before the fix: +16.6 MB; after: ~6 MB (the reply and cached views).
	if grow := int64(heapEnd) - int64(heap0); grow > 12<<20 {
		t.Errorf("live heap grew by %.1f MB", float64(grow)/1e6)
	}
}

type redTheme struct{ msgTheme }

var themeRed = color.NRGBA{R: 0xff, A: 0xff}

func (t redTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameForeground {
		return themeRed
	}
	return t.msgTheme.Color(n, v)
}

// TestThemeOverrideLookups: with the theme cache holding its objects weakly,
// the objects in an override still get its sizes, colours and SVG icon
// colours, also after a garbage collection and a cache clean, and the
// entries go once the override is no longer used.
func TestThemeOverrideLookups(t *testing.T) {
	uiMu.Lock()
	defer uiMu.Unlock()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(deskTheme{})
	lbl := widget.NewLabel("Message text")
	rt := widget.NewRichTextFromMarkdown("Some **bold** text")
	icon := widget.NewIcon(theme.CancelIcon())
	plain := widget.NewLabel("Outside")
	w := test.NewWindow(container.NewVBox(container.NewThemeOverride(container.NewVBox(lbl, rt, icon), redTheme{}), plain))
	defer w.Close()
	w.Resize(fyne.NewSize(400, 300))
	before := test.ThemeOverrideEntries()

	check := func(when string) {
		t.Helper()
		if s := lbl.Theme().Size(theme.SizeNameText); s != fontMessage {
			t.Errorf("%s: label text size %v, want %v", when, s, fontMessage)
		}
		if c := lbl.Theme().Color(theme.ColorNameForeground, theme.VariantDark); c != themeRed {
			t.Errorf("%s: label foreground %v, want the override's red", when, c)
		}
		if s := plain.Theme().Size(theme.SizeNameText); s != fontBody {
			t.Errorf("%s: a label outside the override has text size %v, want %v", when, s, fontBody)
		}
		texts := 0
		deepWalk(rt, func(o fyne.CanvasObject) {
			if tx, ok := o.(*canvas.Text); ok && strings.TrimSpace(tx.Text) != "" {
				texts++
				if tx.TextSize != fontMessage {
					t.Errorf("%s: rich text %q drawn at %v, want %v", when, tx.Text, tx.TextSize, fontMessage)
				}
			}
		})
		if texts == 0 {
			t.Errorf("%s: no rich text drawn", when)
		}
		img := w.Canvas().Capture()
		pos := a.Driver().AbsolutePositionForObject(icon)
		sc := w.Canvas().Scale()
		red := 0
		for y := int(pos.Y * sc); y < int((pos.Y+icon.Size().Height)*sc); y++ {
			for x := int(pos.X * sc); x < int((pos.X+icon.Size().Width)*sc); x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if r > 0xc000 && g < 0x4000 && b < 0x4000 {
					red++
				}
			}
		}
		if red == 0 {
			t.Errorf("%s: the themed SVG icon is not drawn in the override's foreground colour", when)
		}
	}
	check("first draw")
	for range 2 {
		test.CleanCaches(w.Canvas(), 2*time.Minute)
		heapAfterGC()
	}
	check("after a GC and a cache clean")
	if n := test.ThemeOverrideEntries(); n < before {
		t.Errorf("entries of objects still shown were dropped: %d -> %d", before, n)
	}

	w.SetContent(plain)
	lbl, rt, icon = nil, nil, nil
	for range 2 {
		test.CleanCaches(w.Canvas(), 2*time.Minute)
		heapAfterGC()
	}
	if n := test.ThemeOverrideEntries(); n > before-5 {
		t.Errorf("the entries of a dropped override stay: %d before, %d after", before, n)
	}
	if s := plain.Theme().Size(theme.SizeNameText); s != fontBody {
		t.Errorf("label outside the override: text size %v, want %v", s, fontBody)
	}
}
