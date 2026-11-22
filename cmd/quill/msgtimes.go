package main

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Message times (user 2026-11-22): a small, muted time under each message,
// from the transcript (nothing is added to what Claude reads). Settings: a
// switch (on by default) and a format field: empty = the system's clock
// format, or the user's own pattern (HH:mm, h:mm a, ddd d MMM, …).

const (
	prefShowTimes  = "showMsgTimes"
	prefTimeFormat = "msgTimeFormat"
)

func (d *desk) showTimes() bool { return d.app.Preferences().BoolWithFallback(prefShowTimes, true) }

// patternTokens map the user's pattern to Go's time layout, longest first.
var patternTokens = []struct{ tok, layout string }{
	{"yyyy", "2006"}, {"yy", "06"},
	{"MMMM", "January"}, {"MMM", "Jan"}, {"MM", "01"}, {"M", "1"},
	{"dddd", "Monday"}, {"ddd", "Mon"}, {"dd", "02"}, {"d", "2"},
	{"HH", "15"}, {"H", "15"}, {"hh", "03"}, {"h", "3"},
	{"mm", "04"}, {"m", "4"}, {"ss", "05"}, {"s", "5"},
	{"tt", "PM"}, {"a", "PM"},
}

// patternLayout turns a user pattern into a Go layout; ok is false when the
// pattern has no time or date token at all.
func patternLayout(p string) (string, bool) {
	var b strings.Builder
	found := false
	for i := 0; i < len(p); {
		matched := false
		for _, t := range patternTokens {
			if strings.HasPrefix(p[i:], t.tok) {
				b.WriteString(t.layout)
				i += len(t.tok)
				matched, found = true, true
				break
			}
		}
		if !matched {
			b.WriteByte(p[i])
			i++
		}
	}
	return b.String(), found
}

var (
	sysClockOnce sync.Once
	sysClock12   bool
)

// system12h: does this computer's clock use 12-hour time?
func system12h() bool {
	sysClockOnce.Do(func() { sysClock12 = detect12h() })
	return sysClock12
}

func detect12h() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "windows":
		if out, err := exec.CommandContext(ctx, "reg", "query", `HKCU\Control Panel\International`, "/v", "sShortTime").Output(); err == nil {
			return strings.Contains(string(out), "h:") && !strings.Contains(string(out), "H")
		}
	default:
		if out, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "clock-format").Output(); err == nil {
			if f := strings.Trim(strings.TrimSpace(string(out)), "'"); f == "12h" || f == "24h" {
				return f == "12h"
			}
		}
		if b, err := os.ReadFile(homeDir() + "/.config/kdeglobals"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(l), "TimeFormat="); ok {
					return strings.Contains(v, "h") && !strings.Contains(v, "H")
				}
			}
		}
	}
	loc := firstNonEmpty(os.Getenv("LC_TIME"), firstNonEmpty(os.Getenv("LC_ALL"), os.Getenv("LANG")))
	for _, c := range []string{"_US", "_CA", "_AU", "_NZ", "_IN", "_PH", "_PK", "_EG", "_SA"} {
		if strings.Contains(loc, c) {
			return true
		}
	}
	return false
}

// msgTimeLayout: the layout for a message from t (today: time only).
func (d *desk) msgTimeLayout(t time.Time) string {
	if p := strings.TrimSpace(d.app.Preferences().String(prefTimeFormat)); p != "" {
		if l, ok := patternLayout(p); ok {
			return l
		}
	}
	clock := "15:04"
	if system12h() {
		clock = "3:04 PM"
	}
	now := time.Now()
	if y, m, dd := t.Date(); y == now.Year() && m == now.Month() && dd == now.Day() {
		return clock
	}
	if t.Year() == now.Year() {
		return "Mon 2 Jan, " + clock
	}
	return "2 Jan 2006, " + clock
}

func (d *desk) msgTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(d.msgTimeLayout(t))
}

// withTime puts the message's time under it when times are shown.
func (v *chatView) withTime(o fyne.CanvasObject, t time.Time) fyne.CanvasObject {
	if !v.d.showTimes() || t.IsZero() {
		return o
	}
	l := v.muted(v.d.msgTime(t), fontMeta)
	return container.NewVBox(o, l)
}

// timeSettings: Settings → General → Message times.
func (d *desk) timeSettings() fyne.CanvasObject {
	show := widget.NewCheck("Show message times", func(on bool) {
		d.app.Preferences().SetBool(prefShowTimes, on)
		d.redrawChats()
	})
	show.Checked = d.showTimes()
	format := widget.NewEntry()
	format.SetText(d.app.Preferences().String(prefTimeFormat))
	format.SetPlaceHolder("system format (or a pattern like HH:mm, h:mm a, ddd d MMM HH:mm)")
	preview := widget.NewLabel("")
	update := func() {
		p := strings.TrimSpace(format.Text)
		sample := time.Now()
		if p == "" {
			preview.SetText("Preview: " + d.msgTime(sample) + "  (system format)")
			return
		}
		if _, ok := patternLayout(p); !ok {
			preview.SetText("Not a time format: use tokens like HH, h, mm, a, ddd, d, MMM, yyyy")
			return
		}
		preview.SetText("Preview: " + d.msgTime(sample))
	}
	format.OnChanged = func(t string) {
		if _, ok := patternLayout(strings.TrimSpace(t)); ok || strings.TrimSpace(t) == "" {
			d.app.Preferences().SetString(prefTimeFormat, strings.TrimSpace(t))
			d.redrawChats()
		}
		update()
	}
	update()
	return container.NewVBox(show, container.NewBorder(nil, nil, widget.NewLabel("Format"), nil, format), preview)
}

// redrawChats re-renders the shown chats (after a display setting changed).
func (d *desk) redrawChats() {
	views := []*chatView{d.chat}
	for _, p := range d.panes {
		views = append(views, p.view)
	}
	for _, v := range views {
		if v == nil || v.s == nil {
			continue
		}
		s := v.s
		v.s = nil
		v.show(s)
	}
}
