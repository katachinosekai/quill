package main

import (
	"encoding/json"
	"time"

	"github.com/katachinosekai/quill/internal/folders"
	"github.com/katachinosekai/quill/internal/transcript"
)

var phoneStatus = map[status]string{
	stNone: "none", stRunning: "running", stWaiting: "waiting", stIdle: "idle", stError: "error", stTerminal: "terminal", stRestart: "running",
}

func (d *desk) phoneUsage() folders.Usage {
	u := folders.Usage{Version: 1, Meta: map[string]folders.SessionMeta{}}
	u.Usage.At = time.Now().UnixMilli()
	if w, ok := d.rate.Window("five_hour"); ok {
		u.Usage.FiveHour = &folders.UsageWindow{Utilization: w.Utilization, ResetsAt: w.ResetsAt}
	}
	if w, ok := d.rate.Window("seven_day"); ok {
		u.Usage.Weekly = &folders.UsageWindow{Utilization: w.Utilization, ResetsAt: w.ResetsAt}
	}
	u.Usage.Credits = creditsText(d.rate)
	if u.Usage.Credits == "not reported yet" {
		u.Usage.Credits = "not reported"
	}
	u.Usage.Today, u.Usage.Week = d.dayWeekTokens()
	if p := d.phoneSync; p != nil {
		for rc, id := range p.rc {
			info, has := d.infos[id]
			s := d.sessions[id]
			if !has && s == nil {
				continue
			}
			_, _, st := d.rowText(id)
			m := folders.SessionMeta{Status: phoneStatus[st], Msgs: info.Messages, Tokens: int64(info.Usage.Total()), Kmem: d.org.Kmem[id]}
			if !info.Last.IsZero() {
				m.Last = info.Last.UnixMilli()
			}
			if s != nil {
				m.Context, m.ContextMax = s.conv.Stats.ContextUsed, s.conv.Stats.ContextMax
			}
			u.Meta[rc] = m
		}
	}
	return u
}

func (d *desk) phoneUsageJSON() []byte {
	var b []byte
	d.doWait(func() {
		b, _ = json.Marshal(d.phoneUsage())
	})
	return b
}

func (d *desk) dayWeekTokens() (int64, int64) {
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
	return int64(today.Total()), int64(week.Total())
}
