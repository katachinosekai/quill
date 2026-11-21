package main

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

func (d *desk) handoffOwner(name string, self *deskSession) *deskSession {
	for id, k := range d.org.Kmem {
		if k != name || id == self.id {
			continue
		}
		s := d.sessions[id]
		open := false
		for _, t := range d.tabs {
			open = open || t == id
		}
		if open || s != nil && s.running() {
			return d.session(id)
		}
	}
	return nil
}

func (d *desk) kmemCard(s *deskSession) fyne.CanvasObject {
	head := canvas.NewText("Start this session", colText)
	head.TextSize = 16
	head.TextStyle.Bold = true
	sub := mutedText("Resume a kmem handoff, or name a new one. Anything you type now waits until you pick.", fontMeta)
	resume := container.NewVBox(mutedText("Loading handoffs...", fontMeta))
	name := widget.NewEntry()
	name.SetPlaceHolder("Name, e.g. Quill Android")
	preview := mutedText("", fontMeta)
	name.OnChanged = func(t string) {
		if strings.TrimSpace(t) == "" {
			setText(preview, "")
			return
		}
		d.hsMu.Lock()
		known := d.lastHandoffs
		d.hsMu.Unlock()
		setText(preview, "kmem name: "+kmemSlug(t, d.kmemTaken(known)))
	}
	start := widget.NewButton("Start new", func() { d.pickNew(s, name.Text) })
	start.Importance = widget.HighImportance
	name.OnSubmitted = func(string) { d.pickNew(s, name.Text) }
	none := widget.NewButton("No kmem", func() { d.pickNone(s) })
	none.Importance = widget.LowImportance
	newRow := container.NewBorder(nil, nil, nil, container.NewHBox(start, none), name)
	body := container.NewVBox(head, sub, widget.NewSeparator(), mutedText("Resume", fontMeta), resume,
		widget.NewSeparator(), mutedText("New", fontMeta), newRow, preview)
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 14
	card := container.NewStack(bgr, container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16), body))
	go func() {
		hs := d.kmemHandoffs()
		d.do(func() {
			var rows []fyne.CanvasObject
			for _, h := range hs {
				h := h
				label := h.Name + " · saved " + h.Saved.Local().Format("Jan 2 15:04")
				var b *widget.Button
				if owner := d.handoffOwner(h.Name, s); owner != nil {
					title, _, _ := d.rowText(owner.id)
					b = widget.NewButton(label+" · open in tab "+title, func() { d.open(owner.id) })
					b.Importance = widget.LowImportance
				} else {
					b = widget.NewButton(label, func() { d.pickResume(s, h.Name) })
				}
				b.Alignment = widget.ButtonAlignLeading
				rows = append(rows, b)
			}
			if len(rows) == 0 {
				rows = append(rows, mutedText("No saved handoffs yet.", fontMeta))
			}
			resume.Objects = rows
			resume.Refresh()
			relayoutTree(card)
		})
	}()
	return container.NewVBox(padTop(60), container.NewCenter(sized(560, 0, card)))
}

func (d *desk) pickResume(s *deskSession, name string) {
	if !s.pickPending {
		return
	}
	s.pickPending = false
	d.org.Titles[s.id] = name
	d.applyKmemChoice(s, kmemResumeOf+name, name)
	d.afterPick(s, false)
}

func (d *desk) pickNew(s *deskSession, text string) {
	if !s.pickPending {
		return
	}
	title := strings.TrimSpace(text)
	if title == "" {
		title, _, _ = d.rowText(s.id)
	}
	s.pickPending = false
	d.org.Titles[s.id] = title
	d.applyKmemChoice(s, kmemNewChoice, title)
	d.afterPick(s, true)
}

func (d *desk) pickNone(s *deskSession) {
	if !s.pickPending {
		return
	}
	s.pickPending = false
	delete(d.org.Kmem, s.id)
	d.afterPick(s, true)
}

func (d *desk) afterPick(s *deskSession, sendQueued bool) {
	d.saveOrg()
	d.tabSig = ""
	d.syncTabs()
	d.rebuildRows()
	for _, v := range d.allViews() {
		if v.s == s {
			v.rebuild()
		}
	}
	d.updateChrome()
	if sendQueued && len(s.queue) > 0 {
		q := strings.Join(s.queue, "\n\n")
		s.queue = nil
		d.syncQueue(s)
		d.sendTo(s, q)
	}
}

func (d *desk) syncTitleToKmem(id string) {
	if k := d.org.Kmem[id]; k != "" {
		d.org.Titles[id] = k
		d.saveOrg()
		d.tabSig = ""
		d.syncTabs()
		d.rebuildRows()
		d.updateChrome()
	}
}

func (d *desk) offerKmemRename(id, title string) {
	old := d.org.Kmem[id]
	if old == "" || !d.kmemAvailable() {
		return
	}
	next := kmemSlug(title, func(n string) bool { return n != old && d.kmemTaken(nil)(n) })
	if next == old {
		return
	}
	dialog.ShowConfirm("Rename the kmem link too?", fmt.Sprintf("This session saves and resumes kmem handoff “%s”. Rename it to “%s”? A saved handoff moves with it.", old, next),
		func(ok bool) {
			if ok {
				d.renameKmem(id, old, next)
			}
		}, d.win)
}

func (d *desk) renameKmem(id, old, next string) {
	go func() {
		hs, _ := d.kmemHandoffList()
		saved := false
		for _, h := range hs {
			saved = saved || h.Name == old
		}
		var err error
		if saved {
			var out string
			out, err = d.runKmem("kmem", "handoff", "rename", old, next)
			if err != nil {
				err = fmt.Errorf("%s", strings.TrimSpace(out))
			}
		}
		d.do(func() {
			if err != nil {
				if s := d.session(id); s != nil {
					d.notify(s, "kmem rename failed: "+err.Error())
				}
				return
			}
			d.org.Kmem[id] = next
			d.saveOrg()
		})
	}()
}
