package main

import (
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Centered columns that scale with the chat (user 2026-11-22). Every width
// is a share of the width the chat has right now, worked out again on each
// layout, so resizing the window or a pane re-wraps at once:
//   - in a group (one paragraph block, one list, the plan card's
//     questions, the queued messages) a line more than overlongRatio times
//     as wide as the widest other line doesn't stretch the column; it wraps
//     into as few lines as the group's width allows (never narrower than
//     balanceFloorShare of the chat) and those lines are evened out;
//   - a list wider than the chat is capped at listCapShare of the chat and
//     centered, instead of filling it.
const (
	overlongRatio     = 1.5
	balanceFloorShare = 0.35
	listCapShare      = 0.75
	// balanceSlack is extra width so the widget's own wrap, which measures
	// styled runs, doesn't need one more line than the plain-text estimate.
	balanceSlack = 8
)

// wrapText is a line's words, measured once.
type wrapText struct {
	ws      []float32
	sp      float32
	longest float32
	total   float32
}

func measureWords(text string, size float32, style fyne.TextStyle) wrapText {
	words := strings.Fields(text)
	w := wrapText{ws: make([]float32, len(words)), sp: fyne.MeasureText(" ", size, style).Width}
	for i, word := range words {
		w.ws[i] = fyne.MeasureText(word, size, style).Width
		w.longest = max(w.longest, w.ws[i])
		w.total += w.ws[i]
		if i > 0 {
			w.total += w.sp
		}
	}
	return w
}

// lines counts the lines a greedy word wrap at width takes.
func (w wrapText) lines(width float32) int {
	n, x := 1, float32(0)
	for i, ww := range w.ws {
		if i > 0 && x+w.sp+ww > width {
			n++
			x = ww
			continue
		}
		if i > 0 {
			x += w.sp
		}
		x += ww
	}
	return n
}

// fitWidth is the narrowest width that wraps the line into n lines or fewer.
func (w wrapText) fitWidth(n int) float32 {
	lo, hi := w.longest, max(w.total, w.longest)
	for hi-lo > 0.5 {
		mid := (lo + hi) / 2
		if w.lines(mid) <= n {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// balanced is the width the line wraps at: as few lines as target allows,
// evened out, and never narrower than floor (with fewer lines when evening
// out would go below it; at floor itself when even two lines would). 0 when
// the line fits on one line (or is a single word).
func (w wrapText) balanced(target, floor float32) float32 {
	if len(w.ws) < 2 {
		return 0
	}
	target = max(target, floor, w.longest)
	n := w.lines(target)
	if n < 2 {
		return 0
	}
	for ; n >= 2; n-- {
		if b := w.fitWidth(n); b >= floor {
			return b
		}
	}
	return floor
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

// colLayout puts its one object in a column whose width width(avail) works
// out from the width available, centered when center is set. The last
// answer is kept, so an unchanged width costs nothing.
type colLayout struct {
	width  func(avail float32) float32
	center bool
	avail  float32
	w      float32
	// onWidth, when set, runs when the column's laid-out width changes:
	// wrapped labels report their new height only after a layout at the
	// new width, so their container needs one more pass.
	onWidth func()
	laidW   float32
}

// stackBlocks stacks message blocks (columns and wrapped text) top to bottom.
// A column's wrapped text reports its real height only after the column is
// laid out at its width, so a plain VBox placed the next block by the height
// from before the wrap and drew it over the column's last lines; settledVBox
// lays out again when a height changed.
func stackBlocks(objs ...fyne.CanvasObject) *fyne.Container {
	return container.New(settledVBox{}, objs...)
}

func newColumn(center bool, width func(avail float32) float32, o fyne.CanvasObject) *fyne.Container {
	return fyne.NewContainerWithLayout(&colLayout{width: width, center: center, avail: -1}, o)
}

func (l *colLayout) MinSize(objs []fyne.CanvasObject) fyne.Size { return objs[0].MinSize() }

func (l *colLayout) colWidth(avail float32) float32 {
	if avail != l.avail {
		l.avail, l.w = avail, l.width(avail)
	}
	return l.w
}

func (l *colLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := max(min(l.colWidth(size.Width), size.Width), min(objs[0].MinSize().Width, size.Width))
	objs[0].Resize(fyne.NewSize(w, size.Height))
	if w != l.laidW {
		l.laidW = w
		if l.onWidth != nil {
			l.onWidth()
		}
	}
	x := float32(0)
	if l.center {
		x = (size.Width - w) / 2
	}
	objs[0].Move(fyne.NewPos(x, 0))
}

// groupWidth is the column width for a group of lines: widths are the
// lines' natural widths, extra is what each line adds around its text
// (markers, padding, buttons). An overlong line wraps balanced (never
// narrower than balanceFloorShare of avail); a group wider than avail is
// capped at listCapShare of it.
func groupWidth(avail float32, texts []wrapText, extra float32) float32 {
	widths := make([]float32, len(texts))
	natural := float32(0)
	for i, t := range texts {
		widths[i] = t.total
		natural = max(natural, t.total+extra)
	}
	col := natural
	if i, other, ok := overlongLine(widths); ok {
		if b := texts[i].balanced(other, balanceFloorShare*avail-extra); b > 0 {
			col = max(b+balanceSlack, other) + extra
		}
	}
	if col > avail {
		col = listCapShare * avail
	}
	return col
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

// listColumn wraps a centered list in a column that scales with the chat.
func listColumn(l *widget.ListSegment, o fyne.CanvasObject) *fyne.Container {
	size := msgTextSize()
	const indent, pad = 28, 24
	var texts []wrapText
	nested := float32(0)
	for _, item := range l.Items {
		if nl, ok := item.(*widget.ListSegment); ok {
			nested = max(nested, listNaturalWidth(nl, 1))
			continue
		}
		texts = append(texts, measureWords(segText(item), size, fyne.TextStyle{}))
	}
	return newColumn(true, func(avail float32) float32 {
		w := groupWidth(avail, texts, indent+pad)
		if nested > w && nested <= avail {
			w = nested
		}
		return w
	}, o)
}

// paragraphLines splits a centered run of blocks into lines: a
// ParagraphSegment, or the inline text and link segments up to the next
// non-inline one (the markdown parser splits a paragraph at **bold**,
// `code` and links). ok is false for runs with other blocks (code blocks,
// images, rules), which are left alone.
func paragraphLines(run []widget.RichTextSegment) (bounds [][2]int, texts []string, ok bool) {
	from := 0
	var b strings.Builder
	for i, seg := range run {
		switch seg.(type) {
		case *widget.ParagraphSegment, *widget.TextSegment, *widget.HyperlinkSegment:
		default:
			return nil, nil, false
		}
		b.WriteString(segText(seg))
		if seg.Inline() && i < len(run)-1 {
			continue
		}
		bounds = append(bounds, [2]int{from, i + 1})
		texts = append(texts, b.String())
		from, b = i+1, strings.Builder{}
	}
	return bounds, texts, true
}

// balancedParagraph finds the overlong line in a centered run of blocks:
// its segments run[from:to] and a column width function for it; ok is
// false when nothing needs balancing.
func balancedParagraph(run []widget.RichTextSegment) (from, to int, width func(avail float32) float32, ok bool) {
	bounds, texts, ok := paragraphLines(run)
	if !ok {
		return 0, 0, nil, false
	}
	size := msgTextSize()
	widths := make([]float32, len(texts))
	words := make([]wrapText, len(texts))
	for i, t := range texts {
		words[i] = measureWords(t, size, fyne.TextStyle{})
		widths[i] = words[i].total
	}
	i, other, ok := overlongLine(widths)
	if !ok {
		return 0, 0, nil, false
	}
	pad := 2 * msgTheme{}.Size(theme.SizeNameInnerPadding)
	long := words[i]
	return bounds[i][0], bounds[i][1], func(avail float32) float32 {
		b := long.balanced(other, balanceFloorShare*avail-pad)
		if b == 0 {
			return avail
		}
		return b + balanceSlack + pad
	}, true
}

// letteredLine: a drawn line that starts like a lettered list item.
var letteredLine = regexp.MustCompile(`^\s*[a-hA-H][.)]\s+`)

// letteredList lays out lettered items as a real list (rule f5c0dd9, as
// Quill Android 0.9.24 does): fyne's list with the letters as written
// ("a.", "b)") as markers (ListSegment.Markers, a Quill patch in
// third_party/fyne), so items get list spacing and a hanging indent like a
// numbered list. With center the list is a centered column that scales with
// the chat (listColumn); otherwise it is left-aligned. segs are the items'
// segments (bounds index lines within the message's run, base is where
// segs start).
func letteredList(segs []widget.RichTextSegment, bounds [][2]int, base int, lines []string, center bool) fyne.CanvasObject {
	l := &widget.ListSegment{}
	for k, b := range bounds {
		line := segs[b[0]-base : b[1]-base]
		var item []widget.RichTextSegment
		if p, ok := line[0].(*widget.ParagraphSegment); ok && len(line) == 1 {
			item = p.Texts
		} else {
			item = line
		}
		// The paragraph's closing (empty, block) segment ends the item.
		for len(item) > 0 {
			if t, ok := item[len(item)-1].(*widget.TextSegment); ok && !t.Inline() && strings.TrimSpace(t.Text) == "" {
				item = item[:len(item)-1]
				continue
			}
			break
		}
		lead := letteredLine.FindString(lines[k])
		item = dropRunes(item, len([]rune(lead)))
		alignSegments(item, fyne.TextAlignLeading)
		l.Items = append(l.Items, &widget.ParagraphSegment{Texts: item})
		l.Markers = append(l.Markers, strings.TrimSpace(lead))
	}
	t := widget.NewRichText(l)
	t.Wrapping = fyne.TextWrapWord
	if center {
		return listColumn(l, t)
	}
	return t
}

// dropRunes removes the first n characters of the leading text segments.
func dropRunes(segs []widget.RichTextSegment, n int) []widget.RichTextSegment {
	for len(segs) > 0 && n > 0 {
		t, ok := segs[0].(*widget.TextSegment)
		if !ok {
			break
		}
		r := []rune(t.Text)
		if len(r) <= n && t.Inline() {
			n -= len(r)
			segs = segs[1:]
			continue
		}
		t.Text = strings.TrimLeft(string(r[min(n, len(r)):]), " ")
		n = 0
	}
	if len(segs) > 0 {
		if t, ok := segs[0].(*widget.TextSegment); ok {
			t.Text = strings.TrimLeft(t.Text, " ")
		}
	}
	return segs
}
