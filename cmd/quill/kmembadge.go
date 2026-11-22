package main

import (
	"context"
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

// The kmem button's bubble counts what kmem is asking the user for (user
// 2026-11-22, replaces the kmem banner): items in the recycle bin, audit
// problems (kmem rules 21 and 26), maintenance warnings, and tasks waiting for
// the user (topic "user" or a title starting "USER:"). Audit problems and
// maintenance warnings count once each. The kmem panel's Overview lists them
// under "Waiting for you"; the recycle bin lives in Settings → General →
// Memory (kmem).

var kmemBannerEvery = 30 * time.Minute

var (
	briefBin   = regexp.MustCompile(`(?m)^bin: (\d+) item`)
	briefMaint = regexp.MustCompile(`(?m)^maint: [^\n]*?(\d+) warnings?(?: \(([^)\n]*)\))?`)
)

type kmemTask struct {
	ID     string  `json:"id"`
	Prio   int     `json:"prio"`
	Status string  `json:"status"`
	Title  string  `json:"title"`
	Topic  string  `json:"topic"`
	Owner  *string `json:"owner"`
}

// kmemNeeds is what kmem waits on the user for.
type kmemNeeds struct {
	Bin         int
	Audit       string
	Maint       int    // maintenance warnings
	MaintReport string // the report file named in the brief
	UserTasks   []kmemTask
}

// userTask: a task only the user can act on.
func userTask(t kmemTask) bool {
	return t.Topic == "user" || strings.HasPrefix(t.Topic, "user/") || strings.HasPrefix(strings.ToUpper(strings.TrimSpace(t.Title)), "USER:")
}

// parseKmemNeeds reads `kmem brief` and the open tasks.
func parseKmemNeeds(brief string, tasks []kmemTask) kmemNeeds {
	var n kmemNeeds
	if m := briefBin.FindStringSubmatch(brief); m != nil {
		n.Bin, _ = strconv.Atoi(m[1])
	}
	if m := briefMaint.FindStringSubmatch(brief); m != nil {
		n.Maint, _ = strconv.Atoi(m[1])
		if n.Maint > 0 {
			n.MaintReport = strings.TrimSpace(m[2])
		}
	}
	for _, l := range strings.Split(brief, "\n") {
		if strings.HasPrefix(l, "AUDIT PROBLEMS: ") {
			n.Audit = strings.TrimSpace(strings.TrimPrefix(l, "AUDIT PROBLEMS: "))
		}
	}
	for _, t := range tasks {
		if t.Status != "done" && userTask(t) {
			n.UserTasks = append(n.UserTasks, t)
		}
	}
	return n
}

// Count is the number on the bubble.
func (n kmemNeeds) Count() int {
	c := n.Bin + len(n.UserTasks)
	if n.Audit != "" {
		c++
	}
	if n.Maint > 0 {
		c++
	}
	return c
}

func badgeText(c int) string {
	if c > 99 {
		return "99+"
	}
	return strconv.Itoa(c)
}

// badgeLayout: the button fills the box; the bubble sits on its top-right
// corner, half outside it.
type badgeLayout struct{}

func (badgeLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize()
}

func (badgeLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	objs[0].Move(fyne.NewPos(0, 0))
	objs[0].Resize(size)
	b := objs[1].MinSize()
	objs[1].Resize(b)
	objs[1].Move(fyne.NewPos(size.Width-b.Width*0.75, -b.Height*0.25))
}

// bubble is the round count drawn over the kmem button.
type bubble struct {
	box  *fyne.Container
	dot  *canvas.Circle
	text *canvas.Text
}

func newBubble() *bubble {
	b := &bubble{dot: canvas.NewCircle(colAccent), text: canvas.NewText("", color.White)}
	b.text.TextSize = theme.CaptionTextSize()
	b.text.TextStyle = fyne.TextStyle{Bold: true}
	b.text.Alignment = fyne.TextAlignCenter
	b.box = container.New(bubbleLayout{}, b.dot, b.text)
	b.box.Hide()
	return b
}

type bubbleLayout struct{}

func (bubbleLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	t := objs[1].MinSize()
	h := t.Height + 2
	return fyne.NewSize(fyne.Max(h, t.Width+8), h)
}

func (bubbleLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}

func (b *bubble) set(c int) {
	if c <= 0 {
		b.box.Hide()
		return
	}
	b.text.Text = badgeText(c)
	b.box.Show()
	b.box.Refresh()
}

// kmemButtonWithBubble wraps the toolbar's kmem button with its bubble.
func (d *desk) kmemButtonWithBubble() fyne.CanvasObject {
	d.kmemBubble = newBubble()
	return container.New(badgeLayout{}, d.kmemBtn, d.kmemBubble.box)
}

// setKmemNeeds updates the bubble and the Settings bin button (UI thread).
func (d *desk) setKmemNeeds(n kmemNeeds) {
	d.kmemNeedsNow = n
	if d.kmemBubble != nil {
		d.kmemBubble.set(n.Count())
	}
	if d.kmemBinSetBtn != nil {
		d.kmemBinSetBtn.SetText(binButtonText(n.Bin))
	}
}

func binButtonText(bin int) string {
	if bin > 0 {
		return fmt.Sprintf("Recycle bin (%d)...", bin)
	}
	return "Recycle bin..."
}

// readKmemNeeds asks kmem (background goroutine only).
func (d *desk) readKmemNeeds() (kmemNeeds, bool) {
	var brief struct {
		Brief string `json:"brief"`
	}
	if !d.kmemJSON(&brief, "brief") {
		return kmemNeeds{}, false
	}
	var ts struct {
		Tasks []kmemTask `json:"tasks"`
	}
	d.kmemJSON(&ts, "task", "list")
	return parseKmemNeeds(brief.Brief, ts.Tasks), true
}

// refreshKmemNeeds asks kmem in the background and updates the bubble.
func (d *desk) refreshKmemNeeds() {
	d.refreshKmemVer()
	if !d.kmemAvailable() {
		return
	}
	go func() {
		if n, ok := d.readKmemNeeds(); ok {
			d.do(func() { d.setKmemNeeds(n) })
		}
	}()
}

// watchKmemNeeds: once at start, then every kmemBannerEvery until ctx ends.
func (d *desk) watchKmemNeeds(ctx context.Context) {
	d.do(d.refreshKmemNeeds)
	t := time.NewTicker(kmemBannerEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.do(d.refreshKmemNeeds)
		}
	}
}

const (
	kmemTabOverview = 0
	kmemTabTasks    = 2
)

// showKmemPanelTab opens the kmem panel on one of its tabs.
func (d *desk) showKmemPanelTab(tab int) {
	d.showKmemPanel()
	if p := d.kmemPanelNow; p != nil && p.tabs != nil && tab < len(p.tabs.Items) {
		p.tabs.SelectIndex(tab)
	}
}
