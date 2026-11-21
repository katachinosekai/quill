package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/protocol"
)

const (
	chatMaxWidth  = 760
	historyWindow = 40
	historyBatch  = 60
	loadEarlierAt = 400
	maxResultLen  = 12000
	maxDiffLines  = 2000
	diffPreview   = 400
)

type itemView struct {
	holder   *fyne.Container
	stream   *widget.Label
	expanded bool
	full     bool
	height   float32
	sig      string
}

func itemSig(it *chat.Item) string {
	return fmt.Sprintf("%d|%d|%t|%t|%t|%d|%d|%d|%t|%d", it.Kind, len(it.Text), it.Streaming, it.HasResult, it.ResultError, len(it.Result), len(it.Input), it.PermState, it.InPlan, len(it.Diff))
}

type chatView struct {
	d             *desk
	s             *deskSession
	box           *fyne.Container
	scroll        *container.Scroll
	views         map[int]*itemView
	shownFrom     int
	placeholder   bool
	root          fyne.CanvasObject
	col           *fyne.Container
	follow        bool
	autoScrolling bool
	gliding       bool
	queueBox      *fyne.Container
	dismissed     map[*chat.Item]bool
	stickTimer    *time.Timer
	qcards        map[*chat.Item]fyne.CanvasObject
	moreObj       fyne.CanvasObject
	lastY         float32
	newBelow      bool
	jump          *fyne.Container
	glow          *canvas.Rectangle
	glowAnim      *fyne.Animation
	glide         *fyne.Animation
	live          *widget.Label
	stepOpen      map[*chat.Item]bool
	frame         *fyne.Container
	inner         *fyne.Container
	steps         bool
	cache         map[*deskSession]*sessView
	order         []*deskSession
	sel           *selLayer
}

type sessView struct {
	box         *fyne.Container
	views       map[int]*itemView
	shownFrom   int
	placeholder bool
	conv        *chat.Conversation
	scrollY     float32
	center      bool
	follow      bool
	count       int
	more        fyne.CanvasObject
}

const cachedViews = 8

func newChatView(d *desk) *chatView {
	return newChatViewWith(d, d.chatWidth, d.sidebarWidth)
}

func newChatViewWith(d *desk, width, shift func() float32) *chatView {
	v := &chatView{d: d, views: map[int]*itemView{}}
	v.box = chatBox()
	v.cache = map[*deskSession]*sessView{}
	v.sel = newSelLayer(v)
	v.inner = container.New(shownPad{10, 10, 14, 14}, v.box, v.sel)
	v.live = widget.NewLabel(" ")
	v.live.TextStyle.Italic = true
	v.live.Truncation = fyne.TextTruncateEllipsis
	if d.app.Preferences().Bool("centerStatus") {
		v.live.Alignment = fyne.TextAlignCenter
	}
	v.live.Importance = widget.MediumImportance
	v.col = v.inner
	v.scroll = container.NewVScroll(v.inner)
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	border.StrokeWidth = 1
	border.CornerRadius = 16
	inside := container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), v.scroll)
	v.frame = windowCentered(width, shift, container.NewStack(border, inside))
	liveRow := windowCentered(width, shift, container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), v.live))
	v.follow = true
	v.scroll.OnScrolled = func(fyne.Position) {
		if v.autoScrolling {
			return
		}
		y := v.scroll.Offset.Y
		switch {
		case v.atBottom():
			v.follow = true
			v.newBelow = false
		case y < v.lastY-2:
			v.follow = false
		}
		v.lastY = y
		v.syncJump()
		if y < loadEarlierAt && v.shownFrom > 0 && !v.follow {
			v.loadEarlier()
		}
	}
	v.jump = v.newJump()
	overlay := container.NewVBox(layout.NewSpacer(), windowCentered(width, shift, container.NewCenter(v.jump)), padTop(18))
	framed := container.New(layout.NewCustomPaddedLayout(8, 2, 4, 4), container.NewPadded(v.frame))
	v.queueBox = container.NewVBox()
	queueRow := windowCentered(width, shift, container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), v.queueBox))
	v.root = container.NewBorder(nil, container.NewVBox(queueRow, liveRow), nil, nil, container.NewStack(framed, overlay))
	return v
}

func (v *chatView) relayout() {
	relayout(v.frame)
	v.reflow()
}

func (v *chatView) stash() {
	if v.s == nil {
		return
	}
	sv := &sessView{more: v.moreObj, box: v.box, views: v.views, shownFrom: v.shownFrom, placeholder: v.placeholder, conv: v.s.conv, scrollY: v.scroll.Offset.Y, center: v.d.center, follow: v.follow, count: len(v.s.conv.Items)}
	if _, ok := v.cache[v.s]; !ok {
		v.order = append(v.order, v.s)
	}
	v.cache[v.s] = sv
	for len(v.order) > cachedViews {
		delete(v.cache, v.order[0])
		v.order = v.order[1:]
	}
}

func (v *chatView) forget(s *deskSession) {
	if s == v.s {
		return
	}
	delete(v.cache, s)
}

func (v *chatView) setBox(b *fyne.Container) {
	v.box = b
	if v.sel != nil {
		v.sel.clear()
	}
	keep := map[fyne.CanvasObject]bool{b: true}
	for _, sv := range v.cache {
		keep[sv.box] = true
	}
	objs := v.inner.Objects[:0]
	found := false
	for _, o := range v.inner.Objects {
		if !keep[o] {
			continue
		}
		if o == b {
			found = true
			o.Show()
		} else {
			o.Hide()
		}
		objs = append(objs, o)
	}
	if !found {
		objs = append(objs, b)
		b.Show()
	}
	if v.sel != nil {
		objs = append(objs, v.sel)
	}
	v.inner.Objects = objs
	v.relayoutInner()
}

type shownPad struct{ top, bottom, left, right float32 }

func (l shownPad) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var s fyne.Size
	for _, o := range objs {
		if o.Visible() {
			s = s.Max(o.MinSize())
		}
	}
	return s.Add(fyne.NewSize(l.left+l.right, l.top+l.bottom))
}

func (l shownPad) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	inner := fyne.NewSize(size.Width-l.left-l.right, size.Height-l.top-l.bottom)
	for _, o := range objs {
		if !o.Visible() {
			continue
		}
		o.Move(fyne.NewPos(l.left, l.top))
		o.Resize(inner)
	}
}

