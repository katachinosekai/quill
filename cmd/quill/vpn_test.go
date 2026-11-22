package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

func TestRemoteAccessRow(t *testing.T) {
	h := newHarness(t)
	old := vpnAddress
	defer func() { vpnAddress = old }()
	h.ui(func() {
		p := h.d.app.Preferences()
		p.SetString(prefRemoteAccess, "")
		r := h.d.remoteAccessRow(nil)
		if !r.ok || !r.optional || !strings.Contains(r.state, "home network only") {
			t.Fatalf("skipped by default, never missing: %+v", r)
		}
		p.SetString(prefRemoteAccess, "claude-rc")
		if r := h.d.remoteAccessRow(nil); !r.ok || !strings.Contains(r.state, "Remote Control only") {
			t.Fatalf("no-VPN choice: %+v", r)
		}
		vpnAddress = func(o vpnOption) string {
			if o.ID == "tailscale" {
				return "100.64.0.7"
			}
			return ""
		}
		p.SetString(prefRemoteAccess, "tailscale")
		r = h.d.remoteAccessRow(nil)
		if vpnInstalled(vpnOptions[0]) {
			if !r.ok || !strings.Contains(r.state, "connected (100.64.0.7)") {
				t.Fatalf("connected VPN shows its address: %+v", r)
			}
		} else if r.ok || !strings.Contains(r.state, "not installed") {
			t.Fatalf("missing VPN app: %+v", r)
		}
		if h.d.setupMissing() && !strings.Contains(strings.Join(func() []string {
			var out []string
			for _, x := range h.d.setupRows(nil) {
				if !x.ok && !x.optional {
					out = append(out, x.title)
				}
			}
			return out
		}(), ","), "Claude") {
			t.Fatal("remote access never makes Setup count as missing")
		}
		h.d.applyWelcome(welcomeChoices{Lang: "en", Remote: "netbird"})
		if p.String(prefRemoteAccess) != "netbird" {
			t.Fatal("the Welcome's VPN choice is kept for Setup")
		}
		h.d.applyWelcome(welcomeChoices{Lang: "en"})
		if p.String(prefRemoteAccess) != "skip" {
			t.Fatal("no choice means home network only")
		}
	})
	for id, want := range map[string]bool{"8056c2e21c000001": true, "8056c2e21c00000": false, "8056c2e21c00000g": false} {
		if isZeroTierID(id) != want {
			t.Errorf("isZeroTierID(%q) != %v", id, want)
		}
	}
	if os.Getenv("DESK_SHOTS") != "" {
		dir, _ := filepath.Abs(filepath.Join("..", "..", ".shots"))
		os.MkdirAll(dir, 0o755)
		h.ui(func() { h.w.Resize(fyne.NewSize(1100, 760)); h.d.remoteAccessDialog(nil) })
		h.ui(func() { shot(t, h.w, dir, "remote-access.png") })
	}
}
