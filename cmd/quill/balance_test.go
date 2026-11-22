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
func TestBalancedWidthEvensLines(t *testing.T) {
	test.NewTempApp(t)
	size := msgTextSize()
	target := fyne.MeasureText(strings.Repeat("n", 60), size, fyne.TextStyle{}).Width
	words := strings.Fields(longItem)
	ws := make([]float32, len(words))
	for i, w := range words {
		ws[i] = fyne.MeasureText(w, size, fyne.TextStyle{}).Width
	}
	sp := fyne.MeasureText(" ", size, fyne.TextStyle{}).Width
	b := balancedWidth(longItem, target, size, fyne.TextStyle{})
	if b <= 0 || b > target {
		t.Fatalf("balancedWidth = %v, want in (0, %v]", b, target)
	}
	if got, want := greedyLines(ws, sp, b), greedyLines(ws, sp, target); got != want {
		t.Fatalf("balanced wrap takes %d lines, plain wrap %d", got, want)
	}
	// Lines at the balanced width: the shortest is at least 60% of the longest.
	var lines []float32
	x := float32(-1)
	for _, w := range ws {
		if x >= 0 && x+sp+w <= b {
			x += sp + w
			continue
		}
		if x >= 0 {
			lines = append(lines, x)
		}
		x = w
	}
	lines = append(lines, x)
	lo, hi := lines[0], lines[0]
	for _, l := range lines {
		lo, hi = min(lo, l), max(hi, l)
	}
	if lo < 0.6*hi {
		t.Fatalf("unbalanced lines %v at width %v", lines, b)
	}
	// Short text and single words need no balancing.
	if balancedWidth("Done.", target, size, fyne.TextStyle{}) != 0 || balancedWidth(strings.Repeat("x", 200), target, size, fyne.TextStyle{}) != 0 {
		t.Fatal("one-line or one-word text must not be balanced")
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

func TestBalancedListWidth(t *testing.T) {
	test.NewTempApp(t)
	for _, marker := range []string{"1. ", "- "} {
		even := firstList(t, parseMD(marker+"Read the code first\n"+marker+"Change the layout\n"+marker+"Run all the tests"))
		if balancedListWidth(even) != listNaturalWidth(even, 0) {
			t.Fatalf("%q: a list with no overlong item keeps its natural width", marker)
		}
		l := firstList(t, parseMD(marker+"Read the code first\n"+marker+longItem+"\n"+marker+"Run all the tests"))
		nat, bal := listNaturalWidth(l, 0), balancedListWidth(l)
		if bal >= nat {
			t.Fatalf("%q: overlong item: balanced %v, natural %v; want narrower", marker, bal, nat)
		}
		if bal < listNaturalWidth(even, 0) {
			t.Fatalf("%q: balanced width %v is narrower than the other items (%v)", marker, bal, listNaturalWidth(even, 0))
		}
	}
	one := firstList(t, parseMD("- "+longItem))
	if balancedListWidth(one) != listNaturalWidth(one, 0) {
		t.Fatal("a one-item list is unchanged")
	}
}

// balancedColumns lists the fitCenter widths in a centered block.
func balancedColumns(o fyne.CanvasObject) []float32 {
	var out []float32
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		c, ok := o.(*fyne.Container)
		if !ok {
			return
		}
		if f, ok := c.Layout.(fitCenter); ok {
			out = append(out, f.natural)
		}
		for _, k := range c.Objects {
			walk(k)
		}
	}
	walk(o)
	return out
}

func TestCenteredParagraphBalancesOverlongLine(t *testing.T) {
	test.NewTempApp(t)
	got := balancedColumns(centeredBlocks(parseMD("a. Read the code first\nb. " + longItem + "\nc. Run all the tests")))
	if len(got) != 1 {
		t.Fatalf("lettered group with an overlong line: %d balanced columns, want 1", len(got))
	}
	if n := len(balancedColumns(centeredBlocks(parseMD("Read the code first\nChange the layout\nRun all the tests")))); n != 0 {
		t.Fatalf("even paragraph group: %d balanced columns, want 0", n)
	}
	if n := len(balancedColumns(centeredBlocks(parseMD(longItem)))); n != 0 {
		t.Fatalf("single line: %d balanced columns, want 0", n)
	}
	// The overlong line keeps its place between the others.
	o := centeredBlocks(parseMD("Read the code first\n" + longItem + "\nRun all the tests"))
	box, ok := o.(*fyne.Container)
	if !ok || len(box.Objects) != 3 {
		t.Fatalf("want 3 parts (before, balanced line, after), got %T", o)
	}
	if _, ok := box.Objects[1].(*fyne.Container).Layout.(fitCenter); !ok {
		t.Fatal("the middle part is the balanced line")
	}
}

// The markdown parser splits a paragraph at **bold**, `code` and links;
// those pieces are one line, not a group to balance.
func TestCenteredParagraphStyledRunsAreOneLine(t *testing.T) {
	test.NewTempApp(t)
	styled := strings.Repeat("The first paragraph has **bold** words, `code` and a [link](https://example.com) to stream. ", 3)
	if n := len(balancedColumns(centeredBlocks(parseMD(styled)))); n != 0 {
		t.Fatalf("one styled paragraph: %d balanced columns, want 0", n)
	}
	o := centeredBlocks(parseMD("Read the code first\n" + styled + "\nRun all the tests"))
	box, ok := o.(*fyne.Container)
	if !ok || len(box.Objects) != 3 {
		t.Fatalf("want 3 parts (before, balanced styled line, after), got %T", o)
	}
	f, ok := box.Objects[1].(*fyne.Container)
	if !ok {
		t.Fatal("the middle part is the balanced line")
	}
	if got := f.Objects[0].(*widget.RichText).String(); !strings.Contains(got, "bold") || !strings.Contains(got, "link") || strings.Contains(got, "Run all") {
		t.Fatalf("the balanced column holds the whole styled line and nothing else: %q", got)
	}
}

func TestPlanQuestionsBalanceOverlong(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		qs := []planQ{
			{ID: 1, Text: "Rebuild now? Yes or no?", Kind: qYesNo, Recommended: -1},
			{ID: 2, Text: longItem + "?", Kind: qYesNo, Recommended: -1},
			{ID: 3, Text: "Push too? Yes or no?", Kind: qYesNo, Recommended: -1},
		}
		h.d.center = true
		if n := len(balancedColumns(h.d.chat.planQuestionForm(qs, map[int]planAnswer{}))); n != 1 {
			t.Fatalf("centering on: %d balanced questions, want 1", n)
		}
		h.d.center = false
		if n := len(balancedColumns(h.d.chat.planQuestionForm(qs, map[int]planAnswer{}))); n != 0 {
			t.Fatalf("centering off: %d balanced questions, want 0", n)
		}
	})
}
