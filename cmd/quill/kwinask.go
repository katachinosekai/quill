package main

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

const (
	prefKwinAsked = "kwinRulesAsked"
	prefKwinRules = "kwinRules"

	kwinQuestion = "Let KDE remember where Quill Desktop's windows go?"
	kwinExplain  = "Quill Desktop's windows stay where you put them between starts. You can change this any time in Settings → General."
	kwinSwitch   = "Let KDE remember Quill Desktop's window positions"
	kwinNotKDE   = "Only KDE Plasma can remember window positions; other desktops place windows themselves."
)

// setKwinRules records the answer and applies it (off the UI thread: it runs
// programs).
func (d *desk) setKwinRules(on bool) {
	p := d.app.Preferences()
	p.SetBool(prefKwinAsked, true)
	p.SetBool(prefKwinRules, on)
	if d.kwin.onKDE() {
		go d.kwin.Apply(on)
	}
}

// syncKwinRules at start: when the user said yes, repairs rules that were
// removed or changed (Apply writes nothing when they are right).
func (d *desk) syncKwinRules() {
	if d.kwin.onKDE() && d.app.Preferences().Bool(prefKwinRules) {
		go d.kwin.Apply(true)
	}
}

// askKwinWhenReady asks once, on KDE only, once no other dialog is up.
func (d *desk) askKwinWhenReady() {
	if !d.kwin.onKDE() || d.app.Preferences().Bool(prefKwinAsked) {
		return
	}
	go func() {
		for {
			ready, asked := false, false
			d.doWait(func() {
				asked = d.app.Preferences().Bool(prefKwinAsked)
				ready = !asked && d.featureAskReady() && d.featureDlg == nil
			})
			if asked {
				return
			}
			if ready {
				d.do(d.askKwin)
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// askKwin shows the question: Yes is the default (the highlighted button).
func (d *desk) askKwin() {
	if d.app.Preferences().Bool(prefKwinAsked) {
		return
	}
	msg := widget.NewLabel(kwinQuestion + "\n\n" + kwinExplain)
	msg.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustomConfirm("Quill Desktop: window positions", "Yes", "No", msg, func(yes bool) {
		d.setKwinRules(yes)
		d.settingsChanged()
	}, d.win)
	dlg.Resize(fyne.NewSize(460, 200))
	dlg.Show()
}

// kwinSetting: the Settings → General switch and, off KDE, its note.
func (d *desk) kwinSetting() fyne.CanvasObject {
	chk := widget.NewCheck(kwinSwitch, nil)
	chk.Checked = d.app.Preferences().Bool(prefKwinRules)
	chk.OnChanged = func(on bool) {
		if on != d.app.Preferences().Bool(prefKwinRules) {
			d.setKwinRules(on)
			d.settingsChanged()
		}
	}
	if !d.kwin.onKDE() {
		chk.Checked = false
		chk.Disable()
		return container.NewVBox(chk, caption(kwinNotKDE))
	}
	return chk
}
