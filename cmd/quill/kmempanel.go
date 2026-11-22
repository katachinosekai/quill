package main

import (
	"fmt"
	"github.com/katachinosekai/quill/internal/transcript"
	"os"
	"path/filepath"
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

// The kmem panel (user 2026-11-21; the left side of the kmem window since
// 2026-11-22): ended sessions live on as kmem handoffs,
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

// binMeta / binReason split kmem's head line "fact, 1.0 days in bin, by X:
// reason" into what it is and why it is in the bin.
func (it binItem) binMeta() string {
	if i := strings.Index(it.Head, ": "); i > 0 {
		return it.Head[:i]
	}
	return it.Head
}

func (it binItem) binReason() string {
	if i := strings.Index(it.Head, ": "); i > 0 {
		return strings.TrimSpace(it.Head[i+2:])
	}
	return ""
}

var binIDRe = regexp.MustCompile(`^[a-z0-9]{3,16}$`)

func validBinID(id string) bool { return binIDRe.MatchString(id) }

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
	d                 *desk
	w                 *kmemWindow // the window the panel is the left side of
	tabs              *container.AppTabs
	over, hand, tasks *fyne.Container
	topics            *widget.List
	topicList         []kmemTopic
	topicShown        []kmemTopic
	memText           *widget.Label
	memIDs            *widget.Select
	memTopic          string
	data              *kmemPanelData // the last reload, nil while loading
}

// kmemPanelData is what one reload read from kmem.
type kmemPanelData struct {
	brief    string
	topics   []kmemTopic
	handoffs []kmemHandoffRow
	tasks    []kmemTask
	needs    kmemNeeds
}

type kmemHandoffRow struct {
	Name  string `json:"name"`
	Items int    `json:"items"`
	Saved string `json:"saved"`
}

// showKmemPanel: the kmem window replaced the kmem dialog (user 2026-11-22).
func (d *desk) showKmemPanel() { d.openKmemWindow() }

func (d *desk) newKmemPanel(w *kmemWindow) *kmemPanel {
	p := &kmemPanel{d: d, w: w}
	loading := func() *fyne.Container { return container.NewVBox(widget.NewLabel("Loading from kmem...")) }
	p.over, p.hand, p.tasks = loading(), loading(), loading()
	p.tabs = container.NewAppTabs(
		container.NewTabItem("Overview", container.NewVScroll(p.over)),
		container.NewTabItem("Handoffs", container.NewVScroll(p.hand)),
		container.NewTabItem("Tasks", container.NewVScroll(p.tasks)),
		container.NewTabItem("Memory", p.memoryTab()),
	)
	d.kmemPanelNow = p
	return p
}

// parent: the window the panel's dialogs open on (the kmem window).
func (p *kmemPanel) parent() fyne.Window {
	if pw := p.w.parent(); pw != nil {
		return pw
	}
	return p.d.win
}

// prefill: the chat input filler while the operator's chat is on, else nil.
func (p *kmemPanel) prefill() func(string) {
	if p.w == nil {
		return nil
	}
	return p.w.prefill
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
			Handoffs []kmemHandoffRow `json:"handoffs"`
		}
		d.kmemJSON(&hs, "handoff", "list")
		var ts struct {
			Tasks []kmemTask `json:"tasks"`
		}
		d.kmemJSON(&ts, "task", "list")
		needs := parseKmemNeeds(brief.Brief, ts.Tasks)
		needs.RolloutWait = d.readRolloutWait()
		data := &kmemPanelData{brief: brief.Brief, topics: parseTopics(topicsOut), handoffs: hs.Handoffs, tasks: ts.Tasks, needs: needs}
		d.do(func() {
			d.setKmemNeeds(needs)
			p.data = data
			p.render()
			// Memory topics
			p.topicList = data.topics
			p.filterTopics("")
		})
	}()
}

