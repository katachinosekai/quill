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

	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	usageMinMargin = 230
	usageMaxWidth  = 280
	usageGap       = 36
	usageEdge      = 16
)

type usagePanel struct {
	d       *desk
	root    *fyne.Container
	five    *widget.ProgressBar
	fiveTxt *canvas.Text
	week    *widget.ProgressBar
	weekTxt *canvas.Text
	asOf    *canvas.Text
	sess    *widget.Label
	today   *widget.Label

	strip    []*stripItem
	stripRow *fyne.Container
}

func usageText(s string) *canvas.Text {
	t := canvas.NewText(s, colText)
	t.TextSize = fontMeta
	return t
}

func newUsagePanel(d *desk) *usagePanel {
	u := &usagePanel{d: d}
	u.five = widget.NewProgressBar()
	u.week = widget.NewProgressBar()
	u.fiveTxt = usageText("")
	u.weekTxt = usageText("")
	u.asOf = usageText("")
	label := func() *widget.Label {
		l := widget.NewLabel("")
		l.Wrapping = fyne.TextWrapWord
		l.SizeName = theme.SizeNameCaptionText
		return l
	}
	u.sess, u.today = label(), label()
	head := func(s string) fyne.CanvasObject {
		t := canvas.NewText(s, colText)
		t.TextStyle.Bold = true
		t.TextSize = fontBody
		return t
	}
	body := container.NewVBox(
		head("Plan usage"),
		usageText("5-hour session"), u.five, u.fiveTxt,
		usageText("Weekly"), u.week, u.weekTxt, u.asOf,
		padTop(8), head("This session"), u.sess,
		padTop(8), head("All sessions"), u.today,
	)
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 12
	u.root = container.NewStack(r, container.NewPadded(container.NewPadded(body)))
	if b := d.app.Preferences().String("rateLimit"); b != "" {
		var saved struct {
			At   int64               `json:"at"`
			Info *protocol.RateLimit `json:"info"`
		}
		if json.Unmarshal([]byte(b), &saved) == nil && saved.Info != nil {
			d.rate, d.rateAt = saved.Info, time.Unix(saved.At, 0)
		}
	}
	return u
}

func (d *desk) setRateLimit(r *protocol.RateLimit) {
	if r == nil {
		return
	}
	if d.rate != nil && r.UnifiedWindows == nil {
		merged := *d.rate
		merged.Status, merged.Type, merged.Utilization, merged.ResetsAt = r.Status, r.Type, r.Utilization, r.ResetsAt
		if merged.UnifiedWindows != nil {
			w := map[string]protocol.RateWindow{}
			for k, v := range merged.UnifiedWindows {
				w[k] = v
			}
			w[r.Type] = protocol.RateWindow{Utilization: r.Utilization, ResetsAt: r.ResetsAt, Status: r.Status}
			merged.UnifiedWindows = w
		}
		r = &merged
	}
	d.rate, d.rateAt = r, time.Now()
	if b, err := json.Marshal(map[string]any{"at": d.rateAt.Unix(), "info": r}); err == nil {
		d.app.Preferences().SetString("rateLimit", string(b))
	}
	d.usage.update()
	d.syncLimitBanner()
}

func (d *desk) limitMessage() string {
	if d.rate == nil {
		return ""
	}
	now := time.Now().Unix()
	check := func(status string, util float64, resets int64) string {
		if resets > 0 && resets <= now {
			return ""
		}
		if status == "rejected" || util >= 1 {
			msg := "Usage limit reached"
			if r := resetText(resets); r != "" {
				msg += " · " + r
			}
			return msg + "\nClaude can't answer until then."
		}
		return ""
	}
	for _, name := range []string{"five_hour", "seven_day"} {
		if w, ok := d.rate.Window(name); ok {
			if m := check(w.Status, w.Utilization, w.ResetsAt); m != "" {
				return m
			}
		}
	}
	return check(d.rate.Status, d.rate.Utilization, d.rate.ResetsAt)
}

