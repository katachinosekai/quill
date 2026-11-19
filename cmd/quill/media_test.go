package main

import (
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

func writePNG(t *testing.T, path string) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
}

func TestFindMedia(t *testing.T) {
	dir := t.TempDir()
	pic := filepath.Join(dir, "shot.png")
	writePNG(t, pic)
	text := "See " + pic + ", the clip https://example.com/a/clip.mp4?x=1 and https://example.com/song.mp3. Missing: /nope/gone.png and https://example.com/page.html"
	refs := findMedia(text)
	if len(refs) != 3 {
		t.Fatalf("refs: %+v", refs)
	}
	if refs[0].src != pic || refs[0].kind != mediaImage {
		t.Fatalf("image: %+v", refs[0])
	}
	if refs[1].kind != mediaVideo || refs[1].name() != "clip.mp4" {
		t.Fatalf("video: %+v", refs[1])
	}
	if refs[2].kind != mediaAudio || refs[2].src != "https://example.com/song.mp3" {
		t.Fatalf("audio: %+v", refs[2])
	}
}

func TestCachedFetch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "p.png"))
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()
	got := make(chan string, 1)
	cachedFetch(srv.URL+"/p.png", func(p string, err error) {
		if err != nil {
			t.Error(err)
		}
		got <- p
	})
	select {
	case p := <-got:
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			t.Fatalf("cached file: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fetch timed out")
	}
}

func TestAssistantMediaPreview(t *testing.T) {
	h := newHarness(t)
	pic := filepath.Join(t.TempDir(), "out.png")
	writePNG(t, pic)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		it := s.conv.AddUser("x")
		h.d.chat.update([]*chat.Item{it})
		a := &chat.Item{Kind: chat.Assistant, Text: "Saved to " + pic}
		if h.d.chat.render(a, &itemView{}) == nil {
			t.Fatal("no render")
		}
		if h.d.chat.mediaPreviews(a.Text) == nil {
			t.Fatal("image link should get a preview")
		}
		if h.d.chat.mediaPreviews("no media here") != nil {
			t.Fatal("plain text should not get a preview")
		}
	})
}