func (v *chatView) setLive(text string) {
	if v.live == nil || v.steps {
		return
	}
	if text == "" {
		text = " "
	}
	if v.live.Text != text {
		v.live.SetText(text)
	}
}

func msgText(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewThemeOverride(o, msgTheme{})
}

func (v *chatView) show(s *deskSession) {
	if s != v.s {
		v.stash()
	}
	if s != nil {
		if sv, ok := v.cache[s]; ok && s != v.s && sv.conv == s.conv && sv.center == v.d.center {
			v.s = s
			v.views, v.shownFrom, v.placeholder, v.moreObj = sv.views, sv.shownFrom, sv.placeholder, sv.more
			v.setBox(sv.box)
			v.catchUp(sv.count)
			if sv.follow {
				v.scrollBottom()
				return
			}
			v.scroll.Offset = fyne.NewPos(0, sv.scrollY)
			v.reflow()
			v.lastY = v.scroll.Offset.Y
			v.follow = false
			v.newBelow = len(s.conv.Items) > sv.count
			v.syncJump()
			return
		}
		delete(v.cache, s)
	}
	v.s = s
	v.views = map[int]*itemView{}
	v.setBox(chatBox())
	if s == nil {
		v.box.Add(v.emptyState())
		relayout(v.box)
		return
	}
	items := s.conv.Items
	v.shownFrom = 0
	if len(items) > historyWindow {
		v.shownFrom = len(items) - historyWindow
	}
	v.rebuild()
	v.scrollBottom()
}

func (v *chatView) catchUp(seen int) {
	items := v.s.conv.Items
	from := max(v.shownFrom, min(seen, len(items))-20)
	var changed []*chat.Item
	for _, it := range items[from:] {
		if iv, ok := v.views[it.Seq]; !ok || iv.sig != itemSig(it) {
			changed = append(changed, it)
		}
	}
	if len(changed) == 0 {
		return
	}
	follow := v.follow
	v.follow = false
	v.update(changed)
	v.follow = follow
}

func (v *chatView) moreRow(n int) fyne.CanvasObject {
	more := widget.NewButton(fmt.Sprintf("%d earlier messages (scroll up to load)", n), v.loadEarlier)
	more.Importance = widget.LowImportance
	return more
}

func (v *chatView) loadEarlier() {
	if v.s == nil || v.shownFrom == 0 {
		return
	}
	items := v.s.conv.Items
	from := max(0, v.shownFrom-historyBatch)
	var objs []fyne.CanvasObject
	var more fyne.CanvasObject
	if from > 0 {
		more = v.moreRow(from)
		objs = append(objs, more)
	}
	for _, it := range items[from:v.shownFrom] {
		objs = append(objs, v.viewFor(it).holder)
	}
	rest := v.box.Objects
	if len(rest) > 0 && v.moreObj != nil && rest[0] == v.moreObj {
		rest = rest[1:]
	}
	before := v.scroll.Content.MinSize().Height
	v.box.Objects = append(objs, rest...)
	v.moreObj = more
	v.shownFrom = from
	relayout(v.box)
	v.reflow()
	v.reflow()
	after := v.scroll.Content.MinSize().Height
	v.autoScrolling = true
	v.scroll.Offset = fyne.NewPos(0, v.scroll.Offset.Y+after-before)
	v.reflow()
	v.lastY = v.scroll.Offset.Y
	v.autoScrolling = false
}

func (v *chatView) rebuild() {
	v.box.RemoveAll()
	v.views = map[int]*itemView{}
	items := v.s.conv.Items
	v.moreObj = nil
	if v.shownFrom > 0 {
		v.moreObj = v.moreRow(v.shownFrom)
		v.box.Add(v.moreObj)
	}
	v.placeholder = len(items) == 0
	if v.placeholder {
		v.box.Add(v.emptySession())
	}
	for _, it := range items[v.shownFrom:] {
		v.box.Add(v.viewFor(it).holder)
	}
	relayout(v.box)
}

func (v *chatView) emptyState() fyne.CanvasObject {
	t := widget.NewLabel("Pick a session on the left, or start a new one.")
	t.Alignment = fyne.TextAlignCenter
	return container.NewVBox(padTop(120), t)
}

func (v *chatView) emptySession() fyne.CanvasObject {
	t := widget.NewLabel("New session in " + tildePath(v.s.cwd) + "\nType a message below to start.")
	t.Alignment = fyne.TextAlignCenter
	return container.NewVBox(padTop(120), t)
}

func (v *chatView) atBottom() bool {
	content := v.scroll.Content.MinSize().Height
	return v.scroll.Offset.Y+v.scroll.Size().Height >= content-40
}

func (v *chatView) scrollBottom() {
	v.autoScrolling = true
	v.reflow()
	v.scroll.ScrollToBottom()
	v.lastY = v.scroll.Offset.Y
	v.autoScrolling = false
	v.follow = true
	v.newBelow = false
	v.syncJump()
}

func (v *chatView) glideBottom() {
	from := v.scroll.Offset.Y
	to := v.scroll.Content.MinSize().Height - v.scroll.Size().Height
	if !v.d.animate || to <= from {
		v.scrollBottom()
		return
	}
	v.follow = true
	v.syncJump()
	if v.glide != nil {
		v.glide.Stop()
	}
	v.gliding = true
	v.glide = fyne.NewAnimation(320*time.Millisecond, func(p float32) {
		target := v.scroll.Content.MinSize().Height - v.scroll.Size().Height
		v.autoScrolling = true
		v.scroll.Offset = fyne.NewPos(0, from+(target-from)*p)
		v.reflow()
		v.lastY = v.scroll.Offset.Y
		v.autoScrolling = false
		if p >= 1 {
			v.gliding = false
			v.scrollBottom()
		}
	})
	v.glide.Curve = fyne.AnimationEaseInOut
	v.glide.Start()
}

func (v *chatView) pinned() bool {
	content := v.scroll.Content.MinSize().Height
	return v.scroll.Offset.Y+v.scroll.Size().Height >= content-2
}

func (v *chatView) stick() {
	if v.follow && !v.gliding && v.s != nil && !v.steps && !v.pinned() {
		v.scrollBottom()
	}
}

