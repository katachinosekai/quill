package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What the kmem window says about maintenance and the recycle bin (user
// 2026-11-23): the age of the newest maintenance report, and how many bin
// items `kmem trash empty` would really purge (it only purges items binned
// 7+ days unless the user overrides at its prompt).

// kmemBinWaitDays is kmem's purge wait for binned items.
const kmemBinWaitDays = 7

// kmemNow (kmemupdate.go) is the clock; tests replace it.

// kmemMaintDir is kmem's maintenance report folder (tests set it).
var kmemMaintDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "kmem", "maint")
}

var (
	// "maint: 14 h ago, 3 warnings (path)": the age kmem itself printed.
	briefMaintAge = regexp.MustCompile(`(?m)^maint: (.*?),\s*\d+ warnings?`)
	maintFileTime = regexp.MustCompile(`maint-(\d{8})-(\d{6})\.txt$`)
	binDaysRe     = regexp.MustCompile(`, (\d+(?:\.\d+)?) days? in bin`)
)

// maintTimeFromPath reads the report time from its file name
// (maint-YYYYMMDD-HHMMSS.txt, local time).
func maintTimeFromPath(path string) time.Time {
	m := maintFileTime.FindStringSubmatch(path)
	if m == nil {
		return time.Time{}
	}
	t, err := time.ParseInLocation("20060102150405", m[1]+m[2], time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

// newestMaintReport is the newest maint-*.txt in dir with its warning count
// (the lines under "WARNINGS:", "none" not counted); "" when there is none.
func newestMaintReport(dir string) (string, int) {
	if dir == "" {
		return "", 0
	}
	names, _ := filepath.Glob(filepath.Join(dir, "maint-*.txt"))
	var files []string
	for _, n := range names {
		if !maintTimeFromPath(n).IsZero() {
			files = append(files, n)
		}
	}
	if len(files) == 0 {
		return "", 0
	}
	sort.Strings(files)
	newest := files[len(files)-1]
	b, err := os.ReadFile(newest)
	if err != nil {
		return newest, 0
	}
	warn, count := false, 0
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "WARNINGS:":
			warn = true
		case warn && strings.HasSuffix(l, ":") && strings.ToUpper(l) == l:
			warn = false
		case warn && l != "" && l != "none":
			count++
		}
	}
	return newest, count
}

// kmemMaintInfo fills the maintenance age of n from the brief, falling back
// to the newest report in the maint folder when the brief has no maint line.
func kmemMaintInfo(n *kmemNeeds, brief string) {
	if m := briefMaintAge.FindStringSubmatch(brief); m != nil {
		n.MaintAgeText = strings.TrimSpace(m[1])
	}
	if n.MaintSeen {
		return
	}
	path, count := newestMaintReport(kmemMaintDir())
	if path == "" {
		return
	}
	n.MaintSeen, n.Maint = true, count
	n.MaintAt = maintTimeFromPath(path)
	if count > 0 {
		n.MaintReport = path
	}
}

// formatMaintAge: "today 11:46" within the same day, "14 h ago" within 24 h,
// "yesterday 11:46", else "Nov 21 11:46"; "" when the time is unknown.
func formatMaintAge(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	hm := at.Format("15:04")
	y1, m1, d1 := now.Date()
	y2, m2, d2 := at.Date()
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today " + hm
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case now.AddDate(0, 0, -1).Format("2006-01-02") == at.Format("2006-01-02"):
		return "yesterday " + hm
	}
	return at.Format("Jan 2") + " " + hm
}

// maintLine: "Daily maintenance (today 11:46): 3 warnings".
func (n kmemNeeds) maintLine(now time.Time) string {
	age := formatMaintAge(n.MaintAt, now)
	if age == "" {
		age = n.MaintAgeText
	}
	w := "warnings"
	if n.Maint == 1 {
		w = "warning"
	}
	if age == "" {
		return fmt.Sprintf("Daily maintenance: %d %s", n.Maint, w)
	}
	return fmt.Sprintf("Daily maintenance (%s): %d %s", age, n.Maint, w)
}

// binWait is the 7-day state of the recycle bin.
type binWait struct {
	Total     int       // items in the bin
	Purgeable int       // binned 7+ days
	First     time.Time // when the next item becomes purgeable; zero when none waits
}

// parseBinWait reads the days in the bin off `kmem trash list` head lines.
func parseBinWait(out string, now time.Time) binWait {
	var w binWait
	var waiting float64 = -1 // the longest wait among items that are not yet purgeable
	for _, it := range parseBin(out) {
		w.Total++
		days := -1.0
		if m := binDaysRe.FindStringSubmatch(it.Head); m != nil {
			days, _ = strconv.ParseFloat(m[1], 64)
		}
		if days >= kmemBinWaitDays {
			w.Purgeable++
		} else if days >= 0 && days > waiting {
			waiting = days
		}
	}
	if waiting >= 0 {
		w.First = now.Add(time.Duration((kmemBinWaitDays - waiting) * float64(24*time.Hour)))
	}
	return w
}

func binDate(t, now time.Time) string {
	switch t.Format("2006-01-02") {
	case now.Format("2006-01-02"):
		return "today"
	case now.AddDate(0, 0, 1).Format("2006-01-02"):
		return "tomorrow"
	}
	return t.Format("Jan 2")
}

// binLine: "6 items in the recycle bin, none can be emptied yet (7-day wait);
// the first on Nov 29". Empty when the bin is empty. Items with an unknown age
// are counted as waiting without a date.
func (w binWait) binLine(now time.Time) string {
	if w.Total == 0 {
		return ""
	}
	items := fmt.Sprintf("%d items", w.Total)
	if w.Total == 1 {
		items = "1 item"
	}
	switch {
	case w.Purgeable == w.Total:
		if w.Total == 1 {
			return "1 item in the recycle bin, it can be emptied now (7+ days in the bin)"
		}
		return fmt.Sprintf("%s in the recycle bin, all can be emptied now (7+ days in the bin)", items)
	case w.Purgeable == 0:
		s := fmt.Sprintf("%s in the recycle bin, none can be emptied yet (%d-day wait)", items, kmemBinWaitDays)
		if !w.First.IsZero() {
			s += "; the first on " + binDate(w.First, now)
		}
		return s
	}
	s := fmt.Sprintf("%s in the recycle bin, %d can be emptied now (7+ days in the bin)", items, w.Purgeable)
	if !w.First.IsZero() {
		s += "; the next on " + binDate(w.First, now)
	}
	return s
}

// kmemBinInfo reads `kmem trash list` into n when the brief reports bin items
// (background goroutine only; read-only).
func (d *desk) kmemBinInfo(n *kmemNeeds) {
	if n.Bin <= 0 {
		return
	}
	out, err := d.runKmem("kmem", "trash", "list")
	if err != nil && strings.TrimSpace(out) == "" {
		return
	}
	now := kmemNow()
	w := parseBinWait(out, now)
	n.BinKnown, n.BinPurgeable, n.BinFirst = true, w.Purgeable, w.First
	n.BinTotal = w.Total
}

// binWait is the bin state kept in n.
func (n kmemNeeds) binWait() binWait {
	return binWait{Total: n.BinTotal, Purgeable: n.BinPurgeable, First: n.BinFirst}
}

// binText is the window's bin line: the 7-day state when it was read, else
// the plain count.
func (n kmemNeeds) binText(now time.Time) string {
	if n.BinKnown && n.BinTotal > 0 {
		return n.binWait().binLine(now)
	}
	return fmt.Sprintf("%d item(s) in the recycle bin", n.Bin)
}