func (d *desk) syncLimitBanner() {
	msg := d.limitMessage()
	views := []*chatView{d.single}
	for _, p := range d.panes {
		views = append(views, p.view)
	}
	for _, v := range views {
		if v == nil || v.limit == nil {
			continue
		}
		if msg == "" {
			if v.limit.Visible() {
				v.limit.Hide()
			}
			continue
		}
		if v.limitText.Text != msg {
			v.limitText.SetText(msg)
		}
		if !v.limit.Visible() {
			v.limit.Show()
		}
	}
}

func tok(n int64) string {
	return kfmt(int(n))
}

func resetText(unix int64) string {
	if unix <= 0 {
		return ""
	}
	t := time.Unix(unix, 0)
	if time.Until(t) < 24*time.Hour {
		return "resets " + t.Format("3:04 PM")
	}
	return "resets " + t.Format("Mon Jan 2, 3:04 PM")
}

func (u *usagePanel) update() {
	d := u.d
	setWin := func(bar *widget.ProgressBar, txt *canvas.Text, name string) {
		w, ok := d.rate.Window(name)
		if !ok {
			if bar.Value != 0 {
				bar.SetValue(0)
			}
			setText(txt, "no data yet (updates after a reply)")
			return
		}
		if bar.Value != w.Utilization {
			bar.SetValue(w.Utilization)
		}
		setText(txt, fmt.Sprintf("%d%% used · %s", int(w.Utilization*100+0.5), resetText(w.ResetsAt)))
	}
	setWin(u.five, u.fiveTxt, "five_hour")
	setWin(u.week, u.weekTxt, "seven_day")
	asOf := ""
	if !d.rateAt.IsZero() {
		asOf = "as of " + ago(d.rateAt)
	}
	setText(u.asOf, asOf)

	sess := "No session open"
	if s := d.current; s != nil {
		info := d.infos[s.id]
		sess = tok(info.Usage.Total()) + " tokens"
		c := s.conv.Stats
		if c.ContextUsed > 0 {
			sess += "  ·  context " + kfmt(c.ContextUsed)
			if c.ContextMax > 0 {
				sess += fmt.Sprintf(" (%d%%)", c.ContextUsed*100/c.ContextMax)
			}
		}
	}
	setLabel(u.sess, sess)

	var today, week transcript.Tokens
	now := time.Now()
	todayKey := now.Format("2006-01-02")
	weekKeys := map[string]bool{}
	for i := 0; i < 7; i++ {
		weekKeys[now.AddDate(0, 0, -i).Format("2006-01-02")] = true
	}
	for _, info := range d.order {
		for day, t := range info.Days {
			if day == todayKey {
				today = today.Add(t)
			}
			if weekKeys[day] {
				week = week.Add(t)
			}
		}
	}
	setLabel(u.today, "Today "+tok(today.Total())+"  ·  7 days "+tok(week.Total()))
	short := strings.Replace(strings.Replace(sess, "  ·  context ", " · ctx ", 1), " tokens", " tok", 1)
	u.syncStrip(short, "Today "+tok(today.Total())+" · 7d "+tok(week.Total()))
}

type stripItem struct {
	bar   *widget.ProgressBar
	value *canvas.Text
}

func (u *usagePanel) buildStrip() fyne.CanvasObject {
	item := func(title string, withBar bool) (fyne.CanvasObject, *stripItem) {
		it := &stripItem{value: usageText("")}
		head := canvas.NewText(title, colMuted)
		head.TextSize = fontMeta
		head.TextStyle.Bold = true
		parts := []fyne.CanvasObject{container.NewCenter(head)}
		if withBar {
			it.bar = widget.NewProgressBar()
			it.bar.TextFormatter = func() string { return "" }
			parts = append(parts, container.NewCenter(container.NewGridWrap(fyne.NewSize(56, 8), it.bar)))
		}
		parts = append(parts, container.NewCenter(it.value))
		return container.NewHBox(parts...), it
	}
	five, fiveIt := item("5-hour session", true)
	sess, sessIt := item("This session", false)
	all, allIt := item("All sessions", false)
	week, weekIt := item("Weekly", true)
	u.strip = []*stripItem{fiveIt, sessIt, allIt, weekIt}
	sep := func() fyne.CanvasObject { return container.NewCenter(mutedText("│", fontMeta)) }
	gap := func() fyne.CanvasObject { return padLeft(10) }
	row := container.NewHBox(five, gap(), sep(), gap(), sess, gap(), sep(), gap(), all, gap(), sep(), gap(), week)
	u.stripRow = container.New(layout.NewCustomPaddedLayout(2, 4, 0, 0), container.New(&clipLayout{}, container.NewCenter(row)))
	return u.stripRow
}

