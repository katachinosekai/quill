package main

import (
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
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

func TestFetchImageInMemory(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "p.png"))
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.ServeFile(w, r, filepath.Join(dir, "p.png"))
	}))
	defer srv.Close()
	for i := 0; i < 2; i++ {
		got := make(chan fyne.Resource, 1)
		fetchImage(srv.URL+"/p.png", func(r fyne.Resource, err error) {
			if err != nil {
				t.Error(err)
			}
			got <- r
		})
		select {
		case r := <-got:
			if r == nil || len(r.Content()) == 0 {
				t.Fatal("empty image")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("fetch timed out")
		}
	}
	if hits != 1 {
		t.Fatalf("second preview should come from memory, server hits: %d", hits)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatal("web images must not be written to disk")
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

func TestVideoThumbInMemory(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	vid := filepath.Join(t.TempDir(), "clip.mp4")
	if b, err := exec.Command("ffmpeg", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10", "-t", "2", "-pix_fmt", "yuv420p", vid).CombinedOutput(); err != nil {
		t.Skipf("cannot make test video: %s", b)
	}
	r, err := videoThumb(vid)
	if err != nil || r == nil || len(r.Content()) < 8 || string(r.Content()[1:4]) != "PNG" {
		t.Fatalf("thumb: %v", err)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 0 {
		t.Fatal("thumbnails must not be written to disk")
	}
}
