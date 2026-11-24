package main

import (
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The assistant box (docs/design/quill-operator.md §3.2-3.6, phase 2 sub C).
// One box shows one assistant at a time (the Quill operator or kmem), either
// as an overlay over the chat history (all panes' histories in split view;
// the tab row and the message boxes stay visible) or docked as a panel right
// of the chat. Each role registers what it shows (setAssistBoxView): its
// content with its OWN message box. Closing only hides the box: the
// assistant keeps running.
//
// API for the role owners:
//   d.setAssistBoxView(role, v)    register the role's content (kmem: D, quill: E)
//   d.attachAssistBox(r, t, hint)  generic content: chat view + own message box
//                                  routed to the role's session, header status,
//                                  and the role's hidden host (hidden.go)
//   d.openAssistBox(role) / d.closeAssistBox() / d.toggleAssistBox(role)
//   d.assistBoxRole() / d.assistBoxShowing(role) / d.assistBoxDocked()
//   d.assistBubble(role), d.setAssistRunDot(role, on): the button's badge and
//                                  dot for assistRole.bubble / .dot
// The button's dot and badge follow the role's assistant (assist.go): no dot
// when stopped, the idle dot while it runs, the working dot (pulsing) while a
// turn or subagents run; the badge is the role's bubble (updateAssistBadge).

const (
	boxQuill = roleQuill
	boxKmem  = roleKmem

	prefAssistDock   = "assistDock." // + role: docked (bool), per assistant
	prefAssistDockW  = "assistDockW" // the docked panel's width (shared)
	prefBoxAnim      = "assistBoxAnim"
	prefReduceMotion = "reduceMotion" // with prefReduceMotion+"Set" once the user chose

	dockMinW    = 360  // the docked panel's minimum width
	dockChatMin = 480  // the chat column keeps at least this
	dockPaneMin = 300  // per pane in split view
	dockFrac    = 0.38 // the default panel width, of the main area
	dockHandleW = 8
	boxInset    = 8 // the overlay's margin inside the chat history

	boxGrowPart = 0.7 // the rect grows over 70 %; the content fades in over the rest

	quillNotSetUp = "Quill operator not set up yet"
)

// boxAnimNames / boxAnimKeys: the "Assistant box animation" setting (its own
// setting, user 2026-11-23 #13), and its length in ms.
var (
	boxAnimNames = []string{"Off", "Fast", "Normal", "Slow"}
	boxAnimKeys  = []string{"off", "fast", "normal", "slow"}
	boxAnimMS    = map[string]int{"off": 0, "fast": 140, "normal": 220, "slow": 320}
)

// assistBoxView is what a role shows in the box. content is built once and
// kept across open and close (unsent text stays). Every func may be nil.
type assistBoxView struct {
	title   string                   // "Quill operator", "kmem assistant"
	content fyne.CanvasObject        // below the box's header (title, status, Dock, ×)
	input   fyne.Focusable           // its own message box: focused on open
	inputFn func() fyne.Focusable    // instead of input when it is built later (kmem: after the chat attaches)
	popOut  func()                   // a Pop out button in the header (kmem: the box as its own window)
	escape  func() bool              // Escape inside the box, before it closes (a popup, a selection); true = handled
	status  func() statusInfo        // the header's status line (refreshed every second while open)
	shown   func(on bool)            // the box opened (true) or closed / switched away (false)
	inRow   func() fyne.CanvasObject // its own message box row: hidden over the chat (mainbox.go)
}

type assistBox struct {
	role   string // the role shown, "" when closed
	docked bool   // shown as the side panel (wanted and fits)
	views  map[string]*assistBoxView
	btns   map[string]*assistButton
	spare  map[string]*assistBoxView // placeholders for unregistered roles

	area    *fyne.Container // chatHost + layer (the chat history area)
	layer   *fyne.Container // the overlay slot over the chat history
	panel   *fyne.Container // the docked slot
	handle  *dockHandle
	ghostL  *fyne.Container // the animation layer: the dimming, then the ghost
	dim     *dimLayer
	ghost   *canvas.Rectangle
	card    *fyne.Container
	body    *fyne.Container
	veil    *canvas.Rectangle
	outline *canvas.Rectangle
	headRow *fyne.Container
	title   *widget.Label
	status  *widget.Label
	stPulse pulseVeil // the still-working pulse over status (status.go)
	note    *widget.Label
	dockBtn *widget.Button
	popBtn  *widget.Button
	closeX  *widget.Button

	anim    *fyne.Animation
	lastDur time.Duration // the last animation's length (tests)
	rootW   float32       // the main area's width at the last layout
	tickOn  bool
	pulse   bool
}

// assistButton: a role's toolbar button and its portrait twin, each with a
// badge bubble and a run dot.
type assistButton struct {
	role    string
	btn     *widget.Button
	box     fyne.CanvasObject
	bubble  *bubble
	dot     *canvas.Circle
	side    *widget.Button
	sideBox fyne.CanvasObject
	sideBub *bubble
	sideDot *canvas.Circle
}

var wrapKmemDot sync.Once

// box: the desk's assistant box state (created on first use).
func (d *desk) box() *assistBox {
	if d.abox != nil {
		return d.abox
	}
	b := &assistBox{views: map[string]*assistBoxView{}, btns: map[string]*assistButton{}, spare: map[string]*assistBoxView{}}
	d.abox = b
	b.layer = container.NewWithoutLayout()
	b.panel = container.NewWithoutLayout()
	b.handle = newDockHandle(d)
	b.handle.Hide()
	b.ghost = canvas.NewRectangle(colInput)
	b.ghost.CornerRadius = 12
	b.ghost.StrokeColor = fade(colAccent, 0.5)
	b.ghost.StrokeWidth = 1
	b.ghost.Hide()
	b.dim = &dimLayer{root: container.NewWithoutLayout()}
	b.ghostL = container.NewWithoutLayout(b.dim.root, b.ghost)
	b.title = boldLabel("")
	b.status = caption("")
	b.status.Truncation = fyne.TextTruncateEllipsis
	b.stPulse.tint = func(f float64) color.RGBA { return veilOver(colInput, f) }
	b.stPulse.veil = canvas.NewRectangle(b.stPulse.color(0))
	b.note = caption("")
	b.note.Hide()
	b.dockBtn = widget.NewButtonWithIcon("Dock", theme.ViewRestoreIcon(), func() {
		if b.role != "" {
			d.setAssistDock(b.role, !d.assistDockWanted(b.role))
		}
	})
	b.dockBtn.Importance = widget.LowImportance
	b.closeX = widget.NewButtonWithIcon("", theme.CancelIcon(), d.closeAssistBox)
	b.closeX.Importance = widget.LowImportance
	b.popBtn = widget.NewButtonWithIcon("Pop out", theme.ViewFullScreenIcon(), func() {
		if b.role == "" {
			return
		}
		if f := d.viewOf(b.role).popOut; f != nil {
			d.closeAssistBox()
			if b.anim != nil {
				b.anim.Stop()
				b.anim = nil
				d.endBoxAnim()
			}
			b.body.Objects = nil // the content moves to the window
			b.body.Refresh()
			f()
		}
	})
	b.popBtn.Importance = widget.LowImportance
	b.popBtn.Hide()
	b.headRow = container.NewBorder(nil, nil, nil, container.NewHBox(b.popBtn, b.dockBtn, b.closeX), b.title)
	head := container.NewVBox(b.headRow, container.NewStack(b.status, b.stPulse.veil), b.note, widget.NewSeparator())
	b.body = container.NewStack()
	bgRect := canvas.NewRectangle(colInput)
	bgRect.CornerRadius = 12
	b.outline = canvas.NewRectangle(color.Transparent)
	b.outline.CornerRadius = 12
	b.outline.StrokeWidth = 1
	b.outline.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x24}
	b.veil = canvas.NewRectangle(colInput)
	b.veil.CornerRadius = 12
	b.veil.Hide()
	b.card = container.NewStack(bgRect, container.NewPadded(container.NewBorder(head, nil, nil, nil, b.body)), b.outline, b.veil)
	b.card.Hide()
	wrapKmemDot.Do(func() {
		if kmemRole != nil {
			prev := kmemRole.dot
			kmemRole.dot = func(d *desk, on bool) {
				if prev != nil {
					prev(d, on)
				}
				d.syncAssistButtons()
			}
		}
	})
	return b
}

