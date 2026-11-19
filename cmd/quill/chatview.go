package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

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
	d           *desk
	s           *deskSession
	box         *fyne.Container
	scroll      *container.Scroll
	views       map[int]*itemView
	shownFrom   int
	placeholder bool
	root        fyne.CanvasObject
	col         *fyne.Container
	frame       *fyne.Container
	inner       *fyne.Container
	steps       bool
	cache       map[*deskSession]*sessView
	order       []*deskSession
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
	v.col = windowCentered(d.chatWidth, d.sidebarWidth, v.inner)
	v.scroll = container.NewVScroll(container.NewPadded(v.col))
	border := canvas.NewRectangle(color.Transparent)
	border.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x1c}
	border.StrokeWidth = 1
	border.CornerRadius = 16
	v.frame = windowCentered(d.chatWidth, d.sidebarWidth, border)
	v.root = container.NewStack(container.New(layout.NewCustomPaddedLayout(8, 8, 4, 4), container.NewPadded(v.frame)), v.scroll)
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

func (v *chatView) show(s *deskSession) {
	if v.d.act != nil {
		v.d.act.show(s)
	}
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
	return v.scroll.Offset.Y+v.scroll.Size().Height >= content-60
}

func (v *chatView) scrollBottom() {
	v.scroll.Refresh()
	v.scroll.ScrollToBottom()
}

func (v *chatView) update(items []*chat.Item) {
	if v.s == nil || len(items) == 0 {
		return
	}
	stick := v.atBottom()
	added := false
	for _, it := range items {
		if iv, ok := v.views[it.Seq]; ok {
			v.refreshItem(it, iv)
			continue
		}
		if v.placeholder {
			v.box.RemoveAll()
			v.placeholder = false
		}
		v.box.Add(v.viewFor(it).holder)
		added = true
	}
	if added {
		v.box.Refresh()
	}
	if stick {
		v.scrollBottom()
	}
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
			iv.holder.Objects = []fyne.CanvasObject{spaced(it.Kind, iv.stream)}
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
		return bgPad(colBubble, 14, 4, 10, v.markdown(it.Text))
	case chat.Assistant:
		return v.markdown(it.Text)
	case chat.Tool:
		if !v.steps {
			return v.stepLine(it)
		}
		return v.renderTool(it, iv)
	case chat.Permission:
		return v.renderPermission(it)
	case chat.Thinking:
		if !v.steps {
			return v.stepLine(it)
		}
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
	head := foldHead(toolHeader(it), icon, func() {
		iv.expanded = !iv.expanded
		v.refreshItem(it, iv)
	})
	parts := []fyne.CanvasObject{head}
	if iv.expanded {
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
	box := container.NewVBox(parts...)
	if !iv.expanded {
		return box
	}
	return bg(colCard, 8, box)
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
	head := foldHead(label, icon, func() {
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
		top, bottom = 22, 10
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

func foldHead(text string, icon fyne.Resource, onTap func()) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	l.TextStyle.Bold = true
	return newTapRow(container.NewBorder(nil, nil, container.NewCenter(sized(18, 18, widget.NewIcon(icon))), nil, l), onTap)
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
		if v.d.act != nil {
			v.d.act.focus(it)
		}
	})
}