func (v *chatView) followGuard(ctx context.Context) {
	t := time.NewTicker(700 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			v.d.do(func() {
				if v.follow && !v.d.hidden {
					v.stick()
				}
			})
		}
	}
}

func (v *chatView) stickSoon() {
	if v.stickTimer != nil || !v.follow {
		return
	}
	v.stickTimer = time.AfterFunc(150*time.Millisecond, func() {
		v.d.do(func() {
			v.stickTimer = nil
			v.stick()
		})
	})
}

func (v *chatView) keepUp() {
	if v.steps {
		return
	}
	if v.follow {
		v.scrollBottom()
		return
	}
	v.newBelow = true
	v.syncJump()
}

func (v *chatView) newJump() *fyne.Container {
	glow := canvas.NewRectangle(colorAlpha(colAccent, 0x50))
	glow.CornerRadius = 18
	btn := widget.NewButtonWithIcon("Latest", theme.MoveDownIcon(), func() { v.glideBottom() })
	btn.Importance = widget.HighImportance
	v.glow = glow
	v.glowAnim = fyne.NewAnimation(1100*time.Millisecond, func(p float32) {
		glow.FillColor = colorAlpha(colAccent, uint8(0x30+p*0x90))
		glow.Refresh()
	})
	v.glowAnim.AutoReverse = true
	v.glowAnim.RepeatCount = fyne.AnimationRepeatForever
	box := container.NewStack(glow, container.New(layout.NewCustomPaddedLayout(4, 4, 6, 6), btn))
	box.Hide()
	return box
}

func (v *chatView) syncJump() {
	if v.jump == nil {
		return
	}
	show := !v.follow && v.s != nil
	if show == v.jump.Visible() {
		return
	}
	if show {
		v.jump.Show()
		if v.d.animate {
			v.glowAnim.Start()
		}
	} else {
		v.glowAnim.Stop()
		v.jump.Hide()
	}
}

func (v *chatView) update(items []*chat.Item) {
	if v.s == nil || len(items) == 0 {
		return
	}
	added := false
	heads := map[*chat.Item]bool{}
	firstSeq := 0
	if v.shownFrom > 0 && v.shownFrom < len(v.s.conv.Items) {
		firstSeq = v.s.conv.Items[v.shownFrom].Seq
	}
	for _, it := range items {
		if it.Seq < firstSeq {
			continue
		}
		if iv, ok := v.views[it.Seq]; ok {
			v.refreshItem(it, iv)
		} else {
			if v.placeholder {
				v.box.RemoveAll()
				v.placeholder = false
			}
			v.box.Add(v.viewFor(it).holder)
			added = true
		}
		if it.Kind == chat.Tool && !v.steps {
			if h := v.runHead(it); h != it {
				heads[h] = true
			}
		}
	}
	for h := range heads {
		if iv, ok := v.views[h.Seq]; ok {
			v.refreshItem(h, iv)
		}
	}
	if added {
		relayout(v.box)
	}
	v.keepUp()
	v.stickSoon()
}

func (v *chatView) viewFor(it *chat.Item) *itemView {
	iv := &itemView{holder: container.NewStack()}
	if it.Kind == chat.Tool || it.Kind == chat.Thinking {
		iv.expanded = true
	}
	v.views[it.Seq] = iv
	v.refreshItem(it, iv)
	return iv
}

func (v *chatView) refreshItem(it *chat.Item, iv *itemView) {
	iv.sig = itemSig(it)
	if it.Kind == chat.Assistant && it.Streaming {
		if iv.stream == nil {
			iv.stream = widget.NewLabel(it.Text)
			iv.stream.Wrapping = fyne.TextWrapWord
			iv.stream.Alignment = v.align()
			iv.holder.Objects = []fyne.CanvasObject{spaced(it.Kind, msgText(iv.stream))}
			iv.holder.Refresh()
		} else {
			iv.stream.SetText(it.Text)
		}
	} else {
		iv.stream = nil
		iv.holder.Objects = []fyne.CanvasObject{spaced(it.Kind, v.render(it, iv))}
		iv.holder.Refresh()
	}
	if h := iv.holder.MinSize().Height; h != iv.height {
		iv.height = h
		relayout(v.box)
		if v.follow && !v.steps {
			v.scrollBottom()
		}
	}
}

func (v *chatView) align() fyne.TextAlign {
	if v.d.center {
		return fyne.TextAlignCenter
	}
	return fyne.TextAlignLeading
}

func (v *chatView) muted(s string, size float32) *canvas.Text {
	t := mutedText(s, size)
	t.Alignment = v.align()
	return t
}

func (v *chatView) centered(o fyne.CanvasObject) fyne.CanvasObject {
	if v.d.center {
		return container.NewCenter(o)
	}
	return o
}

func (v *chatView) render(it *chat.Item, iv *itemView) fyne.CanvasObject {
	switch it.Kind {
	case chat.User:
		if strings.Contains(it.Text, "```") {
			return bgPad(colBubble, 14, 4, 10, msgText(v.markdown(it.Text)))
		}
		l := widget.NewLabel(it.Text)
		l.Selectable = true
		l.Wrapping = fyne.TextWrapWord
		l.Alignment = v.align()
		return bgPad(colBubble, 14, 4, 10, msgText(l))
	case chat.Assistant:
		if it.InPlan {
			return container.NewWithoutLayout()
		}
		md := msgText(v.markdown(it.Text))
		if it.Streaming {
			return md
		}
		if p := v.mediaPreviews(it.Text); p != nil {
			return container.NewVBox(md, p)
		}
		return md
	case chat.Tool:
		if !v.steps {
			return v.stepRun(it)
		}
		return v.renderTool(it, iv)
	case chat.Permission:
		if it.PermState == chat.PermAutoAllowed {
			return container.NewWithoutLayout()
		}
		if it.PermState == chat.PermPlanAsked {
			l := widget.NewLabel(it.ToolName + ": " + permStateText(it.PermState))
			l.Importance = widget.LowImportance
			l.SizeName = theme.SizeNameCaptionText
			l.Alignment = v.align()
			l.Wrapping = fyne.TextWrapWord
			return l
		}
		return v.renderPermission(it)
	case chat.Plan:
		return v.renderPlan(it)
	case chat.Rotate:
		return v.renderRotate(it)
	case chat.Thinking:
		if strings.TrimSpace(it.Text) == "" {
			return container.NewWithoutLayout()
		}
		return msgText(v.markdown(it.Text))
	case chat.Notice:
		l := widget.NewLabel(it.Text)
		l.Wrapping = fyne.TextWrapWord
		l.Alignment = fyne.TextAlignCenter
		l.TextStyle.Italic = true
		l.Importance = widget.MediumImportance
		l.SizeName = theme.SizeNameCaptionText
		return l
	case chat.Error:
		l := widget.NewLabel(it.Text)
		l.Wrapping = fyne.TextWrapWord
		l.Importance = widget.DangerImportance
		l.Alignment = v.align()
		return bg(color.NRGBA{0x45, 0x22, 0x22, 0xff}, 8, l)
	}
	return widget.NewLabel(it.Text)
}

