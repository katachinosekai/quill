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

// isProposal: a rejected task proposal (kmem 1.15: head "proposal, …").
func (it binItem) isProposal() bool {
	return strings.HasPrefix(it.Head, "proposal,") || it.Head == "proposal"
}

// binRestoreArgs: the kmem words (after `kmem` and any --as) restoring it.
// A rejected proposal needs --by-user with the user's own action, recorded
// by kmem (t3851ba); by is that action in words ("the user tapped Restore …").
func binRestoreArgs(it binItem, by string) []string {
	args := []string{"trash", "restore", it.ID}
	if it.isProposal() {
		args = append(args, "--by-user", by)
	}
	return args
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
	rows     []topicTask       // tasks with their checkpoints
	status   map[string]string // top-level topic -> status fact text
	// snapshots: top-level topic -> its topic snapshot (kmem 1.13.0
	// topic-snapshot; nil map with older kmem).
	snapshots map[string]*topicSnapshot
	read      time.Time // when it was read
}

type kmemHandoffRow struct {
	Name  string `json:"name"`
	Items int    `json:"items"`
	Saved string `json:"saved"`
	// ConvertedTo: the topic this old handoff was converted into (kmem
	// 1.13.0 handoff-convert), "" otherwise.
	ConvertedTo string `json:"converted_to"`
}

// showKmemPanel: the kmem window replaced the kmem dialog (user 2026-11-22).
func (d *desk) showKmemPanel() { d.showKmem() }