// setAssistBoxView registers role's content (nil: none). Register before the
// first open; an open box switches to the new content.
func (d *desk) setAssistBoxView(role string, v *assistBoxView) {
	b := d.box()
	if v == nil {
		delete(b.views, role)
	} else {
		b.views[role] = v
	}
	if b.role == role {
		d.showBoxContent(role)
	}
}

// assistBoxRole: the role shown in the box, "" when closed.
func (d *desk) assistBoxRole() string {
	if d.abox == nil {
		return ""
	}
	return d.abox.role
}

// assistBoxShowing: role is shown (overlay or docked; for seen and alert).
func (d *desk) assistBoxShowing(role string) bool { return role != "" && d.assistBoxRole() == role }

// assistBoxDocked: the box is open as the side panel.
func (d *desk) assistBoxDocked() bool { return d.assistBoxRole() != "" && d.abox.docked }

// assistOverlayOpen: the box is open over the chat history.
func (d *desk) assistOverlayOpen() bool { return d.assistBoxRole() != "" && !d.abox.docked }

func (d *desk) viewOf(role string) *assistBoxView {
	b := d.box()
	if v := b.views[role]; v != nil {
		return v
	}
	v := b.spare[role]
	if v == nil {
		title := roleDef(role).title
		if role == boxQuill {
			title = "Quill operator"
		}
		l := widget.NewLabelWithStyle(quillNotSetUp, fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
		if role != boxQuill {
			l.SetText(title + " not set up yet")
		}
		v = &assistBoxView{title: title, content: container.NewCenter(l)}
		b.spare[role] = v
	}
	return v
}

// toggleAssistBox: the button and the shortcut: open role, or close it when
// it is the one shown.
func (d *desk) toggleAssistBox(role string) {
	if d.assistBoxRole() == role {
		d.closeAssistBox()
		return
	}
	d.openAssistBox(role)
}

// openAssistBox shows role in the box, in its remembered state (overlay or
// docked). Another role open switches the content (cross-fade).
func (d *desk) openAssistBox(role string) {
	b := d.box()
	if role == boxKmem {
		if w := d.kmemWin; w != nil && w.win != nil && !w.closed {
			d.popOutKmem() // popped out: raise the window (the content is there)
			return
		}
		if b.views[role] == nil {
			if !d.kmemAvailable() {
				d.popOutKmem() // says kmem is not available
				return
			}
			d.registerKmemBox()
		}
	}
	if b.role == role {
		d.focusBox()
		return
	}
	if b.anim != nil {
		b.anim.Stop()
		b.anim = nil
		d.endBoxAnim()
	}
	prev, wasDocked := b.role, b.docked
	from, fromOK := d.boxRect()
	if prev != "" {
		if v := d.viewOf(prev); v.shown != nil {
			v.shown(false)
		}
	}
	b.role = role
	d.showBoxContent(role)
	d.placeBox(d.assistDockWanted(role) && d.dockFits())
	d.syncMainTarget()
	v := d.viewOf(role)
	if v.shown != nil {
		v.shown(true)
	}
	d.refreshBoxHeader()
	d.focusBox()
	d.syncAssistButtons()
	switch {
	case prev != "" && wasDocked == b.docked:
		d.animateBox(boxFade, rectF{}, rectF{}, nil)
	case prev != "" && fromOK:
		to, _ := d.boxRect()
		d.animateBox(boxGrow, from, to, nil)
	default:
		btn, _ := d.buttonRect(role)
		to, _ := d.boxRect()
		d.animateBox(boxGrow, btn, to, nil)
	}
}

// closeAssistBox hides the box (the assistant keeps running) and gives the
// focus back to the main message box.
func (d *desk) closeAssistBox() {
	b := d.box()
	if b.role == "" {
		return
	}
	role := b.role
	if b.anim != nil {
		b.anim.Stop()
		b.anim = nil
		d.endBoxAnim()
	}
	from, _ := d.boxRect()
	to, _ := d.buttonRect(role)
	if v := d.viewOf(role); v.shown != nil {
		v.shown(false)
	}
	b.role = ""
	wasDocked := b.docked
	d.syncMainTarget()
	d.syncAssistButtons()
	d.focusMain()
	d.animateBox(boxShrink, from, to, func() {
		if b.role == "" {
			d.placeBox(false)
			b.card.Hide()
			b.body.Objects = nil
			b.body.Refresh()
		}
	})
	if wasDocked {
		b.docked = false
		d.layoutRoot()
	}
}

// closeAssistOverlay: a tab, sidebar row, Ctrl+T or Ctrl+1-9 closes the
// overlay before switching (Q16); a docked panel stays.
func (d *desk) closeAssistOverlay() {
	if d.assistOverlayOpen() && !d.keepBox {
		d.closeAssistBox()
	}
}

func (d *desk) showBoxContent(role string) {
	b := d.box()
	v := d.viewOf(role)
	setLabel(b.title, v.title)
	if v.popOut != nil {
		b.popBtn.Show()
	} else {
		b.popBtn.Hide()
	}
	b.headRow.Refresh() // the title's new width
	if v.content != nil {
		b.body.Objects = []fyne.CanvasObject{v.content}
	} else {
		b.body.Objects = nil
	}
	b.body.Refresh()
	b.card.Show()
}

// focusBox focuses the box's own message box.
func (d *desk) focusBox() {
	b := d.box()
	if b.role == "" || d.win == nil {
		return
	}
	if in := d.main.in; in != nil && d.main.role == b.role {
		d.win.Canvas().Focus(in) // the overlay's message box is the main one
	} else if in := d.viewOf(b.role).focusable(); in != nil {
		d.win.Canvas().Focus(in)
	}
	d.syncBoxOutline()
}

// focusMain: the focus back to the main message box (the focused pane's in split view).
func (d *desk) focusMain() {
	if d.win == nil {
		return
	}
	if d.split() && d.focused != nil && d.focused.input != nil {
		d.win.Canvas().Focus(d.focused.input)
	} else if d.input != nil && !d.split() {
		d.win.Canvas().Focus(d.input)
	}
	d.syncBoxOutline()
}

// boxFocused: the focus is in the box's own message box.
func (d *desk) boxFocused() bool {
	b := d.abox
	if b == nil || b.role == "" || d.win == nil {
		return false
	}
	f := d.win.Canvas().Focused()
	if d.main.in != nil && d.main.role == b.role && f == d.main.in {
		return true // over the chat, the main message box is the box's
	}
	in := d.viewOf(b.role).focusable()
	return in != nil && f != nil && f == in
}

// syncBoxOutline: an accent outline while the box has the focus.
func (d *desk) syncBoxOutline() {
	b := d.abox
	if b == nil {
		return
	}
	c := color.Color(color.NRGBA{0xff, 0xff, 0xff, 0x24})
	w := float32(1)
	if d.boxFocused() {
		c, w = colAccent, 2
	}
	if b.outline.StrokeColor != c || b.outline.StrokeWidth != w {
		b.outline.StrokeColor, b.outline.StrokeWidth = c, w
		b.outline.Refresh()
	}
}

// assistBoxEscape: Escape inside the box: a popup or menu, then the role's
// own (a selection in its chat), then the box closes. It never interrupts
// the assistant (the box's stop button and Ctrl+Enter do).
func (d *desk) assistBoxEscape() bool {
	b := d.abox
	if b == nil || b.role == "" {
		return false
	}
	if d.win != nil && d.win.Canvas().Overlays().Top() != nil && d.escapeOverlay() {
		return true
	}
	if v := d.viewOf(b.role); v.escape != nil && v.escape() {
		return true
	}
	d.closeAssistBox()
	return true
}

// The remembered dock state and the panel width.

func (d *desk) assistDockWanted(role string) bool {
	return d.app.Preferences().Bool(prefAssistDock + role)
}

// setAssistDock docks or undocks role (remembered per assistant); the open
// box moves (animated) when it shows role.
func (d *desk) setAssistDock(role string, on bool) {
	d.app.Preferences().SetBool(prefAssistDock+role, on)
	b := d.box()
	if b.role != role {
		return
	}
	docked := on && d.dockFits()
	if docked == b.docked {
		d.refreshBoxHeader()
		return
	}
	from, ok := d.boxRect()
	d.placeBox(docked)
	d.syncMainTarget()
	d.refreshBoxHeader()
	if ok {
		to, _ := d.boxRect()
		d.animateBox(boxGrow, from, to, nil)
	}
	d.focusBox()
}

// dockMinChat: the width the chat side keeps (all panes in split view).
func (d *desk) dockMinChat() float32 {
	if n := len(d.panes); n > 1 {
		return max(dockChatMin, float32(n)*dockPaneMin+float32(n-1)*paneGap)
	}
	return dockChatMin
}

// dockWidth: the panel's width in a main area total wide; 0 if it doesn't fit.
func (d *desk) dockWidth(total float32) float32 {
	w := float32(d.app.Preferences().Float(prefAssistDockW))
	if w <= 0 {
		w = total * dockFrac
	}
	w = max(w, dockMinW)
	if room := total - dockHandleW - d.dockMinChat(); w > room {
		w = room
	}
	if w < dockMinW {
		return 0
	}
	return w
}

func (d *desk) dockFits() bool {
	b := d.box()
	w := b.rootW
	if w <= 0 && d.mainRoot != nil {
		w = d.mainRoot.Size().Width
	}
	return d.dockWidth(w) > 0
}

// placeBox moves the card into the panel (docked) or the overlay layer.
func (d *desk) placeBox(docked bool) {
	b := d.box()
	b.docked = docked
	b.layer.Objects, b.panel.Objects = nil, nil
	if b.role != "" || b.anim != nil {
		if docked {
			b.panel.Objects = []fyne.CanvasObject{b.card}
		} else {
			b.layer.Objects = []fyne.CanvasObject{b.card}
		}
	}
	d.layoutRoot()
	d.layoutArea()
}

func (d *desk) layoutRoot() {
	if d.mainRoot != nil {
		(&assistRootLayout{d}).Layout(d.mainRoot.Objects, d.mainRoot.Size())
		canvas.Refresh(d.mainRoot)
	}
}

func (d *desk) layoutArea() {
	if b := d.abox; b != nil && b.area != nil {
		(&boxAreaLayout{d}).Layout(b.area.Objects, b.area.Size())
		canvas.Refresh(b.area)
	}
}

func (d *desk) refreshBoxHeader() {
	b := d.abox
	if b == nil || b.role == "" {
		return
	}
	v := d.viewOf(b.role)
	var st statusInfo
	if v.status != nil {
		st = v.status()
	}
	setLabel(b.status, st.text)
	b.stPulse.set(d, st.pulse)
	if b.docked {
		b.dockBtn.SetText("Undock")
	} else {
		b.dockBtn.SetText("Dock")
	}
	fits := d.dockFits()
	note := ""
	if !fits && d.assistDockWanted(b.role) {
		note = "window too narrow to dock; shown over the chat until it is wider"
	}
	if !fits && !b.docked {
		b.dockBtn.Disable()
	} else {
		b.dockBtn.Enable()
	}
	setLabel(b.note, note)
	if note != "" {
		b.note.Show()
	} else {
		b.note.Hide()
	}
}

// The layouts.

// assistRootLayout: the main area (objs[0]) with the docked panel (objs[1])
// and its drag handle (objs[2]) on the right; objs[3] is the animation layer.
type assistRootLayout struct{ d *desk }

func (l *assistRootLayout) MinSize(objs []fyne.CanvasObject) fyne.Size { return objs[0].MinSize() }

func (l *assistRootLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	d := l.d
	b := d.box()
	b.rootW = size.Width
	main, panel, handle, ghostL := objs[0], objs[1], objs[2], objs[3]
	pw := d.dockWidth(size.Width)
	if b.role != "" && d.assistDockWanted(b.role) && (pw > 0) != b.docked {
		// The window was resized across the fit: overlay <-> panel.
		b.docked = pw > 0
		b.layer.Objects, b.panel.Objects = nil, nil
		if b.docked {
			b.panel.Objects = []fyne.CanvasObject{b.card}
		} else {
			b.layer.Objects = []fyne.CanvasObject{b.card}
		}
		defer d.layoutArea()
		defer d.refreshBoxHeader()
		defer d.syncMainTarget()
	}
	ghostL.Move(fyne.NewPos(0, 0))
	ghostL.Resize(size)
	defer d.queueDim()
	if !b.docked || pw <= 0 {
		main.Move(fyne.NewPos(0, 0))
		main.Resize(size)
		panel.Hide()
		handle.Hide()
		return
	}
	mw := size.Width - pw - dockHandleW
	main.Move(fyne.NewPos(0, 0))
	main.Resize(fyne.NewSize(mw, size.Height))
	handle.Move(fyne.NewPos(mw, 0))
	handle.Resize(fyne.NewSize(dockHandleW, size.Height))
	handle.Show()
	panel.Move(fyne.NewPos(mw+dockHandleW, 0))
	panel.Resize(fyne.NewSize(pw, size.Height))
	panel.Show()
	for _, o := range b.panel.Objects {
		o.Move(fyne.NewPos(0, boxInset))
		o.Resize(fyne.NewSize(pw-boxInset, size.Height-2*boxInset))
	}
}

// boxAreaLayout: the chat history (objs[0]) with the overlay layer (objs[1])
// on top; the card covers the history only, all panes' in split view.
type boxAreaLayout struct{ d *desk }

func (l *boxAreaLayout) MinSize(objs []fyne.CanvasObject) fyne.Size { return objs[0].MinSize() }

func (l *boxAreaLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	objs[1].Move(fyne.NewPos(0, 0))
	objs[1].Resize(size)
	pos, sz := l.d.overlayRect(size)
	for _, o := range l.d.box().layer.Objects {
		o.Move(pos)
		o.Resize(sz)
	}
	l.d.queueDim()
}

// overlayRect: the box's rect in the chat history area: the history with a
// margin; in split view one box over all panes' histories (their own message
// boxes stay visible).
func (d *desk) overlayRect(size fyne.Size) (fyne.Position, fyne.Size) {
	if d.split() {
		var x0, y0, x1, y1 float32
		found := false
		for _, p := range d.panes {
			pos, sz, ok := p.historyRect()
			if !ok {
				continue
			}
			if !found {
				x0, y0, x1, y1 = pos.X, pos.Y, pos.X+sz.Width, pos.Y+sz.Height
				found = true
				continue
			}
			x0, y0 = min(x0, pos.X), max(y0, pos.Y)
			x1, y1 = max(x1, pos.X+sz.Width), min(y1, pos.Y+sz.Height)
		}
		if found && x1 > x0 && y1 > y0 {
			return fyne.NewPos(x0, y0), fyne.NewSize(x1-x0, y1-y0)
		}
	}
	return fyne.NewPos(boxInset, boxInset), fyne.NewSize(max(0, size.Width-2*boxInset), max(0, size.Height-2*boxInset))
}

// The animation (§3.3): a plain rounded rect grows from the button to the
// box, then the content fades in; closing plays it in reverse. Switching the
// role in an open box cross-fades.

type rectF struct {
	pos  fyne.Position
	size fyne.Size
}

type boxAnimKind int

const (
	boxGrow boxAnimKind = iota
	boxShrink
	boxFade
)

// assistBoxDuration: the "Assistant box animation" setting; 0 with Reduce
// motion or without animations (tests).
func (d *desk) assistBoxDuration() time.Duration {
	if !d.animate || d.reduceMotion() {
		return 0
	}
	return time.Duration(boxAnimMS[d.boxAnimKey()]) * time.Millisecond
}

func (d *desk) boxAnimKey() string {
	k := d.app.Preferences().StringWithFallback(prefBoxAnim, "normal")
	if _, ok := boxAnimMS[k]; !ok {
		return "normal"
	}
	return k
}

func boxAnimName(key string) string {
	for i, k := range boxAnimKeys {
		if k == key {
			return boxAnimNames[i]
		}
	}
	return "Normal"
}

func boxAnimKeyOf(name string) string {
	for i, n := range boxAnimNames {
		if n == name {
			return boxAnimKeys[i]
		}
	}
	return "normal"
}

func lerpRect(a, b rectF, t float32) rectF {
	l := func(x, y float32) float32 { return x + (y-x)*t }
	return rectF{fyne.NewPos(l(a.pos.X, b.pos.X), l(a.pos.Y, b.pos.Y)), fyne.NewSize(l(a.size.Width, b.size.Width), l(a.size.Height, b.size.Height))}
}

func easeOut(t float32) float32 { return 1 - (1-t)*(1-t) }

func (d *desk) animateBox(kind boxAnimKind, from, to rectF, done func()) {
	b := d.box()
	dur := d.assistBoxDuration()
	b.lastDur = dur
	setGhost := func(r rectF) {
		b.ghost.Move(r.pos)
		b.ghost.Resize(r.size)
		b.ghost.Show()
		b.ghost.Refresh()
	}
	setVeil := func(a float64) {
		if a <= 0 {
			b.veil.Hide()
			return
		}
		b.veil.FillColor = fade(colInput, a)
		b.veil.Show()
		b.veil.Refresh()
	}
	if dur <= 0 || (kind != boxFade && (from.size.IsZero() || to.size.IsZero())) {
		b.ghost.Hide()
		setVeil(0)
		if kind != boxShrink {
			b.card.Show()
		}
		if done != nil {
			done()
		}
		return
	}
	var tick func(f float32)
	switch kind {
	case boxGrow:
		b.card.Hide()
		tick = func(f float32) {
			if f < boxGrowPart {
				setGhost(lerpRect(from, to, easeOut(f/boxGrowPart)))
				return
			}
			b.ghost.Hide()
			b.card.Show()
			setVeil(float64(1 - (f-boxGrowPart)/(1-boxGrowPart)))
		}
	case boxShrink:
		tick = func(f float32) {
			if p := float32(1 - boxGrowPart); f < p {
				setVeil(float64(f / p))
				return
			}
			b.card.Hide()
			setVeil(0)
			t := (f - (1 - boxGrowPart)) / boxGrowPart
			setGhost(lerpRect(from, to, t*t))
		}
	case boxFade:
		tick = func(f float32) { setVeil(float64(1 - f)) }
	}
	a := fyne.NewAnimation(dur, func(f float32) {
		tick(f)
		if f >= 1 {
			b.anim = nil
			b.ghost.Hide()
			setVeil(0)
			if done != nil {
				done()
			}
		}
	})
	a.Curve = fyne.AnimationLinear
	b.anim = a
	tick(0)
	a.Start()
}

// endBoxAnim settles a stopped animation: no ghost, no veil, the card as the state says.
func (d *desk) endBoxAnim() {
	b := d.box()
	b.ghost.Hide()
	b.veil.Hide()
	if b.role != "" {
		b.card.Show()
	} else {
		d.placeBox(false)
		b.card.Hide()
	}
}

// boxRect / buttonRect: rects in the animation layer's coordinates.
func (d *desk) boxRect() (rectF, bool) {
	b := d.box()
	if b.role == "" || !b.card.Visible() && b.anim == nil {
		return rectF{}, false
	}
	return d.layerRect(b.card)
}

func (d *desk) buttonRect(role string) (rectF, bool) {
	bt := d.box().btns[role]
	if bt == nil {
		return rectF{}, false
	}
	for _, o := range []fyne.CanvasObject{bt.btn, bt.side} {
		if o == nil || !o.Visible() || o.Size().IsZero() {
			continue
		}
		if d.layoutMode() == layoutVertical && o == bt.btn && bt.side != nil && bt.side.Visible() && !d.sideOff {
			continue // portrait: grow from the sidebar's button
		}
		if r, ok := d.layerRect(o); ok {
			return r, true
		}
	}
	return rectF{}, false
}

func (d *desk) layerRect(o fyne.CanvasObject) (rectF, bool) {
	b := d.box()
	if d.mainRoot == nil || fyne.CurrentApp() == nil {
		return rectF{}, false
	}
	drv := fyne.CurrentApp().Driver()
	if drv.CanvasForObject(o) == nil || drv.CanvasForObject(b.ghostL) == nil {
		return rectF{}, false
	}
	p := drv.AbsolutePositionForObject(o)
	base := drv.AbsolutePositionForObject(b.ghostL)
	return rectF{fyne.NewPos(p.X-base.X, p.Y-base.Y), o.Size()}, true
}

// The buttons (§3.2): Settings · Quill · kmem, each assistant with a badge
// and a run dot.

func (d *desk) assistButtonFor(role string) *assistButton {
	b := d.box()
	bt := b.btns[role]
	if bt == nil {
		bt = &assistButton{role: role}
		b.btns[role] = bt
	}
	return bt
}

// quillButton: the toolbar's Quill button with its bubble and dot.
func (d *desk) quillButton() fyne.CanvasObject {
	bt := d.assistButtonFor(boxQuill)
	bt.btn = widget.NewButtonWithIcon("Quill", appIcon, func() { d.toggleAssistBox(boxQuill) })
	bt.btn.Importance = widget.MediumImportance
	if bt.bubble == nil {
		bt.bubble = newBubble()
	}
	bt.dot = canvas.NewCircle(colIdle)
	bt.dot.Hide()
	bt.box = container.New(badgeLayout{}, bt.btn, bt.bubble.box, bt.dot)
	return bt.box
}

// kmemButtonBox registers the toolbar's kmem button (kmembadge.go builds it).
func (d *desk) kmemButtonBox() fyne.CanvasObject {
	box := d.kmemButtonWithBubble()
	bt := d.assistButtonFor(boxKmem)
	bt.btn, bt.box, bt.bubble, bt.dot = d.kmemBtn, box, d.kmemBubble, d.kmemRunDot
	return box
}

// sideAssistButton: the portrait sidebar's button for role, with the badge
// and dot mirrored from the toolbar's.
func (d *desk) sideAssistButton(role, label string, icon fyne.Resource) fyne.CanvasObject {
	bt := d.assistButtonFor(role)
	bt.side = widget.NewButtonWithIcon(label, icon, func() { d.toggleAssistBox(role) })
	bt.side.Importance = widget.LowImportance
	bt.side.Alignment = widget.ButtonAlignLeading
	bt.sideBub = newBubble()
	bt.sideDot = canvas.NewCircle(colIdle)
	bt.sideDot.Hide()
	bt.sideBox = container.New(badgeLayout{}, bt.side, bt.sideBub.box, bt.sideDot)
	return bt.sideBox
}

// assistBubble: role's badge bubble (assistRole.bubble for the Quill
// operator: func(d *desk) *bubble { return d.assistBubble(roleQuill) }).
func (d *desk) assistBubble(role string) *bubble {
	bt := d.assistButtonFor(role)
	if bt.bubble == nil {
		bt.bubble = newBubble()
	}
	return bt.bubble
}

// setAssistRunDot: assistRole.dot for roles other than kmem.
func (d *desk) setAssistRunDot(role string, on bool) { d.syncAssistButtons() }

// assistByRole: the desk's assistant for role, nil if none was created.
func (d *desk) assistByRole(role string) *assistant {
	for _, a := range d.assistants() {
		if a.role != nil && a.role.id == role {
			return a
		}
	}
	return nil
}

// assistWorking: the assistant's turn or its subagents run.
func (d *desk) assistWorking(a *assistant) bool {
	if a == nil || !d.assistRunning(a) {
		return false
	}
	s := a.s
	return s.inTurn || s.conv != nil && s.conv.Busy || len(s.agentGrants) > 0 || s.proc != nil && s.proc.Busy()
}

// assistDotState: 0 no dot (stopped), 1 idle (running), 2 working.
func (d *desk) assistDotState(role string) int {
	a := d.assistByRole(role)
	switch {
	case a == nil || !d.assistRunning(a):
		return 0
	case d.assistWorking(a):
		return 2
	}
	return 1
}

// syncAssistButtons: dots and the portrait twins' badges from the roles'
// state; ticks every second while an assistant runs or the box is open.
func (d *desk) syncAssistButtons() {
	b := d.box()
	need := b.role != ""
	b.pulse = !b.pulse
	for role, bt := range b.btns {
		st := d.assistDotState(role)
		if st > 0 {
			need = true
		}
		c := color.Color(colIdle)
		if st == 2 {
			c = colRunning
			if b.pulse {
				c = fade(colRunning, 0.45)
			}
		}
		for _, dot := range []*canvas.Circle{bt.dot, bt.sideDot} {
			if dot == nil {
				continue
			}
			if st == 0 {
				dot.Hide()
				continue
			}
			if dot.FillColor != c || !dot.Visible() {
				dot.FillColor = c
				dot.Show()
				dot.Refresh()
			}
		}
		d.syncAssistAlert(bt) // approval waiting: amber over the dot (assistalert.go)
		if bt.sideBub != nil && bt.bubble != nil {
			mirrorBubble(bt.bubble, bt.sideBub)
		}
		if bt.btn != nil && bt.side != nil {
			imp := widget.LowImportance
			if b.role == role {
				imp = widget.HighImportance
			}
			if bt.side.Importance != imp {
				bt.side.Importance = imp
				bt.side.Refresh()
			}
		}
	}
	d.refreshBoxHeader()
	d.syncBoxOutline()
	if len(d.settingCards) > 0 {
		d.syncSettingCards() // a setting changed in Settings disables its card's Undo
	}
	if need && !b.tickOn {
		b.tickOn = true
		d.afterUI(time.Second, func() {
			b.tickOn = false
			d.syncAssistButtons()
		})
	}
}

func mirrorBubble(from, to *bubble) {
	if !from.box.Visible() {
		if to.box.Visible() {
			to.box.Hide()
		}
		return
	}
	if to.text.Text != from.text.Text || !to.box.Visible() {
		to.text.Text = from.text.Text
		to.box.Show()
		to.box.Refresh()
	}
}

// Reduce motion (§3.3, Q14): a switch in Settings → General that follows the
// system setting until the user changes it.

func (d *desk) reduceMotion() bool {
	p := d.app.Preferences()
	if p.Bool(prefReduceMotion + "Set") {
		return p.Bool(prefReduceMotion)
	}
	return systemReduceMotion()
}

var (
	sysMotionOnce sync.Once
	sysMotion     bool
)

// systemReduceMotion: the desktop's "animations off" (KDE's animation speed
// at instant, GNOME's enable-animations false); read once.
var systemReduceMotion = func() bool {
	sysMotionOnce.Do(func() { sysMotion = readSystemReduceMotion() })
	return sysMotion
}

func readSystemReduceMotion() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if cfg, err := os.UserConfigDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(cfg, "kdeglobals")); err == nil {
			if v, ok := iniValue(string(b), "KDE", "AnimationDurationFactor"); ok {
				return strings.TrimSpace(v) == "0"
			}
		}
	}
	if strings.Contains(strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP")), "GNOME") {
		if out, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "enable-animations").Output(); err == nil {
			return strings.TrimSpace(string(out)) == "false"
		}
	}
	return false
}

