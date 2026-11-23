package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image/color"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
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
	st       *streamView // a streaming reply (streamtext.go)
	expanded bool
	full     bool
	height   float32
	sig      string
}

// itemSig changes whenever an item's shown content does. The text is hashed,
// not just measured: a finished reply can replace the streamed text with one
// of the same length, and the phone API's revs must not miss that.
// itemSigCheap: itemSig without the text hash.
func itemSigCheap(it *chat.Item) string {
	return fmt.Sprintf("%d|%d|%t|%t|%t|%d|%d|%d|%d", it.Kind, len(it.Text), it.Streaming, it.HasResult, it.ResultError, len(it.Result), len(it.Input), it.PermState, len(it.Diff))
}

func itemSig(it *chat.Item) string {
	h := fnv.New64a()
	h.Write([]byte(it.Text))
	return fmt.Sprintf("%d|%d|%x|%t|%t|%t|%d|%d|%d|%d", it.Kind, len(it.Text), h.Sum64(), it.Streaming, it.HasResult, it.ResultError, len(it.Result), len(it.Input), it.PermState, len(it.Diff))
}

// itemTag is what the phone tracker keeps per item instead of the signature
// text: a 64-bit hash of every field itemSig covers, plus the text length.
type itemTag struct {
	n int
	h uint64
}

func itemSigTag(it *chat.Item) itemTag {
	h := fnv.New64a()
	h.Write([]byte(it.Text))
	fmt.Fprintf(h, "|%d|%t|%t|%t|%d|%d|%d|%d", it.Kind, it.Streaming, it.HasResult, it.ResultError, len(it.Result), len(it.Input), it.PermState, len(it.Diff))
	return itemTag{len(it.Text), h.Sum64()}
}

// itemCheapTag is itemSigCheap as a hash.
func itemCheapTag(it *chat.Item) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|%d|%t|%t|%t|%d|%d|%d|%d", it.Kind, len(it.Text), it.Streaming, it.HasResult, it.ResultError, len(it.Result), len(it.Input), it.PermState, len(it.Diff))
	return h.Sum64()
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
	planAns       map[*chat.Item]map[int]planAnswer // plan card answers, kept across redraws
	planPlain     map[*chat.Item]bool               // plans shown as written plus the card, not overlaid
	planApprove   map[*chat.Item]int                // the approval question's pick on a plan card (planapproval.go)
	btnKinds      map[int]string                    // buttons each item shows now (click guard)
	choicePicks   map[*chat.Item]map[int]int        // picks on a multi-question reply (by question index), kept until sent
	choiceTexts   map[*chat.Item]map[int]string     // typed answers on a multi-question reply
	btnChanged    time.Time                         // when any item's buttons last changed
	fadeStart     map[int]time.Time                 // when each item's controls began to fade in (fade.go)
	fadePend      []*canvas.Rectangle               // veils of the controls being built, until noteButtons
	fadesStarted  int                               // control fades started (tests)
	frame         *fyne.Container
	inner         *fyne.Container
	steps         bool
	cache         map[*deskSession]*sessView
	order         []*deskSession
	sel           *selLayer
	jumpBar       *fyne.Container
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
		v.touched()
		y := v.scroll.Offset.Y
		switch {
		case y < v.lastY-2 && !v.pinned():
			// Any upward scroll stops following, however small.
			v.follow = false
		case y >= v.lastY-2 && v.atBottom():
			v.follow = true
			v.newBelow = false
		}
		v.lastY = y
		v.syncJump()
		if y < loadEarlierAt && v.shownFrom > 0 && !v.follow {
			v.loadEarlier()
		}
	}
	v.jump = v.newJump()
	overlay := container.NewVBox(layout.NewSpacer(), windowCentered(width, shift, container.NewHBox(layout.NewSpacer(), v.jump, padLeft(12))), padTop(12))
	v.jumpBar = overlay
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
	defer v.d.drawQueue(v, s)
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
	// The pick card stays while the tab is unpicked, even under a notice
	// (a refused name), so another topic can still be chosen.
	v.placeholder = len(items) == 0 || v.s.pickPending
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
	if v.s.pickPending {
		return v.d.kmemCard(v.s)
	}
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
	glow.CornerRadius = 20
	glow.StrokeColor = colorAlpha(colAccent, 0xc0)
	glow.StrokeWidth = 1.5
	btn := newJumpTap(theme.MoveDownIcon(), func() { v.glideBottom() }, func(on bool) {
		if on {
			glow.StrokeColor = colAccent
			glow.StrokeWidth = 2.5
		} else {
			glow.StrokeColor = colorAlpha(colAccent, 0xc0)
			glow.StrokeWidth = 1.5
		}
		glow.Refresh()
	})
	v.glow = glow
	v.glowAnim = fyne.NewAnimation(1100*time.Millisecond, func(p float32) {
		glow.FillColor = colorAlpha(colAccent, uint8(0x30+p*0x90))
		glow.Refresh()
	})
	v.glowAnim.AutoReverse = true
	v.glowAnim.RepeatCount = fyne.AnimationRepeatForever
	box := container.New(layout.NewGridWrapLayout(fyne.NewSize(40, 40)), container.NewStack(glow, btn))
	box.Hide()
	return box
}

type jumpTap struct {
	widget.BaseWidget
	icon    fyne.Resource
	onTap   func()
	onHover func(bool)
}

