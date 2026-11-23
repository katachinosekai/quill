package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tm(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestFormatMaintAge(t *testing.T) {
	now := tm("2026-11-23 15:00")
	for _, c := range []struct{ at, want string }{
		{"2026-11-23 14:40", "20 min ago"},
		{"2026-11-23 11:46", "today 11:46"},
		{"2026-11-22 23:30", "15 h ago"},
		{"2026-11-22 11:46", "yesterday 11:46"},
		{"2026-11-19 04:00", "Nov 19 04:00"},
	} {
		if got := formatMaintAge(tm(c.at), now); got != c.want {
			t.Errorf("%s: %q want %q", c.at, got, c.want)
		}
	}
	if formatMaintAge(time.Time{}, now) != "" {
		t.Error("unknown time")
	}
	// the early-morning case: 14 h ago, but on the previous day
	if got := formatMaintAge(tm("2026-11-22 11:46"), tm("2026-11-23 02:00")); got != "14 h ago" {
		t.Errorf("14 h: %q", got)
	}
}

func TestMaintLineFromBrief(t *testing.T) {
	brief := "maint: 14 h ago, 3 warnings (/home/user/.local/share/kmem/maint/maint-20261122-114600.txt)\n"
	n := parseKmemNeeds(brief, nil)
	kmemMaintInfo(&n, brief)
	if n.Maint != 3 || n.MaintAgeText != "14 h ago" || n.MaintAt.IsZero() {
		t.Fatalf("needs: %+v", n)
	}
	if got := n.maintLine(tm("2026-11-22 14:00")); got != "Daily maintenance (today 11:46): 3 warnings" {
		t.Errorf("today: %q", got)
	}
	if got := n.maintLine(tm("2026-11-23 02:00")); got != "Daily maintenance (14 h ago): 3 warnings" {
		t.Errorf("h ago: %q", got)
	}
	// no file name: kmem's own wording
	b2 := "maint: 14 h ago, 1 warning (/x/report.txt)\n"
	n2 := parseKmemNeeds(b2, nil)
	kmemMaintInfo(&n2, b2)
	if got := n2.maintLine(kmemNow()); got != "Daily maintenance (14 h ago): 1 warning" {
		t.Errorf("fallback text: %q", got)
	}
}

func TestMaintZeroWarningsHidden(t *testing.T) {
	brief := "maint: 2 h ago, 0 warnings (/home/user/maint/maint-20261122-114600.txt)\n"
	n := parseKmemNeeds(brief, nil)
	kmemMaintInfo(&n, brief)
	if n.Maint != 0 || n.Count() != 0 {
		t.Fatalf("0 warnings must not ask for anything: %+v", n)
	}
}

func TestMaintFallbackToNewestFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("maint-20261121-040008.txt", "kmem maint\nWARNINGS:\nfact f1 names a missing path /x\n")
	write("maint-20261122-114600.txt", "kmem maint\nACTIONS:\nnone\nWARNINGS:\nfact f2 names a missing path /home/user/a\nfact f3 names a missing path /home/user/b\n")
	write("notes.txt", "x")
	old := kmemMaintDir
	kmemMaintDir = func() string { return dir }
	defer func() { kmemMaintDir = old }()
	n := parseKmemNeeds("rules: read\n", nil) // the brief has no maint line
	kmemMaintInfo(&n, "rules: read\n")
	if n.Maint != 2 || n.MaintAt.IsZero() || !strings.HasSuffix(n.MaintReport, "maint-20261122-114600.txt") {
		t.Fatalf("newest file: %+v", n)
	}
	if got := n.maintLine(tm("2026-11-22 14:00")); got != "Daily maintenance (today 11:46): 2 warnings" {
		t.Errorf("line: %q", got)
	}
	write("maint-20261123-040008.txt", "kmem maint\nWARNINGS:\nnone\n")
	n = kmemNeeds{}
	kmemMaintInfo(&n, "")
	if n.Maint != 0 || n.Count() != 0 {
		t.Fatalf("newest file has no warnings: %+v", n)
	}
}

const fakeBin = "[f467b16] fact, %s days in bin, by someone: reason one\n    (decision) preview text\n" +
	"[t9a8265] task, %s days in bin, by someone: reason two\n    (-) preview\n"

