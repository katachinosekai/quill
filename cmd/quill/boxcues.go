package main

import (
	"image/color"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Who the main message box messages (t34c79f, user 2026-11-23): while the
// box is open over the chat, the rest of the main area (tab strip, toolbar,
// chat, all panes) is dimmed by ~30 %, except the box itself and the main
// message box that messages its assistant. That message box gets a frame and
// a send button in the assistant's colour. The kmem assistant also has a
// name chip at the left of the composer. A click on the dimmed area
// closes the box like Escape. Docked: none of this.

const (
	dimAlpha = 0.3 // the dimming's black at full strength
	cueFrame = 2   // the targeted message box's frame width
)

// colKmemAccent: the kmem assistant's colour (Quill's is colAccent).
var colKmemAccent = color.NRGBA{0x4f, 0xb3, 0xa9, 0xff}

// roleAccent: role's colour for the cues.
func roleAccent(role string) color.NRGBA {
	if role == boxKmem {
		return colKmemAccent
	}
	return colAccent
}

// chipLabel: the name chip's text for role.
func chipLabel(role string) string { return roleDef(role).title + " ▸" }

// accentTheme: the desk theme with role's colour as the primary (the send button).
type accentTheme struct {
	deskTheme
	c color.NRGBA
}

func (t accentTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNamePrimary {
		return t.c
	}
	return t.deskTheme.Color(n, v)
}

// targetCue: a message box's cues while it messages an assistant.
type targetCue struct {
	role     string
	card     fyne.CanvasObject // the message box's card (left un-dimmed)
	frame    *canvas.Rectangle // the card's background: the accent frame
	chip     *fyne.Container   // the name chip's slot, left of the entry
	chipBg   *canvas.Rectangle
	chipText *canvas.Text
	send     *container.ThemeOverride // the send button
	onLayout func()                   // the card was laid out (the dimming's hole follows)
}

// newTargetCue builds the chip and wraps send; attach the card afterwards.
func newTargetCue(send *widget.Button) *targetCue {
	c := &targetCue{}
	c.chipText = canvas.NewText("", colAccent)
	c.chipText.TextSize = fontMeta
	c.chipText.TextStyle.Bold = true
	c.chipBg = canvas.NewRectangle(color.Transparent)
	c.chipBg.CornerRadius = 8
	chip := container.NewStack(c.chipBg, container.New(layout.NewCustomPaddedLayout(3, 3, 7, 7), c.chipText))
	c.chip = container.New(&cueChipLayout{c}, chip)
	c.chip.Hide()
	c.send = container.NewThemeOverride(send, deskTheme{})
	return c
}

// attach: card is the message box's card from bg (its first object the background).
func (c *targetCue) attach(card *fyne.Container) {
	c.card = card
	if len(card.Objects) > 0 {
		c.frame, _ = card.Objects[0].(*canvas.Rectangle)
	}
}

// set shows role's cues, none for "".
func (c *targetCue) set(role string) {
	if c == nil || c.role == role {
		return
	}
	c.role = role
	if role == "" {
		c.chip.Hide()
		relayoutTree(c.card)
		if c.frame != nil {
			c.frame.StrokeWidth = 0
			c.frame.StrokeColor = color.Transparent
			c.frame.Refresh()
		}
		c.send.Theme = deskTheme{}
		c.send.Refresh()
		return
	}
	ac := roleAccent(role)
	c.chipText.Text = chipLabel(role)
	c.chipText.Color = ac
	c.chipBg.FillColor = fade(ac, 0.16)
	c.chipBg.StrokeColor = fade(ac, 0.5)
	c.chipBg.StrokeWidth = 1
	setShown(c.chip, operatorButtonRole(role) != boxQuill)
	c.chip.Refresh()
	relayoutTree(c.card)
	if c.frame != nil {
		c.frame.StrokeColor = ac
		c.frame.StrokeWidth = cueFrame
		c.frame.Refresh()
	}
	c.send.Theme = accentTheme{c: ac}
	c.send.Refresh()
}

// cueChipLayout: the chip at the top of its slot (the first line), and a
// hook for the dimming when the card changes size.
type cueChipLayout struct{ c *targetCue }

func (l *cueChipLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	m := objs[0].MinSize()
	return fyne.NewSize(m.Width, m.Height+theme.Padding())
}

func (l *cueChipLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	m := objs[0].MinSize()
	objs[0].Move(fyne.NewPos(0, theme.Padding()))
	objs[0].Resize(fyne.NewSize(size.Width, m.Height))
	if l.c.onLayout != nil {
		l.c.onLayout()
	}
}

// newCue: in's cues, with the dimming following its card.
func (d *desk) newCue(in *sendEntry, send *widget.Button) *targetCue {
	c := newTargetCue(send)
	c.onLayout = d.queueDim
	in.cue = c
	return c
}

// syncCues: the cues on the message box that messages an assistant, none
// elsewhere; then the dimming.
func (d *desk) syncCues() {
	if d.input != nil {
		d.input.cue.set(d.targeted(d.input))
	}
	for _, p := range d.panes {
		if p.input != nil {
			p.input.cue.set(d.targeted(p.input))
		}
	}
	d.syncDim()
}

// The dimming: pieces tiling the main area around its holes (the box and
// the targeted message box), in the animation layer (over the main area,
// under the ghost).

type dimLayer struct {
	root   *fyne.Container
	pieces []*dimPiece
	on     bool    // wanted
	level  float32 // 0..1 of dimAlpha now
	anim   *fyne.Animation
	queued bool

	lastFade time.Duration // the last fade's length (tests)
}

// dimPiece: one dimmed rect; a click closes the box, hover and drags stop here.
type dimPiece struct {
	widget.BaseWidget
	d    *desk
	rect *canvas.Rectangle
}

func newDimPiece(d *desk) *dimPiece {
	p := &dimPiece{d: d, rect: canvas.NewRectangle(color.Transparent)}
	p.ExtendBaseWidget(p)
	return p
}

func (p *dimPiece) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(p.rect) }
func (p *dimPiece) Tapped(e *fyne.PointEvent)           { p.d.dimTapped(e.AbsolutePosition) }
func (p *dimPiece) SecondaryTapped(*fyne.PointEvent)    {}
func (p *dimPiece) MouseIn(*fyne.PointEvent)            {}
func (p *dimPiece) MouseMoved(*fyne.PointEvent)         {}
func (p *dimPiece) MouseOut()                           {}
func (p *dimPiece) Dragged(*fyne.DragEvent)             {}
func (p *dimPiece) DragEnd()                            {}