// iniValue: key's value in [section] of an ini text.
func iniValue(text, section, key string) (string, bool) {
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == "["+section+"]"
			continue
		}
		if !in {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			return v, true
		}
	}
	return "", false
}

// boxSettings: the General rows: Reduce motion and the box animation.
func (d *desk) boxSettings() fyne.CanvasObject {
	rm := widget.NewCheck("Reduce motion (no box and layout animations)", func(on bool) {
		if on != d.reduceMotion() {
			setUserPref(d.app.Preferences(), prefReduceMotion, on)
		}
	})
	rm.Checked = d.reduceMotion()
	sys := "off"
	if systemReduceMotion() {
		sys = "on"
	}
	note := caption("Follows the system setting (now " + sys + ") until you change it.")
	sel := widget.NewSelect(boxAnimNames, func(v string) { d.app.Preferences().SetString(prefBoxAnim, boxAnimKeyOf(v)) })
	sel.Selected = boxAnimName(d.boxAnimKey())
	return container.NewVBox(rm, note, container.NewHBox(widget.NewLabel("Assistant box animation"), sel))
}

// dockHandle: the docked panel's drag handle (its width, remembered).
type dockHandle struct {
	widget.BaseWidget
	d    *desk
	line *canvas.Rectangle
}

func newDockHandle(d *desk) *dockHandle {
	h := &dockHandle{d: d, line: canvas.NewRectangle(color.NRGBA{0xff, 0xff, 0xff, 0x18})}
	h.ExtendBaseWidget(h)
	return h
}

