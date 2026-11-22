package main

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Remote access from outside the home network (user 2026-11-22): optional,
// skipped by default. Quill's phone and PC links work over any VPN that gives
// devices private addresses (the pinned certificate only needs the
// connection passed through), so Setup offers several and the pair link
// carries every interface's address.

const prefRemoteAccess = "remoteAccess" // "" or "skip", or a vpnOption id

type vpnOption struct {
	ID, Name, Hint string
	Bin            string   // its command-line tool
	Ifaces         []string // interface name prefixes it creates
	Linux          string   // install script (runs in a terminal after asking)
	Winget         string   // Windows package id
	Up             string   // command that signs in / connects (Linux)
	Manual         string   // no installer: what to do instead
}

var vpnOptions = []vpnOption{
	{ID: "tailscale", Name: "Tailscale (recommended)", Hint: "Hosted mesh VPN on WireGuard; install, sign in, done. Free for personal use.",
		Bin: "tailscale", Ifaces: []string{"tailscale", "utun"}, Linux: "curl -fsSL https://tailscale.com/install.sh | sh", Winget: "Tailscale.Tailscale", Up: "sudo tailscale up"},
	{ID: "netbird", Name: "NetBird", Hint: "Open-source mesh VPN, hosted or self-hosted.",
		Bin: "netbird", Ifaces: []string{"wt", "netbird"}, Linux: "curl -fsSL https://pkgs.netbird.io/install.sh | sh", Winget: "Netbird.Netbird", Up: "sudo netbird up"},
	{ID: "zerotier", Name: "ZeroTier", Hint: "Mesh VPN with a network ID; you join a network and approve the devices.",
		Bin: "zerotier-cli", Ifaces: []string{"zt"}, Linux: "curl -s https://install.zerotier.com | sudo bash", Winget: "ZeroTier.ZeroTierOne"},
	{ID: "headscale", Name: "Headscale (self-hosted Tailscale)", Hint: "Your own Tailscale control server; devices use the Tailscale app pointed at it.",
		Bin: "tailscale", Ifaces: []string{"tailscale", "utun"}, Linux: "curl -fsSL https://tailscale.com/install.sh | sh", Winget: "Tailscale.Tailscale", Up: "sudo tailscale up --login-server https://YOUR-HEADSCALE-SERVER"},
	{ID: "wireguard", Name: "WireGuard (manual)", Hint: "Plain WireGuard: keys, a forwarded port and a config per device.",
		Bin: "wg", Ifaces: []string{"wg"}, Manual: "Set up a WireGuard tunnel between this PC and your phone (wireguard.com/quickstart), then pair again: the tunnel's address is in the pair link."},
	{ID: "claude-rc", Name: "Claude Code Remote Control only (no VPN)", Hint: "Reach sessions from claude.ai/code anywhere, relayed by Anthropic; Quill for Android's own features need your network or a VPN.",
		Manual: "Turn on Remote Control for a session in Quill Desktop; open it from claude.ai/code or the Claude app."},
}

func vpnByID(id string) (vpnOption, bool) {
	for _, o := range vpnOptions {
		if o.ID == id {
			return o, true
		}
	}
	return vpnOption{}, false
}

// vpnAddress: the first IPv4 address on an interface the option creates ("" = not connected).
var vpnAddress = func(o vpnOption) string {
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		for _, p := range o.Ifaces {
			if !strings.HasPrefix(strings.ToLower(i.Name), p) {
				continue
			}
			addrs, _ := i.Addrs()
			for _, a := range addrs {
				if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
					return ipn.IP.String()
				}
			}
		}
	}
	return ""
}

func vpnInstalled(o vpnOption) bool {
	if o.Bin == "" {
		return true
	}
	_, err := exec.LookPath(o.Bin)
	return err == nil
}

// remoteAccessRow: Setup's optional row (never counts as missing).
func (d *desk) remoteAccessRow(refresh func()) setupRow {
	r := setupRow{title: "Reach Quill from outside your network", optional: true, action: "Choose…", do: func() { d.remoteAccessDialog(refresh) }}
	choice := d.app.Preferences().String(prefRemoteAccess)
	o, ok := vpnByID(choice)
	switch {
	case !ok:
		r.ok, r.state = true, "skipped: phones and other PCs connect on your home network only"
	case o.Manual != "" && o.Bin == "":
		r.ok, r.state = true, o.Name
	case !vpnInstalled(o):
		r.state = o.Name + ": not installed yet"
	default:
		if ip := vpnAddress(o); ip != "" {
			r.ok, r.state = true, o.Name+": connected ("+ip+"); the pair link includes this address"
		} else {
			r.state = o.Name + ": installed, not connected"
		}
	}
	return r
}

