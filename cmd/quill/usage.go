package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/codex"
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
	d                 *desk
	root              *fyne.Container
	five              *widget.ProgressBar
	fiveTxt           *canvas.Text
	week              *widget.ProgressBar
	weekTxt           *canvas.Text
	asOf              *canvas.Text
	sess              *widget.Label
	today             *widget.Label
	provider          *widget.Label
	cloudPlan, totals *fyne.Container

	strip                                         []*stripItem
	stripRow, stripClaude, stripCodex, stripOther *fyne.Container
	stripText                                     *widget.Label
	codexSession                                  *stripItem
	codexSessionRow                               fyne.CanvasObject
	stripDetails                                  *widget.Button
	codexStripWindows, codexPanelWindows          *fyne.Container
	codexWindows                                  []planUsageWindow
	detailsCodex                                  bool
	details                                       string
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
	u.sess, u.today, u.provider = label(), label(), label()
	head := func(s string) fyne.CanvasObject {
		t := canvas.NewText(s, colText)
		t.TextStyle.Bold = true
		t.TextSize = fontBody
		return t
	}
	u.cloudPlan = container.NewVBox(
		head("Claude plan usage"),
		usageText("5-hour session"), u.five, u.fiveTxt,
		usageText("Weekly"), u.week, u.weekTxt, u.asOf,
	)
	u.totals = container.NewVBox(padTop(8), head("Claude Code history"), u.today)
	u.provider.Hide()
	u.codexPanelWindows = container.NewVBox()
	u.codexPanelWindows.Hide()
	body := container.NewVBox(u.cloudPlan, u.provider, u.codexPanelWindows, padTop(8), head("This session"), u.sess, u.totals)
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
		if r.OverageStatus != "" {
			merged.OverageStatus, merged.OverageReason, merged.UsingOverage = r.OverageStatus, r.OverageReason, r.UsingOverage
		}
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
	d.clearLimitIfAllowed()
	d.checkUsagePause()
}

func creditsText(r *protocol.RateLimit) string {
	if r == nil || r.OverageStatus == "" {
		return "not reported yet"
	}
	if r.UsingOverage {
		return "in use"
	}
	switch r.OverageReason {
	case "out_of_credits":
		return "out of credits"
	case "org_level_disabled":
		return "turned off"
	}
	switch r.OverageStatus {
	case "allowed", "allowed_warning":
		return "available"
	case "rejected":
		return "not available"
	}
	return r.OverageStatus
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
	return "resets " + localDate(t, dateDowMD) + ", " + t.Format("3:04 PM")
}

