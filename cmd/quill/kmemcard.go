package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
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
		if open || s != nil && (s.running() || d.hostHolds(s)) {
			return d.session(id)
		}
	}
	return nil
}

// ownerLabel: where a live handoff or topic runs, for the start card.
func (d *desk) ownerLabel(owner *deskSession) string {
	if role := hiddenRole(owner); role != "" {
		return roleDef(role).owner
	}
	title, _, _ := d.rowText(owner.id)
	return "open in tab " + title
}

// kmemCard is a new tab's "Open a topic" card (docs/design/topic-tabs.md
// §3.2): pick a top-level kmem topic or name a new one; the tab's title,
// kmem name and fresh session follow the topic. A live topic switches to its
// tab instead.
func (d *desk) kmemCard(s *deskSession) fyne.CanvasObject {
	head := canvas.NewText("Open a topic", colText)
	head.TextSize = 16
	head.TextStyle.Bold = true
	// Two lines: one is wider than the 560 px card.
	sub := container.NewVBox(mutedText("Each topic has one coordinator session that starts fresh from kmem.", fontMeta),
		mutedText("Anything you type now waits until you pick.", fontMeta))
	list := container.NewVBox(mutedText("Loading topics...", fontMeta))
	name := widget.NewEntry()
	name.SetPlaceHolder("New topic, e.g. Garden planner")
	preview := mutedText("", fontMeta)
	name.OnChanged = func(t string) {
		if strings.TrimSpace(t) == "" {
			setText(preview, "")
			return
		}
		slug := topicSlugFor(t)
		if _, ok := cachedTopic(slug); ok {
			setText(preview, "kmem topic: "+slug+" (exists; opens it)")
			return
		}
		setText(preview, "kmem topic: "+slug)
	}
	start := widget.NewButton("Open", func() { d.pickNewTopic(s, name.Text) })
	start.Importance = widget.HighImportance
	name.OnSubmitted = func(string) { d.pickNewTopic(s, name.Text) }
	newRow := container.NewBorder(nil, nil, nil, start, name)
	scroll := container.NewVScroll(list) // there can be dozens of top-level topics
	scroll.SetMinSize(fyne.NewSize(0, list.MinSize().Height))
	// The new-topic row sits above the list so dozens of topics never push
	// it out of view.
	body := container.NewVBox(head, sub, widget.NewSeparator(), mutedText("New topic…", fontMeta), newRow, preview,
		widget.NewSeparator(), mutedText("Topics (the eye hides one until you open it)", fontMeta), scroll)
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 14
	card := container.NewStack(bgr, container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16), body))
	go func() {
		ts := d.loadTopics()
		if d.kmemHasFeature(extSessionsFeature) {
			d.loadExtSessions() // on demand: the card decides what is live
		}
		var fill func()
		foldOpen := false
		var foldAcc *widget.Accordion
		fill = func() {
			if foldAcc != nil {
				foldOpen = foldAcc.Items[0].Open // keep the fold as the user left it
				foldAcc = nil
			}
			var rows, dormant []fyne.CanvasObject
			for _, t := range ts {
				t := t
				if d.isDormant(t.Slug) {
					open := widget.NewButton(t.Title, func() { d.pickTopic(s, t, false) })
					open.Alignment = widget.ButtonAlignLeading
					show := widget.NewButton("Show again", func() { d.setDormant(t.Slug, false) })
					show.Importance = widget.LowImportance
					dormant = append(dormant, container.NewBorder(nil, nil, nil, show, open))
					continue
				}
				var b *widget.Button
				if owner := d.topicOwner(t.Slug, s); owner != nil {
					b = widget.NewButton(t.Title+" · "+d.ownerLabel(owner), func() { d.open(owner.id) })
					b.Importance = widget.LowImportance
				} else if e, ext := d.topicExternal(t.Slug); ext {
					if why := d.extTakeOverBlock(e); why != "" {
						b = widget.NewButton(t.Title+" · running elsewhere ("+e.Name+"): "+why, nil)
						b.Disable()
					} else {
						b = widget.NewButton(t.Title+" · running elsewhere ("+e.Name+") · Take over", func() { d.offerTakeOver(e, s) })
						b.Importance = widget.LowImportance
					}
				} else {
					b = widget.NewButton(topicButtonLabel(t), func() { d.pickTopic(s, t, false) })
				}
				b.Alignment = widget.ButtonAlignLeading
				hide := widget.NewButtonWithIcon("", theme.VisibilityOffIcon(), func() { d.setDormant(t.Slug, true) })
				hide.Importance = widget.LowImportance
				rows = append(rows, container.NewBorder(nil, nil, nil, hide, b))
			}
			if len(rows) == 0 && len(dormant) == 0 {
				rows = append(rows, mutedText("No kmem topics yet: name a new one above.", fontMeta))
			}
			if len(dormant) > 0 {
				acc := widget.NewAccordion(widget.NewAccordionItem(
					fmt.Sprintf("Dormant (%d)", len(dormant)), container.NewVBox(dormant...)))
				if foldOpen {
					acc.Open(0)
				}
				foldAcc = acc
				rows = append(rows, acc)
			}
			list.Objects = rows
			list.Refresh()
			scroll.SetMinSize(fyne.NewSize(0, min(list.MinSize().Height, 380)))
			// The card grew: lay out the chat column it sits in, not just
			// the card at its old size.
			for _, v := range d.allViews() {
				if v.s == s {
					v.reflow()
				}
			}
			relayoutTree(card)
		}
		d.do(func() {
			dormantCards[s] = func() {
				if !s.pickPending {
					delete(dormantCards, s)
					return
				}
				fill()
			}
			fill()
		})
	}()
	return container.NewVBox(padTop(60), container.NewCenter(sized(560, 0, card)))
}

// pickResume / pickNew: the handoff start paths, still used by the phone API
// and the kmem window until they move to topics (topic-tabs §8 D, G).
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
			if next != old {
				delete(d.org.KmemTitles, old)
			}
			title, _, _ := d.rowText(id)
			d.rememberKmemTitle(next, title)
			d.moveKmemProject(old, next)
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
