package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/protocol"
)

const usagePollEvery = time.Minute

func parseUsageWindows(raw json.RawMessage) (map[string]protocol.RateWindow, bool) {
	var r struct {
		RateLimits map[string]*struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(raw, &r) != nil || len(r.RateLimits) == 0 {
		return nil, false
	}
	out := map[string]protocol.RateWindow{}
	for _, name := range []string{"five_hour", "seven_day"} {
		w := r.RateLimits[name]
		if w == nil {
			continue
		}
		rw := protocol.RateWindow{Utilization: w.Utilization / 100}
		if t, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil {
			rw.ResetsAt = t.Unix()
		}
		out[name] = rw
	}
	return out, len(out) > 0
}

func (d *desk) applyUsageWindows(w map[string]protocol.RateWindow) {
	r := protocol.RateLimit{}
	if d.rate != nil {
		r = *d.rate
	}
	merged := map[string]protocol.RateWindow{}
	for k, v := range r.UnifiedWindows {
		merged[k] = v
	}
	for k, v := range w {
		merged[k] = v
	}
	r.UnifiedWindows = merged
	if fw, ok := w["five_hour"]; ok {
		r.Type, r.Utilization, r.ResetsAt = "five_hour", fw.Utilization, fw.ResetsAt
	}
	d.setRateLimit(&r)
}

func (d *desk) usageSource() *deskSession {
	var busy *deskSession
	for _, s := range d.runningSessions() {
		if !s.conv.Busy {
			return s
		}
		busy = s
	}
	return busy
}

func (d *desk) pollUsage(done func(error)) {
	s := d.usageSource()
	if s == nil || s.proc == nil {
		if done != nil {
			done(fmt.Errorf("no running session to ask"))
		}
		return
	}
	p := s.proc
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		raw, err := p.Control(ctx, map[string]any{"subtype": "get_usage", "skip_behaviors": true})
		cancel()
		d.do(func() {
			if err == nil {
				if w, ok := parseUsageWindows(raw); ok {
					d.applyUsageWindows(w)
				} else {
					err = fmt.Errorf("no rate limits in the reply")
				}
			}
			if done != nil {
				done(err)
			}
		})
	}()
}

func (d *desk) usageLoop(ctx context.Context) {
	t := time.NewTicker(usagePollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.do(func() {
				d.pollUsage(nil)
				d.checkResume()
			})
		}
	}
}

// local commands answered by Quill at once, even mid-turn

func pct(f float64) int { return int(f*100 + 0.5) }

func windowText(name string, w protocol.RateWindow) string {
	return fmt.Sprintf("%s %d%% used, %s", name, pct(w.Utilization), resetText(w.ResetsAt))
}

func (d *desk) usageSummary() string {
	var parts []string
	if w, ok := d.rate.Window("five_hour"); ok {
		parts = append(parts, windowText("5-hour window", w))
	}
	if w, ok := d.rate.Window("seven_day"); ok {
		parts = append(parts, windowText("week", w))
	}
	if len(parts) == 0 {
		return "No usage numbers yet."
	}
	return strings.Join(parts, " · ")
}

func (d *desk) cmdUsage(s *deskSession) {
	d.pollUsage(func(err error) {
		msg := "Usage: " + d.usageSummary()
		if err != nil {
			msg += " (last known; " + err.Error() + ")"
		}
		d.notify(s, msg)
	})
}

func (d *desk) cmdCost(s *deskSession) {
	st := s.conv.Stats
	d.notify(s, fmt.Sprintf("This session: $%.4f, %d turns, model %s.", st.CostUSD, st.Turns, firstNonEmpty(st.Model, s.model)))
}

func (d *desk) cmdContext(s *deskSession) {
	st := s.conv.Stats
	if st.ContextUsed == 0 {
		d.notify(s, "No context measured yet; it is reported after Claude's next turn.")
		return
	}
	msg := fmt.Sprintf("Context: %s tokens", kfmt(st.ContextUsed))
	if st.ContextMax > 0 {
		msg += fmt.Sprintf(" of %s (%d%%)", kfmt(st.ContextMax), st.ContextUsed*100/st.ContextMax)
	}
	d.notify(s, msg+".")
}

func (d *desk) cmdStatus(s *deskSession) {
	title, _, st := d.rowText(s.id)
	lines := []string{
		"Session: " + title + " (" + s.id + ")",
		"Folder: " + tildePath(s.cwd),
		"Model: " + s.model + " · mode: " + s.mode + " · status: " + firstNonEmpty(st.String(), "not running"),
	}
	if k := d.org.Kmem[s.id]; k != "" {
		lines = append(lines, "kmem: "+k)
	}
	if s.rcOn {
		lines = append(lines, "Remote Control: on")
	}
	if s.chrome != "" {
		lines = append(lines, "Claude in Chrome: "+s.chrome)
	}
	if n := s.conv.Background(); n > 0 {
		lines = append(lines, fmt.Sprintf("Background tasks: %d", n))
	}
	lines = append(lines, "Usage: "+d.usageSummary())
	d.notify(s, strings.Join(lines, "\n"))
}

// pause near the usage limit