func (u *usagePanel) update() {
	d := u.d
	s := d.current
	if operator := d.targetSession(d.main.role); operator != nil {
		s = operator
	}
	if s != nil && (s.codex || isLocal(s.model)) {
		u.updateAgentUsage(s)
		d.syncRowCaptions()
		return
	}
	u.detailsCodex = false
	u.cloudPlan.Show()
	u.provider.Hide()
	u.codexPanelWindows.Hide()
	u.totals.Show()
	u.showUsageStrip(false, false, "")
	setUsageWindow(u.five, u.fiveTxt, claudeUsageWindow(d.rate, "five_hour"), false)
	setUsageWindow(u.week, u.weekTxt, claudeUsageWindow(d.rate, "seven_day"), false)
	asOf := ""
	if !d.rateAt.IsZero() {
		asOf = "as of " + ago(d.rateAt)
	}
	setText(u.asOf, asOf)

	sess := "No session open"
	if s != nil {
		usage := d.infos[s.id].Usage
		sess = claudeTokenSummary(usage)
		if context := usageContext(s.conv.Stats.ContextUsed, s.conv.Stats.ContextMax); context != "" {
			sess += "  ·  " + context
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
	u.details = strings.Join([]string{firstNonEmpty(d.acct.Plan(), "Claude Code"), u.fiveTxt.Text, u.weekTxt.Text,
		"Usage credits: " + creditsText(d.rate), sess, u.today.Text}, "\n")
	short := compactUsageSummary(sess)
	u.syncStrip(short, "Today "+tok(today.Total())+" · 7d "+tok(week.Total()))
	d.syncRowCaptions()
}

type stripItem struct {
	bar   *widget.ProgressBar
	value *canvas.Text
}

func usageStripItem(title string, withBar bool) (fyne.CanvasObject, *stripItem) {
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

func (u *usagePanel) buildStrip() fyne.CanvasObject {
	five, fiveIt := usageStripItem("5-hour session", true)
	sess, sessIt := usageStripItem("This session", false)
	all, allIt := usageStripItem("All sessions", false)
	credits, creditsIt := usageStripItem("Usage credits", false)
	week, weekIt := usageStripItem("Weekly", true)
	u.strip = []*stripItem{fiveIt, sessIt, allIt, creditsIt, weekIt}
	row := container.NewHBox(usageStripParts(five, sess, all, credits, week)...)
	u.stripClaude = container.New(&clipLayout{}, container.NewCenter(row))
	u.stripText = widget.NewLabel("")
	u.stripText.Truncation = fyne.TextTruncateEllipsis
	u.stripOther = container.NewStack(u.stripText)
	u.stripOther.Hide()
	u.codexSessionRow, u.codexSession = usageStripItem("This session", false)
	u.codexStripWindows = container.NewHBox(u.codexSessionRow)
	u.stripCodex = container.New(&clipLayout{}, container.NewCenter(u.codexStripWindows))
	u.stripCodex.Hide()
	u.stripDetails = widget.NewButton("Usage details", u.showUsageDetails)
	u.stripDetails.Importance = widget.LowImportance
	u.stripRow = container.New(layout.NewCustomPaddedLayout(2, 4, 0, 0),
		container.NewBorder(nil, nil, nil, u.stripDetails, container.NewStack(u.stripClaude, u.stripCodex, u.stripOther)))
	return u.stripRow
}

func usageStripParts(items ...fyne.CanvasObject) []fyne.CanvasObject {
	var parts []fyne.CanvasObject
	for i, item := range items {
		if i > 0 {
			parts = append(parts, padLeft(10), container.NewCenter(mutedText("│", fontMeta)), padLeft(10))
		}
		parts = append(parts, item)
	}
	return parts
}

func compactUsageSummary(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	line = strings.Replace(line, "  ·  context ", " · ctx ", 1)
	return strings.Replace(line, " tokens", " tok", 1)
}

func (u *usagePanel) syncStrip(sess, all string) {
	if len(u.strip) != 5 {
		return
	}
	setUsageWindow(u.strip[0].bar, u.strip[0].value, claudeUsageWindow(u.d.rate, "five_hour"), true)
	setText(u.strip[1].value, sess)
	setText(u.strip[2].value, all)
	setText(u.strip[3].value, creditsText(u.d.rate))
	setUsageWindow(u.strip[4].bar, u.strip[4].value, claudeUsageWindow(u.d.rate, "seven_day"), true)
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

func usageContext(used, max int) string {
	if used <= 0 && max <= 0 {
		return ""
	}
	if max <= 0 {
		return "context " + kfmt(used)
	}
	if used <= 0 {
		return "context window " + kfmt(max)
	}
	return fmt.Sprintf("context %s / %s (%d%%)", kfmt(used), kfmt(max), used*100/max)
}

func claudeTokenSummary(t transcript.Tokens) string {
	if t.Total() == 0 {
		return "No token data yet"
	}
	return tok(t.Total()) + " tokens\n" + tok(t.In) + " input · " + tok(t.Out) + " output · " +
		tok(t.CacheRead) + " cached read · " + tok(t.CacheWrite) + " cached write"
}

func codexTokenSummary(usage *protocol.CodexUsage) string {
	if usage == nil || usage.Total.Total == 0 {
		return "No token data yet"
	}
	t := usage.Total
	input := max(0, t.Input-t.CachedInput-t.CacheWriteInput)
	detail := tok(t.Total) + " tokens\n" + tok(input) + " input · " + tok(t.Output) + " output · " +
		tok(t.CachedInput) + " cached read"
	if t.CacheWriteInput > 0 {
		detail += " · " + tok(t.CacheWriteInput) + " cached write"
	}
	return detail
}

func (u *usagePanel) updateAgentUsage(s *deskSession) {
	d := u.d
	provider, limits := "Local model", ""
	var windows []planUsageWindow
	info := d.infos[s.id].Usage
	short, detail := "No token data yet", claudeTokenSummary(info)
	if info.Total() > 0 {
		short = tok(info.Total()) + " tokens"
	}
	if s.codex {
		provider = "Codex"
		detail = codexTokenSummary(s.conv.Stats.CodexUsage)
		acct := d.codexAuth.snapshot.Account
		switch {
		case !d.codexAuth.ready:
			limits = "Account status unavailable"
		case acct == nil && !d.codexAuth.snapshot.RequiresOpenAIAuth:
			limits = "Codex provider does not require a ChatGPT sign-in"
		case acct == nil:
			limits = "ChatGPT signed out"
		case acct.Type != "chatgpt":
			limits = "ChatGPT plan limits unavailable for this sign-in method"
		default:
			provider = "ChatGPT " + planLabel(acct.PlanType) + " · Codex"
			if acct.PlanType == "" {
				provider = "ChatGPT · Codex"
			}
			windows, limits = codexUsageWindows(d.codexAuth.snapshot.Limits)
		}
	}
	used, max := s.conv.Stats.ContextUsed, s.conv.Stats.ContextMax
	if max <= 0 && isLocal(s.model) {
		max = d.localWindows[s.model]
	}
	context := usageContext(used, max)
	if context != "" {
		short += " · " + context
		detail += "  ·  " + context
	}
	model := firstNonEmpty(s.conv.Stats.Model, s.model)
	if !s.codex && model != "" && model != "default" {
		detail += "\n" + model
	}
	u.detailsCodex = s.codex
	u.updateCodexWindows(windows)
	u.cloudPlan.Hide()
	u.totals.Hide()
	u.provider.Show()
	heading := provider
	if limits != "" {
		heading += "\n" + limits
	}
	setLabel(u.provider, heading)
	if s.codex && u.codexSession != nil {
		text := compactUsageSummary(detail)
		if u.codexSession.value.Text != text {
			setText(u.codexSession.value, text)
			u.codexStripWindows.Refresh()
		}
	}
	setLabel(u.sess, detail)
	u.details = heading + "\n\n" + detail
	u.showUsageStrip(true, s.codex, provider+" · "+short)
}

type planUsageWindow struct {
	label       string
	utilization float64
	reset       string
	reported    bool
}

func claudeUsageWindow(rate *protocol.RateLimit, name string) planUsageWindow {
	w, ok := rate.Window(name)
	return planUsageWindow{utilization: w.Utilization, reset: resetText(w.ResetsAt), reported: ok}
}

func setUsageWindow(bar *widget.ProgressBar, label *canvas.Text, w planUsageWindow, compact bool) {
	value := "no data yet"
	if w.reported {
		value = fmt.Sprintf("%d%%", int(w.utilization*100+0.5))
		if !compact {
			value += " used"
		}
		if w.reset != "" {
			value += " · " + w.reset
		}
	} else if !compact {
		value += " (updates after a reply)"
	}
	fill := min(max(w.utilization, 0), 1)
	if bar.Value != fill {
		bar.SetValue(fill)
	}
	setText(label, value)
}

func usageWindowRows(windows []planUsageWindow, compact bool) []fyne.CanvasObject {
	var rows []fyne.CanvasObject
	for _, w := range windows {
		if compact {
			row, item := usageStripItem(w.label, true)
			setUsageWindow(item.bar, item.value, w, true)
			rows = append(rows, row)
		} else {
			bar, value := widget.NewProgressBar(), usageText("")
			setUsageWindow(bar, value, w, false)
			rows = append(rows, container.NewVBox(usageText(w.label), bar, value))
		}
	}
	return rows
}

func (u *usagePanel) updateCodexWindows(windows []planUsageWindow) {
	if !slices.Equal(u.codexWindows, windows) {
		u.codexWindows = windows
		u.codexPanelWindows.Objects = usageWindowRows(windows, false)
		u.codexPanelWindows.Refresh()
		if u.codexStripWindows != nil {
			rows := usageWindowRows(windows, true)
			items := []fyne.CanvasObject{u.codexSessionRow}
			if len(rows) > 0 {
				items = append([]fyne.CanvasObject{rows[0], u.codexSessionRow}, rows[1:]...)
			}
			u.codexStripWindows.Objects = usageStripParts(items...)
			u.codexStripWindows.Refresh()
		}
	}
	setShown(u.codexPanelWindows, len(windows) > 0)
}

func codexUsageWindows(limits map[string]codex.AccountLimit) ([]planUsageWindow, string) {
	keys := make([]string, 0, len(limits))
	for key := range limits {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var windows []planUsageWindow
	var details []string
	for _, key := range keys {
		limit := limits[key]
		label := firstNonEmpty(firstNonEmpty(limit.LimitName, limit.LimitID), firstNonEmpty(key, "Codex"))
		for i, window := range []*codex.LimitWindow{limit.Primary, limit.Secondary} {
			if window == nil {
				continue
			}
			var title string
			switch window.WindowDurationMins {
			case 300:
				title = "5-hour session"
			case 10080:
				title = "Weekly"
			case 0:
				title = []string{"Primary", "Secondary"}[i]
			default:
				duration := time.Duration(window.WindowDurationMins) * time.Minute
				title = strings.TrimSuffix(duration.String(), "0s")
				if window.WindowDurationMins%60 == 0 {
					title = strings.TrimSuffix(title, "0m")
				}
			}
			if len(limits) > 1 {
				title = label + " · " + title
			}
			windows = append(windows, planUsageWindow{title, window.UsedPercent / 100, resetText(window.ResetsAt), true})
		}
		if c := limit.Credits; c != nil {
			switch {
			case c.Unlimited:
				details = append(details, label+" credits · unlimited")
			case c.Balance != "":
				details = append(details, label+" credits · "+c.Balance)
			case c.HasCredits:
				details = append(details, label+" credits · available")
			default:
				details = append(details, label+" credits · none")
			}
		}
	}
	if len(windows) == 0 {
		windows = []planUsageWindow{{label: "5-hour session"}, {label: "Weekly"}}
	}
	return windows, strings.Join(details, "\n")
}

func (u *usagePanel) showUsageStrip(other, codex bool, text string) {
	if u.stripOther == nil {
		return
	}
	setLabel(u.stripText, text)
	setShown(u.stripClaude, !other)
	setShown(u.stripCodex, codex)
	setShown(u.stripOther, other && !codex)
	setShown(u.stripDetails, true)
}

func (u *usagePanel) showUsageDetails() {
	dlg := dialog.NewCustom("Usage details", "Close", container.NewVScroll(u.usageDetailsContent()), u.d.win)
	dlg.Resize(fyne.NewSize(440, 300))
	dlg.Show()
}

func (u *usagePanel) usageDetailsContent() fyne.CanvasObject {
	text := widget.NewLabel(u.details)
	text.Wrapping = fyne.TextWrapWord
	if !u.detailsCodex {
		return text
	}
	heading, detail, _ := strings.Cut(u.details, "\n\n")
	text.SetText("This session\n" + detail)
	plan := widget.NewLabel(heading)
	plan.Wrapping = fyne.TextWrapWord
	rows := []fyne.CanvasObject{plan}
	rows = append(rows, usageWindowRows(u.codexWindows, false)...)
	return container.NewVBox(append(rows, text)...)
}
