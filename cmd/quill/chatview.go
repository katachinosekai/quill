package main

import (
	"bytes"
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
	historyWindow = 80
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
}

type sessView struct {
	box         *fyne.Container
	views       map[int]*itemView
	shownFrom   int
	placeholder bool
	conv        *chat.Conversation
	scrollY     float32
	center      bool
}

const cachedViews = 8

func newChatView(d *desk) *chatView {
	v := &chatView{d: d, views: map[int]*itemView{}}
	v.box = container.NewVBox()
	v.cache = map[*deskSession]*sessView{}
	v.inner = container.New(layout.NewCustomPaddedLayout(10, 10, 14, 14), v.box)
	v.live = widget.NewLabel(" ")
	v.live.TextStyle.Italic = true
	v.live.Importance = widget.MediumImportance
	v.col = v.inner
	v.scroll = container.NewVScroll(v.inner)
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	border.StrokeWidth = 1
	border.CornerRadius = 16
	inside := container.New(layout.NewCustomPaddedLayout(2, 2, 2, 2), v.scroll)
	v.frame = windowCentered(d.chatWidth, d.sidebarWidth, container.NewStack(border, inside))
	liveRow := windowCentered(d.chatWidth, d.sidebarWidth, container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), v.live))
	v.follow = true
	v.scroll.OnScrolled = func(fyne.Position) {
		if v.autoScrolling {
			return
		}
		v.follow = v.atBottom()
		v.syncJump()
	}
	v.jump = v.newJump()
	overlay := container.NewVBox(layout.NewSpacer(), windowCentered(d.chatWidth, d.sidebarWidth, container.NewCenter(v.jump)), padTop(18))
	framed := container.New(layout.NewCustomPaddedLayout(8, 2, 4, 4), container.NewPadded(v.frame))
	v.root = container.NewBorder(nil, liveRow, nil, nil, container.NewStack(framed, overlay))
	return v
}

func (v *chatView) relayout() {
	v.frame.Refresh()
	v.col.Refresh()
	v.scroll.Refresh()
}

