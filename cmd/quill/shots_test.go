package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
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
	sessions := t.TempDir()
	uiMu.Lock()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(deskTheme{})
	w := test.NewWindow(nil)
	d := newDesk(a, w, config{ProjectsRoot: root, SessionsDir: sessions, ClaudeBin: "false"})
	stubKmem(d)
	gateUI(t, d)
	w.SetContent(d.build())
	w.Resize(fyne.NewSize(1280, 820))
	uiMu.Unlock()
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
	s := openLoaded(t, d, "11111111-2222-3333-4444-555555555555")
	if !hasRow(d.rows, "11111111-2222-3333-4444-555555555555") {
		t.Fatalf("rows %+v", d.rows)
	}
	if len(d.chat.views) != len(s.conv.Items) || d.input.Disabled() {
		t.Fatalf("views %d items %d", len(d.chat.views), len(s.conv.Items))
	}
	d.search.SetText("rename")
	if !hasRow(d.rows, "11111111-2222-3333-4444-555555555555") {
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
	h.ui(func() { h.d.folderMenu2(h.d.groups[0], fyne.NewPos(120, 90)) })
	snap("folder-menu.png")
	h.ui(func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
	})
	h.ui(func() { h.w.Resize(fyne.NewSize(820, 700)) })
	snap("small.png")
	h.ui(func() { h.d.setSidebar(false) })
	snap("small-collapsed.png")
	h.ui(func() {
		h.d.setSidebar(true)
		h.w.Resize(fyne.NewSize(1280, 860))
	})
	h.ui(func() {
		for _, it := range h.d.sessions[editsID].conv.Items {
			if it.Kind == chat.Tool && it.ToolName == "Bash" {
				h.d.chat.toggleStep(it)
			}
		}
	})
	snap("steps-open.png")

	var s *deskSession
	h.ui(func() {
		h.d.center = true
		s = h.d.createSession(h.proj, "haiku", "default")
		h.d.send("demo")
	})
	h.eventually("permission", func() bool { return s.status() == stWaiting && h.button("Allow") != nil })
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

	media := t.TempDir()
	mpic := filepath.Join(media, "chart.png")
	mimg := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for x := 0; x < 320; x++ {
		for y := 0; y < 180; y++ {
			mimg.Set(x, y, color.NRGBA{uint8(x * 255 / 320), uint8(y * 255 / 180), 160, 255})
		}
	}
	if f, err := os.Create(mpic); err == nil {
		_ = png.Encode(f, mimg)
		f.Close()
	}
	maud := filepath.Join(media, "voice.wav")
	_ = os.WriteFile(maud, []byte("RIFF"), 0o600)
	h.ui(func() {
		ms := h.d.createSession(h.proj, "default", "default")
		u := ms.conv.AddUser("make a chart and a voice note")
		a := &chat.Item{Kind: chat.Assistant, Text: "Here is the chart " + mpic + " and the recording " + maud}
		ms.conv.Items = append(ms.conv.Items, a)
		h.d.chat.show(ms)
		h.d.chat.update([]*chat.Item{u, a})
	})
	time.Sleep(300 * time.Millisecond)
	snap("media.png")
	h.ui(func() {
		ls := h.d.createSession(h.proj, "default", "default")
		u := ls.conv.AddUser("give me the steps")
		a := &chat.Item{Kind: chat.Assistant, Text: "Here is the plan:\n\n1. Build the RPM in a clean Fedora container\n2. Make the AppImage from the RPM contents\n3. Upload both to the release\n   - check the license files\n   - check the file names\n\nThen publish it.\n\n- short item\n- a much longer bullet item that goes on for a while to show wrapping behaviour"}
		ls.conv.Items = append(ls.conv.Items, a)
		h.d.center = true
		h.d.chat.show(ls)
		h.d.chat.update([]*chat.Item{u, a})
	})
	time.Sleep(200 * time.Millisecond)
	snap("lists.png")
	h.ui(func() {
		ids := []string{}
		for id := range h.d.sessions {
			ids = append(ids, id)
		}
		for _, id := range ids[:min(2, len(ids))] {
			h.d.openInSplit(id)
		}
	})
	time.Sleep(200 * time.Millisecond)
	snap("split.png")
	h.ui(func() { h.d.setSplit(1) })
	h.ui(func() { h.d.viewMedia(mediaRef{src: mpic, kind: mediaImage}, nil) })
	time.Sleep(200 * time.Millisecond)
	snap("lightbox.png")
	h.ui(func() {
		h.d.lightboxClose()
		h.d.setInputCenter(true)
		h.d.input.SetText("centered typing\nsecond line is a bit longer than the first")
		h.w.Canvas().Focus(h.d.input)
	})
	time.Sleep(200 * time.Millisecond)
	snap("typing-center.png")
	h.ui(func() {
		h.d.setInputCenter(false)
		h.d.input.SetText("")
	})

	h.ui(func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.center = false
		ls := h.d.createSession(h.proj, "default", "default")
		var paste strings.Builder
		for i := 0; i < 80; i++ {
			fmt.Fprintf(&paste, "log line %d: something happened in the loader while reading mods\n", i)
		}
		paste.WriteString("/run/media/someone/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/SteamLibrary/steamapps/workshop/content/108600/1234567890/mods/SomeVeryLongModName/media/lua/client/AVeryLongFileNameWithoutAnySpaces.lua")
		ls.conv.AddUser(paste.String())
		long := "Here is a long answer. " + strings.Repeat("The quick brown fox jumps over the lazy dog again and again. ", 12) +
			"\n\n```\n" + strings.Repeat("x", 300) + "\nfunc main() { fmt.Println(\"a very long line of code that keeps going and going past the edge of the window\") }\n```\n\nA path: /run/media/someone/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/SteamLibrary/steamapps/workshop/content/108600/1234567890/mods/x.lua"
		line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": long}}}})
		ev, err := protocol.ParseLine(line)
		if err != nil {
			t.Fatal(err)
		}
		ls.conv.Apply(ev)
		h.d.chat.show(ls)
	})
	h.ui(func() { h.d.chat.scroll.ScrollToTop() })
	snap("long.png")
	h.ui(func() { h.d.chat.scrollBottom() })
	snap("long-bottom.png")
	h.ui(func() {
		h.d.chat.scroll.Offset = fyne.NewPos(0, 0)
		h.d.chat.scroll.Refresh()
		h.d.chat.scroll.OnScrolled(h.d.chat.scroll.Offset)
	})
	snap("jump.png")
	h.ui(func() { h.d.chat.scrollBottom() })

	h.ui(func() {
		h.d.cmdList = []protocol.Command{
			{Name: "compact", Description: "Clear conversation history but keep a summary in context", ArgumentHint: "<instructions>"},
			{Name: "clear", Description: "Clear conversation history and free up context"},
			{Name: "code-review", Description: "Review the current diff for correctness bugs", ArgumentHint: "[level] [target]"},
			{Name: "config", Description: "Open the settings panel"},
			{Name: "context", Description: "Show current context usage"},
			{Name: "cost", Description: "Show the total cost of this session"},
		}
		h.d.open(editsID)
	})
	h.eventually("history again", func() bool { return h.d.sessions[editsID].loaded })
	h.ui(func() { h.d.input.SetText("/c") })
	snap("commands.png")
}