func (v *chatView) markdown(text string) fyne.CanvasObject {
	rt := widget.NewRichTextFromMarkdown(hardBreaks(text))
	rt.Wrapping = fyne.TextWrapWord
	for i, seg := range rt.Segments {
		if cb, ok := seg.(*widget.CodeBlockSegment); ok {
			rt.Segments[i] = &wrapCode{text: cb.Text}
		}
	}
	if v.d.center {
		return centeredBlocks(rt.Segments)
	}
	return rt
}

func centeredBlocks(segs []widget.RichTextSegment) fyne.CanvasObject {
	var parts []fyne.CanvasObject
	var run []widget.RichTextSegment
	flush := func() {
		if len(run) == 0 {
			return
		}
		alignSegments(run, fyne.TextAlignCenter)
		t := widget.NewRichText(run...)
		t.Wrapping = fyne.TextWrapWord
		parts = append(parts, t)
		run = nil
	}
	for _, seg := range segs {
		l, ok := seg.(*widget.ListSegment)
		if !ok {
			run = append(run, seg)
			continue
		}
		flush()
		t := widget.NewRichText(l)
		t.Wrapping = fyne.TextWrapWord
		parts = append(parts, container.New(fitCenter{natural: listNaturalWidth(l, 0)}, t))
	}
	flush()
	if len(parts) == 1 {
		return parts[0]
	}
	return container.NewVBox(parts...)
}

func listNaturalWidth(l *widget.ListSegment, depth int) float32 {
	size := msgTheme{}.Size(theme.SizeNameText)
	indent := float32(depth+1) * 28
	var w float32
	for _, item := range l.Items {
		switch it := item.(type) {
		case *widget.ListSegment:
			w = max(w, listNaturalWidth(it, depth+1))
		default:
			var b strings.Builder
			if p, ok := item.(*widget.ParagraphSegment); ok {
				for _, t := range p.Texts {
					if nl, ok := t.(*widget.ListSegment); ok {
						w = max(w, listNaturalWidth(nl, depth+1))
						continue
					}
					b.WriteString(t.Textual())
				}
			} else {
				b.WriteString(item.Textual())
			}
			w = max(w, indent+fyne.MeasureText(b.String(), size, fyne.TextStyle{}).Width)
		}
	}
	return w + 24
}

func alignSegments(segs []widget.RichTextSegment, a fyne.TextAlign) {
	for _, s := range segs {
		switch t := s.(type) {
		case *widget.TextSegment:
			t.Style.Alignment = a
		case *widget.HyperlinkSegment:
			t.Alignment = a
		case *widget.ParagraphSegment:
			alignSegments(t.Texts, a)
		case *widget.ListSegment:
			alignSegments(t.Items, a)
		}
	}
}

func toolHeader(it *chat.Item) string {
	name := it.ToolName
	if name == "" {
		name = "Tool"
	}
	s := name
	if it.Summary != "" {
		sum := it.Summary
		if len(it.Diff) > 0 && it.DiffPath != "" {
			sum = chat.ShortPath(it.DiffPath, "")
		}
		s += "  " + sum
	}
	if len(it.Diff) > 0 {
		a, r := chat.DiffStats(it.Diff)
		s += fmt.Sprintf("  (+%d -%d)", a, r)
	}
	switch {
	case !it.HasResult:
		s += "  ..."
	case it.ResultError:
		s += "  - failed"
	}
	return s
}

func (v *chatView) renderTool(it *chat.Item, iv *itemView) fyne.CanvasObject {
	icon := theme.NavigateNextIcon()
	if iv.expanded {
		icon = theme.MenuDropDownIcon()
	}
	head := foldHead(toolHeader(it), icon, v.d.center, func() {
		iv.expanded = !iv.expanded
		v.refreshItem(it, iv)
	})
	parts := []fyne.CanvasObject{head}
	if iv.expanded {
		parts = append(parts, v.toolBody(it, iv)...)
	}
	box := container.NewVBox(parts...)
	if !iv.expanded {
		return box
	}
	return bg(colCard, 8, box)
}

func (v *chatView) toolBody(it *chat.Item, iv *itemView) []fyne.CanvasObject {
	var parts []fyne.CanvasObject
	{
		if len(it.Diff) > 0 {
			parts = append(parts, v.diffView(it, iv))
		} else if in := toolInputText(it); in != "" {
			parts = append(parts, mono(in))
		}
		if it.HasResult && (len(it.Diff) == 0 || it.ResultError) {
			res := it.Result
			if len(res) > maxResultLen {
				res = res[:maxResultLen] + "\n... (truncated)"
			}
			if strings.TrimSpace(res) == "" {
				res = "(no output)"
			}
			if it.ResultError {
				r := mono(res)
				r.Importance = widget.DangerImportance
				parts = append(parts, r)
			} else {
				parts = append(parts, v.monoFold(res, it, iv))
			}
		}
	}
	return parts
}

func toolInputText(it *chat.Item) string {
	if len(it.Input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(it.Input, &m) == nil {
		if cmd, ok := m["command"].(string); ok && it.ToolName == "Bash" {
			return "$ " + cmd
		}
	}
	var buf bytes.Buffer
	if json.Indent(&buf, it.Input, "", "  ") != nil {
		return string(it.Input)
	}
	s := buf.String()
	if len(s) > maxResultLen {
		s = s[:maxResultLen] + "\n... (truncated)"
	}
	return s
}

const toolPreviewLines = 60

func (v *chatView) monoFold(s string, it *chat.Item, iv *itemView) fyne.CanvasObject {
	lines := strings.Count(s, "\n") + 1
	if iv.full || lines <= toolPreviewLines {
		return mono(s)
	}
	cut := s
	for i, n := 0, 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
			if n == toolPreviewLines {
				cut = s[:i]
				break
			}
		}
	}
	more := widget.NewButton(fmt.Sprintf("Show all %d lines", lines), func() {
		iv.full = true
		v.refreshItem(it, iv)
	})
	more.Importance = widget.LowImportance
	return container.NewVBox(mono(cut), container.NewHBox(more))
}