const pauseMessage = "Quill: the plan's 5-hour usage is at %d%%. Finish only the step you are on, save your state (kmem notes or handoff if you use kmem), don't start anything new, and stop. Quill will tell you when the usage window resets (around %s)."

const resumeMessage = "Quill: the usage window has reset. Continue where you stopped."

func (d *desk) pauseThreshold() int {
	return d.app.Preferences().IntWithFallback("usagePauseAt", 95)
}

func (d *desk) pauseEnabled() bool {
	return d.app.Preferences().BoolWithFallback("usagePause", true)
}

func (d *desk) checkUsagePause() {
	w, ok := d.rate.Window("five_hour")
	if !ok || !d.pauseEnabled() || d.usagePaused {
		return
	}
	if pct(w.Utilization) < d.pauseThreshold() {
		return
	}
	if w.ResetsAt > 0 && time.Unix(w.ResetsAt, 0).Before(time.Now()) {
		return
	}
	d.pauseForUsage(pct(w.Utilization), w.ResetsAt)
}

func (d *desk) pauseForUsage(used int, resets int64) {
	d.usagePaused = true
	d.pauseUntil = time.Unix(resets, 0)
	at := "the reset"
	if resets > 0 {
		at = d.pauseUntil.Local().Format("15:04")
	}
	d.pausedIDs = nil
	for _, s := range d.runningSessions() {
		if s.conv.Busy || s.conv.Background() > 0 {
			d.pausedIDs = append(d.pausedIDs, s.id)
			d.sendTo(s, fmt.Sprintf(pauseMessage, used, at))
		}
	}
	d.showPauseBanner(fmt.Sprintf("Paused near the usage limit (%d%% of the 5-hour window). Queued messages and automatic actions wait until %s.", used, at))
	d.alert(d.current, "finished", fmt.Sprintf("Quill paused sessions: 5-hour usage at %d%%", used))
}

func (d *desk) checkResume() {
	if !d.usagePaused {
		return
	}
	if !d.pauseUntil.IsZero() && d.pauseUntil.Unix() > 0 && time.Now().After(d.pauseUntil.Add(30*time.Second)) {
		d.resumeFromPause()
		return
	}
	if w, ok := d.rate.Window("five_hour"); ok && pct(w.Utilization) < d.pauseThreshold()-10 {
		d.resumeFromPause()
	}
}

func (d *desk) resumeFromPause() {
	if !d.usagePaused {
		return
	}
	d.usagePaused = false
	d.pauseUntil = time.Time{}
	ids := d.pausedIDs
	d.pausedIDs = nil
	d.hidePauseBanner()
	for _, id := range ids {
		if s := d.session(id); s != nil {
			d.sendTo(s, resumeMessage)
		}
	}
	for _, s := range d.runningSessions() {
		if d.idleForQueue(s) {
			d.flushQueue(s)
		}
	}
}

func (d *desk) buildPauseBanner() fyne.CanvasObject {
	d.pauseText = widget.NewLabel("")
	d.pauseText.Wrapping = fyne.TextWrapWord
	d.pauseText.TextStyle = fyne.TextStyle{Bold: true}
	resume := widget.NewButtonWithIcon("Resume now", theme.MediaPlayIcon(), d.resumeFromPause)
	resume.Importance = widget.HighImportance
	bgr := canvas.NewRectangle(colCard)
	bgr.CornerRadius = 8
	bgr.StrokeColor = colWaiting
	bgr.StrokeWidth = 1.5
	d.pauseBanner = container.NewStack(bgr, container.NewPadded(container.NewBorder(nil, nil, nil, container.NewCenter(resume), d.pauseText)))
	d.pauseBanner.Hide()
	return d.pauseBanner
}

func (d *desk) showPauseBanner(text string) {
	if d.pauseBanner == nil {
		return
	}
	d.pauseText.SetText(text)
	d.pauseBanner.Show()
	if d.headerBox != nil {
		d.headerBox.Refresh()
	}
}

func (d *desk) hidePauseBanner() {
	if d.pauseBanner == nil {
		return
	}
	d.pauseBanner.Hide()
	if d.headerBox != nil {
		d.headerBox.Refresh()
	}
}

func (d *desk) pauseSettings() fyne.CanvasObject {
	chk := widget.NewCheck("Pause sessions near the usage limit", func(on bool) { d.app.Preferences().SetBool("usagePause", on) })
	chk.Checked = d.pauseEnabled()
	levels := []string{"80%", "85%", "90%", "95%", "98%"}
	sel := widget.NewSelect(levels, func(v string) {
		var n int
		if _, err := fmt.Sscanf(v, "%d%%", &n); err == nil {
			d.app.Preferences().SetInt("usagePauseAt", n)
		}
	})
	sel.SetSelected(fmt.Sprintf("%d%%", d.pauseThreshold()))
	note := widget.NewLabel("At this share of the 5-hour window, Quill holds queued messages and automatic actions, asks working sessions to save their state and stop, and resumes them after the reset.")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	return container.NewVBox(container.NewHBox(chk, boxed(sel)), note)
}

const pausedNote = "(Quill: the usage pause is on. Answer this, but don't resume paused work or start new tasks until Quill says the window has reset.)\n\n"
