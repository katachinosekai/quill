package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func (d *desk) handoffOwner(name string, self *deskSession) *deskSession {
	if owner := d.runningKmemOwner(name, self); owner != nil {
		return owner
	}
	for id, k := range d.org.Kmem {
		if k != name || id == self.id {
			continue
		}
		s := d.sessions[id]
		open := false
		for _, t := range d.tabs {
			open = open || t == id
		}
		if open || s != nil && isHiddenSession(s) && !s.codex && d.hostHolds(s) {
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

// topicSearchDelay: how long typing pauses before kmem is asked for content
// matches (tests set it to 0).
var topicSearchDelay = 200 * time.Millisecond

// topicRow is one topic on the card: its open button (or the live tab / "running elsewhere" /
// dormant forms), the eye (or "Show again") and an optional line under it.
func (d *desk) topicRow(s *deskSession, t topicInfo, hit *topicHit, related []string) fyne.CanvasObject {
	var b *widget.Button
	dormant := d.isDormant(t.Slug)
	if dormant {
		b = widget.NewButton(t.Title+" · dormant", func() { d.pickTopic(s, t, false) })
	} else if owner := d.topicOwner(t.Slug, s); owner != nil {
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
	var side *widget.Button
	if dormant {
		side = widget.NewButton("Show again", func() { d.setDormant(t.Slug, false) })
	} else {
		side = widget.NewButtonWithIcon("", theme.VisibilityOffIcon(), func() { d.setDormant(t.Slug, true) })
	}
	side.Importance = widget.LowImportance
	row := container.NewBorder(nil, nil, nil, side, b)
	var extra []fyne.CanvasObject
	if hit != nil && hit.Why == "content" {
		extra = append(extra, indented(mutedText("mentions "+hit.Snippet, fontMeta)))
	}
	if len(related) > 0 {
		extra = append(extra, indented(mutedText("Related: "+strings.Join(related, ", "), fontMeta)))
	}
	if len(extra) == 0 {
		return row
	}
	return container.NewVBox(append([]fyne.CanvasObject{row}, extra...)...)
}

func indented(o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(0, 0, 12, 0), o)
}

// kmemCard is a new tab's "Open a topic" card (docs/design/topic-tabs.md
// §3.2), search-first (kmem fb10aa6): the search box has focus and the last
// five opened topics sit under it; typing lists matching topics (slug, title,
// then kmem content "mentions ...") and offers "New topic". Browse all shows
// the whole list with the dormant fold. A live topic switches to its tab
// instead of starting one.
func (d *desk) kmemCard(s *deskSession) fyne.CanvasObject {
	head := canvas.NewText("Open a topic", colText)
	head.TextSize = 16
	head.TextStyle.Bold = true
	// Two lines: one is wider than the 560 px card.
	sub := container.NewVBox(mutedText("Each topic has one coordinator session that starts fresh from kmem.", fontMeta),
		mutedText("Anything you type now waits until you pick.", fontMeta))
	list := container.NewVBox(mutedText("Loading topics...", fontMeta))
	search := widget.NewEntry()
	search.SetPlaceHolder("Search topics, or name a new one")
	preview := mutedText("", fontMeta)
	dymBox := container.NewVBox()
	newBtn := widget.NewButton("New topic", func() { d.pickNewTopic(s, search.Text) })
	newBtn.Importance = widget.HighImportance
	newBtn.Hide()
	search.OnSubmitted = func(t string) {
		if strings.TrimSpace(t) != "" {
			d.pickNewTopic(s, t) // an existing topic opens instead
		}
	}
	scroll := container.NewVScroll(list) // there can be dozens of top-level topics
	scroll.SetMinSize(fyne.NewSize(0, list.MinSize().Height))
	body := container.NewVBox(head, sub, widget.NewSeparator(), search, dymBox, newBtn, scroll, preview)
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 14
	card := container.NewStack(bgr, container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16), body))
	go func() {
		ts := d.loadTopics()
		if d.kmemHasFeature(extSessionsFeature) {
			d.loadExtSessions() // on demand: the card decides what is live
		}
		var render func()
		var foldAcc *widget.Accordion
		foldOpen, browse := false, false
		var content []topicHit // kmem content matches for the typed text
		related := map[string][]string{}
		var timer *time.Timer
		gen := 0
		render = func() {
			if foldAcc != nil {
				foldOpen = foldAcc.Items[0].Open // keep the fold as the user left it
				foldAcc = nil
			}
			q := strings.TrimSpace(search.Text)
			var rows []fyne.CanvasObject
			dym := (*topicInfo)(nil)
			if q == "" {
				rows = d.recentRows(s, ts, &browse, &foldAcc, foldOpen, render)
			} else {
				res := d.searchLocal(q, ts)
				hits := res.Hits
				seen := map[string]bool{}
				for _, h := range hits {
					seen[h.Topic.Slug] = true
				}
				for _, h := range content {
					if !seen[h.Topic.Slug] {
						hits = append(hits, h)
					}
				}
				dym = res.DidYouMean
				for i := range hits {
					h := hits[i]
					rows = append(rows, d.topicRow(s, h.Topic, &h, related[h.Topic.Slug]))
				}
				if len(rows) == 0 {
					rows = append(rows, mutedText("No topic matches.", fontMeta))
				}
			}
			list.Objects = rows
			list.Refresh()
			scroll.SetMinSize(fyne.NewSize(0, min(list.MinSize().Height, 380)))
			dymBox.Objects = nil
			if dym != nil {
				t := *dym
				b := widget.NewButton("Did you mean "+t.Title+"?", func() { d.pickTopic(s, t, false) })
				b.Alignment = widget.ButtonAlignLeading
				dymBox.Objects = []fyne.CanvasObject{b}
			}
			dymBox.Refresh()
			if q == "" {
				sub.Show()
				newBtn.Hide()
				setText(preview, "")
			} else {
				sub.Hide() // room for the results on a short window
				slug := topicSlugFor(q)
				newBtn.SetText("New topic: " + q)
				if _, ok := cachedTopic(slug); ok {
					newBtn.SetText("Open topic: " + q)
					setText(preview, "kmem topic: "+slug+" (exists; opens it)")
				} else {
					setText(preview, "kmem topic: "+slug)
				}
				newBtn.Show()
			}
			// The card grew: lay out the chat column it sits in, not just
			// the card at its old size.
			for _, v := range d.allViews() {
				if v.s == s {
					v.reflow()
				}
			}
			relayoutTree(card)
		}
		search.OnChanged = func(string) {
			gen++
			my := gen
			content = nil
			render() // name and title matches at once
			if timer != nil {
				timer.Stop()
			}
			q := strings.TrimSpace(search.Text)
			if q == "" {
				return
			}
			timer = time.AfterFunc(topicSearchDelay, func() {
				res := d.searchTopics(q, ts)
				var hits []topicHit
				rel := map[string][]string{}
				for _, h := range res.Hits {
					if h.Why == "content" {
						hits = append(hits, h)
					}
				}
				for i, h := range res.Hits {
					if i < 3 {
						if r := d.relatedTopics(h.Topic.Slug); len(r) > 0 {
							rel[h.Topic.Slug] = r
						}
					}
				}
				d.do(func() {
					if my == gen {
						content, related = hits, rel
						render()
					}
				})
			})
		}
		d.do(func() {
			dormantCards[s] = func() {
				if !s.pickPending {
					delete(dormantCards, s)
					return
				}
				render()
			}
			render()
			if d.win != nil && d.current == s {
				d.win.Canvas().Focus(search)
			}
		})
	}()
	return container.NewVBox(padTop(60), container.NewCenter(sized(560, 0, card)))
}

// recentRows: the empty-search body: Recent, and "Browse all" which
// expands the full list with the dormant fold.
func (d *desk) recentRows(s *deskSession, ts []topicInfo, browse *bool, foldAcc **widget.Accordion, foldOpen bool, render func()) []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	if *browse {
		var dormant []fyne.CanvasObject
		for _, t := range ts {
			if d.isDormant(t.Slug) {
				dormant = append(dormant, d.topicRow(s, t, nil, nil))
				continue
			}
			rows = append(rows, d.topicRow(s, t, nil, nil))
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
			*foldAcc = acc
			rows = append(rows, acc)
		}
		less := widget.NewButton("Show recent only", func() { *browse = false; render() })
		less.Importance = widget.LowImportance
		return append([]fyne.CanvasObject{mutedText("All topics (the eye hides one until you open it)", fontMeta)}, append(rows, less)...)
	}
	rows = append(rows, mutedText("Recent", fontMeta))
	recent := d.recentTopics(ts, recentTopicsShow)
	for _, t := range recent {
		rows = append(rows, d.topicRow(s, t, nil, nil))
	}
	if len(recent) == 0 {
		if len(ts) == 0 {
			rows = append(rows, mutedText("No kmem topics yet: name a new one above.", fontMeta))
		} else {
			rows = append(rows, mutedText("Nothing opened yet.", fontMeta))
		}
	}
	if len(ts) > 0 {
		all := widget.NewButton(fmt.Sprintf("Browse all (%d)", len(ts)), func() { *browse = true; render() })
		all.Importance = widget.LowImportance
		rows = append(rows, all)
	}
	return rows
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
