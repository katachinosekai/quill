package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

func TestDefaultKmemRulesText(t *testing.T) {
	rule := regexp.MustCompile(`(?m)^(\d+[a-z]?\d?)\.\s`) // kmem's RULE_RE
	if n := len(rule.FindAllString(defaultKmemRules, -1)); n < 10 {
		t.Fatalf("numbered rules kmem can cite: %d", n)
	}
	low := strings.ToLower(defaultKmemRules)
	bad := []string{"zomboid", "pzlock", "brave", "steam", "/home/", "/run/media", "c:\\users\\"}
	// The current user's name and home folder, whoever runs the test.
	if u, err := user.Current(); err == nil && len(u.Username) >= 3 {
		bad = append(bad, strings.ToLower(u.Username))
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		bad = append(bad, strings.ToLower(h))
	}
	for _, bad := range bad {
		if strings.Contains(low, bad) {
			t.Errorf("the default rules are generic, but mention %q", bad)
		}
	}
}

func TestInstallDefaultRules(t *testing.T) {
	t.Setenv("KMEM_CONFIG", "")
	home := t.TempDir()
	p, err := installDefaultRules(home)
	if err != nil || p != filepath.Join(home, ".local", "share", "kmem", "RULES.md") {
		t.Fatalf("installs where kmem reads rules: %q %v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != defaultKmemRules {
		t.Fatal("the file holds the default rules")
	}
	os.WriteFile(p, []byte("1. my own rule\n"), 0o644)
	if _, err := installDefaultRules(home); !errors.Is(err, errRulesExist) {
		t.Fatalf("an existing file is refused: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "1. my own rule\n" {
		t.Fatal("an existing rules file stays byte for byte")
	}
	// A rules_file in kmem's config is where kmem reads, so that is checked.
	home2 := t.TempDir()
	cfg := filepath.Join(home2, ".config", "kmem", "config.json")
	os.MkdirAll(filepath.Dir(cfg), 0o755)
	os.WriteFile(cfg, []byte(`{"rules_file": "~/notes/RULES.md"}`), 0o644)
	if got := kmemRulesPath(home2); got != filepath.Join(home2, "notes", "RULES.md") {
		t.Fatalf("config rules_file: %q", got)
	}
}

func TestDefaultRulesOptionIsOffUntilTicked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KMEM_CONFIG", "")
	h := newHarness(t)
	rules := filepath.Join(home, ".local", "share", "kmem", "RULES.md")
	h.ui(func() {
		box := h.d.defaultRulesSettings()
		var found bool
		deepWalk(box, func(o fyne.CanvasObject) {
			if c, ok := o.(*widget.Check); ok {
				found = true
				if c.Checked || c.Disabled() || !strings.Contains(c.Text, "recommended for beginners") {
					t.Fatalf("the option starts off and says it is for beginners: %q checked=%v", c.Text, c.Checked)
				}
				if _, err := os.Stat(rules); err == nil {
					t.Fatal("nothing is installed before the box is ticked")
				}
				c.SetChecked(true)
				if !c.Checked || !c.Disabled() || !strings.Contains(c.Text, "set up") {
					t.Fatalf("after ticking it shows the rules are set up: %q", c.Text)
				}
			}
		})
		if !found {
			t.Fatal("the option is shown")
		}
	})
	if b, err := os.ReadFile(rules); err != nil || string(b) != defaultKmemRules {
		t.Fatalf("ticking installs the default rules: %v", err)
	}
	// With a rules file there, building the option opens no dialog.
	h.ui(func() {
		for _, o := range h.w.Canvas().Overlays().List() {
			h.w.Canvas().Overlays().Remove(o)
		}
		h.d.defaultRulesSettings()
		if n := len(h.w.Canvas().Overlays().List()); n != 0 {
			t.Fatalf("an existing rules file opens no dialog: %d overlay(s)", n)
		}
	})
}
