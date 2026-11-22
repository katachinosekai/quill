package main

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Balanced wrapping for centered text (user 2026-11-22): in a group (one
// paragraph block, one list, the plan card's questions) a line more than
// overlongRatio times as wide as the widest other line doesn't stretch the
// column; it wraps into as few lines as the group's width allows, and those
// lines are made about equally long (no one-word tail).
const (
	overlongRatio = 1.5
	// balanceFloorChars keeps a long line from being squeezed into a very
	// narrow column next to a short line such as "Done.".
	balanceFloorChars = 40
	// balanceSlack is extra width so the widget's own wrap, which measures
	// styled runs, doesn't need one more line than the plain-text estimate.
	balanceSlack = 8
)

// greedyLines counts the lines a greedy word wrap of words (widths ws,
// space sp) takes at width.
func greedyLines(ws []float32, sp, width float32) int {
	n, x := 1, float32(0)
	for i, w := range ws {
		if i > 0 && x+sp+w > width {
			n++
			x = w
			continue
		}
		if i > 0 {
			x += sp
		}
		x += w
	}
	return n
}

// balancedWidth is the narrowest text width that wraps text into the same
// number of lines as wrapping it at target does, or 0 when the text fits
// on one line at target (or is a single word).
func balancedWidth(text string, target, size float32, style fyne.TextStyle) float32 {
	words := strings.Fields(text)
	if len(words) < 2 {
		return 0
	}
	ws := make([]float32, len(words))
	var longest float32
	for i, w := range words {
		ws[i] = fyne.MeasureText(w, size, style).Width
		longest = max(longest, ws[i])
	}
	sp := fyne.MeasureText(" ", size, style).Width
	target = max(target, longest, fyne.MeasureText(strings.Repeat("n", balanceFloorChars), size, style).Width)
	n := greedyLines(ws, sp, target)
	if n < 2 {
		return 0
	}
	lo, hi := longest, target
	for hi-lo > 0.5 {
		mid := (lo + hi) / 2
		if greedyLines(ws, sp, mid) <= n {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// overlongLine finds the one line in widths that is more than overlongRatio
// times as wide as the widest other line; ok is false when there is none
// (fewer than two lines, or no such line).
func overlongLine(widths []float32) (idx int, widestOther float32, ok bool) {
	if len(widths) < 2 {
		return 0, 0, false
	}
	for i, w := range widths {
		if w > widths[idx] {
			idx = i
		}
	}
	for i, w := range widths {
		if i != idx {
			widestOther = max(widestOther, w)
		}
	}
	if widestOther <= 0 || widths[idx] <= overlongRatio*widestOther {
		return 0, 0, false
	}
	return idx, widestOther, true
}

func msgTextSize() float32 { return msgTheme{}.Size(theme.SizeNameText) }

func segText(seg widget.RichTextSegment) string {
	if p, ok := seg.(*widget.ParagraphSegment); ok {
		var b strings.Builder
		for _, t := range p.Texts {
			b.WriteString(t.Textual())
		}
		return b.String()
	}
	return seg.Textual()
}

// balancedListWidth is the column width for a centered list: its natural
// width, or narrower when one top-level item is overlong, so that item
// wraps into balanced lines.
func balancedListWidth(l *widget.ListSegment) float32 {
	natural := listNaturalWidth(l, 0)
	size := msgTextSize()
	const indent, pad = 28, 24
	var widths []float32
	var texts []string
	for _, item := range l.Items {
		if nl, ok := item.(*widget.ListSegment); ok {
			widths = append(widths, listNaturalWidth(nl, 1)-indent-pad)
			texts = append(texts, "")
			continue
		}
		t := segText(item)
		texts = append(texts, t)
		widths = append(widths, fyne.MeasureText(t, size, fyne.TextStyle{}).Width)
	}
	i, other, ok := overlongLine(widths)
	if !ok || texts[i] == "" {
		return natural
	}
	b := balancedWidth(texts[i], other, size, fyne.TextStyle{})
	if b == 0 {
		return natural
	}
	return min(natural, indent+max(b+balanceSlack, other)+pad)
}

// balancedParagraph finds the overlong line in a centered run of blocks and
// returns its segments run[from:to] and the column width it should wrap in;
// ok is false when nothing needs balancing. A line is a ParagraphSegment,
// or the inline text and link segments up to the next non-inline one (the
// markdown parser splits a paragraph at **bold**, `code` and links).
func balancedParagraph(run []widget.RichTextSegment) (from, to int, width float32, ok bool) {
	type line struct {
		from, to int
		text     string
	}
	var lines []line
	cur := line{}
	var b strings.Builder
	for i, seg := range run {
		switch seg.(type) {
		case *widget.ParagraphSegment, *widget.TextSegment, *widget.HyperlinkSegment:
		default:
			return 0, 0, 0, false // code blocks, images, rules: leave the run alone
		}
		b.WriteString(segText(seg))
		if seg.Inline() && i < len(run)-1 {
			continue
		}
		cur.to, cur.text = i+1, b.String()
		lines = append(lines, cur)
		cur, b = line{from: i + 1}, strings.Builder{}
	}
	size := msgTextSize()
	widths := make([]float32, len(lines))
	for i, l := range lines {
		widths[i] = fyne.MeasureText(l.text, size, fyne.TextStyle{}).Width
	}
	i, other, ok := overlongLine(widths)
	if !ok {
		return 0, 0, 0, false
	}
	bw := balancedWidth(lines[i].text, other, size, fyne.TextStyle{})
	if bw == 0 {
		return 0, 0, 0, false
	}
	pad := 2 * msgTheme{}.Size(theme.SizeNameInnerPadding)
	return lines[i].from, lines[i].to, bw + balanceSlack + pad, true
}

// balancedLabel finds the overlong label in a group of labels (the plan
// card's questions) and the width it should wrap in; idx is -1 when
// nothing needs balancing.
func balancedLabel(texts []string, size float32) (idx int, width float32) {
	widths := make([]float32, len(texts))
	for i, t := range texts {
		widths[i] = fyne.MeasureText(t, size, fyne.TextStyle{}).Width
	}
	i, other, ok := overlongLine(widths)
	if !ok {
		return -1, 0
	}
	b := balancedWidth(texts[i], other, size, fyne.TextStyle{})
	if b == 0 {
		return -1, 0
	}
	return i, max(b+balanceSlack, other) + 2*theme.Size(theme.SizeNameInnerPadding)
}