func (h *dockHandle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.New(layout.NewCustomPaddedLayout(0, 0, 3, 3), h.line))
}

func (h *dockHandle) Cursor() desktop.Cursor { return desktop.HResizeCursor }

func (h *dockHandle) Dragged(e *fyne.DragEvent) {
	d := h.d
	b := d.box()
	if !b.docked || b.rootW <= 0 {
		return
	}
	cur := d.dockWidth(b.rootW)
	w := max(dockMinW, min(cur-e.Dragged.DX, b.rootW-dockHandleW-d.dockMinChat()))
	d.app.Preferences().SetFloat(prefAssistDockW, float64(w))
	d.layoutRoot()
}

func (h *dockHandle) DragEnd() {}

// The generic content: a chat view with its own message box routed to the
// role's session (the Quill operator uses it; kmem adds its panels).

type assistChat struct {
	a     *assistant
	view  *chatView
	input *sendEntry
	root  fyne.CanvasObject
	box   *assistBoxView

	// The reason strip above the chat: why the role isn't running, with the
	// role's one action ("Move it here", "Take over").
	inRow fyne.CanvasObject // its own message box: hidden over the chat (the main one messages it)

	top       *fyne.Container // above the chat: the reason strip, the mirrored plan cards (Quill)
	mirrors   *planMirrors    // the Quill operator's mirrored plan cards of other sessions (planmirror.go)
	reasonBox *fyne.Container
	reasonLbl *widget.Label
	reasonBtn *widget.Button
	tap       func()
}

