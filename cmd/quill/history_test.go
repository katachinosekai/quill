package main

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
)

func TestUpDownInMessageBox(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		s := h.d.createSession(h.proj, "default", "default")
		h.d.open(s.id)
		s.conv.AddUser("older message")
		s.conv.AddUser("newest message")
		in := h.d.input
		key := func(n fyne.KeyName) { in.TypedKey(&fyne.KeyEvent{Name: n}) }
		var hints []string
		in.onFix = func(m string) { hints = append(hints, m) }

		// Multi-line draft: Up and Down move through the lines first.
		in.SetText("line one\nline two\nline three")
		in.CursorRow, in.CursorColumn = 2, 3
		key(fyne.KeyUp)
		if in.CursorRow != 1 || in.Text != "line one\nline two\nline three" {
			t.Fatalf("Up moves a line up: row %d %q", in.CursorRow, in.Text)
		}
		key(fyne.KeyDown)
		key(fyne.KeyDown)
		if in.CursorRow != 2 || !strings.HasPrefix(in.Text, "line one") {
			t.Fatalf("Down moves down and stays on the last line: row %d", in.CursorRow)
		}
		key(fyne.KeyUp)
		key(fyne.KeyUp)
		if in.CursorRow != 0 || in.Text != "line one\nline two\nline three" {
			t.Fatal("reaching the top line doesn't leave the draft")
		}

		// One long wrapped line: Up moves up a visual line first.
		long := strings.Repeat("wrapped words go on and on ", 40)
		in.SetText(long)
		in.SetCursorTextOffset(len([]rune(long)))
		if in.CursorRow == 0 {
			t.Fatal("setup: the long line wraps onto several rows")
		}
		row := in.CursorRow
		key(fyne.KeyUp)
		if in.CursorRow != row-1 || in.Text != long {
			t.Fatalf("Up in wrapped text moves one row up: %d -> %d", row, in.CursorRow)
		}
		in.SetText("line one\nline two\nline three")
		in.CursorRow, in.CursorColumn = 0, 0

		// On the top line with text: the first Up arms (and hints), the second recalls.
		key(fyne.KeyUp)
		if in.Text != "line one\nline two\nline three" || len(hints) != 1 || !strings.Contains(hints[0], "again") {
			t.Fatalf("first Up on the top line only hints: %q %v", in.Text, hints)
		}
		key(fyne.KeyUp)
		if in.Text != "newest message" {
			t.Fatalf("second Up recalls the newest message: %q", in.Text)
		}

		// Browsing: one press each way, edits and cursor moves keep the draft.
		key(fyne.KeyUp)
		if in.Text != "older message" {
			t.Fatalf("browsing: one Up goes further back: %q", in.Text)
		}
		in.SetText("older message, edited")
		key(fyne.KeyLeft)
		key(fyne.KeyBackspace)
		key(fyne.KeyDown)
		if in.Text != "newest message" {
			t.Fatalf("browsing: Down goes newer: %q", in.Text)
		}
		key(fyne.KeyDown)
		if in.Text != "line one\nline two\nline three" {
			t.Fatalf("Down past the newest brings the draft back: %q", in.Text)
		}

		// A slow second press doesn't count: two quick presses are needed.
		in.CursorRow, in.CursorColumn = 0, 0
		key(fyne.KeyUp)
		in.upArmed = time.Now().Add(-time.Second)
		key(fyne.KeyUp)
		if !strings.HasPrefix(in.Text, "line one") {
			t.Fatal("a second Up after the window only arms again")
		}
		key(fyne.KeyRight)
		key(fyne.KeyUp)
		if !strings.HasPrefix(in.Text, "line one") {
			t.Fatal("another key in between resets the double press")
		}

		// Empty box: one Up.
		in.SetText("")
		key(fyne.KeyUp)
		if in.Text != "newest message" {
			t.Fatalf("empty box: one Up recalls: %q", in.Text)
		}
		key(fyne.KeyDown)
		if in.Text != "" {
			t.Fatalf("back to the empty draft: %q", in.Text)
		}

		// Queued messages: double press when something is typed, Down on the
		// last line undoes the edit and gives the draft back.
		s.queue = []string{"queued one"}
		in.SetText("typing")
		in.CursorRow = 0
		key(fyne.KeyUp)
		if in.Text != "typing" || len(s.queue) != 1 {
			t.Fatal("queued: first Up with text only arms")
		}
		key(fyne.KeyUp)
		if in.Text != "queued one" || s.qedit == nil {
			t.Fatalf("queued: second Up opens the queued message: %q", in.Text)
		}
		key(fyne.KeyDown)
		if in.Text != "typing" || s.qedit != nil || len(s.queue) != 1 {
			t.Fatalf("Down on the last line undoes the queued edit: %q", in.Text)
		}
	})
}
