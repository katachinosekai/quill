package main

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type nameEntry struct {
	widget.Entry
	onEsc func()
}

func newNameEntry() *nameEntry {
	e := &nameEntry{}
	e.ExtendBaseWidget(e)
	return e
}

func (e *nameEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape && e.onEsc != nil {
		e.onEsc()
		return
	}
	e.Entry.TypedKey(k)
}

func (d *desk) askNewName(s *deskSession, fallback string) {
	if d.nameClose != nil {
		d.nameClose()
	}
	e := newNameEntry()
	e.SetPlaceHolder(fallback)
	head := canvas.NewText("Name this session", colMuted)
	head.TextSize = fontMeta
	head.TextStyle.Bold = true
	hint := canvas.NewText("Enter to keep it · leave empty for "+fallback, colMuted)
	hint.TextSize = fontMeta
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 14
	bgr.StrokeColor = color.NRGBA{0xff, 0xff, 0xff, 0x22}
	bgr.StrokeWidth = 1
	body := container.New(layout.NewCustomPaddedLayout(8, 10, 12, 12), container.NewVBox(head, sized(300, 0, e), hint))
	card := container.NewStack(bgr, body)
	at := fyne.NewPos(8, 8)
	drv := fyne.CurrentApp().Driver()
	if c := d.tabChips[s.id]; c != nil {
		at = drv.AbsolutePositionForObject(c).Add(fyne.NewPos(0, c.Size().Height+2))
	} else if d.strip != nil && d.strip.plus != nil {
		at = drv.AbsolutePositionForObject(d.strip.plus).Add(fyne.NewPos(0, d.strip.plus.Size().Height+2))
	}
	var closeFn func()
	dd := &dropdown{card: card, at: at, onClosed: func() {
		d.nameClose = nil
		if name := strings.TrimSpace(e.Text); name != "" {
			d.org.Titles[s.id] = name
			d.saveOrg()
			d.tabSig = ""
			d.syncTabs()
			d.rebuildRows()
			d.updateChrome()
		}
		d.win.Canvas().Focus(d.input)
	}}
	e.onEsc = func() {
		e.SetText("")
		closeFn()
	}
	e.OnSubmitted = func(string) { closeFn() }
	closeFn = d.showDropdownWith(dd)
	d.nameClose = closeFn
	d.win.Canvas().Focus(e)
}