func (v *chatView) stash() {
	if v.s == nil {
		return
	}
	sv := &sessView{box: v.box, views: v.views, shownFrom: v.shownFrom, placeholder: v.placeholder, conv: v.s.conv, scrollY: v.scroll.Offset.Y, center: v.d.center}
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
	v.inner.Objects = []fyne.CanvasObject{b}
	v.inner.Refresh()
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
			v.views, v.shownFrom, v.placeholder = sv.views, sv.shownFrom, sv.placeholder
			v.setBox(sv.box)
			v.scroll.Offset = fyne.NewPos(0, sv.scrollY)
			v.scroll.Refresh()
			return
		}
		delete(v.cache, s)
	}
	v.s = s
	v.views = map[int]*itemView{}
	v.setBox(container.NewVBox())
	if s == nil {
		v.box.Add(v.emptyState())
		v.box.Refresh()
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

func (v *chatView) rebuild() {
	v.box.RemoveAll()
	v.views = map[int]*itemView{}
	items := v.s.conv.Items
	if v.shownFrom > 0 {
		more := widget.NewButton(fmt.Sprintf("Show %d earlier messages", v.shownFrom), func() {
			v.shownFrom = 0
			v.rebuild()
		})
		more.Importance = widget.LowImportance
		v.box.Add(more)
	}
	v.placeholder = len(items) == 0
	if v.placeholder {
		v.box.Add(v.emptySession())
	}
	for _, it := range items[v.shownFrom:] {
		v.box.Add(v.viewFor(it).holder)
	}
	v.box.Refresh()
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
	v.scroll.Refresh()
	v.scroll.ScrollToBottom()
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
	v.glide = fyne.NewAnimation(320*time.Millisecond, func(p float32) {
		target := v.scroll.Content.MinSize().Height - v.scroll.Size().Height
		v.autoScrolling = true
		v.scroll.Offset = fyne.NewPos(0, from+(target-from)*p)
		v.scroll.Refresh()
		v.autoScrolling = false
		if p >= 1 {
			v.scrollBottom()
		}
	})
	v.glide.Curve = fyne.AnimationEaseInOut
	v.glide.Start()
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
	for _, it := range items {
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
		v.box.Refresh()
	}
	v.keepUp()
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
		v.box.Refresh()
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
		return msgText(v.markdown(it.Text))
	case chat.Tool:
		if !v.steps {
			return v.stepRun(it)
		}
		return v.renderTool(it, iv)
	case chat.Permission:
		if it.PermState == chat.PermAutoAllowed || it.PermState == chat.PermPlanAsked {
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
	case chat.Thinking:
		return v.renderThinking(it, iv)
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
		rt.Segments = centerLists(rt.Segments, "")
		alignSegments(rt.Segments, fyne.TextAlignCenter)
	}
	return rt
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
		parts = append(parts, mutedText(it.DiffPath, 12))
	}
	parts = append(parts, container.NewHScroll(grid))
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

func (v *chatView) renderPermission(it *chat.Item) fyne.CanvasObject {
	title := widget.NewLabel("Allow " + it.ToolName + "?")
	title.TextStyle = fyne.TextStyle{Bold: true}
	parts := []fyne.CanvasObject{title}
	if it.Request != nil && it.Request.DecisionReason != "" {
		r := widget.NewLabel(it.Request.DecisionReason)
		r.Wrapping = fyne.TextWrapWord
		parts = append(parts, r)
	}
	if it.Summary != "" {
		s := widget.NewLabel(it.Summary)
		s.Wrapping = fyne.TextWrapWord
		parts = append(parts, s)
	}
	parts = append(parts, permDetail(v, it)...)
	if it.PermState == chat.PermPending {
		allow := widget.NewButtonWithIcon("Allow", theme.ConfirmIcon(), func() { v.d.answer(v.s, it, protocol.Allow) })
		allow.Importance = widget.HighImportance
		always := widget.NewButton("Allow always (this session)", func() { v.d.answer(v.s, it, protocol.AllowAlways) })
		deny := widget.NewButtonWithIcon("Deny", theme.CancelIcon(), func() { v.d.answer(v.s, it, protocol.Deny) })
		deny.Importance = widget.DangerImportance
		if !v.d.canAnswer(v.s) {
			allow.Disable()
			always.Disable()
			deny.Disable()
		}
		parts = append(parts, container.NewHBox(allow, always, deny))
	} else {
		parts = append(parts, mutedText(permStateText(it.PermState), 13))
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
	top, bottom := float32(6), float32(6)
	switch k {
	case chat.User:
		top, bottom = 8, 4
	case chat.Assistant:
		top, bottom = 4, 10
	case chat.Tool, chat.Thinking:
		top, bottom = 0, 0
	case chat.Notice:
		top, bottom = 8, 8
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
	var chips, open []fyne.CanvasObject
	for _, t := range v.s.conv.Items[start : end+1] {
		chips = append(chips, v.stepChip(t))
		if v.isOpen(t) {
			iv := v.views[t.Seq]
			if iv == nil {
				iv = &itemView{holder: container.NewStack()}
			}
			body := append([]fyne.CanvasObject{mutedText(toolHeader(t), 12)}, v.toolBody(t, iv)...)
			open = append(open, container.NewThemeOverride(bg(colCard, 8, container.NewVBox(body...)), smallTheme{}))
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
	m.TextSize = 13
	name := it.ToolName
	if name == "" {
		name = "Tool"
	}
	n := mutedText(name, 12)
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
	title.TextStyle = fyne.TextStyle{Bold: true}
	msg := widget.NewLabel("Claude listed everything it will do above. Approving runs all of it: every command and change for this request is approved automatically until Claude finishes.")
	msg.Wrapping = fyne.TextWrapWord
	parts := []fyne.CanvasObject{title, msg}
	border := colAccent
	if it.PermState == chat.PermPending {
		run := widget.NewButtonWithIcon("Approve plan & run all", theme.ConfirmIcon(), func() { v.d.answerPlan(v.s, it, true) })
		run.Importance = widget.HighImportance
		no := widget.NewButtonWithIcon("Don't run", theme.CancelIcon(), func() { v.d.answerPlan(v.s, it, false) })
		no.Importance = widget.DangerImportance
		if !v.d.canAnswer(v.s) {
			run.Disable()
		}
		parts = append(parts, container.NewHBox(run, no))
	} else {
		text := "Not run"
		switch it.PermState {
		case chat.PermAllowed:
			text = "Approved: running the whole plan"
		case chat.PermCancelled:
			text = "No longer needed"
		}
		parts = append(parts, mutedText(text, 13))
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 10
	r.StrokeColor = border
	r.StrokeWidth = 1.5
	return container.NewVBox(padTop(4), container.NewStack(r, container.NewPadded(container.NewVBox(parts...))), padTop(4))
}