// syncTop shows c.top only while something in it shows: a visible empty
// top would still take the Border's padding and shift the chat down.
func (c *assistChat) syncTop() {
	on := false
	for _, o := range c.top.Objects {
		if o.Visible() {
			on = true
		}
	}
	if on != c.top.Visible() {
		if on {
			c.top.Show()
		} else {
			c.top.Hide()
		}
	}
}

// attachAssistBox builds role's chat for the box (once), registers it as the
// role's box content and as the role's hidden host (events, seen, open on a
// notification click).
func (d *desk) attachAssistBox(r *assistRole, title, placeholder string) *assistChat {
	a := d.assistFor(r)
	c := &assistChat{a: a}
	c.view = newChatViewWith(d, func() float32 { return chatMaxWidth }, func() float32 { return 0 })
	c.input = newSendEntry(func(t string) { d.assistBoxSend(c, t) })
	c.input.later = d.afterUI
	c.input.ac = d.newAutocorrector()
	c.input.autoFormat = d.autoFormatOn
	c.input.known = func() func(string) bool { return d.knownWords(a.s) }
	c.input.onShortcut = d.handleShortcut
	c.input.onHistory = func(k *fyne.KeyEvent) bool { return d.historyKey(c.input, a.s, k) }
	c.input.onEscape = d.assistBoxEscape
	c.input.onFocus = d.syncBoxOutline
	c.input.onSendNow = func(t string) {
		if s := a.s; s != nil && s.running() {
			d.interrupt(s)
		}
		d.assistBoxSend(c, t)
	}
	c.input.hint = placeholder
	c.input.SetPlaceHolder(placeholder)
	c.input.SetMinRowsVisible(2)
	d.alignInput(c.input)
	send := widget.NewButtonWithIcon("", theme.MailSendIcon(), func() { d.assistBoxSend(c, c.input.Text) })
	send.Importance = widget.HighImportance
	stop := widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		if s := a.s; s != nil {
			d.interrupt(s)
		}
	})
	stop.Importance = widget.DangerImportance
	in := bg(colBackground, 10, container.NewBorder(nil, nil, nil, container.NewVBox(stop, layout.NewSpacer(), send), c.input))
	c.inRow = in
	c.reasonLbl = widget.NewLabel("")
	c.reasonLbl.Wrapping = fyne.TextWrapWord
	c.reasonBtn = widget.NewButton("", func() {
		if c.tap != nil {
			c.tap()
		}
	})
	c.reasonBtn.Importance = widget.HighImportance
	c.reasonBox = container.NewVBox(bg(colBackground, 10, container.NewVBox(c.reasonLbl, c.reasonBtn)))
	c.reasonBox.Hide()
	c.top = container.NewVBox(c.reasonBox)
	c.top.Hide() // nothing above the chat: laid out as without c.top (syncTop)
	c.root = container.NewBorder(c.top, in, nil, nil, c.view.root)
	c.box = &assistBoxView{
		title:   title,
		content: c.root,
		input:   c.input,
		escape: func() bool {
			if c.view.sel != nil && c.view.sel.has {
				c.view.sel.clear()
				return true
			}
			return false
		},
		status: func() statusInfo { return d.assistIndicator(a) },
		inRow:  func() fyne.CanvasObject { return c.inRow },
		shown: func(on bool) {
			if !on {
				return
			}
			if a.s != nil && c.view.s != a.s {
				c.view.show(a.s)
			}
			d.markAssistSeen(a, false)
		},
	}
	d.setAssistBoxView(r.id, c.box)
	d.setHiddenHost(r.hidden, &hiddenHost{
		session: func() *deskSession { return c.view.s },
		view:    func() *chatView { return c.view },
		shown:   func() bool { return d.assistBoxShowing(r.id) },
		open:    func() { d.openAssistBox(r.id) },
	})
	return c
}