func (d *desk) dimTapped(abs fyne.Position) {
	for _, bt := range d.abox.btns {
		btn := bt.btn
		if btn == nil || !btn.Visible() {
			continue
		}
		pos := abs.Subtract(d.app.Driver().AbsolutePositionForObject(btn))
		sz := btn.Size()
		if pos.X >= 0 && pos.X < sz.Width && pos.Y >= 0 && pos.Y < sz.Height {
			btn.Tapped(&fyne.PointEvent{AbsolutePosition: abs, Position: pos})
			return
		}
	}
	if !d.assistOverlayOpen() {
		return
	}
	chip := d.tabChipAt(abs)
	d.closeAssistBox()
	if chip != nil && chip.onTap != nil {
		chip.onTap()
	}
}

// tabChipAt: the tab strip's tab under the absolute point abs, or nil.
func (d *desk) tabChipAt(abs fyne.Position) *tabChip {
	if d.strip == nil || d.strip.box == nil || fyne.CurrentApp() == nil {
		return nil
	}
	drv := fyne.CurrentApp().Driver()
	for _, o := range d.strip.box.Objects {
		c, ok := o.(*tabChip)
		if !ok || !c.Visible() {
			continue
		}
		p, sz := drv.AbsolutePositionForObject(c), c.Size()
		if abs.X >= p.X && abs.X < p.X+sz.Width && abs.Y >= p.Y && abs.Y < p.Y+sz.Height {
			return c
		}
	}
	return nil
}

func dimColor(level float32) color.Color {
	return color.NRGBA{0, 0, 0, uint8(255 * dimAlpha * level)}
}

// dimShown: the dimming shows (over the chat, or fading out).
func (d *desk) dimShown() bool {
	b := d.abox
	return b != nil && b.dim != nil && (b.dim.on || b.dim.level > 0)
}

// syncDim turns the dimming on over the chat and off otherwise (faded with
// the box animation's length; none with Reduce motion), then lays it out.
func (d *desk) syncDim() {
	b := d.abox
	if b == nil || b.dim == nil {
		return
	}
	m := b.dim
	on := d.assistOverlayOpen()
	if on == m.on {
		d.layoutDim()
		return
	}
	m.on = on
	if m.anim != nil {
		m.anim.Stop()
		m.anim = nil
	}
	from, to := m.level, float32(0)
	if on {
		to = 1
	}
	dur := d.assistBoxDuration()
	m.lastFade = 0
	if dur <= 0 || from == to {
		m.level = to
		d.layoutDim()
		return
	}
	m.lastFade = dur
	d.layoutDim()
	var a *fyne.Animation
	a = fyne.NewAnimation(dur, func(f float32) {
		if m.anim != a {
			return
		}
		m.level = from + (to-from)*f
		if f >= 1 {
			m.level = to
			m.anim = nil
			d.layoutDim()
			return
		}
		for _, p := range m.pieces {
			p.rect.FillColor = dimColor(m.level)
			p.rect.Refresh()
		}
	})
	a.Curve = fyne.AnimationLinear
	m.anim = a
	a.Start()
}

