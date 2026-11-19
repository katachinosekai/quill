package main

import (
	"os"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/transcript"
)

func TestRealTranscriptProbe(t *testing.T) {
	path, out := os.Getenv("QUILL_PROBE_TRANSCRIPT"), os.Getenv("QUILL_PROBE_OUT")
	if path == "" || out == "" {
		t.Skip()
	}
	a := test.NewTempApp(t)
	a.Settings().SetTheme(deskTheme{})
	w := test.NewWindow(nil)
	defer w.Close()
	d := newDesk(a, w, config{ProjectsRoot: t.TempDir(), SessionsDir: t.TempDir(), ClaudeBin: "false"})
	w.SetContent(d.build())
	w.Resize(fyne.NewSize(1400, 900))
	conv, err := transcript.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &deskSession{id: "probe", cwd: conv.Cwd, conv: conv, loaded: true, persisted: true, model: "default", mode: "default"}
	d.sessions["probe"] = s
	d.current = s
	start := time.Now()
	d.chat.show(s)
	d.updateChrome()
	t.Logf("items %d, render %v", len(conv.Items), time.Since(start))
	snap := func(name string) {
		time.Sleep(300 * time.Millisecond)
		shot(t, w, out, name)
	}
	snap("real-bottom.png")
	d.chat.scroll.Offset = fyne.NewPos(0, d.chat.scroll.Offset.Y-1500)
	d.chat.scroll.Refresh()
	snap("real-up.png")
}