func mono(s string) *widget.Label {
	l := widget.NewLabel(s)
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.Wrapping = fyne.TextWrapBreak
	l.SizeName = theme.SizeNameCaptionText
	return l
}

func (v *chatView) diffView(it *chat.Item, iv *itemView) fyne.CanvasObject {
	lines := it.Diff
	limit := diffPreview
	if iv.full {
		limit = maxDiffLines
	}
	cut := 0
	if len(lines) > limit {
		cut = len(lines) - limit
		lines = lines[:limit]
	}
	grid := widget.NewTextGrid()
	rows := make([]widget.TextGridRow, 0, len(lines)+1)
	addStyle := &widget.CustomTextGridStyle{FGColor: colAddFG, BGColor: colAdd}
	delStyle := &widget.CustomTextGridStyle{FGColor: colDelFG, BGColor: colDel}
	for _, l := range lines {
		text := string(l.Op) + " " + strings.ReplaceAll(l.Text, "\t", "    ")
		if r := []rune(text); len(r) > 400 {
			text = string(r[:400]) + "..."
		}
		row := widget.TextGridRow{}
		for _, r := range text {
			row.Cells = append(row.Cells, widget.TextGridCell{Rune: r})
		}
		switch l.Op {
		case '+':
			row.Style = addStyle
		case '-':
			row.Style = delStyle
		}
		rows = append(rows, row)
	}
	grid.Rows = rows
	parts := []fyne.CanvasObject{}
	if it.DiffPath != "" {
		parts = append(parts, mutedText(it.DiffPath, fontMeta))
	}
	parts = append(parts, v.sideScroll(grid))
	if cut > 0 {
		more := widget.NewButton(fmt.Sprintf("Show %d more lines", cut), func() {
			iv.full = true
			v.refreshItem(it, iv)
		})
		more.Importance = widget.LowImportance
		parts = append(parts, more)
	}
	return container.NewVBox(parts...)
}

func (v *chatView) renderQuestions(it *chat.Item) fyne.CanvasObject {
	if it.PermState == chat.PermPending {
		if c, ok := v.qcards[it]; ok {
			return c
		}
		if v.qcards == nil {
			v.qcards = map[*chat.Item]fyne.CanvasObject{}
		}
		c := v.buildQuestions(it)
		v.qcards[it] = c
		return c
	}
	delete(v.qcards, it)
	return v.buildQuestions(it)
}

func (v *chatView) buildQuestions(it *chat.Item) fyne.CanvasObject {
	owner := v.s
	qs := protocol.ParseQuestions(it.Input)
	var parts []fyne.CanvasObject
	head := widget.NewLabel("Claude is asking")
	head.Alignment = v.align()
	head.TextStyle = fyne.TextStyle{Bold: true}
	parts = append(parts, head)
	if it.PermState != chat.PermPending {
		text := it.Text
		if text == "" {
			text = permStateText(it.PermState)
		}
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapWord
		l.Alignment = v.align()
		parts = append(parts, l)
		return questionCard(parts, false)
	}
	type pick struct {
		q      protocol.Question
		radio  *widget.RadioGroup
		checks *widget.CheckGroup
		other  *widget.Entry
	}
	var picks []pick
	for _, q := range qs {
		ql := widget.NewLabel(q.Question)
		ql.Wrapping = fyne.TextWrapWord
		ql.TextStyle = fyne.TextStyle{Bold: true}
		ql.Alignment = v.align()
		parts = append(parts, ql)
		var labels []string
		for _, o := range q.Options {
			labels = append(labels, o.Label)
		}
		pk := pick{q: q, other: widget.NewEntry()}
		pk.other.SetPlaceHolder("Other (type your own answer)")
		if q.MultiSelect {
			pk.checks = widget.NewCheckGroup(labels, nil)
			parts = append(parts, v.centered(pk.checks))
		} else {
			pk.radio = widget.NewRadioGroup(labels, nil)
			parts = append(parts, v.centered(pk.radio))
		}
		for _, o := range q.Options {
			if o.Description != "" {
				d := widget.NewLabel(o.Label + ": " + o.Description)
				d.Wrapping = fyne.TextWrapWord
				d.SizeName = theme.SizeNameCaptionText
				d.Importance = widget.LowImportance
				d.Alignment = v.align()
				parts = append(parts, d)
			}
		}
		parts = append(parts, pk.other, padTop(4))
		picks = append(picks, pk)
	}
	send := widget.NewButtonWithIcon("Send answers", theme.ConfirmIcon(), func() {
		answers := map[string]string{}
		for _, pk := range picks {
			var chosen []string
			if pk.radio != nil && pk.radio.Selected != "" {
				chosen = append(chosen, pk.radio.Selected)
			}
			if pk.checks != nil {
				chosen = append(chosen, pk.checks.Selected...)
			}
			if t := strings.TrimSpace(pk.other.Text); t != "" {
				chosen = append(chosen, t)
			}
			if len(chosen) > 0 {
				answers[pk.q.Question] = strings.Join(chosen, ", ")
			}
		}
		if len(answers) == 0 {
			return
		}
		v.d.answerQuestions(owner, it, answers)
	})
	ok := v.d.canAnswer(v.s)
	if !ok {
		send.Disable()
	}
	sendG := glowWrap(send, colIdle, ok)
	skip := glowButton("Skip", theme.CancelIcon(), colError, ok, func() { v.d.answer(owner, it, protocol.Deny) })
	parts = append(parts, v.buttonRow(sendG, skip))
	return questionCard(parts, true)
}

