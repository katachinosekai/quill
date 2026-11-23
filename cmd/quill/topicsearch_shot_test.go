package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

// TestTopicSearchShots writes the search-first card (empty with Recent,
// typing with matches and mentions, did-you-mean) to $QUILL_SHOT_DIR.
func TestTopicSearchShots(t *testing.T) {
	dir := os.Getenv("QUILL_SHOT_DIR")
	if dir == "" {
		t.Skip("set QUILL_SHOT_DIR to write screenshots")
	}
	h := newHarness(t)
	topicKmem(t, h)
	recallRunner(h, nil)
	shot := func(name string) {
		img := h.w.Canvas().Capture()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		png.Encode(f, img)
		f.Close()
	}
	h.ui(func() {
		h.w.Resize(fyne.NewSize(1100, 700))
		h.d.noteRecentTopic("beta")
		h.d.noteRecentTopic("alpha")
	})
	openSearchCard(t, h)
	h.ui(func() { shot("1-empty-recent") })
	h.ui(func() { cardSearchEntry(h).SetText("zebra") })
	h.eventually("mentions", func() bool { return strings.Contains(cardTexts(h), "mentions") })
	h.ui(func() { shot("2-typing-mentions") })
	h.ui(func() { cardSearchEntry(h).SetText("Alpa") })
	h.eventually("dym", func() bool { return strings.Contains(cardRowLabels(h), "Did you mean") })
	h.ui(func() { shot("3-did-you-mean") })
}
