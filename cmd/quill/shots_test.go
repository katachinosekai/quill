package main

import (
	"bufio"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/live"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

func copyFixture(t *testing.T, root, cwd, id, src string) {
	t.Helper()
	dir := filepath.Join(root, transcript.Sanitize(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureDesk(t *testing.T) (*desk, fyne.Window) {
	root := t.TempDir()
	copyFixture(t, root, "/home/user/proj", "11111111-2222-3333-4444-555555555555", "../../internal/transcript/testdata/edits.jsonl")
	copyFixture(t, root, "/tmp/ws", "514ff30e-119d-4e8f-a1e8-660618817758", "../../internal/transcript/testdata/real-smoke.jsonl")
	a := test.NewTempApp(t)
	a.Settings().SetTheme(deskTheme{})
	w := test.NewWindow(nil)
	d := newDesk(a, w, config{ProjectsRoot: root, SessionsDir: t.TempDir(), ClaudeBin: "false"})
	w.SetContent(d.build())
	w.Resize(fyne.NewSize(1280, 820))
	infos, err := d.scanner.Scan()
	if err != nil || len(infos) != 2 {
		t.Fatalf("scan: %v %d", err, len(infos))
	}
	d.order = infos
	for _, i := range infos {
		d.infos[i.ID] = i
	}
	d.rebuildRows()
	return d, w
}

func openLoaded(t *testing.T, d *desk, id string) *deskSession {
	s := d.session(id)
	conv, err := transcript.Load(d.infos[id].Path)
	if err != nil {
		t.Fatal(err)
	}
	s.conv, s.loaded, s.loadedMod, s.cwd = conv, true, d.infos[id].ModTime, conv.Cwd
	d.open(id)
	return s
}

func replayInto(t *testing.T, c *chat.Conversation, path string, keep func(*protocol.Event) bool) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		ev, err := protocol.ParseLine(sc.Bytes())
		if err == nil && keep(ev) {
			c.Apply(ev)
		}
	}
}

func shot(t *testing.T, w fyne.Window, dir, name string) {
	time.Sleep(300 * time.Millisecond)
	img := w.Canvas().Capture()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestDeskWithFixtures(t *testing.T) {
	d, w := fixtureDesk(t)
	defer w.Close()
	if len(d.rows) != 4 {
		t.Fatalf("rows %+v", d.rows)
	}
	s := openLoaded(t, d, "11111111-2222-3333-4444-555555555555")
	if len(d.chat.views) != len(s.conv.Items) || d.input.Disabled() {
		t.Fatalf("views %d items %d", len(d.chat.views), len(s.conv.Items))
	}
	d.search.SetText("rename")
	if len(d.rows) != 2 {
		t.Fatalf("search rows %+v", d.rows)
	}
	d.search.SetText("")
	d.terms["514ff30e-119d-4e8f-a1e8-660618817758"] = live.Process{PID: 4242, SessionID: "514ff30e-119d-4e8f-a1e8-660618817758"}
	ts := openLoaded(t, d, "514ff30e-119d-4e8f-a1e8-660618817758")
	if ts.status() != stTerminal || d.input.Disabled() || !strings.Contains(d.note.Text, "takes it over") {
		t.Fatal("terminal session should offer takeover")
	}
}

func TestSendEntryKeys(t *testing.T) {
	test.NewTempApp(t)
	var sent []string
	e := newSendEntry(func(s string) { sent = append(sent, s) })
	w := test.NewWindow(e)
	defer w.Close()
	w.Canvas().Focus(e)
	test.Type(e, "a")
	e.KeyDown(&fyne.KeyEvent{Name: "LeftShift"})
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	e.KeyUp(&fyne.KeyEvent{Name: "LeftShift"})
	test.Type(e, "b")
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if len(sent) != 1 || sent[0] != "a\nb" {
		t.Fatalf("sent %q text %q", sent, e.Text)
	}
}

func TestHardBreaks(t *testing.T) {
	got := hardBreaks("one\ntwo\n\n- a\n- b\n```\nx\ny\n```")
	want := "one\n\ntwo\n\n- a\n- b\n```\nx\ny\n```"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestScreenshots(t *testing.T) {
	if os.Getenv("DESK_SHOTS") == "" {
		t.Skip("set DESK_SHOTS=1 to render the window to PNGs under .shots/")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	snap := func(name string) {
		time.Sleep(200 * time.Millisecond)
		h.ui(func() { shot(t, h.w, dir, name) })
	}
	h.ui(func() { h.d.open(editsID) })
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	snap("history.png")

	var s *deskSession
	h.ui(func() {
		s = h.d.createSession(h.proj, "haiku", "default")
		h.d.send("demo")
	})
	h.eventually("permission", func() bool { return s.status() == stWaiting && h.button("Allow") != nil })
	h.ui(func() { test.Tap(h.button("Thought")) })
	snap("permission.png")

	h.ui(func() { test.Tap(h.button("Allow")) })
	h.eventually("done", func() bool { it := lastItem(s); return it != nil && it.Text == "done allow" })
	h.ui(func() {
		h.d.center = true
		h.d.chat.show(s)
		h.d.updateChrome()
	})
	snap("centered.png")

	h.ui(func() { h.d.open(smokeID) })
	h.eventually("terminal", func() bool { return h.d.sessions[smokeID].loaded })
	snap("terminal.png")

	h.ui(func() { h.d.newSessionDialog() })
	snap("new-session.png")
}