func questionCard(parts []fyne.CanvasObject, pending bool) fyne.CanvasObject {
	border := colAccent
	if !pending {
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 10
	r.StrokeColor = border
	r.StrokeWidth = 1.5
	return container.NewVBox(padTop(4), container.NewStack(r, container.NewPadded(container.NewVBox(parts...))), padTop(4))
}

func (v *chatView) renderPermission(it *chat.Item) fyne.CanvasObject {
	owner := v.s
	if it.ToolName == "AskUserQuestion" {
		return v.renderQuestions(it)
	}
	title := widget.NewLabel("Allow " + it.ToolName + "?")
	title.Alignment = v.align()
	title.TextStyle = fyne.TextStyle{Bold: true}
	parts := []fyne.CanvasObject{title}
	if it.Request != nil && it.Request.DecisionReason != "" {
		r := widget.NewLabel(it.Request.DecisionReason)
		r.Wrapping = fyne.TextWrapWord
		r.Alignment = v.align()
		parts = append(parts, r)
	}
	if it.Summary != "" {
		s := widget.NewLabel(it.Summary)
		s.Wrapping = fyne.TextWrapWord
		s.Alignment = v.align()
		parts = append(parts, s)
	}
	parts = append(parts, permDetail(v, it)...)
	if it.PermState == chat.PermPending {
		ok := v.d.canAnswer(v.s)
		allow := glowButton("Allow", theme.ConfirmIcon(), colIdle, ok, func() { v.d.answer(owner, it, protocol.Allow) })
		always := glowButton("Allow always (this session)", nil, colAccent, ok, func() { v.d.answer(owner, it, protocol.AllowAlways) })
		deny := glowButton("Deny", theme.CancelIcon(), colError, ok, func() { v.d.answer(owner, it, protocol.Deny) })
		parts = append(parts, v.buttonRow(allow, always, deny))
	} else {
		parts = append(parts, v.muted(permStateText(it.PermState), fontBody))
	}
	border := colAccent
	if it.PermState != chat.PermPending {
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 10
	r.StrokeColor = border
	r.StrokeWidth = 1.5
	return container.NewVBox(padTop(4), container.NewStack(r, container.NewPadded(container.NewVBox(parts...))), padTop(4))
}

func permStateText(s chat.PermState) string {
	switch s {
	case chat.PermAllowed:
		return "Allowed"
	case chat.PermAllowedAlways:
		return "Allowed for this session"
	case chat.PermDenied:
		return "Denied"
	case chat.PermCancelled:
		return "No longer needed"
	case chat.PermAutoAllowed:
		return "Approved with the plan"
	case chat.PermPlanAsked:
		return "Held: asked Claude for its full plan first"
	case chat.PermRetryAllowed:
		return "Allowed: you approved it on the blocked card"
	case chat.PermRuleAllowed:
		return "Allowed, and your rules were added"
	}
	return ""
}

func hardBreaks(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines)*2)
	fence := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		out = append(out, l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			continue
		}
		if fence || t == "" || i == len(lines)-1 {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if next == "" || structural(t) || structural(next) {
			continue
		}
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}

func structural(t string) bool {
	if strings.HasPrefix(t, "|") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
		return true
	}
	if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") {
		return true
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	return i > 0 && i < len(t) && (t[i] == '.' || t[i] == ')')
}

func (v *chatView) renderThinking(it *chat.Item, iv *itemView) fyne.CanvasObject {
	icon := theme.NavigateNextIcon()
	if iv.expanded {
		icon = theme.MenuDropDownIcon()
	}
	label := "Thought"
	if it.Streaming {
		label = "Thinking..."
	}
	head := foldHead(label, icon, v.d.center, func() {
		iv.expanded = !iv.expanded
		v.refreshItem(it, iv)
	})
	if !iv.expanded {
		return head
	}
	body := widget.NewLabel(it.Text)
	body.Wrapping = fyne.TextWrapWord
	body.TextStyle = fyne.TextStyle{Italic: true}
	body.Alignment = v.align()
	return bg(colCard, 8, container.NewVBox(head, body))
}

func permDetail(v *chatView, it *chat.Item) []fyne.CanvasObject {
	if it.PermState != chat.PermPending {
		return nil
	}
	if len(it.Diff) > 0 {
		return []fyne.CanvasObject{v.diffView(it, &itemView{})}
	}
	in := toolInputText(it)
	if in == "" {
		return nil
	}
	if len(in) > 1500 {
		in = in[:1500] + "\n..."
	}
	return []fyne.CanvasObject{mono(in)}
}

func spaced(k chat.Kind, obj fyne.CanvasObject) fyne.CanvasObject {
	top, bottom := float32(2), float32(2)
	switch k {
	case chat.User:
		top, bottom = 10, 2
	case chat.Assistant, chat.Thinking:
		top, bottom = 0, 0
	case chat.Tool:
		top, bottom = 0, 0
	case chat.Notice:
		top, bottom = 4, 4
	}
	return container.New(layout.NewCustomPaddedLayout(top, bottom, 0, 0), obj)
}

type wrapCode struct{ text string }

func (c *wrapCode) Inline() bool    { return false }
func (c *wrapCode) Textual() string { return c.text }
func (c *wrapCode) Visual() fyne.CanvasObject {
	l := widget.NewLabel(c.text)
	l.Selectable = true
	l.TextStyle = fyne.TextStyle{Monospace: true}
	l.Wrapping = fyne.TextWrapBreak
	l.SizeName = theme.SizeNameCaptionText
	return bgPad(colBubble, 8, 2, 6, l)
}
func (c *wrapCode) Update(o fyne.CanvasObject) {
	o.(*fyne.Container).Objects[1].(*fyne.Container).Objects[0].(*widget.Label).SetText(c.text)
}
func (c *wrapCode) Select(_, _ fyne.Position) {}
func (c *wrapCode) SelectedText() string      { return "" }
func (c *wrapCode) Unselect()                 {}

func foldHead(text string, icon fyne.Resource, center bool, onTap func()) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.TextStyle.Bold = true
	ic := container.NewCenter(sized(18, 18, widget.NewIcon(icon)))
	if center {
		return newTapRow(container.New(&flowLayout{gap: 2, center: true}, ic, l), onTap)
	}
	l.Wrapping = fyne.TextWrapWord
	return newTapRow(container.NewBorder(nil, nil, ic, nil, l), onTap)
}