// remoteAccessDialog lets the user pick a VPN (or skip) and install/connect it.
func (d *desk) remoteAccessDialog(after func()) {
	names := []string{"Skip: only on my home network"}
	for _, o := range vpnOptions {
		names = append(names, o.Name)
	}
	pick := widget.NewRadioGroup(names, nil)
	cur, ok := vpnByID(d.app.Preferences().String(prefRemoteAccess))
	if ok {
		pick.Selected = cur.Name
	} else {
		pick.Selected = names[0]
	}
	hint := widget.NewLabel("")
	hint.Wrapping = fyne.TextWrapWord
	install := widget.NewButton("Install…", nil)
	connect := widget.NewButton("Sign in / connect…", nil)
	var dlg dialog.Dialog
	sync := func() {
		o, ok := vpnByOptionName(pick.Selected)
		install.Hide()
		connect.Hide()
		if !ok {
			hint.SetText("Quill for Android and other PCs reach this PC only on your home network. You can change this any time in Settings → Setup.")
			return
		}
		text := o.Hint
		if o.Manual != "" {
			text += "\n\n" + o.Manual
		}
		if ip := vpnAddress(o); ip != "" {
			text += "\n\nConnected: " + ip
		} else if o.Bin != "" && vpnInstalled(o) {
			text += "\n\nInstalled, not connected."
		}
		hint.SetText(text)
		if o.Bin != "" && !vpnInstalled(o) && (o.Linux != "" || o.Winget != "") {
			install.Show()
		}
		if o.Bin != "" && vpnInstalled(o) && (o.Up != "" || o.ID == "zerotier") && vpnAddress(o) == "" {
			connect.Show()
		}
	}
	pick.OnChanged = func(string) { sync() }
	install.OnTapped = func() {
		o, _ := vpnByOptionName(pick.Selected)
		script := o.Linux
		if runtime.GOOS == "windows" {
			script = "winget install -e --id " + o.Winget + " --accept-source-agreements --accept-package-agreements"
		}
		d.runInstaller("Install "+o.Name, "This installs "+o.Name+" with its own installer.", script, func() { d.do(sync) })
	}
	connect.OnTapped = func() {
		o, _ := vpnByOptionName(pick.Selected)
		if o.ID == "zerotier" {
			e := widget.NewEntry()
			e.SetPlaceHolder("16-character network ID")
			dialog.ShowForm("Join a ZeroTier network", "Join", "Cancel", []*widget.FormItem{widget.NewFormItem("Network ID", e)}, func(ok bool) {
				if id := strings.TrimSpace(e.Text); ok && isZeroTierID(id) {
					d.runInstaller("Join ZeroTier", "This joins the network; approve this PC in your ZeroTier network's settings afterwards.", "sudo zerotier-cli join "+id, func() { d.do(sync) })
				}
			}, d.win)
			return
		}
		d.runInstaller("Connect "+o.Name, "This signs this PC in; follow the link it prints.", o.Up, func() { d.do(sync) })
	}
	sync()
	body := container.NewVBox(widget.NewLabel("Quill for Android and other PCs can reach this PC from anywhere through a VPN. Optional."),
		pick, hint, container.NewHBox(install, connect))
	dlg = dialog.NewCustomConfirm("Reach Quill from outside your network", "Save", "Cancel", container.NewVScroll(body), func(ok bool) {
		if !ok {
			return
		}
		id := "skip"
		if o, found := vpnByOptionName(pick.Selected); found {
			id = o.ID
		}
		d.app.Preferences().SetString(prefRemoteAccess, id)
		if after != nil {
			after()
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(640, 560))
	dlg.Show()
}

func vpnByOptionName(name string) (vpnOption, bool) {
	for _, o := range vpnOptions {
		if o.Name == name {
			return o, true
		}
	}
	return vpnOption{}, false
}

func isZeroTierID(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// netbirdName: this PC's NetBird DNS name, for the pair link ("" if none).
func netbirdName() string {
	path, err := exec.LookPath("netbird")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "status", "--json").Output()
	if err != nil {
		return ""
	}
	var st struct {
		FQDN string `json:"fqdn"`
	}
	if json.Unmarshal(out, &st) != nil {
		return ""
	}
	return strings.TrimSuffix(st.FQDN, ".")
}
