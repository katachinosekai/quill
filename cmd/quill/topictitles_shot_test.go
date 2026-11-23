package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

var scriptSamples = []struct{ name, title string }{
	{"japanese", "日本語メモ"},
	{"chinese", "项目计划"},
	{"korean", "프로젝트"},
	{"arabic", "مشروع جديد"},
	{"hindi", "नई परियोजना"},
	{"russian", "Новый проект"},
}

// TestTopicTitleScriptShots renders the Open a topic card (all samples listed
// as topics, one typed in the entry) and a topic tab for each sample to
// $QUILL_SHOT_DIR, for a human to look at.
func TestTopicTitleScriptShots(t *testing.T) {
	dir := os.Getenv("QUILL_SHOT_DIR")
	if dir == "" {
		t.Skip("set QUILL_SHOT_DIR to write screenshots")
	}
	h := newHarness(t)
	topicKmem(t, h)
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
		m := map[string]string{}
		for i, s := range scriptSamples {
			m[fmt.Sprintf("topic-%d", i+1)] = s.title
		}
		h.d.org.TopicTitles = m
		seedSavedTopicTitles(m)
	})
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "topics" {
			s := ""
			for i := range scriptSamples {
				s += fmt.Sprintf("topic-%d/notes  2 facts, 1 KB\n", i+1)
			}
			return s, nil
		}
		return "", fmt.Errorf("unexpected")
	}
	h.ui(func() { h.d.newSessionDialog() })
	h.eventually("rows", func() bool { return len(topicCacheList()) == len(scriptSamples) })
	h.ui(func() {
		for _, s := range scriptSamples {
			setEntryText(h.d.chat.box, s.title)
			h.w.Canvas().Refresh(h.w.Content())
			shot("card-" + s.name)
		}
	})
	for _, s := range scriptSamples {
		h.ui(func() {
			h.d.newSessionDialog()
			h.d.pickNewTopic(h.d.current, s.title)
			shot("tab-" + s.name)
		})
	}
}

func topicCacheList() []topicInfo {
	topicCache.mu.Lock()
	defer topicCache.mu.Unlock()
	return topicCache.list
}

func setEntryText(root fyne.CanvasObject, text string) {
	deepWalk(root, func(o fyne.CanvasObject) {
		if e, ok := o.(*widget.Entry); ok {
			e.SetText(text)
		}
	})
}