func (v *chatView) stepLine(it *chat.Item) fyne.CanvasObject {
	if it.Kind == chat.Thinking && it.Text == "" && !it.Streaming {
		return container.NewWithoutLayout()
	}
	text := "✻ Thought"
	if it.Kind == chat.Tool {
		mark := "…"
		switch {
		case it.ResultError:
			mark = "✗"
		case it.HasResult:
			mark = "✓"
		}
		text = mark + "  " + toolHeader(it)
	}
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.Importance = widget.MediumImportance
	l.SizeName = theme.SizeNameCaptionText
	return newTapRow(l, func() {
		v.toggleStep(it)
	})
}

func (v *chatView) runBounds(it *chat.Item) (int, int) {
	items := v.s.conv.Items
	i := -1
	for k := len(items) - 1; k >= 0; k-- {
		if items[k] == it {
			i = k
			break
		}
	}
	if i < 0 {
		return -1, -1
	}
	start, end := i, i
	for start > 0 && items[start-1].Kind == chat.Tool {
		start--
	}
	for end+1 < len(items) && items[end+1].Kind == chat.Tool {
		end++
	}
	return start, end
}

func (v *chatView) runHead(it *chat.Item) *chat.Item {
	if v.s == nil {
		return it
	}
	start, _ := v.runBounds(it)
	if start < 0 {
		return it
	}
	return v.s.conv.Items[start]
}

func (v *chatView) stepRun(it *chat.Item) fyne.CanvasObject {
	if v.s == nil {
		return v.stepLine(it)
	}
	start, end := v.runBounds(it)
	if start < 0 {
		return v.stepLine(it)
	}
	if v.s.conv.Items[start] != it {
		return container.NewWithoutLayout()
	}
	if v.d.hideChips {
		var cards []fyne.CanvasObject
		for _, t := range v.s.conv.Items[start : end+1] {
			if autoBlocked(t) && !v.dismissed[t] {
				cards = append(cards, v.blockedCard(t))
			}
		}
		if len(cards) == 0 {
			return container.NewWithoutLayout()
		}
		return container.NewVBox(cards...)
	}
	var chips, open []fyne.CanvasObject
	for _, t := range v.s.conv.Items[start : end+1] {
		chips = append(chips, v.stepChip(t))
		if autoBlocked(t) && !v.dismissed[t] {
			open = append(open, v.blockedCard(t))
			continue
		}
		if v.isOpen(t) {
			iv := v.views[t.Seq]
			if iv == nil {
				iv = &itemView{holder: container.NewStack()}
			}
			body := append([]fyne.CanvasObject{mutedText(toolHeader(t), fontMeta)}, v.toolBody(t, iv)...)
			var card fyne.CanvasObject = container.NewThemeOverride(bg(colCard, 8, container.NewVBox(body...)), smallTheme{})
			if v.d.center {
				card = container.New(fitCenter{natural: toolNaturalWidth(t)}, card)
			}
			open = append(open, card)
		}
	}
	row := container.New(&flowLayout{gap: 6, center: v.d.center}, chips...)
	if len(open) == 0 {
		return row
	}
	return container.NewVBox(append([]fyne.CanvasObject{row}, open...)...)
}

func (v *chatView) isOpen(it *chat.Item) bool {
	if open, ok := v.stepOpen[it]; ok {
		return open
	}
	return !v.d.collapseChips
}

func (v *chatView) toggleStep(it *chat.Item) {
	if v.stepOpen == nil {
		v.stepOpen = map[*chat.Item]bool{}
	}
	v.stepOpen[it] = !v.isOpen(it)
	h := v.runHead(it)
	if iv, ok := v.views[h.Seq]; ok {
		v.refreshItem(h, iv)
	}
}

func (v *chatView) stepChip(it *chat.Item) fyne.CanvasObject {
	mark, col := "…", colMuted
	switch {
	case it.ResultError:
		mark, col = "✗", colError
	case it.HasResult:
		mark, col = "✓", colIdle
	}
	m := canvas.NewText(mark, col)
	m.TextStyle.Bold = true
	m.TextSize = fontBody
	name := it.ToolName
	if name == "" {
		name = "Tool"
	}
	n := mutedText(name, fontBody)
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 8
	chip := container.NewStack(bgr, container.New(layout.NewCustomPaddedLayout(2, 2, 7, 7), container.NewHBox(m, n)))
	return newTapRow(chip, func() {
		v.toggleStep(it)
	})
}

type flowLayout struct {
	gap    float32
	width  float32
	center bool
}

func (l *flowLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var w, h float32
	for _, o := range objs {
		m := o.MinSize()
		w = max(w, m.Width)
		h = max(h, m.Height)
	}
	n := float32(l.rows(objs, l.width))
	return fyne.NewSize(w, h*n+l.gap*(n-1))
}

func (l *flowLayout) rows(objs []fyne.CanvasObject, width float32) int {
	if width <= 0 {
		return 1
	}
	rows, x := 1, float32(0)
	for _, o := range objs {
		w := o.MinSize().Width
		if x > 0 && x+w > width {
			rows++
			x = 0
		}
		x += w + l.gap
	}
	return rows
}

func (l *flowLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	var row []fyne.CanvasObject
	x, y, rowH := float32(0), float32(0), float32(0)
	place := func() {
		off := float32(0)
		if l.center && x > 0 {
			off = max(0, (size.Width-(x-l.gap))/2)
		}
		cx := off
		for _, o := range row {
			m := o.MinSize()
			o.Resize(m)
			o.Move(fyne.NewPos(cx, y+(rowH-m.Height)/2))
			cx += m.Width + l.gap
		}
	}
	for _, o := range objs {
		m := o.MinSize()
		if x > 0 && x+m.Width > size.Width {
			place()
			row, x = nil, 0
			y += rowH + l.gap
			rowH = 0
		}
		row = append(row, o)
		x += m.Width + l.gap
		rowH = max(rowH, m.Height)
	}
	place()
	l.width = size.Width
}

func centerLists(segs []widget.RichTextSegment, indent string) []widget.RichTextSegment {
	var out []widget.RichTextSegment
	for _, seg := range segs {
		l, ok := seg.(*widget.ListSegment)
		if !ok {
			out = append(out, seg)
			continue
		}
		for i, item := range l.Items {
			mark := indent + "• "
			if l.Ordered {
				mark = fmt.Sprintf("%s%d. ", indent, i+1)
			}
			var texts []widget.RichTextSegment
			var nested []widget.RichTextSegment
			switch it := item.(type) {
			case *widget.ParagraphSegment:
				texts = it.Texts
			case *widget.ListSegment:
				nested = append(nested, it)
			default:
				texts = []widget.RichTextSegment{it}
			}
			if len(texts) > 0 {
				p := &widget.ParagraphSegment{Texts: append([]widget.RichTextSegment{&widget.TextSegment{Text: mark, Style: widget.RichTextStyleInline}}, texts...)}
				out = append(out, p)
			}
			out = append(out, centerLists(nested, indent+"   ")...)
		}
	}
	return out
}