// assistBoxSend sends the box's message to the role's session (queued and
// started when it isn't running); never to the current tab.
func (d *desk) assistBoxSend(c *assistChat, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	d.assistDeliver(c.a, text)
	c.input.SetText("")
	if s := c.a.s; s != nil && c.view.s != s {
		c.view.show(s)
	}
}

// assistIndicator: the box header's status, the main chat's indicator
// (liveStatus: "✻ Thinking…", "● Bash: go test", "Waiting for your
// approval", "Ready", "Stopped", "· last activity 12 s ago" and the 30 s
// pulse) plus the subagents it waits on and its model; the reason line or
// "Stopped" while it doesn't run.
func (d *desk) assistIndicator(a *assistant) statusInfo {
	s := a.s
	if s == nil || !d.assistRunning(a) {
		if a.reason != "" {
			return statusInfo{text: a.reason, state: a.reason}
		}
		return statusInfo{text: "Stopped", state: "Stopped"}
	}
	now := statusNow()
	s.trackTurn(now)
	st := liveStatus(s, now)
	if n := len(s.agentGrants); n > 0 && !strings.Contains(st.text, "subagent") {
		st.text = st.state + " · " + plural(n, "subagent") + strings.TrimPrefix(st.text, st.state)
	}
	if s.model != "" {
		st.text += " · " + s.model
	}
	return st
}

