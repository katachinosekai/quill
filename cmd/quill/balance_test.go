package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

const longItem = "Make the overlong item wrap into a few balanced lines that are about as wide as each other, weighted against the other short items in the same list, instead of stretching the column"

func TestOverlongLine(t *testing.T) {
	for _, c := range []struct {
		widths []float32
		idx    int
		ok     bool
	}{
		{[]float32{100}, 0, false},          // a single line: nothing to weigh against
		{[]float32{100, 70, 50}, 0, false},  // 100 <= 1.5*70
		{[]float32{50, 200, 70}, 1, true},   // 200 > 1.5*70
		{[]float32{60, 90, 60}, 0, false},   // exactly 1.5x is not overlong
		{[]float32{60, 91, 60}, 1, true},    // just over
		{[]float32{300, 280, 50}, 0, false}, // two long lines: the widest other is 280
	} {
		idx, _, ok := overlongLine(c.widths)
		if ok != c.ok || ok && idx != c.idx {
			t.Errorf("overlongLine(%v) = %d, %v; want %d, %v", c.widths, idx, ok, c.idx, c.ok)
		}
	}
}

// The balanced width keeps the line count of a plain wrap at the target and
// evens the lines out: no short tail.
func TestBalancedEvensLines(t *testing.T) {
	test.NewTempApp(t)
	size := msgTextSize()
	w := measureWords(longItem, size, fyne.TextStyle{})
	target := w.total / 3.2
	b := w.balanced(target, 0)
	if b <= 0 || b > target {
		t.Fatalf("balanced = %v, want in (0, %v]", b, target)
	}
	if got, want := w.lines(b), w.lines(target); got != want {
		t.Fatalf("balanced wrap takes %d lines, plain wrap %d", got, want)
	}
	var lines []float32
	x := float32(-1)
	for _, ww := range w.ws {
		if x >= 0 && x+w.sp+ww <= b {
			x += w.sp + ww
			continue
		}
		if x >= 0 {
			lines = append(lines, x)
		}
		x = ww
	}
	lines = append(lines, x)
	lo, hi := lines[0], lines[0]
	for _, l := range lines {
		lo, hi = min(lo, l), max(hi, l)
	}
	if lo < 0.6*hi {
		t.Fatalf("unbalanced lines %v at width %v", lines, b)
	}
	if measureWords("Done.", size, fyne.TextStyle{}).balanced(target, 0) != 0 || measureWords(strings.Repeat("x", 200), size, fyne.TextStyle{}).balanced(target, 0) != 0 {
		t.Fatal("one-line or one-word text must not be balanced")
	}
	// The floor wins over evening out: fewer lines, never narrower.
	for _, floor := range []float32{target * 0.9, target * 1.5, w.total * 0.6} {
		if fb := w.balanced(target, floor); fb != 0 && fb < floor {
			t.Fatalf("floor %v: width %v", floor, fb)
		}
	}
}

func parseMD(md string) []widget.RichTextSegment {
	return widget.NewRichTextFromMarkdown(hardBreaks(md)).Segments
}

func firstList(t *testing.T, segs []widget.RichTextSegment) *widget.ListSegment {
	t.Helper()
	for _, s := range segs {
		if l, ok := s.(*widget.ListSegment); ok {
			return l
		}
	}
	t.Fatal("no list")
	return nil
}

// columns finds the scaling columns in a centered block.
func columns(o fyne.CanvasObject) []*colLayout {
	var out []*colLayout
	deepWalk(o, func(x fyne.CanvasObject) {
		if c, ok := x.(*fyne.Container); ok {
			if l, ok := c.Layout.(*colLayout); ok {
				out = append(out, l)
			}
		}
	})
	return out
}

// Widths scale with the chat: no fixed numbers (user 2026-11-22).
func TestListColumnScalesWithChat(t *testing.T) {
	test.NewTempApp(t)
	for _, marker := range []string{"1. ", "- "} {
		even := firstList(t, parseMD(marker+"Read the code first\n"+marker+"Change the layout\n"+marker+"Run all the tests"))
		nat := listNaturalWidth(even, 0)
		c := columns(listColumn(even, widget.NewRichText(even)))[0]
		for _, avail := range []float32{nat + 10, 2 * nat, 3000} {
			if got := c.width(avail); got != nat {
				t.Fatalf("%q: even list at %v: column %v, want its natural %v", marker, avail, got, nat)
			}
		}
		l := firstList(t, parseMD(marker+"Read the code first\n"+marker+longItem+"\n"+marker+"Run all the tests"))
		c = columns(listColumn(l, widget.NewRichText(l)))[0]
		natLong := listNaturalWidth(l, 0)
		var prev float32
		for _, avail := range []float32{natLong * 1.2, natLong * 1.6, natLong * 2.5} {
			w := c.width(avail)
			if w >= natLong {
				t.Fatalf("%q: overlong item at %v: column %v, want narrower than %v", marker, avail, w, natLong)
			}
			if w < balanceFloorShare*avail-1 {
				t.Fatalf("%q: column %v narrower than %.0f%% of the chat (%v)", marker, w, balanceFloorShare*100, avail)
			}
			if w < prev {
				t.Fatalf("%q: a wider chat gave a narrower column (%v after %v)", marker, w, prev)
			}
			prev = w
		}
	}
}