// render fills Overview, Handoffs and Tasks from the last reload (again when
// the chat turns on or off, for the buttons that fill its input).
func (p *kmemPanel) render() {
	data := p.data
	if data == nil {
		return
	}
	d := p.d
	needs := data.needs
	fill := p.prefill()
	// Overview
	var big []string
	for _, t := range data.topics {
		if t.KB > 8 {
			big = append(big, fmt.Sprintf("%s (%d KB)", t.Name, t.KB))
		}
	}
	ov := append(p.waitingRows(needs), boldLabel("kmem brief"), wrapLabel(strings.TrimSpace(firstNonEmpty(data.brief, "kmem didn't answer."))))
	if len(big) > 0 {
		ov = append(ov, widget.NewSeparator(), boldLabel("Sections over 8 KB (maintenance asks to split them)"))
		list := wrapLabel(strings.Join(big, ", "))
		if fill != nil {
			split := widget.NewButtonWithIcon("Suggest split", theme.ContentCutIcon(), func() {
				fill("Suggest how to split these kmem sections over 8 KB (rule 29: split downward, dry run first): " + strings.Join(big, ", "))
			})
			split.Importance = widget.LowImportance
			ov = append(ov, container.NewBorder(nil, nil, nil, split, list))
		} else {
			ov = append(ov, list)
		}
	}
	ov = append(ov, widget.NewSeparator(), caption(fmt.Sprintf("%d topics, %d handoffs, %d open tasks, %d item(s) in the recycle bin (Settings → General → Memory (kmem)).", len(data.topics), len(data.handoffs), len(data.tasks), needs.Bin)))
	setBox(p.over, ov...)
	// Handoffs
	var hrows []fyne.CanvasObject
	hrows = append(hrows, caption("Ended sessions live on as kmem handoffs. Resume starts a live session from one; Delete moves its facts to the recycle bin."))
	for _, h := range data.handoffs {
		h := h
		saved := h.Saved
		if t, err := time.Parse(time.RFC3339, h.Saved); err == nil {
			saved = ago(t)
		}
		state := "ended"
		owner := d.handoffOwner(h.Name, &deskSession{})
		inWindow := isHiddenSession(owner) || owner != nil && owner == p.w.session()
		if inWindow {
			state = "live in the kmem window"
		} else if owner != nil {
			title, _, _ := d.rowText(owner.id)
			state = "live in “" + title + "”"
		}
		show := widget.NewButtonWithIcon("Show", theme.VisibilityIcon(), func() { p.showHandoff(h.Name) })
		resume := widget.NewButtonWithIcon("Resume", theme.MediaPlayIcon(), func() { p.resumeHandoff(h.Name) })
		del := widget.NewButtonWithIcon("Delete...", theme.DeleteIcon(), func() { p.deleteHandoff(h.Name, h.Items) })
		if owner != nil {
			resume.SetText("Open")
			id := owner.id
			resume.OnTapped = func() {
				if inWindow {
					d.openKmemWindow()
					return
				}
				d.showWindow()
				d.open(id)
			}
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
	for _, t := range data.tasks {
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
}

// waitingRows: "Waiting for you" at the top of Overview, one row per thing
// the kmem button's bubble counts; nothing while kmem asks for nothing. The
// Explain and Ask the operator buttons fill the chat input (never send) and
// show only while the operator's chat is on.
func (p *kmemPanel) waitingRows(n kmemNeeds) []fyne.CanvasObject {
	if n.Count() == 0 {
		return nil
	}
	d := p.d
	fill := p.prefill()
	ask := func(label string, text string) *widget.Button {
		if fill == nil {
			return nil
		}
		return widget.NewButtonWithIcon(label, theme.HelpIcon(), func() { fill(text) })
	}
	row := func(text string, btns ...*widget.Button) fyne.CanvasObject {
		var box []fyne.CanvasObject
		for _, b := range btns {
			if b != nil {
				b.Importance = widget.LowImportance
				box = append(box, b)
			}
		}
		if len(box) == 0 {
			return wrapLabel(text)
		}
		return container.NewBorder(nil, nil, nil, container.NewHBox(box...), wrapLabel(text))
	}
	rows := []fyne.CanvasObject{boldLabel(fmt.Sprintf("Waiting for you (%d)", n.Count()))}
	if len(n.Issues) > 0 {
		var titles, ids []string
		for _, t := range n.Issues {
			titles = append(titles, "["+t.ID+"] "+oneLine(t.Title, 120))
			ids = append(ids, t.ID)
		}
		var btn *widget.Button
		if fill != nil {
			btn = ask("Ask the operator", "Please handle the open kmem issue tasks from other sessions ("+strings.Join(ids, ", ")+"): propose a plan for each.")
		} else {
			btn = widget.NewButtonWithIcon("Tasks", theme.ListIcon(), func() { p.tabs.SelectIndex(kmemTabTasks) })
		}
		rows = append(rows, row(fmt.Sprintf("%d kmem issue(s) from other sessions: %s", len(n.Issues), strings.Join(titles, "; ")), btn))
	}
	if n.Bin > 0 {
		rows = append(rows, row(fmt.Sprintf("%d item(s) in the recycle bin wait for you to restore or delete them.", n.Bin),
			widget.NewButtonWithIcon("Review bin", theme.DeleteIcon(), func() { d.showBinDialogOn(p.parent()) })))
	}
	if n.Audit != "" {
		rows = append(rows, row("kmem audit found problems (only you decide how to repair them): "+n.Audit,
			ask("Explain", "Explain these kmem audit problems (explain only; I decide how to repair them): "+n.Audit)))
	}
	if n.Maint > 0 {
		var open *widget.Button
		if n.MaintReport != "" {
			report := n.MaintReport
			open = widget.NewButtonWithIcon("Open report", theme.FileTextIcon(), func() { d.openPath(report) })
		}
		about := fmt.Sprintf("Explain the %d warning(s) of kmem's daily maintenance", n.Maint)
		if n.MaintReport != "" {
			about += " (report " + n.MaintReport + ")"
		}
		rows = append(rows, row(fmt.Sprintf("Daily maintenance has %d warning(s).", n.Maint), open, ask("Explain", about+".")))
	}
	if n.RolloutWait != "" {
		rows = append(rows, row("A kmem rollout is paused, waiting for you: "+n.RolloutWait, nil))
	}
	if len(n.UserTasks) > 0 {
		var titles []string
		for _, t := range n.UserTasks {
			titles = append(titles, "["+t.ID+"] "+oneLine(t.Title, 120))
		}
		rows = append(rows, row(fmt.Sprintf("%d task(s) wait for you: %s", len(n.UserTasks), strings.Join(titles, "; ")),
			widget.NewButtonWithIcon("Tasks", theme.ListIcon(), func() { p.tabs.SelectIndex(kmemTabTasks) })))
	}
	return append(rows, widget.NewSeparator())
}

func (p *kmemPanel) showHandoff(name string) {
	d := p.d
	go func() {
		out, _ := d.runKmem("kmem", "handoff", "get", name)
		d.do(func() {
			dlg := dialog.NewCustom("Handoff “"+name+"”", "Close", container.NewVScroll(wrapLabel(strings.TrimSpace(firstNonEmpty(out, "kmem didn't answer.")))), p.parent())
			dlg.Resize(fyne.NewSize(760, 480))
			dlg.Show()
		})
	}()
}

// resumeHandoff starts a live session from a handoff: through its ended
// session when Quill still knows it (same folder, model and mode), else in a
// new session in the home folder.
func (p *kmemPanel) resumeHandoff(name string) {
	if name != kmemWinOperator {
		p.d.showWindow()
	}
	p.d.resumeHandoffNamed(name)
}

// resumeHandoffNamed: the live session holding the name if there is one,
// else a fresh session from the handoff (through its ended session when Quill
// still knows it, keeping folder, model and mode; else in the home folder).
// The kmem operator's handoff opens the kmem window instead (§4 #21); its
// assistant (if started yet) or the live owner comes back, else an empty
// session.
func (d *desk) resumeHandoffNamed(name string) *deskSession {
	if name == kmemWinOperator {
		d.openKmemWindow()
		if s := d.kmemWin.session(); s != nil {
			return s
		}
		if owner := d.handoffOwner(name, &deskSession{}); owner != nil {
			return owner
		}
		return &deskSession{}
	}
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

var kmemNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

func validKmemName(s string) bool { return kmemNameRe.MatchString(s) }

// resumeNamedFromCLI: `quill --resume NAME` (user 2026-11-21): bring the
// window up and open that kmem session, or start one from its handoff.
func (d *desk) resumeNamedFromCLI(name string) {
	if name == kmemWinOperator && d.kmemAvailable() {
		d.openKmemWindow()
		return
	}
	d.showWindow()
	if !d.kmemAvailable() {
		return
	}
	if d.handoffOwner(name, &deskSession{}) != nil {
		d.resumeHandoffNamed(name)
		return
	}
	go func() {
		known := false
		for _, h := range d.kmemHandoffs() { // runs kmem: off the UI thread
			known = known || h.Name == name
		}
		d.do(func() {
			if known {
				d.resumeHandoffNamed(name)
			} else if s := d.current; s != nil {
				d.notify(s, "quill --resume: there is no kmem handoff or live session called “"+name+"”.")
			}
		})
	}()
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
			d.trashHandoffNamed(name, "Quill: the user deleted handoff "+name+" in the kmem window", p.reload)
		}, p.parent())
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
					dialog.ShowError(fmt.Errorf("kmem: %s", oneLine(firstNonEmpty(strings.TrimSpace(out), err.Error()), 300)), p.parent())
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
		}, p.parent())
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
	auto := widget.NewButtonWithIcon("Claude's auto memory (this project)", theme.DocumentIcon(), func() {
		p.memTopic = ""
		p.topics.UnselectAll()
		p.setMemText(autoMemoryText(d.cwdOf(currentID(d))))
	})
	auto.Importance = widget.LowImportance
	right := container.NewBorder(nil, container.NewHBox(p.memIDs, pin, unpin, toBin), nil, nil, container.NewVScroll(p.memText))
	split := container.NewHSplit(container.NewBorder(container.NewVBox(search, auto), nil, nil, nil, p.topics), right)
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

func currentID(d *desk) string {
	if d.current != nil {
		return d.current.id
	}
	return ""
}

// autoMemoryText shows Claude Code's own auto memory for a project folder
// (~/.claude/projects/<folder>/memory/MEMORY.md and the files next to it),
// read only, so both kinds of memory are visible in one place.
func autoMemoryText(cwd string) string {
	if cwd == "" {
		return "Open a session first: its project's auto memory is shown here."
	}
	dir := filepath.Join(transcript.DefaultRoot(), transcript.Sanitize(cwd), "memory")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "No auto memory for " + folderLabel(cwd) + " (" + tildePath(dir) + " doesn't exist). Claude Code writes it when auto memory is on for this project."
	}
	var b strings.Builder
	b.WriteString("Claude's auto memory for " + folderLabel(cwd) + " (read only; " + tildePath(dir) + ")\n\n")
	files := []string{"MEMORY.md"}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "MEMORY.md" {
			files = append(files, e.Name())
		}
	}
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		if len(data) > 64<<10 {
			data = append(data[:64<<10], []byte("\n… (cut)")...)
		}
		b.WriteString("── " + f + " ──\n" + strings.TrimSpace(string(data)) + "\n\n")
	}
	return strings.TrimSpace(b.String())
}
