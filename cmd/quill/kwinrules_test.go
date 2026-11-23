package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

const kwinOther = `[General]
count=2
rules=other-a,other-b

[other-a]
Description=Placeholder rule A
wmclass=example-a
wmclassmatch=1
position=10,20
positionrule=2

[other-b]
Description=Placeholder rule B
wmclass=example-b
sizerule=3
`

const kwinLegacy = `[General]
count=3
rules=other-a,quill-remember-place,other-b

[other-a]
Description=Placeholder rule A
wmclass=example-a

[quill-remember-place]
Description=Quill: remember position and size
position=0,0
positionrule=4
size=1720,1440
sizerule=4
wmclass=io.github.katachinosekai.quill
wmclassmatch=1

[other-b]
Description=Placeholder rule B
wmclass=example-b
`

type kwinFake struct {
	k     *kwinRules
	calls []string
	have  map[string]bool
	clock time.Time
}

// newKwinFake: a KDE desktop with a temp config dir holding rc (if not "");
// kwriteconfig6 and qdbus6 are fakes that edit the temp file / record calls.
func newKwinFake(t *testing.T, desktop, rc string, progs ...string) *kwinFake {
	t.Helper()
	f := &kwinFake{have: map[string]bool{}, clock: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	for _, p := range progs {
		f.have[p] = true
	}
	dir := t.TempDir()
	if rc != "" {
		if err := os.WriteFile(filepath.Join(dir, "kwinrulesrc"), []byte(rc), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.k = &kwinRules{
		Desktop: desktop, ConfigDir: dir,
		Has: func(n string) bool { return f.have[n] },
		Now: func() time.Time { f.clock = f.clock.Add(time.Second); return f.clock },
	}
	f.k.Run = func(name string, args ...string) error {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		if strings.HasPrefix(name, "kwriteconfig") {
			// --file F --group G --key K (VALUE | --delete)
			file, group, key, val := args[1], args[3], args[5], args[6]
			b, _ := os.ReadFile(file)
			ini := parseINI(string(b))
			if val == "--delete" {
				ini.del(group, key)
			} else {
				ini.set(group, key, val)
			}
			return os.WriteFile(file, []byte(ini.String()), 0o600)
		}
		return nil
	}
	return f
}

func (f *kwinFake) rc(t *testing.T) *iniFile {
	b, err := os.ReadFile(filepath.Join(f.k.ConfigDir, "kwinrulesrc"))
	if err != nil {
		t.Fatal(err)
	}
	return parseINI(string(b))
}

func (f *kwinFake) count(prefix string) (n int) {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return
}

func (f *kwinFake) backups() []string {
	m, _ := filepath.Glob(filepath.Join(f.k.ConfigDir, "kwinrulesrc.quill-backup-*"))
	return m
}

func checkOthers(t *testing.T, ini *iniFile) {
	t.Helper()
	if ini.get("other-a", "position") != "10,20" || ini.get("other-a", "positionrule") != "2" ||
		ini.get("other-a", "wmclass") != "example-a" || ini.get("other-b", "sizerule") != "3" ||
		ini.get("other-b", "Description") != "Placeholder rule B" {
		t.Fatalf("an unrelated rule changed:\n%s", ini)
	}
}

func TestKwinInstallKeepsOthers(t *testing.T) {
	f := newKwinFake(t, "KDE", kwinOther, "kwriteconfig6", "qdbus6")
	if err := f.k.Apply(true); err != nil {
		t.Fatal(err)
	}
	ini := f.rc(t)
	if got := ini.get("General", "rules"); got != "other-a,other-b,quill-main,quill-kmem" {
		t.Fatalf("rules = %q", got)
	}
	if ini.get("General", "count") != "4" {
		t.Fatalf("count = %q", ini.get("General", "count"))
	}
	for _, c := range []struct{ id, desc, class string }{
		{"quill-main", "Quill Desktop: remember position and size", appID},
		{"quill-kmem", "Quill Desktop kmem window: remember position and size", appID + ".kmem"},
	} {
		if ini.get(c.id, "Description") != c.desc || ini.get(c.id, "wmclass") != c.class ||
			ini.get(c.id, "wmclassmatch") != "1" || ini.get(c.id, "positionrule") != "4" || ini.get(c.id, "sizerule") != "4" {
			t.Fatalf("%s wrong:\n%s", c.id, ini)
		}
	}
	checkOthers(t, ini)
	if f.count("qdbus6 org.kde.KWin /KWin reconfigure") != 1 {
		t.Fatalf("reconfigure calls: %v", f.calls)
	}
	if len(f.backups()) != 1 {
		t.Fatalf("backups: %v", f.backups())
	}
	if b, _ := os.ReadFile(f.backups()[0]); string(b) != kwinOther {
		t.Fatal("the backup is not the old file")
	}
}

func TestKwinRemovesLegacy(t *testing.T) {
	f := newKwinFake(t, "KDE", kwinLegacy, "kwriteconfig6", "qdbus6")
	if err := f.k.Apply(true); err != nil {
		t.Fatal(err)
	}
	ini := f.rc(t)
	if got := ini.get("General", "rules"); got != "other-a,other-b,quill-main,quill-kmem" || ini.get("General", "count") != "4" {
		t.Fatalf("General: %q count %q", got, ini.get("General", "count"))
	}
	if strings.Contains(ini.String(), "quill-remember-place") || strings.Contains(ini.String(), "1720") {
		t.Fatalf("legacy rule left:\n%s", ini)
	}
	if ini.get("other-b", "wmclass") != "example-b" {
		t.Fatal("other rule lost")
	}
}

func TestKwinIdempotent(t *testing.T) {
	f := newKwinFake(t, "KDE", kwinOther, "kwriteconfig6", "qdbus6")
	f.k.Apply(true)
	before := f.rc(t).String()
	f.calls = nil
	f.k.backedUp = false // a new run
	if err := f.k.Apply(true); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || f.rc(t).String() != before || len(f.backups()) != 1 {
		t.Fatalf("the second apply wrote: %v", f.calls)
	}
}

func TestKwinOffRemovesOnlyOurs(t *testing.T) {
	f := newKwinFake(t, "KDE", kwinLegacy, "kwriteconfig6", "qdbus6")
	f.k.Apply(true)
	f.k.Apply(false)
	ini := f.rc(t)
	if ini.get("General", "rules") != "other-a,other-b" || ini.get("General", "count") != "2" {
		t.Fatalf("General after off:\n%s", ini)
	}
	if strings.Contains(ini.String(), "quill") {
		t.Fatalf("our rules left:\n%s", ini)
	}
	if ini.get("other-a", "wmclass") != "example-a" || ini.get("other-b", "wmclass") != "example-b" {
		t.Fatal("other rules changed")
	}
	// off with nothing of ours left: no write, no reload
	f.calls = nil
	f.k.Apply(false)
	if len(f.calls) != 0 {
		t.Fatalf("off twice wrote: %v", f.calls)
	}
}

func TestKwinOffNoFileCreatesNothing(t *testing.T) {
	f := newKwinFake(t, "KDE", "", "kwriteconfig6", "qdbus6")
	if err := f.k.Apply(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.k.ConfigDir, "kwinrulesrc")); err == nil || len(f.calls) != 0 {
		t.Fatal("off wrote a file")
	}
}

func TestKwinBackupPruned(t *testing.T) {
	f := newKwinFake(t, "KDE", kwinOther, "kwriteconfig6", "qdbus6")
	for i := 0; i < 5; i++ {
		f.k.backedUp = false
		on := i%2 == 0
		if err := f.k.Apply(on); err != nil {
			t.Fatal(err)
		}
	}
	b := f.backups()
	if len(b) != 3 {
		t.Fatalf("backups kept: %v", b)
	}
	// once per run: two writes in one run make one backup
	f.k.backedUp = false
	n := len(f.backups())
	f.k.Apply(true)
	f.k.Apply(false)
	if len(f.backups()) > n+1 {
		t.Fatal("more than one backup in a run")
	}
}

func TestKwinNonKDEDoesNothing(t *testing.T) {
	for _, d := range []string{"GNOME", "", "XFCE:KDEish"} {
		f := newKwinFake(t, d, kwinOther, "kwriteconfig6", "qdbus6")
		if err := f.k.Apply(true); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(f.k.ConfigDir, "kwinrulesrc"))
		if len(f.calls) != 0 || string(b) != kwinOther || len(f.backups()) != 0 {
			t.Fatalf("%q: touched the config: %v", d, f.calls)
		}
	}
	if !(&kwinRules{Desktop: "ubuntu:KDE", ConfigDir: "/x"}).onKDE() {
		t.Fatal("KDE in a list is KDE")
	}
	var nilK *kwinRules
	if nilK.onKDE() || nilK.Apply(true) != nil {
		t.Fatal("a nil kwinRules does nothing")
	}
}

func TestKwinFallbacks(t *testing.T) {
	// kwriteconfig5 and qdbus (no 6)
	f := newKwinFake(t, "KDE", kwinOther, "kwriteconfig5", "qdbus")
	f.k.Apply(true)
	if f.count("kwriteconfig5 ") == 0 || f.count("kwriteconfig6") != 0 || f.count("qdbus org.kde.KWin /KWin reconfigure") != 1 {
		t.Fatalf("calls: %v", f.calls)
	}
	// only dbus-send for the reload
	f = newKwinFake(t, "KDE", kwinOther, "kwriteconfig6", "dbus-send")
	f.k.Apply(true)
	if f.count("dbus-send --session --type=method_call --dest=org.kde.KWin /KWin org.kde.KWin.reconfigure") != 1 {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestKwinNoKwriteconfigWritesINI(t *testing.T) {
	f := newKwinFake(t, "KDE", kwinLegacy+"\n# keep me\n", "qdbus6")
	if err := f.k.Apply(true); err != nil {
		t.Fatal(err)
	}
	if f.count("kwriteconfig") != 0 {
		t.Fatal("no kwriteconfig is installed")
	}
	ini := f.rc(t)
	if ini.get("General", "rules") != "other-a,other-b,quill-main,quill-kmem" || ini.get("General", "count") != "4" ||
		ini.get("quill-kmem", "wmclass") != appID+".kmem" || ini.get("quill-main", "sizerule") != "4" {
		t.Fatalf("direct write:\n%s", ini)
	}
	if ini.get("other-a", "wmclass") != "example-a" || !strings.Contains(ini.String(), "# keep me") || strings.Contains(ini.String(), "remember-place") {
		t.Fatalf("direct write lost content:\n%s", ini)
	}
	f.k.Apply(false)
	ini = f.rc(t)
	if strings.Contains(ini.String(), "quill") || ini.get("General", "count") != "2" || ini.get("other-b", "wmclass") != "example-b" {
		t.Fatalf("direct off:\n%s", ini)
	}
}

func TestKwinNewFile(t *testing.T) {
	f := newKwinFake(t, "KDE", "", "kwriteconfig6", "qdbus6")
	f.k.Apply(true)
	ini := f.rc(t)
	if ini.get("General", "rules") != "quill-main,quill-kmem" || ini.get("General", "count") != "2" || len(f.backups()) != 0 {
		t.Fatalf("fresh file:\n%s", ini)
	}
}

func TestKwinAskedOnce(t *testing.T) {
	h := newHarness(t)
	f := newKwinFake(t, "KDE", kwinOther, "kwriteconfig6", "qdbus6")
	h.d.kwin = f.k
	p := h.d.app.Preferences()
	shown := func() bool {
		h.ui(func() { h.d.askKwin() })
		var top bool
		h.ui(func() { top = h.w.Canvas().Overlays().Top() != nil })
		return top
	}
	if !shown() {
		t.Fatal("the question is asked on first start")
	}
	h.ui(func() { tapDialogButton(t, h, "Yes") })
	if !p.Bool(prefKwinAsked) || !p.Bool(prefKwinRules) {
		t.Fatal("yes is remembered")
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.count("qdbus6") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if f.count("qdbus6") != 1 {
		t.Fatalf("KWin not reloaded: %v", f.calls)
	}
	if shown() {
		t.Fatal("the question is never asked twice")
	}
}

func TestKwinSettingOffKDE(t *testing.T) {
	h := newHarness(t)
	check := func(desktop string) (disabled bool, note bool) {
		h.d.kwin = newKwinFake(t, desktop, "").k
		h.ui(func() {
			deepWalk(h.d.kwinSetting(), func(o fyne.CanvasObject) {
				switch w := o.(type) {
				case *widget.Check:
					disabled = w.Disabled()
				case *widget.Label:
					note = note || w.Text == kwinNotKDE
				}
			})
		})
		return
	}
	if dis, note := check("GNOME"); !dis || !note {
		t.Fatal("off KDE the switch is disabled with the note")
	}
	if dis, note := check("KDE"); dis || note {
		t.Fatal("on KDE the switch works")
	}
}