func newJumpTap(icon fyne.Resource, onTap func(), onHover func(bool)) *jumpTap {
	j := &jumpTap{icon: icon, onTap: onTap, onHover: onHover}
	j.ExtendBaseWidget(j)
	return j
}

func (j *jumpTap) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewCenter(sized(20, 20, widget.NewIcon(j.icon))))
}

func (j *jumpTap) Tapped(*fyne.PointEvent)        { j.onTap() }
func (j *jumpTap) MouseIn(*desktop.MouseEvent)    { j.onHover(true) }
func (j *jumpTap) MouseMoved(*desktop.MouseEvent) {}
func (j *jumpTap) MouseOut()                      { j.onHover(false) }
func (j *jumpTap) Cursor() desktop.Cursor         { return desktop.PointerCursor }

func (v *chatView) syncJump() {
	if v.jump == nil {
		return
	}
	show := !v.follow && v.s != nil
	if show == v.jump.Visible() {
		return
	}
	defer func() {
		if v.jumpBar != nil {
			relayoutTree(v.jumpBar)
		}
	}()
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
			if v.placeholder && !v.s.pickPending {
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
		// A plan's reply shows its controls while it is pending.
		if it.Kind == chat.Plan {
			if r := planReply(v.s.conv, it); r != nil {
				heads[r] = true
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
	if v.sel != nil && v.sel.has {
		v.sel.Refresh()
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

// blankItem: a finished item with nothing to show (an empty reply, user
// message, thought, notice or error). It takes no row and no padding; the
// chat box's VBox skips hidden objects.
func blankItem(it *chat.Item) bool {
	switch it.Kind {
	case chat.User, chat.Assistant, chat.Thinking, chat.Notice, chat.Error:
		return !it.Streaming && strings.TrimSpace(it.Text) == ""
	}
	return false
}

// emptyObj: a rendered item that draws nothing (an empty container).
func emptyObj(o fyne.CanvasObject) bool {
	c, ok := o.(*fyne.Container)
	return ok && len(c.Objects) == 0
}

func (v *chatView) refreshItem(it *chat.Item, iv *itemView) {
	iv.sig = itemSig(it)
	if !blankItem(it) && !iv.holder.Visible() {
		iv.holder.Show()
	}
	if blankItem(it) {
		iv.st = nil
		iv.holder.Objects = nil
		iv.holder.Hide()
	} else if streaming(it, iv) {
		v.refreshStream(it, iv)
	} else {
		iv.st = nil
		o := v.render(it, iv)
		if emptyObj(o) {
			// Nothing to show (a tool call while chips are hidden, a later
			// tool in a run, an auto-allowed permission): no row, no padding.
			iv.holder.Objects = nil
			iv.holder.Hide()
		} else {
			iv.holder.Objects = []fyne.CanvasObject{spaced(it.Kind, o)}
			iv.holder.Refresh()
		}
	}
	v.settleHeight(iv)
}

// settleHeight: after an item's content changed, relayout. Following, stay at
// the bottom; reading, keep the view on the same message (an item wholly above
// the viewport shifts the offset by its height change).
func (v *chatView) settleHeight(iv *itemView) {
	h := iv.holder.MinSize().Height
	if h == iv.height {
		return
	}
	old := iv.height
	iv.height = h
	if v.follow && !v.steps {
		relayout(v.box)
		v.scrollBottom()
		return
	}
	top := iv.holder.Position().Y + v.box.Position().Y
	relayout(v.box)
	if !v.follow && old > 0 && top+old <= v.scroll.Offset.Y {
		v.autoScrolling = true
		v.scroll.Offset = fyne.NewPos(0, max(0, v.scroll.Offset.Y+h-old))
		v.reflow()
		v.lastY = v.scroll.Offset.Y
		v.autoScrolling = false
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
	o := v.renderItem(it, iv)
	if it.Kind == chat.User || it.Kind == chat.Assistant {
		o = v.withTime(o, it.Time)
	}
	if (it.Kind == chat.User || it.Kind == chat.Assistant) && v.s != nil {
		s := v.s
		m := newMsgMenu(o, func(at fyne.Position) { v.touched(); v.d.messageMenu(s, it, at) })
		m.onTap = func() { v.touched(); v.d.clearSelections() }
		m.onDouble = func(at fyne.Position) {
			v.touched()
			if v.sel != nil {
				v.sel.selectWordAt(at)
			}
		}
		return m
	}
	return o
}

func (v *chatView) renderItem(it *chat.Item, iv *itemView) fyne.CanvasObject {
	switch it.Kind {
	case chat.User:
		if strings.Contains(it.Text, "```") {
			return bgPad(colBubble, 14, 4, 10, msgText(v.markdown(it.Text)))
		}
		l := widget.NewLabel(it.Text)
		l.Wrapping = fyne.TextWrapWord
		l.Alignment = v.align()
		return bgPad(colBubble, 14, 4, 10, msgText(l))
	case chat.Assistant:
		if it.Streaming {
			return msgText(v.markdown(closeOpenMarkdown(it.Text)))
		}
		if o := v.renderPlanReply(it); o != nil {
			return o
		}
		o, plain := v.renderChoiceReply(it)
		if o != nil {
			return o
		}
		if !plain {
			if body, card := v.choiceCard(it); card != nil {
				return container.NewVBox(msgText(v.markdownBlocks(body)), card)
			}
		}
		md := msgText(v.markdownBlocks(it.Text))
		if p := v.mediaPreviews(it.Text); p != nil {
			return container.NewVBox(md, p)
		}
		return md
	case chat.Tool:
		var row fyne.CanvasObject
		if !v.steps {
			row = v.stepRun(it)
		} else {
			row = v.renderTool(it, iv)
		}
		if it.ToolName == "SendUserFile" {
			if p := v.mediaPreviews(sentFiles(it)); p != nil {
				return container.NewVBox(row, p)
			}
		}
		return row
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
		if strings.HasPrefix(it.Text, openerNotice) {
			if p := v.mediaPreviews(it.Text); p != nil {
				return container.NewVBox(l, p)
			}
		}
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
	return v.markdownFade(text, nil)
}

// markdownFade is markdown with the last characters (b's budget) in the
// colours of newly revealed text (textfade.go); b nil: no fade.
func (v *chatView) markdownFade(text string, b *fadeBudget) fyne.CanvasObject {
	rt := widget.NewRichTextFromMarkdown(hardBreaks(autolinks(text)))
	rt.Wrapping = fyne.TextWrapWord
	wrapCodeBlocks(rt)
	if b != nil {
		rt.Segments = fadeSegs(rt.Segments, b)
	}
	if v.d.center {
		return centeredBlocks(rt.Segments)
	}
	if hasLetteredRun(text) {
		return blocks(rt.Segments, false)
	}
	return rt
}

func centeredBlocks(segs []widget.RichTextSegment) fyne.CanvasObject {
	return blocks(segs, true)
}

// hasLetteredRun: the markdown has two or more lettered lines in a row
// (blank lines between are fine).
func hasLetteredRun(md string) bool {
	prev := false
	for _, l := range strings.Split(md, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		cur := rawLettered.MatchString(l)
		if cur && prev {
			return true
		}
		prev = cur
	}
	return false
}

// blocks lays a message's blocks out: with center, paragraphs centered
// (an overlong line balanced) and lists as centered columns; lettered
// items (2+ in a row) as lists either way (letteredList).
func blocks(segs []widget.RichTextSegment, center bool) fyne.CanvasObject {
	var parts []fyne.CanvasObject
	var run []widget.RichTextSegment
	text := func(segs ...widget.RichTextSegment) *widget.RichText {
		if center {
			alignSegments(segs, fyne.TextAlignCenter)
		}
		t := widget.NewRichText(segs...)
		t.Wrapping = fyne.TextWrapWord
		return t
	}
	plain := func(run []widget.RichTextSegment) {
		if len(run) == 0 {
			return
		}
		if !center {
			parts = append(parts, text(run...))
			return
		}
		if from, to, w, ok := balancedParagraph(run); ok {
			// One overlong line: it wraps in its own narrower column.
			if from > 0 {
				parts = append(parts, text(run[:from]...))
			}
			parts = append(parts, newColumn(true, w, text(run[from:to]...)))
			if to < len(run) {
				parts = append(parts, text(run[to:]...))
			}
		} else {
			parts = append(parts, text(run...))
		}
	}
	flush := func() {
		if len(run) == 0 {
			return
		}
		// Lettered lines ("a. …", "b) …") aren't markdown lists, but rule
		// f5c0dd9 treats them as one: two or more in a row are one
		// left-aligned block, centered as a column like a list.
		bounds, lines, ok := paragraphLines(run)
		if !ok {
			plain(run)
			run = nil
			return
		}
		from := 0
		for i := 0; i < len(lines); {
			j := i
			for j < len(lines) && letteredLine.MatchString(lines[j]) {
				j++
			}
			if j-i < 2 {
				i = max(j, i+1)
				continue
			}
			plain(run[from:bounds[i][0]])
			parts = append(parts, letteredList(run[bounds[i][0]:bounds[j-1][1]], bounds[i:j], bounds[i][0], lines[i:j], center))
			from = bounds[j-1][1]
			i = j
		}
		plain(run[from:])
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
		if center {
			parts = append(parts, listColumn(l, t))
		} else {
			parts = append(parts, t)
		}
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
	if v.planOverlaid(it) {
		return container.NewWithoutLayout() // its reply has the controls (planoverlay.go)
	}
	title := widget.NewLabel(planQuestion(v.s.conv, it))
	title.Alignment = v.align()
	title.Wrapping = fyne.TextWrapWord
	title.TextStyle = fyne.TextStyle{Bold: true}
	msg := widget.NewLabel("Claude's plan is above. Approving runs this plan: every command and change, including its background jobs and subagents, is approved until the plan is done.")
	msg.Wrapping = fyne.TextWrapWord
	msg.Alignment = v.align()
	msg.SizeName = theme.SizeNameCaptionText
	parts := []fyne.CanvasObject{title, msg}
	border := colAccent
	owner := v.s
	state := planActive
	if owner != nil {
		state = planState(owner.conv, it)
	}
	if it.PermState == chat.PermPending && state == planActive {
		no := glowButton("Don't run", theme.CancelIcon(), colError, true, v.guarded(func() { v.d.answerPlan(owner, it, false) }))
		approve := glowButton("Approve plan & run all", theme.ConfirmIcon(), colIdle, v.d.canAnswer(owner), v.guarded(func() { v.d.answerPlan(owner, it, true) }))
		var qs []planQ
		var appr *approvalOpts
		kind := "pending"
		if owner != nil {
			qs = planItemQuestions(owner, it)
			appr = planItemApproval(owner, it)
		}
		switch {
		case appr != nil:
			// The approval question's own options (or picks, with a
			// "Don't run") answer the plan (planapproval.go); other questions share the card and one Send.
			msg.SetText("Claude's plan is above. " + approvalCaption(appr))
			// The title is the approval question: its buttons go right under
			// it, the other questions after them.
			var ans map[int]planAnswer
			if len(qs) > 0 {
				ans = v.planAnswers(it, qs)
			}
			parts = append(parts, v.approvalRadio(owner, it, appr, true))
			if len(qs) > 0 {
				parts = append(parts, v.planQuestionForm(qs, ans))
			}
			parts = append(parts, v.approvalSend(owner, it, appr, qs, ans, true)...)
			kind = approvalKind(appr, qs)
		case len(qs) > 0:
			// Several questions, or one that isn't a pick between options:
			// a control for each, all answers go with Approve.
			ans := v.planAnswers(it, qs)
			parts = append(parts, v.planQuestionForm(qs, ans))
			run := glowButton("Approve plan & send answers", theme.ConfirmIcon(), colIdle, v.d.canAnswer(owner), v.guarded(func() { v.d.answerPlanAnswers(owner, it, qs, ans) }))
			parts = append(parts, v.buttonRow(run, no))
		default:
			parts = append(parts, v.buttonRow(approve, no))
		}
		v.noteButtons(it.Seq, kind)
	} else if it.PermState == chat.PermAllowed && owner != nil && !owner.conv.Busy && !owner.inTurn && !replied(owner, it) {
		// Not while a turn is under way: Busy drops between a turn's steps,
		// and this button flipped in and out under the pointer (tb704f5).
		resend := glowButton("Resend approval", theme.MailSendIcon(), colIdle, true, v.guarded(func() { v.d.answerPlan(owner, it, true) }))
		parts = append(parts, v.buttonRow(resend))
		v.noteButtons(it.Seq, "resend")
	} else {
		v.noteButtons(it.Seq, "none")
		// Inactive buttons with a small label: a stale card can't be clicked.
		if owner != nil {
			if appr := planItemApproval(owner, it); appr != nil {
				parts = append(parts, v.approvalRadio(owner, it, appr, false))
			} else {
				parts = append(parts, v.buttonRow(
					glowButton("Approve plan & run all", theme.ConfirmIcon(), colIdle, false, nil),
					glowButton("Don't run", theme.CancelIcon(), colError, false, nil)))
			}
		}
		parts = append(parts, v.muted(planStatus(it, state), fontBody))
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 10
	r.StrokeColor = border
	r.StrokeWidth = 1.5
	return v.fadeIn(it.Seq, container.NewVBox(padTop(4), container.NewStack(r, container.NewPadded(container.NewVBox(parts...))), padTop(4)))
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

// cardInset: chat column padding plus a card's own padding and border.
const cardInset = 64

func (v *chatView) buttonRow(btns ...fyne.CanvasObject) fyne.CanvasObject {
	return newFlowRow(6, v.d.center, func() float32 { return v.d.chatWidth() - cardInset }, btns...)
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

var mdNoise = strings.NewReplacer("**", "", "__", "", "`", "")

func planQuestion(c *chat.Conversation, plan *chat.Item) string {
	text := ""
	if c != nil {
		text = c.PlanText(plan)
		if text == "" {
			for i := len(c.Items) - 1; i >= 0; i-- {
				if c.Items[i] == plan {
					for j := i - 1; j >= 0; j-- {
						if c.Items[j].Kind == chat.User {
							break
						}
						if c.Items[j].Kind == chat.Assistant && strings.TrimSpace(c.Items[j].Text) != "" {
							text = c.Items[j].Text
							break
						}
					}
					break
				}
			}
		}
	}
	text = strings.TrimSpace(text)
	// With numbered closing questions (the planFormatHint format) the approval
	// one is the title.
	if q := chat.ClosingApproval(text); q != "" {
		if q = strings.TrimSpace(strings.TrimRight(mdNoise.Replace(q), "*_ ")); len(q) <= 300 {
			return q
		}
	}
	last := strings.TrimSpace(text[strings.LastIndex(text, "\n")+1:])
	last = strings.TrimSpace(strings.TrimLeft(mdNoise.Replace(last), "#>*-_ "))
	last = strings.TrimRight(last, "*_ ")
	if strings.Contains(last, "?") && len(last) <= 300 {
		return last
	}
	return "Approve this plan?"
}

func closeOpenMarkdown(s string) string {
	fences := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fences++
		}
	}
	if fences%2 == 1 {
		return s + "\n```"
	}
	last := s[strings.LastIndex(s, "\n\n")+1:]
	add := ""
	if strings.Count(last, "`")%2 == 1 {
		add = "`"
	}
	outside := ""
	for i, part := range strings.Split(last+add, "`") {
		if i%2 == 0 {
			outside += part
		}
	}
	if strings.Count(outside, "**")%2 == 1 {
		if add == "" && strings.HasSuffix(s, "**") {
			return strings.TrimSuffix(s, "**")
		}
		add += "**"
	}
	return s + add
}

var parenAside = regexp.MustCompile(`\s*\([^)]*\)`)

// optionMark: "(option 1)", "(Option B)", "(choice 2)", "(A)", "(2)".
var optionMark = regexp.MustCompile(`(?i)\((?:option|choice)\s+(\w{1,3})\)|\(([A-D1-4])\)`)

var leadIn = regexp.MustCompile(`(?i)^(?:for|in|on|to|with|as|if|when|after|before|about|regarding|by|from|at)\b`)

// maxOptionWords: longer unmarked options make no buttons (labels stay short).
const maxOptionWords = 16

// lastQOutsideParens: the index of the last "?" outside parentheses, or -1.
func lastQOutsideParens(s string) int {
	depth, at := 0, -1
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '?':
			if depth == 0 {
				at = i
			}
		}
	}
	return at
}

var yesWord, noWord = regexp.MustCompile(`(?i)^\W*yes\b`), regexp.MustCompile(`(?i)^\W*no\b`)

// yesNoOptions: a "Yes, …" / "No, …" pair.
func yesNoOptions(a, b string) bool {
	return yesWord.MatchString(a) && noWord.MatchString(b)
}

func parseChoices(line string) (string, []string, bool) {
	// A quote or a table row names a question, it doesn't ask it.
	if t := strings.TrimSpace(line); strings.HasPrefix(t, ">") || strings.HasPrefix(t, "|") {
		return "", nil, false
	}
	clean := strings.TrimSpace(strings.TrimLeft(mdNoise.Replace(line), "#>*-_ "))
	q := strings.LastIndex(clean, "?")
	if q < 0 {
		return "", nil, false
	}
	// Inline lettered options ("…, or pick from: a) x, b) y?") are a multi
	// question's (planquestions.go), not buttons.
	if inlineLetterOpts(clean) != nil {
		return "", nil, false
	}
	// Options marked "(option 1)", "(option 2)" or "(A)", "(B)" get those
	// labels, however long they are (user 2026-11-21, Zomboid Networking 2).
	var marks []string
	for _, m := range optionMark.FindAllStringSubmatch(clean[:q], -1) {
		marks = append(marks, "Option "+strings.ToUpper(firstNonEmpty(m[1], m[2])))
	}
	// Asides in parentheses explain an option; the button shows just the
	// option ("option 1 (Quill's paired PCs)" → "Option 1").
	// "Where should it live? Inside (recommended) or linked?": the options
	// are the sentence after the question (user 2026-11-22).
	body := clean[:q]
	if p := lastQOutsideParens(body); p >= 0 {
		body = strings.TrimSpace(listLead.ReplaceAllString(strings.TrimSpace(body[p+1:]), ""))
	}
	ask := strings.TrimSpace(parenAside.ReplaceAllString(body, ""))
	sep := ", or "
	i := strings.LastIndex(ask, sep)
	if i < 0 {
		sep = " or "
		i = strings.LastIndex(ask, sep)
	}
	if i < 0 {
		return "", nil, false
	}
	a := strings.TrimSpace(ask[:i])
	b := strings.TrimSpace(ask[i+len(sep):])
	if c := strings.LastIndex(a, ": "); c >= 0 {
		a = strings.TrimSpace(a[c+2:])
	}
	low := strings.ToLower(a)
	for _, stem := range []string{"do you want me to ", "would you like me to ", "should i ", "shall i ", "should we ", "shall we ", "do you want to ", "want me to ", "should it ", "do i "} {
		if strings.HasPrefix(low, stem) {
			a = strings.TrimSpace(a[len(stem):])
			break
		}
	}
	// "Which panels should I build, or is there something else?": the first
	// part is the question itself, not an option.
	if f := strings.ToLower(strings.Fields(a + " x")[0]); f == "which" || f == "what" || f == "who" || f == "where" || f == "when" || f == "why" || f == "how" {
		return "", nil, false
	}
	opts := []string{a}
	// "Yes, 2 subagents (recommended) or no, I'll do it myself?" is two
	// options: commas inside a yes/no pair don't split it.
	if yesNoOptions(a, b) {
		// keep a whole
	} else if strings.Contains(a, ", ") {
		// "a, b, or c" and "a, b or c" both list three options.
		opts = mergeContinuations(strings.Split(a, ", "))
		// A lead-in before the first comma is not an option ("For the icon,
		// use blue, green, or red?").
		if len(opts) >= 2 && leadIn.MatchString(opts[0]) {
			opts = opts[1:]
		}
	}
	opts = append(opts, b)
	if len(opts) > 4 || strings.Contains(b, " or ") {
		return "", nil, false
	}
	if len(marks) == len(opts) {
		return strings.TrimRight(clean, "*_ "), marks, true
	}
	for i, o := range opts {
		o = strings.TrimSpace(o)
		if o == "" || len(strings.Fields(o)) > maxOptionWords {
			return "", nil, false
		}
		r := []rune(o)
		r[0] = []rune(strings.ToUpper(string(r[0])))[0]
		opts[i] = string(r)
	}
	return strings.TrimRight(clean, "*_ "), opts, true
}

// clickGuard: after a card's buttons change (appear, swap, go), clicks are
// ignored this long, so a click aimed at the old buttons can't hit the new
// ones (user 2026-11-22, tb704f5: "Yes" became "Resend approval" under the
// pointer).
var clickGuard = 600 * time.Millisecond

// noteButtons records which buttons an item shows now; a change starts the
// click guard.
func (v *chatView) noteButtons(seq int, kind string) {
	if v.btnKinds == nil {
		v.btnKinds = map[int]string{}
	}
	_, seen := v.btnKinds[seq]
	v.startFades(seq, !seen, kind)
	if v.btnKinds[seq] != kind {
		v.btnKinds[seq] = kind
		v.btnChanged = time.Now()
	}
}

// guarded wraps a button's action in the click guard.
func (v *chatView) guarded(fn func()) func() {
	return func() {
		if time.Since(v.btnChanged) < clickGuard {
			return
		}
		fn()
	}
}

// numChoiceQ: one numbered question of a reply, with its option buttons (none
// if its line names no options).
type numChoiceQ struct {
	num   string
	idx   int // unique in the reply (numbers can repeat: markdown's "1." on every item)
	title string
	opts  []string
	// line, end: the original lines of the reply the question is on (end
	// exclusive), for showing its buttons in place (choiceoverlay.go).
	line, end int
}

var numLead = regexp.MustCompile(`^\s*(?:[-*]\s*)?(?:\*\*)?(\d+)[.)]\s+`)

// numberedChoiceQs: when a reply ends with two or more numbered question
// lines, the text before them and each question with its options (user
// 2026-11-22, t4981e5: only the last of five got buttons).
func numberedChoiceQs(text string) (string, []numChoiceQ) {
	joined, first := chat.JoinOptionLineSpans(text)
	n := len(joined)
	for n > 0 && strings.TrimSpace(joined[n-1]) == "" {
		n--
	}
	lines := joined[:n]
	nOrig := len(strings.Split(text, "\n"))
	first0 := n
	for i := n - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if !numLead.MatchString(t) {
			break
		}
		first0 = i
	}
	var qs []numChoiceQ
	body := lines[:first0:first0]
	for i := first0; i < n; i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		m := numLead.FindStringSubmatch(l)
		rest := strings.TrimSpace(l[len(m[0]):])
		// A numbered line that asks nothing is a plan step, not a question:
		// it stays message text (user 2026-11-23: five steps drew as cards
		// above the one real question).
		_, _, hasOpts := parseChoices(rest)
		if !hasOpts && !strings.Contains(codeOrURL.ReplaceAllString(rest, " "), "?") {
			body = append(body, l)
			continue
		}
		q := numChoiceQ{num: m[1], idx: len(qs), title: m[1] + ". " + strings.Trim(rest, "*_ ")}
		if title, opts, ok := parseChoices(rest); ok {
			q.title, q.opts = m[1]+". "+title, opts
		}
		q.line, q.end = first[i], nOrig
		if i+1 < len(first) {
			q.end = first[i+1]
		}
		qs = append(qs, q)
	}
	if len(qs) < 2 {
		return "", nil
	}
	return strings.Join(body, "\n"), qs
}

// choiceGate: whether a reply's choice buttons show at all (ok), and whether
// they are live (latest). A reply that repeats a pending plan card's
// questions, or has a plan card after it, shows none.
func (v *chatView) choiceGate(it *chat.Item) (ok, latest bool) {
	s := v.s
	if keptPlan(s.conv, it.Text) != nil {
		return false, false // repeats the unanswered plan card's questions
	}
	latest = true
	after := false
	for _, x := range s.conv.Items {
		if x == it {
			after = true
			continue
		}
		if !after {
			continue
		}
		if x.Kind == chat.Plan {
			return false, false
		}
		if x.Kind == chat.User || x.Kind == chat.Assistant && strings.TrimSpace(x.Text) != "" {
			latest = false
		}
	}
	return true, latest
}

func (v *chatView) choiceCard(it *chat.Item) (string, fyne.CanvasObject) {
	s := v.s
	if s == nil || it.Streaming {
		return "", nil
	}
	text := strings.TrimRight(it.Text, " \n")
	cut := strings.LastIndex(text, "\n")
	line := text[cut+1:]
	title, opts, ok := parseChoices(line)
	var multi []numChoiceQ
	multiBody := ""
	if mb, mq := numberedChoiceQs(text); mq != nil {
		for _, q := range mq {
			if len(q.opts) > 0 {
				multi, multiBody = mq, mb
				break
			}
		}
	}
	if !ok && multi == nil {
		return "", nil
	}
	ok2, latest := v.choiceGate(it)
	if !ok2 {
		return "", nil
	}
	kind := "none"
	if latest {
		kind = "choice"
	}
	buttons := func(prefix string, opts []string) fyne.CanvasObject {
		var btns []fyne.CanvasObject
		for _, o := range opts {
			o := o
			label := o
			if r := []rune(label); len(r) > 60 {
				label = string(r[:59]) + "…"
			}
			n := len(btns)
			btns = append(btns, glowButton(label, theme.ConfirmIcon(), colIdle, v.d.canAnswer(s), v.guarded(func() { v.d.pickChoice(s, it, prefix+choiceReply(n, o)) })))
		}
		return v.buttonRow(btns...)
	}
	label := func(t string) *widget.Label {
		head := widget.NewLabel(t)
		head.TextStyle = fyne.TextStyle{Bold: true}
		head.Wrapping = fyne.TextWrapWord
		head.Alignment = v.align()
		return head
	}
	if multi != nil {
		// Each numbered question gets its own card and buttons. A click
		// only marks the pick (it can be changed); "Send answers" sends all
		// of them in one reply, one line per question (multiChoiceReply).
		// A button each time would drop the other questions' buttons, and
		// sending on the last pick would give no chance to change one.
		if v.choicePicks == nil {
			v.choicePicks = map[*chat.Item]map[int]int{}
		}
		picks := v.choicePicks[it]
		if picks == nil {
			picks = map[int]int{}
			v.choicePicks[it] = picks
		}
		var cards []fyne.CanvasObject
		missing := 0
		for _, q := range multi {
			q := q
			parts := []fyne.CanvasObject{label(q.title)}
			if latest && len(q.opts) > 0 {
				kind += "|" + q.num + ":" + strings.Join(q.opts, "/")
				var btns []fyne.CanvasObject
				for i, o := range q.opts {
					i := i
					icon, tint := fyne.Resource(nil), color.Color(colIdle)
					if p, ok := picks[q.idx]; ok && p == i {
						icon, tint = theme.ConfirmIcon(), colAccent
					}
					btns = append(btns, glowButton(shortLabel(o, 60), icon, tint, v.d.canAnswer(s), v.guarded(func() {
						picks[q.idx] = i
						if iv, ok := v.views[it.Seq]; ok {
							v.refreshItem(it, iv)
						}
					})))
				}
				if _, ok := picks[q.idx]; !ok {
					missing++
				}
				parts = append(parts, v.buttonRow(btns...))
			}
			cards = append(cards, questionCard(parts, latest))
		}
		if latest {
			send := glowButton("Send answers", theme.MailSendIcon(), colIdle, v.d.canAnswer(s) && missing == 0, v.guarded(func() {
				v.d.pickChoice(s, it, multiChoiceReply(multi, picks, nil))
			}))
			cards = append(cards, v.buttonRow(send))
			if missing > 0 {
				note := widget.NewLabel("Pick an answer for each question, then send.")
				note.SizeName = theme.SizeNameCaptionText
				cards = append(cards, note)
			}
		}
		v.noteButtons(it.Seq, kind)
		return multiBody, v.fadeIn(it.Seq, container.NewVBox(cards...))
	}
	parts := []fyne.CanvasObject{label(title)}
	if latest {
		kind += ":" + strings.Join(opts, "/")
		parts = append(parts, buttons("", opts))
	}
	v.noteButtons(it.Seq, kind)
	body := ""
	if cut > 0 {
		body = text[:cut]
	}
	return body, v.fadeIn(it.Seq, questionCard(parts, latest))
}

func (d *desk) pickChoice(s *deskSession, it *chat.Item, option string) {
	if s != nil && s.remote != nil {
		d.remoteChoice(s, it, option)
		return
	}
	after := false
	for _, x := range s.conv.Items {
		if x == it {
			after = true
			continue
		}
		if after && (x.Kind == chat.User || x.Kind == chat.Assistant && strings.TrimSpace(x.Text) != "") {
			return
		}
	}
	d.sendTo(s, option)
	d.chatFor(s, []*chat.Item{it})
}

func (d *desk) refreshChoiceCards(s *deskSession, reply *chat.Item) {
	var prev *chat.Item
	for _, it := range s.conv.Items {
		if it == reply {
			break
		}
		if it.Kind == chat.Assistant && strings.TrimSpace(it.Text) != "" {
			prev = it
		}
	}
	if prev == nil {
		return
	}
	text := strings.TrimRight(prev.Text, " \n")
	if _, _, ok := parseChoices(text[strings.LastIndex(text, "\n")+1:]); !ok {
		return
	}
	for _, v := range d.allViews() {
		if v.s != s {
			continue
		}
		if iv, ok := v.views[prev.Seq]; ok {
			v.refreshItem(prev, iv)
		}
	}
}

// multiChoiceReply: the picks of a multi-question reply as one message, one
// line per question ("Question 2: I pick the second option: Later."), in
// question order, and typed answers ("Question 3: text"). picks and texts are
// keyed by the question's index (several questions can share a number);
// choicePicks reads the picks back.
func multiChoiceReply(qs []numChoiceQ, picks map[int]int, texts map[int]string) string {
	var lines []string
	for _, q := range qs {
		if p, ok := picks[q.idx]; ok && p >= 0 && p < len(q.opts) {
			lines = append(lines, "Question "+q.num+": "+choiceReply(p, q.opts[p]))
		} else if t := strings.TrimSpace(texts[q.idx]); t != "" && len(q.opts) == 0 {
			lines = append(lines, "Question "+q.num+": "+strings.Join(strings.Fields(t), " "))
		}
	}
	return strings.Join(lines, "\n")
}

// answeredPicks: the picks and typed answers an answered multi-question
// reply's user message holds (multiChoiceReply's lines), by question index.
func answeredPicks(qs []numChoiceQ, msg string) (map[int]int, map[int]string) {
	picks, texts := map[int]int{}, map[int]string{}
	lines := strings.Split(msg, "\n")
	at := 0
	for _, q := range qs {
		if at >= len(lines) {
			break
		}
		rest, ok := strings.CutPrefix(lines[at], "Question "+q.num+": ")
		if !ok {
			continue
		}
		at++
		matched := false
		for p, o := range q.opts {
			if rest == choiceReply(p, o) {
				picks[q.idx], matched = p, true
			}
		}
		if !matched && len(q.opts) == 0 {
			texts[q.idx] = rest
		}
	}
	return picks, texts
}

func choiceReply(n int, option string) string {
	which := "next"
	if n >= 0 && n < 4 {
		which = [...]string{"first", "second", "third", "fourth"}[n]
	}
	return "I pick the " + which + " option: " + option + "."
}

// touched: the user scrolled, clicked or dragged in this view; for another
// PC's session it keeps the connection (user 2026-11-21: your touches and
// typing count, output and an open window don't).
func (v *chatView) touched() {
	if v.d != nil {
		v.d.noteActivity(v.s)
	}
}

// shortLabel cuts a button label to n runes.
func shortLabel(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// planAnswers: the card's answers for this plan, with recommended options
// picked to start with.
func (v *chatView) planAnswers(it *chat.Item, qs []planQ) map[int]planAnswer {
	if v.planAns == nil {
		v.planAns = map[*chat.Item]map[int]planAnswer{}
	}
	ans := v.planAns[it]
	if ans == nil {
		ans = map[int]planAnswer{}
		for _, q := range qs {
			switch {
			case q.Kind == qSingle && q.Recommended >= 0:
				ans[q.ID] = planAnswer{Choice: q.Recommended}
			case q.Kind == qMulti && len(q.RecommendedPicks) > 0:
				ans[q.ID] = planAnswer{Picks: append([]int(nil), q.RecommendedPicks...), Choice: -1}
			case q.Kind == qYesNo && q.Recommended >= 0 && q.Recommended < 2:
				// The recommended Yes or No is picked by default too.
				yes := q.Recommended == 0
				ans[q.ID] = planAnswer{Yes: &yes, Choice: -1}
			}
		}
		v.planAns[it] = ans
	}
	return ans
}

// planQuestionForm: each plan question with its control (Yes/No, one of the
// options, any of them, or a text field).
func (v *chatView) planQuestionForm(qs []planQ, ans map[int]planAnswer) fyne.CanvasObject {
	box := container.NewVBox()
	// With centering on, the questions and their options are one centered
	// column (left-aligned inside, like a list) that scales with the chat;
	// an overlong question wraps balanced (balance.go).
	size := theme.Size(theme.SizeNameText)
	var lines []wrapText
	// Numbers are the plan's own, so the reply matches them; a question the
	// Approve button covers is noted, not renumbered away.
	gaps := planQGaps(qs)
	for i, q := range qs {
		q := q
		l := widget.NewLabel(fmt.Sprintf("%d. %s", planQNum(q, i), q.Text))
		l.Wrapping = fyne.TextWrapWord
		lines = append(lines, measureWords(l.Text, size, fyne.TextStyle{}))
		labels := recommendedLabels(q)
		for _, o := range labels {
			lines = append(lines, measureWords(o, size, fyne.TextStyle{}))
		}
		ctl := planControl(q, labels, ans)
		box.Add(container.NewVBox(l, ctl))
	}
	if gaps != "" {
		g := widget.NewLabel(gaps)
		g.SizeName = theme.SizeNameCaptionText
		box.Add(g)
	}
	note := widget.NewLabel("Unanswered questions are sent as \"no answer\".")
	note.SizeName = theme.SizeNameCaptionText
	box.Add(note)
	if !v.d.center {
		return box
	}
	// Labels and radio rows add their padding and the radio icon.
	extra := 2*theme.Size(theme.SizeNameInnerPadding) + theme.Size(theme.SizeNameInlineIcon) + 2*theme.Padding()
	return newColumn(true, func(avail float32) float32 { return groupWidth(avail, lines, extra) }, box)
}

// planControl: a plan question's control (planQuestionForm's and the reply
// overlay's), writing its answer into ans; labels are recommendedLabels(q).
func planControl(q planQ, labels []string, ans map[int]planAnswer) fyne.CanvasObject {
	switch q.Kind {
	case qYesNo:
		r := widget.NewRadioGroup(labels, nil)
		r.Horizontal = true
		if a, ok := ans[q.ID]; ok && a.Yes != nil {
			r.Selected = labels[map[bool]int{true: 0, false: 1}[*a.Yes]]
		}
		r.OnChanged = func(sel string) {
			a := ans[q.ID]
			if sel == "" {
				a.Yes = nil
			} else {
				yes := sel == labels[0]
				a.Yes = &yes
			}
			ans[q.ID] = a
		}
		return r
	case qSingle:
		r := widget.NewRadioGroup(labels, nil)
		if a, ok := ans[q.ID]; ok && a.Choice >= 0 && a.Choice < len(labels) {
			r.Selected = labels[a.Choice]
		}
		r.OnChanged = func(sel string) {
			a := planAnswer{Choice: -1}
			for j, o := range labels {
				if o == sel {
					a.Choice = j
				}
			}
			ans[q.ID] = a
		}
		return r
	case qMulti:
		c := widget.NewCheckGroup(q.Options, nil)
		if a, ok := ans[q.ID]; ok {
			for _, p := range a.Picks {
				if p >= 0 && p < len(q.Options) {
					c.Selected = append(c.Selected, q.Options[p])
				}
			}
		}
		c.OnChanged = func(sel []string) {
			picks := []int{}
			for j, o := range q.Options {
				for _, x := range sel {
					if x == o {
						picks = append(picks, j)
					}
				}
			}
			ans[q.ID] = planAnswer{Picks: picks, Choice: -1}
		}
		return c
	default:
		e := widget.NewMultiLineEntry()
		e.Wrapping = fyne.TextWrapWord
		e.SetMinRowsVisible(2)
		e.SetPlaceHolder("Your answer")
		if a, ok := ans[q.ID]; ok {
			e.Text = a.Text
		}
		e.OnChanged = func(t string) { ans[q.ID] = planAnswer{Text: t, Choice: -1} }
		return e
	}
}

// recommendedLabels: the option labels a question's radio buttons show,
// with "(recommended)" after the recommended one (user 2026-11-22).
func recommendedLabels(q planQ) []string {
	opts := q.Options
	if q.Kind == qYesNo {
		opts = []string{"Yes", "No"}
	}
	out := append([]string(nil), opts...)
	if q.Recommended >= 0 && q.Recommended < len(out) {
		out[q.Recommended] += " (recommended)"
	}
	return out
}
