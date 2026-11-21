package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKmemNeeds(t *testing.T) {
	brief := "rules: read, unchanged\ntasks: 11 not done (10 unclaimed); kmem task list\nmaint: 12 h ago, 3 warnings (/x/maint.txt)\n" +
		"AUDIT PROBLEMS: chain broken at event 12; backup kmem-20261120.db does not match\n" +
		"bin: 2 item(s) waiting for the user (kmem trash list; only the user runs kmem trash empty)\nmemory: ...\n"
	bin, audit := kmemNeeds(brief)
	if bin != 2 || !strings.HasPrefix(audit, "chain broken") {
		t.Fatalf("needs: bin %d audit %q", bin, audit)
	}
	if bin, audit := kmemNeeds("rules: read, unchanged\ntasks: 0 not done\nmaint: 1 h ago, 0 warnings (x)\n"); bin != 0 || audit != "" {
		t.Fatalf("nothing to decide: bin %d audit %q", bin, audit)
	}
}

func TestKmemBanner(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		if h.d.kmemBanner.Visible() {
			t.Fatal("hidden while there is nothing to decide")
		}
		h.d.setKmemBanner(2, "")
		if !h.d.kmemBanner.Visible() || !h.d.kmemBinBtn.Visible() || h.d.kmemAuditBtn.Visible() || !strings.Contains(h.d.kmemBannerText.Text, "2 item(s) in the kmem recycle bin") {
			t.Fatalf("bin items show with Review bin only: %q", h.d.kmemBannerText.Text)
		}
		h.d.setKmemBanner(0, "chain broken at event 12")
		if !h.d.kmemAuditBtn.Visible() || h.d.kmemBinBtn.Visible() || !strings.Contains(h.d.kmemBannerText.Text, "audit found problems") {
			t.Fatalf("audit problems show with Details only: %q", h.d.kmemBannerText.Text)
		}
		h.d.setKmemBanner(2, "chain broken at event 12")
	})
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { shot(t, h.w, dir, "kmem-banner.png") })
	}
	h.ui(func() {
		h.d.setKmemBanner(0, "")
		if h.d.kmemBanner.Visible() {
			t.Fatal("hidden again once the bin is empty and the audit is OK")
		}
	})
}
