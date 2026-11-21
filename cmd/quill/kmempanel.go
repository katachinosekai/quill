package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// The kmem panel (user 2026-11-21): ended sessions live on as kmem handoffs,
// and kmem's tasks, memory and recycle bin are managed here. Every write is a
// click by the user. Emptying the bin (kmem's final purge) runs
// `kmem trash empty` in a terminal, where kmem itself asks the user to type
// the confirmation; no session, hook or phone can reach it.

type kmemTopic struct {
	Name  string
	Facts int
	KB    int
}

var topicLine = regexp.MustCompile(`^(\S+)\s+(\d+) facts?, (\d+) KB`)

func parseTopics(out string) []kmemTopic {
	var ts []kmemTopic
	for _, l := range strings.Split(out, "\n") {
		if m := topicLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			n, _ := strconv.Atoi(m[2])
			kb, _ := strconv.Atoi(m[3])
			ts = append(ts, kmemTopic{m[1], n, kb})
		}
	}
	return ts
}

type binItem struct {
	ID, Head, Preview string
}

var binHead = regexp.MustCompile(`^\[([a-z0-9]+)\]\s+(.*)$`)

func parseBin(out string) []binItem {
	var items []binItem
	for _, l := range strings.Split(out, "\n") {
		if m := binHead.FindStringSubmatch(l); m != nil {
			items = append(items, binItem{ID: m[1], Head: m[2]})
			continue
		}
		if t := strings.TrimSpace(l); t != "" && len(items) > 0 {
			it := &items[len(items)-1]
			it.Preview = strings.TrimSpace(it.Preview + " " + t)
		}
	}
	return items
}

var factID = regexp.MustCompile(`\[(f[0-9a-f]{6,})\]`)