func (d *desk) newKmemPanel(w *kmemWindow) *kmemPanel {
	p := &kmemPanel{d: d, w: w}
	loading := func() *fyne.Container { return container.NewVBox(widget.NewLabel("Loading from kmem...")) }
	p.over, p.hand, p.tasks = loading(), loading(), loading()
	p.tabs = container.NewAppTabs(
		container.NewTabItem("Overview", container.NewVScroll(p.over)),
		container.NewTabItem("Topics", container.NewVScroll(p.hand)),
		container.NewTabItem("Tasks", container.NewVScroll(p.tasks)),
		container.NewTabItem("Memory", p.memoryTab()),
	)
	d.kmemPanelNow = p
	p.tabs.OnSelected = func(*container.TabItem) { p.shown() }
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
	if p.w.codexShown {
		return p.w.prefillCodex
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
	d.reconcileKmemNames(true, nil) // tab links follow handoffs renamed outside Quill Desktop
	go func() {
		data, _ := d.readKmemPanelData(true)
		needs := data.needs
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

// readKmemPanelData reads what the kmem window shows (background goroutine
// only); the phone API's /phone/kmem/* reads use it too. ok is false when
// kmem didn't answer `kmem brief --json`. withStatus also reads each
// "TOPIC/status" fact (the Topics tab).
func (d *desk) readKmemPanelData(withStatus bool) (*kmemPanelData, bool) {
	return d.readKmemPanelDataWith(withStatus, d.kmemJSONShared, d.runKmemShared)
}

// readKmemPanelDataWith reads through js and run: the shared loader for the
// window, the plain runner for the phone API (its own cache decides).
func (d *desk) readKmemPanelDataWith(withStatus bool, js func(any, ...string) bool, run func(...string) (string, error)) (*kmemPanelData, bool) {
	var brief struct {
		Brief string `json:"brief"`
	}
	ok := js(&brief, "brief")
	topicsOut, _ := run("topics")
	var hs struct {
		Handoffs []kmemHandoffRow `json:"handoffs"`
	}
	js(&hs, "handoff", "list")
	var tr struct {
		Tasks []topicTask `json:"tasks"`
	}
	js(&tr, "task", "list")
	tasks := make([]kmemTask, len(tr.Tasks))
	for i, t := range tr.Tasks {
		tasks[i] = t.kmemTask
	}
	needs := parseKmemNeeds(brief.Brief, tasks)
	kmemMaintInfo(&needs, brief.Brief)
	d.kmemBinInfo(&needs)
	needs.RolloutWait = d.readRolloutWait()
	data := &kmemPanelData{brief: brief.Brief, topics: parseTopics(topicsOut), handoffs: hs.Handoffs, tasks: tasks, needs: needs, rows: tr.Tasks, status: map[string]string{}, read: time.Now()}
	// Assumption (kmem 1.8.1 has no status facts yet): a topic's status
	// is the current fact under "TOPIC/status", read when that topic exists.
	for _, t := range data.topics {
		if top := topLevel(t.Name); withStatus && t.Name == top+"/status" {
			out, _ := d.runKmem("kmem", "get", t.Name)
			data.status[top] = statusText(out)
		}
	}
	if d.kmemHasFeature(topicSnapshotFeature) {
		data.snapshots = readSnapshots(js, data.handoffs, panelTopicNames(data))
	}
	return data, ok
}

// panelTopicNames: the top-level topics the Topics tab lists (kmem's topics
// and the tasks' topics; never "handoff").
func panelTopicNames(data *kmemPanelData) map[string]bool {
	names := map[string]bool{}
	for _, t := range data.topics {
		if top := topLevel(t.Name); top != "handoff" {
			names[top] = true
		}
	}
	for _, t := range data.rows {
		if t.Topic != "" {
			names[topLevel(t.Topic)] = true
		}
	}
	return names
}

// shown: the Overview is on screen (the window is open and the tab selected),
// so what it lists counts as seen (kmemseen.go). The Mark all read button
// goes away with it, so the rows are drawn again.
func (p *kmemPanel) shown() {
	if p.data == nil || p.w == nil || p.w.closed || p.tabs.SelectedIndex() != kmemTabOverview {
		return
	}
	d := p.d
	if d.kmemUnread() == 0 {
		return
	}
	d.markKmemSeen(false)
}

// render fills Overview, Handoffs and Tasks from the last reload (again when
// the chat turns on or off, for the buttons that fill its input).
func (p *kmemPanel) render() {
	data := p.data
	if data == nil {
		return
	}
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
	ov = append(ov, widget.NewSeparator(), caption(fmt.Sprintf("%d topics, %d handoffs, %d open tasks; %s (Settings → General → Memory (kmem)).", len(data.topics), len(data.handoffs), len(data.tasks), needs.binText(kmemNow()))))
	setBox(p.over, ov...)
	setBox(p.hand, p.topicsRows(data)...)
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
	less := sortLess()
	sort.Slice(order, func(i, j int) bool { return less(order[i], order[j]) })
	trows := []fyne.CanvasObject{caption("Open and claimed tasks by topic (read-only here; sessions claim and finish them).")}
	for _, topic := range order {
		trows = append(trows, boldLabel(topic))
		for _, l := range by[topic] {
			trows = append(trows, wrapLabel(l))
		}
	}
	setBox(p.tasks, trows...)
	p.shown()
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
	head := fyne.CanvasObject(boldLabel(fmt.Sprintf("Waiting for you (%d)", n.Count())))
	if d.kmemUnread() > 0 {
		mark := widget.NewButtonWithIcon("Mark all read", theme.ConfirmIcon(), func() { d.markKmemSeen(true) })
		mark.Importance = widget.LowImportance
		head = container.NewBorder(nil, nil, nil, mark, head)
	}
	rows := []fyne.CanvasObject{head}
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
		rows = append(rows, row(n.binText(kmemNow())+".",
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
		rows = append(rows, row(n.maintLine(kmemNow()), open, ask("Explain", about+".")))
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
	// follow renames made outside Quill Desktop first, so a NEW handoff
	// under an old name never opens the renamed tab (bug 2026-11-22)
	p.d.reconcileKmemNames(true, func(kmemRenameScan) { p.d.resumeHandoffNamed(name) })
}

// resumeHandoffNamed: the live session holding the name if there is one,
// else a fresh session from the handoff (through its ended session when Quill
// still knows it, keeping folder, model and mode; else in the home folder).
// The kmem operator's handoff opens the kmem window instead (§4 #21); its
// assistant (if started yet) or the live owner comes back, else an empty
// session.
func (d *desk) resumeHandoffNamed(name string) *deskSession {
	d.setDormant(name, false)
	switch name {
	case roleCodexQuill:
		d.openQuillCodex()
		a := d.codexAssistant(codexQuillRole)
		d.startAgent(a)
		return a.s
	case roleCodexKmem:
		d.openKmemCodex()
		a := d.codexAssistant(codexKmemRole)
		d.startAgent(a)
		return a.s
	}
	if name == kmemWinOperator {
		d.showKmem()
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
	if dir := d.org.CodexTopics[name]; dir != "" {
		return d.openCodexTopic(topicInfo{Slug: name, Title: topicTitle(name)}, dir)
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
			out, _ := d.runKmem("kmem", "find", "--", q)
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

// topicTitleTexts: the titles known for a kmem topic: the one typed in New
// topic, kmem's config label (title, as) and the title of a handoff name.
func (d *desk) topicTitleTexts(name string) []string {
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	top := topLevel(name)
	add(d.org.TopicTitles[top])
	add(d.org.KmemTitles[name])
	c := kmemSessions()[top]
	add(c.Title)
	add(c.As)
	return out
}

// titlesMatch: some title contains the query (already lower-cased and
// trimmed), compared with titleKey normalising (NFC, spaces) and the same
// case folding as the name search (foldContains; q is already foldKey'd).
func titlesMatch(titles []string, q string) bool {
	for _, t := range titles {
		if foldContains(titleKey(t), titleKey(q)) {
			return true
		}
	}
	return false
}

func (p *kmemPanel) filterTopics(q string) {
	q = foldKey(strings.TrimSpace(q))
	p.topicShown = p.topicShown[:0]
	for _, t := range p.topicList {
		if q == "" || foldContains(t.Name, q) || titlesMatch(p.d.topicTitleTexts(t.Name), q) {
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

// topicTask is a task row with its last checkpoint (empty until kmem's task
// list reports one).
type topicTask struct {
	kmemTask
	Checkpoint string `json:"checkpoint"`
}

func topLevel(name string) string {
	if i := strings.Index(name, "/"); i > 0 {
		return name[:i]
	}
	return name
}

// statusText: the first line of `kmem get T/status`, without the id prefix.
func statusText(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			l = strings.TrimSpace(factID.ReplaceAllString(l, ""))
			if i := strings.Index(l, ": "); i > 0 && i < 16 && !strings.Contains(l[:i], " ") {
				l = l[i+2:] // the kind prefix ("decision: ")
			}
			return oneLine(l, 240)
		}
	}
	return ""
}

// topicsRows builds the read-only Topics tab: per top-level kmem topic its
// title (when kmem stores one), the status fact, the topic snapshot (kmem
// 1.13.0), claimed tasks (checkpoint, owner), open tasks and a live marker;
// old handoffs go under "History (read-only)" with Show only (a topic's
// snapshot is shown with its topic instead).
func (p *kmemPanel) topicsRows(data *kmemPanelData) []fyne.CanvasObject {
	d := p.d
	names := panelTopicNames(data)
	now := time.Now()
	var order []string
	for n := range names {
		order = append(order, n)
	}
	less := sortLess()
	sort.Slice(order, func(i, j int) bool { return less(order[i], order[j]) })
	rows := []fyne.CanvasObject{caption("Read-only view of kmem's topics: status, claimed and open tasks. Sessions claim and finish tasks; nothing is changed here.")}
	if len(order) == 0 {
		rows = append(rows, wrapLabel("No topics yet: kmem has no topics or tasks to show."))
	}
	for _, name := range order {
		live := "not live"
		if owner := d.handoffOwner(name, &deskSession{}); owner != nil {
			live = "live in a tab"
			if isHiddenSession(owner) {
				live = roleDef(hiddenRole(owner)).owner
			}
		}
		var claimed, open []topicTask
		for _, t := range data.rows {
			if topLevel(t.Topic) != name {
				continue
			}
			if t.Owner != nil && *t.Owner != "" {
				claimed = append(claimed, t)
			} else {
				open = append(open, t)
			}
		}
		status := firstNonEmpty(data.status[name], "no status fact yet")
		head := name
		if t, ok := cachedTopic(name); ok && t.KmemTitle != "" && t.KmemTitle != name {
			head = t.KmemTitle + " (" + name + ")"
		}
		rows = append(rows, boldLabel(head+"  ·  "+live), wrapLabel("Status: "+status))
		if snap := data.snapshots[name]; snap != nil {
			lines := snapshotLines(snap, now)
			rows = append(rows, caption(lines[0]))
			for _, l := range lines[1:] {
				rows = append(rows, wrapLabel(l))
			}
		}
		for _, t := range claimed {
			line := fmt.Sprintf("Claimed [%s] %s (owner %s)", t.ID, t.Title, *t.Owner)
			if t.Checkpoint != "" {
				line += "; checkpoint: " + t.Checkpoint
			}
			rows = append(rows, wrapLabel(line))
		}
		for _, t := range open {
			rows = append(rows, wrapLabel(fmt.Sprintf("Open [%s] p%d %s", t.ID, t.Prio, t.Title)))
		}
		if len(claimed)+len(open) == 0 {
			rows = append(rows, caption("No claimed or open tasks."))
		}
		rows = append(rows, widget.NewSeparator())
	}
	var hist []fyne.CanvasObject
	for _, h := range data.handoffs {
		h := h
		if data.snapshots[h.Name] != nil {
			continue // shown under its topic
		}
		saved := h.Saved
		if t, err := time.Parse(time.RFC3339, h.Saved); err == nil {
			saved = ago(t)
		}
		show := widget.NewButtonWithIcon("Show", theme.VisibilityIcon(), func() { p.showHandoff(h.Name) })
		show.Importance = widget.LowImportance
		hist = append(hist, container.NewBorder(nil, nil,
			container.NewVBox(boldLabel(h.Name), caption(handoffMeta(saved, h))), nil,
			container.NewHBox(layout.NewSpacer(), show)))
	}
	if len(hist) == 0 {
		hist = append(hist, caption("No old handoffs."))
	}
	findOld := widget.NewButtonWithIcon("Find old conversation", theme.SearchIcon(), p.showFindOld)
	findOld.Importance = widget.LowImportance
	acc := widget.NewAccordion(widget.NewAccordionItem("History (read-only)", container.NewVBox(hist...)))
	return append(rows, container.NewHBox(findOld), acc)
}

// handoffMeta: the history row's caption ("saved 2d ago · 3 items", plus
// the topic an old handoff was converted into).
func handoffMeta(saved string, h kmemHandoffRow) string {
	m := fmt.Sprintf("saved %s · %d items", saved, h.Items)
	if h.ConvertedTo != "" {
		m += " · converted to topic " + h.ConvertedTo
	}
	return m
}
