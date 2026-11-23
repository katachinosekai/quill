package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func TestKmemNeeds(t *testing.T) {
	brief := "rules: read, unchanged\ntasks: 11 not done (10 unclaimed); kmem task list\nmaint: 12 h ago, 3 warnings (/x/maint.txt)\n" +
		"AUDIT PROBLEMS: chain broken at event 12; backup kmem-20261120.db does not match\n" +
		"bin: 2 item(s) waiting for the user (kmem trash list; only the user runs kmem trash empty)\nmemory: ...\n"
	tasks := []kmemTask{
		{ID: "t1", Status: "open", Title: "USER: send the screenshots", Topic: "launcher"},
		{ID: "t2", Status: "open", Title: "pick a name", Topic: "user"},
		{ID: "t3", Status: "open", Title: "refactor the user list", Topic: "quill"},
		{ID: "t4", Status: "done", Title: "USER: old", Topic: "user"},
	}
	n := parseKmemNeeds(brief, tasks)
	if n.Bin != 2 || !strings.HasPrefix(n.Audit, "chain broken") || n.Maint != 3 || n.MaintReport != "/x/maint.txt" || len(n.UserTasks) != 2 {
		t.Fatalf("needs: %+v", n)
	}
	if n.Count() != 2+1+1+2 {
		t.Fatalf("bin items + audit once + maint once + user tasks: %d", n.Count())
	}
	only := []struct {
		brief string
		tasks []kmemTask
		want  int
	}{
		{"bin: 4 item(s) waiting for the user\n", nil, 4},
		{"AUDIT PROBLEMS: x\n", nil, 1},
		{"maint: 7 h ago, 11 warnings (/m.txt)\n", nil, 1},
		{"", tasks[:1], 1},
		{"rules: read, unchanged\ntasks: 0 not done\nmaint: 1 h ago, 0 warnings (x)\n", tasks[2:], 0},
	}
	for _, c := range only {
		if got := parseKmemNeeds(c.brief, c.tasks).Count(); got != c.want {
			t.Errorf("%q: count %d want %d", c.brief, got, c.want)
		}
	}
	if badgeText(5) != "5" || badgeText(120) != "99+" {
		t.Fatal("badge text")
	}
}

func TestKmemBubble(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.kmemBubble == nil || h.d.kmemBubble.box.Visible() {
			t.Fatal("the bubble is hidden while kmem asks for nothing")
		}
		h.d.setKmemNeeds(kmemNeeds{Bin: 2, Audit: "chain broken", Maint: 3, MaintSig: "w:x", RolloutWait: "stage x",
			Issues: []kmemTask{{ID: "t1", Status: "open", Title: "kmem issue: a", Topic: "kmem"}}})
		if !h.d.kmemBubble.box.Visible() || h.d.kmemBubble.text.Text != "3" { // bin and audit do not badge
			t.Fatalf("bubble: %q", h.d.kmemBubble.text.Text)
		}
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, h.w, dir, "kmem-bubble.png") })
	}
	h.ui(func() {
		h.d.setKmemNeeds(kmemNeeds{})
		if h.d.kmemBubble.box.Visible() {
			t.Fatal("hidden again once nothing waits")
		}
	})
}

func TestBinInKmemSettings(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemVer.reset()
	h.d.kmemRun = func(prog string, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "version --json":
			return `{"version": "1.7.0", "api": 1}`, nil
		case "trash list":
			return "[f467b16] fact, 1 days in bin, by quill: dup\n    (decision) x\n", nil
		}
		return "", nil
	}
	h.ui(func() {
		h.d.kmemNeedsNow = kmemNeeds{Bin: 1}
		set := h.d.kmemSettings()
		btn := buttonContaining([]fyne.CanvasObject{set}, "Recycle bin")
		if btn == nil || btn.Text != "Recycle bin (1)..." {
			t.Fatal("Settings → General → Memory (kmem) has the recycle bin with its count")
		}
		test.Tap(btn)
		if h.d.kmemBinNow == nil {
			t.Fatal("the button opens the bin dialog")
		}
		h.d.setKmemNeeds(kmemNeeds{})
		if btn.Text != "Recycle bin..." {
			t.Fatalf("count follows kmem: %q", btn.Text)
		}
	})
	h.eventually("bin listed", func() bool { return buttonContaining(h.d.kmemBinNow.box.Objects, "f467b16") != nil })
}