func factIDs(out string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range factID.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

type kmemPanel struct {
	d                      *desk
	dlg                    dialog.Dialog
	tabs                   *container.AppTabs
	over, hand, tasks, bin *fyne.Container
	topics                 *widget.List
	topicList              []kmemTopic
	topicShown             []kmemTopic
	memText                *widget.Label
	memIDs                 *widget.Select
	memTopic               string
}

func (d *desk) showKmemPanel() {
	if !d.kmemAvailable() {
		dialog.ShowInformation("kmem", "kmem is not available on this computer. Set it up in Settings → General → Memory (kmem).", d.win)
		return
	}
	p := d.newKmemPanel()
	p.dlg = dialog.NewCustom("kmem", "Close", p.tabs, d.win)
	p.dlg.Resize(fyne.NewSize(940, 640))
	p.dlg.Show()
	p.reload()
}

func (d *desk) newKmemPanel() *kmemPanel {
	p := &kmemPanel{d: d}
	loading := func() *fyne.Container { return container.NewVBox(widget.NewLabel("Loading from kmem...")) }
	p.over, p.hand, p.tasks, p.bin = loading(), loading(), loading(), loading()
	p.tabs = container.NewAppTabs(
		container.NewTabItem("Overview", container.NewVScroll(p.over)),
		container.NewTabItem("Handoffs", container.NewVScroll(p.hand)),
		container.NewTabItem("Tasks", container.NewVScroll(p.tasks)),
		container.NewTabItem("Memory", p.memoryTab()),
		container.NewTabItem("Recycle bin", container.NewVScroll(p.bin)),
	)
	d.kmemPanelNow = p
	return p
}

func caption(t string) *widget.Label {
	l := widget.NewLabel(t)
	l.Wrapping = fyne.TextWrapWord
	l.SizeName = theme.SizeNameCaptionText
	return l
}

func boldLabel(t string) *widget.Label {
	l := widget.NewLabel(t)
	l.TextStyle = fyne.TextStyle{Bold: true}
	l.Wrapping = fyne.TextWrapWord
	return l
}

func wrapLabel(t string) *widget.Label {
	l := widget.NewLabel(t)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func setBox(c *fyne.Container, objs ...fyne.CanvasObject) {
	c.Objects = objs
	c.Refresh()
}

// reload reads everything in the background and fills the tabs.
func (p *kmemPanel) reload() {
	d := p.d
	go func() {
		var brief struct {
			Brief string `json:"brief"`
		}
		d.kmemJSON(&brief, "brief")
		topicsOut, _ := d.runKmem("kmem", "topics")
		var hs struct {
			Handoffs []struct {
				Name  string `json:"name"`
				Items int    `json:"items"`
				Saved string `json:"saved"`
			} `json:"handoffs"`
		}
		d.kmemJSON(&hs, "handoff", "list")
		var ts struct {
			Tasks []struct {
				ID     string  `json:"id"`
				Prio   int     `json:"prio"`
				Status string  `json:"status"`
				Title  string  `json:"title"`
				Topic  string  `json:"topic"`
				Owner  *string `json:"owner"`
			} `json:"tasks"`
		}
		d.kmemJSON(&ts, "task", "list")
		binOut, _ := d.runKmem("kmem", "trash", "list")
		topics := parseTopics(topicsOut)
		bin := parseBin(binOut)
		d.do(func() {
			// Overview
			var big []string
			for _, t := range topics {
				if t.KB > 8 {
					big = append(big, fmt.Sprintf("%s (%d KB)", t.Name, t.KB))
				}
			}
			ov := []fyne.CanvasObject{boldLabel("kmem brief"), wrapLabel(strings.TrimSpace(firstNonEmpty(brief.Brief, "kmem didn't answer.")))}
			if len(big) > 0 {
				ov = append(ov, widget.NewSeparator(), boldLabel("Sections over 8 KB (maintenance asks to split them)"), wrapLabel(strings.Join(big, ", ")))
			}
			ov = append(ov, widget.NewSeparator(), caption(fmt.Sprintf("%d topics, %d handoffs, %d open tasks, %d item(s) in the recycle bin.", len(topics), len(hs.Handoffs), len(ts.Tasks), len(bin))))
			setBox(p.over, ov...)
			// Handoffs
			var hrows []fyne.CanvasObject
			hrows = append(hrows, caption("Ended sessions live on as kmem handoffs. Resume starts a live session from one; Delete moves its facts to the recycle bin."))
			for _, h := range hs.Handoffs {
				h := h
				saved := h.Saved
				if t, err := time.Parse(time.RFC3339, h.Saved); err == nil {
					saved = ago(t)
				}
				state := "ended"
				owner := d.handoffOwner(h.Name, &deskSession{})
				if owner != nil {
					title, _, _ := d.rowText(owner.id)
					state = "live in “" + title + "”"
				}
				show := widget.NewButtonWithIcon("Show", theme.VisibilityIcon(), func() { p.showHandoff(h.Name) })
				resume := widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() { p.resumeHandoff(h.Name) })
				del := widget.NewButtonWithIcon("Delete...", theme.DeleteIcon(), func() { p.deleteHandoff(h.Name, h.Items) })
				if owner != nil {
					resume.SetText("Open")
					resume.OnTapped = func() { d.open(owner.id); p.dlg.Hide() }
					del.Disable()
				}
				for _, b := range []*widget.Button{show, resume, del} {
					b.Importance = widget.LowImportance
				}
				hrows = append(hrows, container.NewBorder(nil, nil,
					container.NewVBox(boldLabel(h.Name), caption(fmt.Sprintf("saved %s · %d items · %s", saved, h.Items, state))), nil,
					container.NewHBox(layout.NewSpacer(), show, resume, del)), widget.NewSeparator())
			}
			setBox(p.hand, hrows...)
			// Tasks, grouped by topic
			by := map[string][]string{}
			var order []string
			for _, t := range ts.Tasks {
				if _, ok := by[t.Topic]; !ok {
					order = append(order, t.Topic)
				}
				who := "open"
				if t.Owner != nil && *t.Owner != "" {
					who = "claimed by " + *t.Owner
				}
				by[t.Topic] = append(by[t.Topic], fmt.Sprintf("[%s] p%d · %s — %s", t.ID, t.Prio, who, t.Title))
			}
			sort.Strings(order)
			trows := []fyne.CanvasObject{caption("Open and claimed tasks by topic (read-only here; sessions claim and finish them).")}
			for _, topic := range order {
				trows = append(trows, boldLabel(topic))
				for _, l := range by[topic] {
					trows = append(trows, wrapLabel(l))
				}
			}
			setBox(p.tasks, trows...)
			// Memory topics
			p.topicList = topics
			p.filterTopics("")
			// Recycle bin
			p.fillBin(bin)
		})
	}()
}

func (p *kmemPanel) fillBin(bin []binItem) {
	d := p.d
	empty := widget.NewButtonWithIcon("Empty bin...", theme.DeleteIcon(), p.emptyBin)
	empty.Importance = widget.DangerImportance
	rows := []fyne.CanvasObject{
		caption("Facts and tasks moved to the bin are hidden everywhere. Restore brings one back. Empty bin is kmem's final purge: it blanks the text in kmem's history and backups and cannot be undone. Only you can do it; kmem asks you to type the confirmation in a terminal and only purges items that have been in the bin for 7 days or more."),
	}
	if len(bin) == 0 {
		rows = append(rows, widget.NewLabel("The recycle bin is empty."))
		empty.Disable()
	}
	for _, it := range bin {
		it := it
		restore := widget.NewButtonWithIcon("Restore", theme.ContentUndoIcon(), func() {
			go func() {
				out, err := d.runKmem("kmem", "trash", "restore", it.ID)
				d.do(func() {
					if err != nil {
						dialog.ShowError(fmt.Errorf("kmem: %s", oneLine(firstNonEmpty(strings.TrimSpace(out), err.Error()), 300)), d.win)
					}
					p.reload()
				})
			}()
		})
		restore.Importance = widget.LowImportance
		rows = append(rows, container.NewBorder(nil, nil, nil, restore,
			container.NewVBox(boldLabel("["+it.ID+"] "+it.Head), caption(it.Preview))), widget.NewSeparator())
	}
	rows = append(rows, container.NewHBox(layout.NewSpacer(), empty))
	setBox(p.bin, rows...)
}

func (p *kmemPanel) emptyBin() {
	d := p.d
	dialog.ShowConfirm("Empty the recycle bin?",
		"A terminal opens and runs kmem's final purge (kmem trash empty). kmem lists what will be purged and asks you to type the confirmation there. Purged text is gone for good, also from backups.",
		func(ok bool) {
			if !ok {
				return
			}
			path := firstNonEmpty(kmemBin(), "kmem")
			cmd := terminalCmd(path)
			if err := cmd.Start(); err != nil {
				dialog.ShowError(fmt.Errorf("could not open a terminal (%v); run `kmem trash empty` in a terminal yourself", err), d.win)
				return
			}
			go func() {
				_ = cmd.Wait()
				d.do(p.reload)
			}()
		}, d.win)
}

func (p *kmemPanel) showHandoff(name string) {
	d := p.d
	go func() {
		out, _ := d.runKmem("kmem", "handoff", "get", name)
		d.do(func() {
			dlg := dialog.NewCustom("Handoff “"+name+"”", "Close", container.NewVScroll(wrapLabel(strings.TrimSpace(firstNonEmpty(out, "kmem didn't answer.")))), d.win)
			dlg.Resize(fyne.NewSize(760, 480))
			dlg.Show()
		})
	}()
}

// resumeHandoff starts a live session from a handoff: through its ended
// session when Quill still knows it (same folder, model and mode), else in a
// new session in the home folder.
func (p *kmemPanel) resumeHandoff(name string) {
	p.dlg.Hide()
	p.d.resumeHandoffNamed(name)
}

// resumeHandoffNamed: the live session holding the name if there is one,
// else a fresh session from the handoff (through its ended session when Quill
// still knows it, keeping folder, model and mode; else in the home folder).
func (d *desk) resumeHandoffNamed(name string) *deskSession {
	if owner := d.handoffOwner(name, &deskSession{}); owner != nil {
		d.open(owner.id)
		return owner
	}
	for id, k := range d.org.Kmem {
		if k == name && d.sessions[id] != nil {
			if ns := d.resumeFromHandoff(id); ns != nil {
				return ns
			}
		}
	}
	ns := d.createSession(homeDir(), "default", "default")
	ns.pickPending = true
	d.pickResume(ns, name)
	return ns
}

// trashHandoffNamed moves every fact of a handoff to the kmem recycle bin.
func (d *desk) trashHandoffNamed(name, why string, done func()) {
	go func() {
		var r struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if d.kmemJSON(&r, "handoff", "get", name) {
			for _, it := range r.Items {
				_, _ = d.runKmem("kmem", "trash", it.ID, why)
			}
		}
		if done != nil {
			d.do(done)
		}
	}()
}

func (p *kmemPanel) deleteHandoff(name string, items int) {
	d := p.d
	dialog.ShowConfirm("Delete handoff “"+name+"”?",
		fmt.Sprintf("Its %d fact(s) go to the kmem recycle bin, where you can restore them until you empty the bin.", items),
		func(ok bool) {
			if !ok {
				return
			}
			d.trashHandoffNamed(name, "Quill: the user deleted handoff "+name+" in the kmem panel", p.reload)
		}, d.win)
}

func (p *kmemPanel) memoryTab() fyne.CanvasObject {
	d := p.d
	search := widget.NewEntry()
	search.SetPlaceHolder("Filter topics, or Enter to search all facts")
	p.topics = widget.NewList(func() int { return len(p.topicShown) },
		func() fyne.CanvasObject { return widget.NewLabel("topic") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			t := p.topicShown[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s  (%d, %d KB)", t.Name, t.Facts, t.KB))
		})
	p.memText = wrapLabel("Pick a topic on the left.")
	p.memIDs = widget.NewSelect(nil, nil)
	p.memIDs.PlaceHolder = "fact"
	act := func(label string, icon fyne.Resource, f func(id string)) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, func() {
			if p.memIDs.Selected != "" {
				f(p.memIDs.Selected)
			}
		})
		b.Importance = widget.LowImportance
		return b
	}
	run := func(args ...string) {
		go func() {
			out, err := d.runKmem("kmem", args...)
			d.do(func() {
				if err != nil {
					dialog.ShowError(fmt.Errorf("kmem: %s", oneLine(firstNonEmpty(strings.TrimSpace(out), err.Error()), 300)), d.win)
				}
				p.showTopic(p.memTopic)
			})
		}()
	}
	pin := act("Pin", theme.ConfirmIcon(), func(id string) { run("pin", id) })
	unpin := act("Unpin", theme.CancelIcon(), func(id string) { run("unpin", id) })
	toBin := act("Move to bin...", theme.DeleteIcon(), func(id string) {
		e := widget.NewEntry()
		e.SetPlaceHolder("Why (kept with the item)")
		dialog.ShowForm("Move "+id+" to the recycle bin", "Move", "Cancel", []*widget.FormItem{widget.NewFormItem("Reason", e)}, func(ok bool) {
			if ok && strings.TrimSpace(e.Text) != "" {
				run("trash", id, "Quill (user): "+strings.TrimSpace(e.Text))
			}
		}, d.win)
	})
	p.topics.OnSelected = func(i widget.ListItemID) { p.showTopic(p.topicShown[i].Name) }
	search.OnChanged = p.filterTopics
	search.OnSubmitted = func(q string) {
		if strings.TrimSpace(q) == "" {
			return
		}
		p.memTopic = ""
		go func() {
			out, _ := d.runKmem("kmem", "find", q)
			d.do(func() { p.setMemText(firstNonEmpty(strings.TrimSpace(out), "Nothing found.")) })
		}()
	}
	right := container.NewBorder(nil, container.NewHBox(p.memIDs, pin, unpin, toBin), nil, nil, container.NewVScroll(p.memText))
	split := container.NewHSplit(container.NewBorder(search, nil, nil, nil, p.topics), right)
	split.Offset = 0.32
	return split
}

func (p *kmemPanel) filterTopics(q string) {
	q = strings.ToLower(strings.TrimSpace(q))
	p.topicShown = p.topicShown[:0]
	for _, t := range p.topicList {
		if q == "" || strings.Contains(strings.ToLower(t.Name), q) {
			p.topicShown = append(p.topicShown, t)
		}
	}
	if p.topics != nil {
		p.topics.UnselectAll()
		p.topics.Refresh()
	}
}

func (p *kmemPanel) showTopic(topic string) {
	if topic == "" {
		return
	}
	d := p.d
	p.memTopic = topic
	go func() {
		out, _ := d.runKmem("kmem", "get", topic)
		d.do(func() {
			if p.memTopic == topic {
				p.setMemText(firstNonEmpty(strings.TrimSpace(out), "No current facts."))
			}
		})
	}()
}

func (p *kmemPanel) setMemText(t string) {
	p.memText.SetText(t)
	p.memIDs.Options = factIDs(t)
	p.memIDs.ClearSelected()
	p.memIDs.Refresh()
}