func binOut(a, b string) string {
	return strings.NewReplacer("%s", "X").Replace("") + strings.Replace(strings.Replace(fakeBin, "%s", a, 1), "%s", b, 1)
}

func TestBinWaitLine(t *testing.T) {
	now := tm("2026-11-23 12:00")
	cases := []struct{ out, want string }{
		{binOut("1.9", "0.4"), "2 items in the recycle bin, none can be emptied yet (7-day wait); the first on Nov 28"},
		{binOut("6.5", "0.4"), "2 items in the recycle bin, none can be emptied yet (7-day wait); the first on tomorrow"},
		{binOut("7.2", "3.0"), "2 items in the recycle bin, 1 can be emptied now (7+ days in the bin); the next on Nov 27"},
		{binOut("8.0", "9.5"), "2 items in the recycle bin, all can be emptied now (7+ days in the bin)"},
		{binOut("1.0", "1.0")[:strings.Index(binOut("1.0", "1.0"), "[t9a8265]")], "1 item in the recycle bin, none can be emptied yet (7-day wait); the first on Nov 29"},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseBinWait(c.out, now).binLine(now); got != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.out, got, c.want)
		}
	}
}

// fakeInfoKmem: read-only stub with placeholder data; records every call.
func fakeInfoKmem(brief, trash string, calls *[]string) func(string, ...string) (string, error) {
	return func(prog string, args ...string) (string, error) {
		a := strings.Join(args, " ")
		*calls = append(*calls, a)
		switch a {
		case "version --json":
			return `{"version": "test", "api": 1}`, nil
		case "brief --json":
			b, _ := json.Marshal(map[string]any{"brief": brief, "api": 1})
			return string(b), nil
		case "task list --json":
			return `{"tasks": [], "api": 1}`, nil
		case "trash list":
			return trash, nil
		}
		return "", nil
	}
}

func TestReadKmemNeedsBinAndMaint(t *testing.T) {
	h := newHarness(t)
	oldNow := kmemNow
	kmemNow = func() time.Time { return tm("2026-11-23 12:00") }
	defer func() { kmemNow = oldNow }()
	var calls []string
	brief := "maint: 14 h ago, 3 warnings (/home/user/.local/share/kmem/maint/maint-20261122-114600.txt)\nbin: 2 item(s) waiting for the user\n"
	h.d.kmemRun = fakeInfoKmem(brief, binOut("1.9", "0.4"), &calls)
	n, ok := h.d.readKmemNeeds()
	if !ok || n.Maint != 3 || n.Bin != 2 || !n.BinKnown || n.BinPurgeable != 0 {
		t.Fatalf("needs: ok=%v %+v", ok, n)
	}
	if got := n.binText(kmemNow()); !strings.Contains(got, "none can be emptied yet (7-day wait); the first on Nov 28") {
		t.Errorf("bin text: %q", got)
	}
	// phone body: the new fields
	body := kmemNeedsBody(n, kmemScrubber())
	if body["maintAge"] != "yesterday 11:46" || body["maintWarnings"] != 3 || body["binPurgeable"] != 0 {
		t.Errorf("phone fields: %v", body)
	}
	if first, _ := body["binFirstPurgeable"].(int64); first != n.BinFirst.UnixMilli() || first == 0 {
		t.Errorf("binFirstPurgeable: %v", body["binFirstPurgeable"])
	}
	// no bin: trash list is never asked
	calls = nil
	h.d.kmemRun = fakeInfoKmem("maint: 1 h ago, 0 warnings (/x/maint-20261123-110000.txt)\n", "", &calls)
	n, _ = h.d.readKmemNeeds()
	for _, c := range calls {
		if strings.HasPrefix(c, "trash") && c != "trash list" {
			t.Errorf("only read-only kmem calls: %q", c)
		}
		if c == "trash list" {
			t.Errorf("empty bin must not be listed")
		}
	}
	if n.Count() != 0 {
		t.Errorf("nothing asked: %+v", n)
	}
	body = kmemNeedsBody(n, kmemScrubber())
	if body["binPurgeable"] != -1 || body["binFirstPurgeable"] != int64(0) {
		t.Errorf("unknown bin: %v", body)
	}
}