// assistStatusText is the header's status text.
func (d *desk) assistStatusText(a *assistant) string { return d.assistIndicator(a).text }

// focusable: the view's own message box, nil if none (yet).
func (v *assistBoxView) focusable() fyne.Focusable {
	if v.inputFn != nil {
		if f := v.inputFn(); f != nil {
			return f
		}
	}
	return v.input
}

// registerKmemBox: the kmem box (kmemBoxContent: panels left, the
// assistant's chat right) as the kmem role's box content. Opening attaches
// the assistant's chat (kmemWindowOpenHook: kmemAssistOpen), closing hides it
// (the assistant keeps running); Pop out moves the content to its window.
func (d *desk) registerKmemBox() {
	content := d.kmemBoxContent()
	v := &assistBoxView{
		title:   "kmem assistant",
		content: content,
		inputFn: func() fyne.Focusable {
			if a := d.assist(); a.input != nil && d.kmemAssistChatOn() {
				return a.input
			}
			return nil
		},
		escape: func() bool {
			if w := d.kmemWin; w != nil && w.view != nil && w.view.sel != nil && w.view.sel.has {
				w.view.sel.clear()
				return true
			}
			return false
		},
		status: func() statusInfo { return d.assistIndicator(d.assist()) },
		popOut: d.popOutKmem,
		inRow: func() fyne.CanvasObject {
			if a := d.assist(); a.inRow != nil {
				return a.inRow
			}
			return nil
		},
	}
	v.shown = func(on bool) {
		w := d.kmemWin
		if w == nil || w.closed || w.split != content {
			// The pop-out window closed meanwhile: a fresh content.
			if !on {
				return
			}
			content = d.kmemBoxContent()
			v.content = content
			d.showBoxContent(boxKmem)
			w = d.kmemWin
		}
		if on {
			if kmemWindowOpenHook != nil {
				kmemWindowOpenHook(d, w)
			}
			if a := d.assist(); a.input != nil && a.input.onEscape == nil {
				a.input.onEscape = d.assistBoxEscape
			}
			d.markAssistSeen(d.assist(), false)
			return
		}
		if kmemWindowCloseHook != nil {
			kmemWindowCloseHook(d, w)
		}
	}
	d.setAssistBoxView(boxKmem, v)
	d.setHiddenHost(roleKmem, &hiddenHost{
		session: func() *deskSession { return d.kmemWin.session() },
		view: func() *chatView {
			if w := d.kmemWin; w != nil {
				return w.view
			}
			return nil
		},
		shown: func() bool {
			w := d.kmemWin
			return d.assistBoxShowing(boxKmem) || w != nil && w.win != nil && !w.closed && w.s != nil
		},
		open: d.showKmem,
	})
}