func (v *chatView) renderPlan(it *chat.Item) fyne.CanvasObject {
	title := widget.NewLabel("Approve this plan?")
	title.Alignment = v.align()
	title.TextStyle = fyne.TextStyle{Bold: true}
	msg := widget.NewLabel("Approving runs all of it: every command and change for this request is approved automatically until Claude finishes.")
	msg.Wrapping = fyne.TextWrapWord
	msg.Alignment = v.align()
	msg.SizeName = theme.SizeNameCaptionText
	parts := []fyne.CanvasObject{title}
	if plan := v.s.conv.PlanText(it); plan != "" {
		parts = append(parts, msgText(v.markdown(plan)), widget.NewSeparator())
	} else {
		msg.SetText("Claude listed everything it will do above. " + msg.Text)
	}
	parts = append(parts, msg)
	border := colAccent
	owner := v.s
	if it.PermState == chat.PermPending {
		run := glowButton("Approve plan & run all", theme.ConfirmIcon(), colIdle, v.d.canAnswer(owner), func() { v.d.answerPlan(owner, it, true) })
		no := glowButton("Don't run", theme.CancelIcon(), colError, true, func() { v.d.answerPlan(owner, it, false) })
		parts = append(parts, v.buttonRow(run, no))
	} else if it.PermState == chat.PermAllowed && owner != nil && !owner.conv.Busy && !replied(owner, it) {
		parts = append(parts, v.muted("Approved, but Claude hasn't replied yet", fontBody))
		resend := glowButton("Resend approval", theme.MailSendIcon(), colIdle, true, func() { v.d.answerPlan(owner, it, true) })
		parts = append(parts, v.buttonRow(resend))
	} else {
		text := "Not run"
		switch it.PermState {
		case chat.PermAllowed:
			text = "Approved: running the whole plan"
		case chat.PermCancelled:
			text = "No longer needed"
		}
		parts = append(parts, v.muted(text, fontBody))
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 10
	r.StrokeColor = border
	r.StrokeWidth = 1.5
	return container.NewVBox(padTop(4), container.NewStack(r, container.NewPadded(container.NewVBox(parts...))), padTop(4))
}

type fitCenter struct{ natural float32 }

func (l fitCenter) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize()
}

func (l fitCenter) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	w := max(min(l.natural, size.Width), min(objs[0].MinSize().Width, size.Width))
	objs[0].Resize(fyne.NewSize(w, size.Height))
	objs[0].Move(fyne.NewPos((size.Width-w)/2, 0))
}

func toolNaturalWidth(it *chat.Item) float32 {
	text := toolInputText(it) + "\n" + toolHeader(it)
	if it.HasResult {
		res := it.Result
		if len(res) > maxResultLen {
			res = res[:maxResultLen]
		}
		text += "\n" + res
	}
	longest := ""
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if c := len([]rune(line)); c > n {
			n, longest = c, line
		}
	}
	size := smallTheme{}.Size(theme.SizeNameCaptionText)
	w := fyne.MeasureText(longest, size, fyne.TextStyle{Monospace: true}).Width
	return w + 40
}

type passScroll struct {
	container.Scroll
	parent func() *container.Scroll
}

func (v *chatView) sideScroll(content fyne.CanvasObject) fyne.CanvasObject {
	p := &passScroll{parent: func() *container.Scroll { return v.scroll }}
	p.Direction = container.ScrollHorizontalOnly
	p.Content = content
	p.ExtendBaseWidget(p)
	return p
}

func (p *passScroll) Scrolled(ev *fyne.ScrollEvent) {
	dx, dy := ev.Scrolled.DX, ev.Scrolled.DY
	if dy*dy > dx*dx {
		if parent := p.parent(); parent != nil {
			parent.Scrolled(ev)
		}
		return
	}
	p.Scroll.Scrolled(ev)
}

func relayout(c *fyne.Container) {
	if c == nil {
		return
	}
	if c.Layout != nil {
		c.Layout.Layout(c.Objects, c.Size())
	}
	canvas.Refresh(c)
}

func (v *chatView) relayoutInner() {
	relayout(v.inner)
	v.scroll.Base.Refresh()
}

func (v *chatView) reflow() {
	relayout(v.box)
	relayout(v.inner)
	v.scroll.Base.Refresh()
}

func (v *chatView) buttonRow(btns ...fyne.CanvasObject) fyne.CanvasObject {
	var objs []fyne.CanvasObject
	for i, b := range btns {
		if i > 0 {
			objs = append(objs, padLeft(6))
		}
		objs = append(objs, b)
	}
	row := container.NewHBox(objs...)
	if v.d.center {
		return container.NewCenter(row)
	}
	return row
}

func glowButton(label string, icon fyne.Resource, tint color.Color, enabled bool, tap func()) fyne.CanvasObject {
	b := widget.NewButtonWithIcon(label, icon, tap)
	b.Importance = widget.LowImportance
	if !enabled {
		b.Disable()
	}
	return glowWrap(b, tint, enabled)
}

func glowWrap(b *widget.Button, tint color.Color, enabled bool) fyne.CanvasObject {
	b.Importance = widget.LowImportance
	b.Refresh()
	a := 1.0
	if !enabled {
		a = 0.35
	}
	halo := canvas.NewRectangle(color.Transparent)
	halo.CornerRadius = 12
	halo.StrokeColor = fade(tint, 0.22*a)
	halo.StrokeWidth = 5
	face := canvas.NewRectangle(fade(tint, 0.16*a))
	face.CornerRadius = 9
	face.StrokeColor = fade(tint, 0.75*a)
	face.StrokeWidth = 1.5
	return container.NewStack(halo, container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), container.NewStack(face, b)))
}

const chatItemGap = 2

func chatBox() *fyne.Container {
	return container.New(layout.NewCustomPaddedVBoxLayout(chatItemGap))
}
