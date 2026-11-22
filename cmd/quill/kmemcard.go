package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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
		if open || s != nil && (s.running() || s == d.kmemWin.session()) {
			return d.session(id)
		}
	}
	return nil
}

// ownerLabel: where a live handoff runs, for resume lists.
func (d *desk) ownerLabel(owner *deskSession) string {
	if isHiddenSession(owner) {
		return "live in the kmem window"
	}
	title, _, _ := d.rowText(owner.id)
	return "open in tab " + title
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
	newRow := container.NewBorder(nil, nil, nil, start, name)
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
					b = widget.NewButton(label+" · "+d.ownerLabel(owner), func() { d.open(owner.id) })
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

func (d *desk) renameKmem(id, old, next string) {
	go func() {
		// Always ask kmem: it moves a saved handoff and/or the config "sessions" label (kmem 1.6.3 moves a label even
		// before the first save). "no handoff OLD" = nothing in kmem yet, so only the tab's link changes.
		// --as: kmem history shows "by Quill Desktop/OLD at <time>", not "unknown".
		out, err := d.runKmem("kmem", "handoff", "rename", old, next, "--as", "Quill Desktop/"+old, "--json")
		labelFailed := false
		if err != nil {
			if msg := strings.TrimSpace(out); strings.Contains(msg, "no handoff "+old) {
				err = nil
			} else {
				err = fmt.Errorf("%s", msg)
			}
		} else {
			for _, line := range strings.Split(out, "\n") {
				var r struct {
					Config string `json:"config"`
				}
				if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &r) == nil {
					labelFailed = r.Config == "error"
				}
			}
		}
		d.do(func() {
			s := d.session(id)
			if err != nil {
				if s != nil {
					d.notify(s, "kmem rename failed: "+err.Error())
				}
				return
			}
			if labelFailed && s != nil {
				d.notify(s, "kmem renamed the handoff but could not move its config label from "+old+" to "+next)
			}
			d.org.Kmem[id] = next
			if p, ok := d.org.Projects[old]; ok && d.org.Projects[next] == "" && !d.kmemNameInUse(old) {
				d.org.Projects[next] = p // the project set in Quill is keyed by kmem name
				delete(d.org.Projects, old)
			}
			resetKmemSessions() // the label just moved; don't group by the cached old key for a minute
			d.saveOrg()
		})
	}()
}

// kmemNameInUse: another tab still links to kmem name k.
func (d *desk) kmemNameInUse(k string) bool {
	for _, v := range d.org.Kmem {
		if v == k {
			return true
		}
	}
	return false
}