// queueDim lays the dimming out after the current layout pass (positions
// settle after sizes).
func (d *desk) queueDim() {
	b := d.abox
	if b == nil || b.dim == nil || !d.dimShown() || b.dim.queued {
		return
	}
	b.dim.queued = true
	d.afterUI(0, func() {
		b.dim.queued = false
		d.layoutDim()
	})
}

// layoutDim tiles the animation layer around the holes.
func (d *desk) layoutDim() {
	b := d.abox
	if b == nil || b.dim == nil {
		return
	}
	m := b.dim
	var rects []rectF
	if d.dimShown() {
		d.layoutOverlay()
		size := b.ghostL.Size()
		var holes []rectF
		if r, ok := d.underRect(b.card, b.layer); ok && len(b.layer.Objects) != 0 {
			holes = append(holes, r)
		}
		if in := d.main.in; in != nil && in.cue != nil && in.cue.card != nil {
			if r, ok := d.layerRect(in.cue.card); ok {
				holes = append(holes, r)
			}
		}
		rects = dimRects(size, holes)
	}
	for len(m.pieces) < len(rects) {
		p := newDimPiece(d)
		m.pieces = append(m.pieces, p)
		m.root.Objects = append(m.root.Objects, p)
	}
	col := dimColor(m.level)
	for i, p := range m.pieces {
		if i >= len(rects) {
			p.Hide()
			continue
		}
		p.Move(rects[i].pos)
		p.Resize(rects[i].size)
		p.rect.FillColor = col
		p.Show()
		p.rect.Refresh()
	}
	m.root.Resize(b.ghostL.Size())
	canvas.Refresh(m.root)
}

// underRect: o's rect in the animation layer, by its parent's position (o
// may be hidden while the box animates).
func (d *desk) underRect(o, parent fyne.CanvasObject) (rectF, bool) {
	r, ok := d.layerRect(parent)
	if !ok || o.Size().IsZero() {
		return rectF{}, false
	}
	return rectF{r.pos.Add(o.Position()), o.Size()}, true
}

// dimRects: rects covering size except holes (a grid on the holes' edges;
// each row's runs merged, equal runs of adjacent rows merged).
func dimRects(size fyne.Size, holes []rectF) []rectF {
	if size.IsZero() {
		return nil
	}
	xs := []float32{0, size.Width}
	ys := []float32{0, size.Height}
	clip := func(v, hi float32) float32 { return max(0, min(hi, v)) }
	for _, h := range holes {
		xs = append(xs, clip(h.pos.X, size.Width), clip(h.pos.X+h.size.Width, size.Width))
		ys = append(ys, clip(h.pos.Y, size.Height), clip(h.pos.Y+h.size.Height, size.Height))
	}
	xs, ys = uniqSorted(xs), uniqSorted(ys)
	inHole := func(x, y float32) bool {
		for _, h := range holes {
			if x > h.pos.X && x < h.pos.X+h.size.Width && y > h.pos.Y && y < h.pos.Y+h.size.Height {
				return true
			}
		}
		return false
	}
	var out []rectF
	open := map[[2]float32]int{} // a run's x0,x1 -> its rect in out, ending at the previous row
	for j := 0; j+1 < len(ys); j++ {
		y0, y1 := ys[j], ys[j+1]
		next := map[[2]float32]int{}
		for i := 0; i+1 < len(xs); {
			if inHole((xs[i]+xs[i+1])/2, (y0+y1)/2) {
				i++
				continue
			}
			k := i
			for k+1 < len(xs) && !inHole((xs[k]+xs[k+1])/2, (y0+y1)/2) {
				k++
			}
			key := [2]float32{xs[i], xs[k]}
			if n, ok := open[key]; ok {
				out[n].size.Height = y1 - out[n].pos.Y
				next[key] = n
			} else {
				out = append(out, rectF{fyne.NewPos(xs[i], y0), fyne.NewSize(xs[k]-xs[i], y1-y0)})
				next[key] = len(out) - 1
			}
			i = k
		}
		open = next
	}
	return out
}

func uniqSorted(v []float32) []float32 {
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	out := v[:0]
	for i, x := range v {
		if i == 0 || x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}
