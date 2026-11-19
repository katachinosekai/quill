package main

import (
	"encoding/json"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

const (
	usageMinMargin = 230
	usageMaxWidth  = 280
	usageGap       = 36
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
}

func newUsagePanel(d *desk) *usagePanel {
	u := &usagePanel{d: d}
	u.five = widget.NewProgressBar()
	u.week = widget.NewProgressBar()
	u.fiveTxt = mutedText("", 11)
	u.weekTxt = mutedText("", 11)
	u.asOf = mutedText("", 10)
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
		t.TextSize = 13
		return t
	}
	body := container.NewVBox(
		head("Plan usage"),
		mutedText("5-hour session", 11), u.five, u.fiveTxt,
		mutedText("Weekly", 11), u.week, u.weekTxt, u.asOf,
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
			bar.SetValue(0)
			txt.Text = "no data yet (updates after a reply)"
			txt.Refresh()
			return
		}
		bar.SetValue(w.Utilization)
		txt.Text = fmt.Sprintf("%d%% used · %s", int(w.Utilization*100+0.5), resetText(w.ResetsAt))
		txt.Refresh()
	}
	setWin(u.five, u.fiveTxt, "five_hour")
	setWin(u.week, u.weekTxt, "seven_day")
	u.asOf.Text = ""
	if !d.rateAt.IsZero() {
		u.asOf.Text = "as of " + ago(d.rateAt)
	}
	u.asOf.Refresh()

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
	u.sess.SetText(sess)

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
	u.today.SetText("Today " + tok(today.Total()) + "  ·  7 days " + tok(week.Total()))
}

type usageLayout struct{ d *desk }

func (l usageLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return objs[0].MinSize()
}

func (l usageLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	chat, panel := objs[0], objs[1]
	chat.Resize(size)
	chat.Move(fyne.NewPos(0, 0))
	w := min(size.Width, l.d.chatWidth())
	x := (size.Width-w)/2 - l.d.sidebarWidth()/2
	x = max(0, min(x, size.Width-w))
	margin := size.Width - (x + w)
	if margin < usageMinMargin || panel.MinSize().Height > size.Height-16 {
		panel.Hide()
		return
	}
	panel.Show()
	pw := min(usageMaxWidth, margin-usageGap-18)
	ph := panel.MinSize().Height
	y := max(8, (size.Height-ph)/2)
	panel.Resize(fyne.NewSize(pw, ph))
	panel.Move(fyne.NewPos(x+w+usageGap, y))
}