// A list wider than the chat is capped at listCapShare of it and centered.
func TestWideListCappedToShareOfChat(t *testing.T) {
	test.NewTempApp(t)
	long := strings.Repeat("A plan step that is long but not much longer than the others in the list. ", 3)
	l := firstList(t, parseMD("1. "+long+"\n2. "+long+"x\n3. "+long+"y"))
	c := columns(listColumn(l, widget.NewRichText(l)))[0]
	nat := listNaturalWidth(l, 0)
	for _, avail := range []float32{nat * 0.5, nat * 0.9} {
		if got, want := c.width(avail), listCapShare*avail; got != want {
			t.Fatalf("list wider than the chat (%v > %v): column %v, want %v", nat, avail, got, want)
		}
	}
	if got := c.width(nat * 1.1); got != nat {
		t.Fatalf("list that fits: column %v, want natural %v", got, nat)
	}
}

func TestCenteredParagraphBalancesOverlongLine(t *testing.T) {
	test.NewTempApp(t)
	if n := len(columns(centeredBlocks(parseMD("a. Read the code first\nb. " + longItem + "\nc. Run all the tests")))); n != 1 {
		t.Fatalf("lettered group with an overlong line: %d columns, want 1", n)
	}
	if n := len(columns(centeredBlocks(parseMD("Read the code first\nChange the layout\nRun all the tests")))); n != 0 {
		t.Fatalf("even paragraph group: %d columns, want 0", n)
	}
	if n := len(columns(centeredBlocks(parseMD(longItem)))); n != 0 {
		t.Fatalf("single line: %d columns, want 0", n)
	}
	o := centeredBlocks(parseMD("Read the code first\n" + longItem + "\nRun all the tests"))
	box, ok := o.(*fyne.Container)
	if !ok || len(box.Objects) != 3 {
		t.Fatalf("want 3 parts (before, balanced line, after), got %T", o)
	}
	c, ok := box.Objects[1].(*fyne.Container).Layout.(*colLayout)
	if !ok {
		t.Fatal("the middle part is the balanced line")
	}
	// Narrow chat: the line wraps at the chat's own width anyway.
	if w := c.width(300); w > 300 {
		t.Fatalf("column %v wider than a 300 px chat", w)
	}
	if a, b := c.width(1000), c.width(2000); b < a {
		t.Fatalf("a wider chat gave a narrower column: %v then %v", a, b)
	}
}

// The markdown parser splits a paragraph at **bold**, `code` and links;
// those pieces are one line, not a group to balance.
func TestCenteredParagraphStyledRunsAreOneLine(t *testing.T) {
	test.NewTempApp(t)
	styled := strings.Repeat("The first paragraph has **bold** words, `code` and a [link](https://example.com) to stream. ", 3)
	if n := len(columns(centeredBlocks(parseMD(styled)))); n != 0 {
		t.Fatalf("one styled paragraph: %d columns, want 0", n)
	}
	o := centeredBlocks(parseMD("Read the code first\n" + styled + "\nRun all the tests"))
	box, ok := o.(*fyne.Container)
	if !ok || len(box.Objects) != 3 {
		t.Fatalf("want 3 parts (before, balanced styled line, after), got %T", o)
	}
	got := box.Objects[1].(*fyne.Container).Objects[0].(*widget.RichText).String()
	if !strings.Contains(got, "bold") || !strings.Contains(got, "link") || strings.Contains(got, "Run all") {
		t.Fatalf("the balanced column holds the whole styled line and nothing else: %q", got)
	}
}

// colLayout re-works its width when the chat is resized.
func TestColumnFollowsResize(t *testing.T) {
	test.NewTempApp(t)
	calls := 0
	o := widget.NewLabel("x")
	c := newColumn(true, func(avail float32) float32 { calls++; return avail / 2 }, o)
	c.Resize(fyne.NewSize(800, 40))
	calls = 1
	c.Resize(fyne.NewSize(800, 50))
	if o.Size().Width != 400 || o.Position().X != 200 || calls != 1 {
		t.Fatalf("at 800: width %v x %v calls %d", o.Size().Width, o.Position().X, calls)
	}
	c.Resize(fyne.NewSize(1200, 50))
	if o.Size().Width != 600 || o.Position().X != 300 || calls != 2 {
		t.Fatalf("at 1200: width %v x %v calls %d", o.Size().Width, o.Position().X, calls)
	}
}