func (u *usagePanel) syncStrip(sess, all string) {
	if len(u.strip) != 4 {
		return
	}
	win := func(it *stripItem, name string) {
		w, ok := u.d.rate.Window(name)
		if !ok {
			setText(it.value, "no data yet")
			return
		}
		if it.bar.Value != w.Utilization {
			it.bar.SetValue(w.Utilization)
		}
		txt := fmt.Sprintf("%d%%", int(w.Utilization*100+0.5))
		if r := resetText(w.ResetsAt); r != "" {
			txt += " · " + r
		}
		setText(it.value, txt)
	}
	win(u.strip[0], "five_hour")
	setText(u.strip[1].value, sess)
	setText(u.strip[2].value, all)
	win(u.strip[3], "seven_day")
	if u.stripRow != nil && u.stripRow.Visible() {
		u.stripRow.Refresh()
	}
}

type usageLayout struct{ d *desk }

func (l usageLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize()
}

func (l usageLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	chat, panel, sizes := objs[0], objs[1], objs[2]
	chat.Resize(size)
	chat.Move(fyne.NewPos(0, 0))
	if l.d.split() || l.d.layoutMode() == layoutHorizontal {
		panel.Hide()
		if l.d.split() {
			sizes.Hide()
			return
		}
	}
	if !sizes.Visible() {
		sizes.Resize(fyne.NewSize(0, 0))
	}
	w := min(size.Width, l.d.chatWidth())
	x := (size.Width-w)/2 - l.d.sidebarWidth()/2
	x = max(0, min(x, size.Width-w))
	margin := size.Width - (x + w)
	sm := sizes.MinSize()
	sw := max(sm.Width, min(usageMaxWidth, margin-usageGap-18))
	sx := size.Width - sw - usageEdge
	sy := size.Height - sm.Height - 12
	sizes.Resize(fyne.NewSize(sw, sm.Height))
	sizes.Move(fyne.NewPos(sx, sy))
	room := sy - 12
	if l.d.layoutMode() == layoutHorizontal || margin < usageMinMargin || panel.MinSize().Height > room-8 {
		panel.Hide()
		return
	}
	panel.Show()
	pw := min(usageMaxWidth, margin-usageGap-18)
	ph := panel.MinSize().Height
	y := max(8, (room-ph)/2)
	panel.Resize(fyne.NewSize(pw, ph))
	left := x + w + usageGap/2
	px := left + (size.Width-usageEdge-left-pw)/2
	panel.Move(fyne.NewPos(max(px, x+w+usageGap/2), y))
}

func (d *desk) sizePanel() fyne.CanvasObject {
	row := func(name, pref string, lo float32, get func() float32) fyne.CanvasObject {
		label := usageText(name)
		val := usageText("")
		show := func(v float32) { setText(val, fmt.Sprintf("%d%%", int(v*100+0.5))) }
		sl := widget.NewSlider(float64(lo), 1)
		sl.Step = 0.01
		sl.Value = float64(get())
		show(float32(sl.Value))
		sl.OnChanged = func(v float64) {
			show(float32(v))
			d.setShare(pref, float32(v))
		}
		sl.OnChangeEnded = func(v float64) { d.app.Preferences().SetFloat(pref, v) }
		left := container.NewGridWrap(fyne.NewSize(84, 24), container.NewCenter(label))
		right := container.NewGridWrap(fyne.NewSize(36, 24), container.NewCenter(val))
		return container.NewBorder(nil, nil, left, right, container.NewThemeOverride(sl, compactTheme{}))
	}
	body := container.NewVBox(
		row("Chat width", "chatShare", minChatShare, d.chatShare),
		row("Message box", "inputShare", minInputShare, d.inputShare),
	)
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 12
	return container.NewStack(r, container.New(layout.NewCustomPaddedLayout(2, 2, 6, 6), body))
}

type cornerLayout struct{}

func (cornerLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize()
}

func (cornerLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
}
