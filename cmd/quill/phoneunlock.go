package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/folders"
)

// Home-network unlock (user 2026-11-22, via Quill Android): a phone on the
// trusted home network unlocks with a hardware-key signature instead of the
// device password. Conservative by design; every check fails closed:
//
//   - only devices whose first hello said "phone" (never PCs, never unknown);
//   - only when the device's Password switch is on and a device password is set;
//   - only when its switch "No password on this home network" is on (off by
//     default) and it registered a P-256 key from a password-unlocked session;
//   - only within 30 days of the last password unlock, and only while the
//     device password is the one that unlock used;
//   - only when the request comes from one of this PC's directly connected
//     subnets (no VPN, no loopback, not this PC's own address), arrived on
//     that interface's own address, is the remembered home network, and that
//     network's default gateway still has the remembered MAC. The phone's
//     SSID/BSSID count for nothing. The home network is recorded only by
//     turning the switch on (from a still valid password-unlocked
//     connection); a readable, different gateway MAC forgets it and turns
//     the switch off.
//
// A failed proof (bad signature, unknown, expired, replayed or other device's
// nonce) counts toward the password lock-out (pcMaxFails).

const (
	prefLanUnlock = "lanUnlock"
	lanMaxAge     = 30 * 24 * time.Hour
	lanNonceTTL   = 60 * time.Second
	lanNoncesMax  = 4
	lanSigVersion = "quill-unlock-v1"
)

// lanHome is the remembered home network of one device: this PC's interface,
// its subnets at the time, and the MAC of its IPv4 default gateway.
type lanHome struct {
	Iface    string   `json:"iface"`
	Prefixes []string `json:"prefixes"`
	GwMAC    string   `json:"gwMac"`
}

// lanDevice is the per-device state kept in the lanUnlock preference.
type lanDevice struct {
	On     bool     `json:"on"`
	Key    string   `json:"key,omitempty"` // base64 SPKI DER, P-256
	KeyAt  int64    `json:"keyAt,omitempty"`
	PwAt   int64    `json:"pwAt,omitempty"`   // last password unlock (ms)
	PwHash string   `json:"pwHash,omitempty"` // which device password that was
	Home   *lanHome `json:"home,omitempty"`
}

// netIface / netSnap: what this PC's interfaces look like right now.
type netIface struct {
	Name  string
	Flags net.Flags
	Addrs []netip.Prefix // own address with its prefix length
	GwMAC string         // MAC of the IPv4 default gateway on it ("" = none known)
}

type netSnap struct {
	Ifaces []netIface
}

// lanConn is one request's connection: the phone's source address and the
// local address it reached on this PC (both needed; missing = not local).
type lanConn struct {
	IP    netip.Addr
	Local netip.Addr
}

func (c lanConn) String() string { return c.IP.String() }

func connAddr(s string) netip.Addr {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap().WithZone("")
}

// Test hooks: the network view and the request's connection.
var (
	lanSnapshot = readNetSnap
	lanConnOf   = func(r *http.Request) lanConn {
		c := lanConn{IP: connAddr(r.RemoteAddr)}
		if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && la != nil {
			c.Local = connAddr(la.String())
		}
		return c
	}
)

var (
	lanVPNRanges = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}
	lanVPNNames  = []string{"wg", "tun", "zt", "tailscale"}
)

func vpnIface(i netIface) bool {
	for _, p := range lanVPNNames {
		if strings.HasPrefix(i.Name, p) {
			return true
		}
	}
	return i.Flags&net.FlagPointToPoint != 0
}

// lanIfaceOK: an interface that can carry a home network.
func lanIfaceOK(i netIface) bool {
	return i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 && !vpnIface(i)
}

// lanAddrOK: an address that can be a phone on the home network.
func lanAddrOK(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, v := range lanVPNRanges {
		if v.Contains(ip) {
			return false
		}
	}
	return true
}

// lanPrefixOK: a connected subnet that can be a home network (not VPN, not
// link-local, not absurdly wide).
func lanPrefixOK(p netip.Prefix) bool {
	a := p.Addr()
	if !lanAddrOK(a) {
		return false
	}
	if a.Is4() && p.Bits() < 8 || a.Is6() && p.Bits() < 48 {
		return false
	}
	for _, v := range lanVPNRanges {
		if v.Overlaps(p) {
			return false
		}
	}
	return true
}

// local finds the interface and subnet that make ip a directly connected
// neighbour of this PC; ok=false for VPN, loopback, own addresses and
// anything not on a connected subnet.
func (s netSnap) local(ip netip.Addr) (netIface, netip.Prefix, bool) {
	ip = ip.Unmap().WithZone("")
	if !lanAddrOK(ip) {
		return netIface{}, netip.Prefix{}, false
	}
	for _, i := range s.Ifaces {
		for _, a := range i.Addrs {
			if a.Addr().Unmap() == ip {
				return netIface{}, netip.Prefix{}, false // this PC itself
			}
		}
	}
	for _, i := range s.Ifaces {
		if !lanIfaceOK(i) {
			continue
		}
		for _, a := range i.Addrs {
			p := a.Masked()
			if lanPrefixOK(p) && p.Contains(ip) {
				return i, p, true
			}
		}
	}
	return netIface{}, netip.Prefix{}, false
}

// localVia: like local, and the connection also arrived on one of the
// matched interface's own addresses (http.LocalAddrContextKey).
func (s netSnap) localVia(c lanConn) (netIface, netip.Prefix, bool) {
	i, p, ok := s.local(c.IP)
	if !ok || !c.Local.IsValid() {
		return netIface{}, netip.Prefix{}, false
	}
	for _, a := range i.Addrs {
		if a.Addr().Unmap() == c.Local.Unmap().WithZone("") {
			return i, p, true
		}
	}
	return netIface{}, netip.Prefix{}, false
}

func (s netSnap) iface(name string) (netIface, bool) {
	for _, i := range s.Ifaces {
		if i.Name == name {
			return i, true
		}
	}
	return netIface{}, false
}

// learn remembers the home network a phone on connection c is on (nil when
// c isn't local or the interface's gateway MAC isn't known).
func (s netSnap) learn(c lanConn) *lanHome {
	i, _, ok := s.localVia(c)
	if !ok || i.GwMAC == "" {
		return nil
	}
	h := &lanHome{Iface: i.Name, GwMAC: i.GwMAC}
	for _, a := range i.Addrs {
		if p := a.Masked(); lanPrefixOK(p) {
			h.Prefixes = append(h.Prefixes, p.String())
		}
	}
	return h
}

type homeState int

const (
	homeOK      homeState = iota
	homeUnknown           // can't tell right now (interface down, gateway MAC unreadable): deny, keep the home
	homeChanged           // gateway MAC readable and different: forget the home
)

// homeState: is the remembered network still what this PC is on (same
// interface, not a VPN, one of its subnets, same gateway MAC)?
func (s netSnap) homeState(h lanHome) homeState {
	if h.GwMAC == "" {
		return homeChanged
	}
	i, ok := s.iface(h.Iface)
	if !ok || !lanIfaceOK(i) || i.GwMAC == "" {
		return homeUnknown
	}
	if !strings.EqualFold(i.GwMAC, h.GwMAC) {
		return homeChanged
	}
	for _, a := range i.Addrs {
		if h.hasPrefix(a.Masked()) {
			return homeOK
		}
	}
	return homeUnknown
}

func (h lanHome) hasPrefix(p netip.Prefix) bool {
	for _, s := range h.Prefixes {
		if q, err := netip.ParsePrefix(s); err == nil && q == p {
			return true
		}
	}
	return false
}

// atHome: c is a neighbour on the remembered, still valid home network.
func (s netSnap) atHome(h lanHome, c lanConn) bool {
	if s.homeState(h) != homeOK {
		return false
	}
	i, p, ok := s.localVia(c)
	return ok && i.Name == h.Iface && h.hasPrefix(p)
}

func lanSignedString(deviceID, nonce, fp string) string {
	return lanSigVersion + "|" + deviceID + "|" + nonce + "|" + fp
}

func lanPwHash(stored string) string {
	sum := sha256.Sum256([]byte("quill-lan-pw|" + stored))
	return hex.EncodeToString(sum[:16])
}

// parseLanKey accepts a base64 (standard) SubjectPublicKeyInfo of a P-256 key.
func parseLanKey(s string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("publicKey must be base64 (standard alphabet)")
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("publicKey must be an X.509 SubjectPublicKeyInfo")
	}
	ec, ok := k.(*ecdsa.PublicKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("publicKey must be an EC P-256 key")
	}
	return ec, nil
}

// --- persisted per-device state (UI thread) ---

func (d *desk) lanDevices() map[string]lanDevice {
	m := map[string]lanDevice{}
	_ = json.Unmarshal([]byte(d.app.Preferences().String(prefLanUnlock)), &m)
	return m
}

func (d *desk) saveLanDevices(m map[string]lanDevice) {
	known := map[string]bool{}
	for _, p := range d.phones() {
		known[p.ID] = true
	}
	for id := range m {
		if !known[id] {
			delete(m, id)
		}
	}
	b, _ := json.Marshal(m)
	d.app.Preferences().SetString(prefLanUnlock, string(b))
}

// lanCheck: may device id unlock without the password on connection c right
// now? (UI thread.) When the home network's gateway MAC is readable and
// different, the home is forgotten and the switch turned off: the user turns
// it on again in Quill Desktop → Devices (nothing re-learns it by itself).
func (d *desk) lanCheck(id string, c lanConn, snap netSnap, now time.Time) bool {
	if !lanUnlockSupported {
		return false
	}
	p, ok := d.device(id)
	stored := d.app.Preferences().String(prefPCPassword)
	if !ok || p.Kind != "phone" || !p.NeedsUnlock() || stored == "" {
		return false
	}
	m := d.lanDevices()
	st := m[id]
	if !st.On || st.Key == "" || st.Home == nil || st.PwAt <= 0 {
		return false
	}
	pw := time.UnixMilli(st.PwAt)
	if now.Sub(pw) >= lanMaxAge || pw.After(now.Add(time.Minute)) {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(st.PwHash), []byte(lanPwHash(stored))) != 1 {
		return false
	}
	if snap.homeState(*st.Home) == homeChanged {
		st.On, st.Home = false, nil
		m[id] = st
		d.saveLanDevices(m)
		d.updatePhoneList()
		return false
	}
	return snap.atHome(*st.Home, c)
}

// setLanUnlock is the per-device switch "No password on this home network".
// Turning it on remembers the current home network, from the connection of
// the phone's password unlock, which must still be valid; that is the only
// place the home network is ever recorded. Returns why it can't turn on.
func (d *desk) setLanUnlock(id string, on bool) error {
	p, ok := d.device(id)
	if !ok || p.Kind != "phone" {
		return fmt.Errorf("only phones can unlock without a password")
	}
	m := d.lanDevices()
	st := m[id]
	if !on {
		st.On, st.Home = false, nil
		m[id] = st
		d.saveLanDevices(m)
		return nil
	}
	var home *lanHome
	if a := d.phone().api; a != nil {
		a.lan.mu.Lock()
		tok, c, ok := a.lan.pwTok[id], a.lan.pwConn[id], a.lan.pwTok[id] != ""
		a.lan.mu.Unlock()
		if ok && d.pcLock.valid(id, tok, d.pcIdle(), false) {
			home = lanSnapshot().learn(c)
		}
	}
	if home == nil {
		return fmt.Errorf("Unlock %s with the password while it is on this PC's home network (not over a VPN), then turn this on.", p.Name)
	}
	st.On, st.Home = true, home
	m[id] = st
	d.saveLanDevices(m)
	return nil
}

// revokeLanUnlock: the notice's Revoke: switch off, key dropped, session ended.
func (d *desk) revokeLanUnlock(id string) {
	m := d.lanDevices()
	delete(m, id)
	d.saveLanDevices(m)
	d.pcLock.forget(id)
	if a := d.phone().api; a != nil {
		a.lan.mu.Lock()
		delete(a.lan.pwTok, id)
		a.lan.mu.Unlock()
	}
	d.updatePhoneList()
}

// lanNotice tells the user a phone got in without a password, with Revoke
// (UI thread).
func (a *phoneAPI) lanNotice(id, name, ip string) {
	d := a.d
	title := "Phone " + name + " unlocked without a password"
	body := "Phone " + name + " unlocked without a password from " + ip + "."
	d.desktopNotify("", title, body+" Revoke it in Quill Desktop.")
	if d.win == nil {
		return
	}
	if old := a.lan.dialogs[id]; old != nil {
		old.Hide()
	}
	msg := widget.NewLabel(body + "\n\nRevoke turns off \"No password on this home network\" for it and drops its key; it then needs the password again.")
	dlg := dialog.NewCustomConfirm("Unlocked without a password", "Revoke", "OK", msg, func(revoke bool) {
		delete(a.lan.dialogs, id)
		if revoke {
			d.revokeLanUnlock(id)
		}
	}, d.win)
	a.lan.dialogs[id] = dlg
	dlg.Show()
}

// --- lock-out helpers shared with the password unlock ---

func (l *pcLock) lockedOut(id string) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[id]
	now := l.now()
	if f.n >= pcMaxFails && now.Before(f.until) {
		return true, fmt.Sprintf("too many failed unlocks; try again in %d min", int(f.until.Sub(now).Minutes())+1)
	}
	return false, ""
}

// failProof counts a bad key proof like a wrong password.
func (l *pcLock) failProof(id, code, what string) (string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, already := l.failLocked(id, l.now())
	if already || n >= pcMaxFails {
		return "locked_out", fmt.Sprintf("%s; locked for %d min", what, int(pcLockout.Minutes()))
	}
	return code, fmt.Sprintf("%s (%d of %d tries)", what, n, pcMaxFails)
}

// grant gives device id a fresh unlock token (after a valid key proof);
// "" when a concurrent failure locked the device meanwhile.
func (l *pcLock) grant(id string) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if f := l.fails[id]; f.n >= pcMaxFails && now.Before(f.until) {
		return ""
	}
	delete(l.fails, id)
	l.tokens[id] = pcToken{tok: tok, last: now}
	return tok
}

// --- in-memory state of the phone API ---

type lanNonce struct {
	dev string
	exp time.Time
}

type lanMem struct {
	mu     sync.Mutex
	nonces map[string]lanNonce
	pwTok  map[string]string  // token of the device's last password unlock
	pwConn map[string]lanConn // and the connection it came from

	dialogs map[string]dialog.Dialog // open notices (UI thread)
}

func newLanMem() *lanMem {
	return &lanMem{nonces: map[string]lanNonce{}, pwTok: map[string]string{}, pwConn: map[string]lanConn{}, dialogs: map[string]dialog.Dialog{}}
}

func (m *lanMem) issue(dev string, now time.Time) (string, time.Time) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	n := base64.RawURLEncoding.EncodeToString(b)
	exp := now.Add(lanNonceTTL)
	m.mu.Lock()
	defer m.mu.Unlock()
	var mine []string
	for k, v := range m.nonces {
		if !now.Before(v.exp) {
			delete(m.nonces, k)
		} else if v.dev == dev {
			mine = append(mine, k)
		}
	}
	for len(mine) >= lanNoncesMax {
		old := 0
		for i, k := range mine {
			if m.nonces[k].exp.Before(m.nonces[mine[old]].exp) {
				old = i
			}
		}
		delete(m.nonces, mine[old])
		mine = append(mine[:old], mine[old+1:]...)
	}
	m.nonces[n] = lanNonce{dev: dev, exp: exp}
	return n, exp
}

// take uses up a nonce: ok only when it was issued to dev and hasn't
// expired. It is deleted either way (single use).
func (m *lanMem) take(n, dev string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.nonces[n]
	delete(m.nonces, n)
	return ok && v.dev == dev && now.Before(v.exp)
}

// --- HTTP handlers ---

// lanHello: the hello's "lanUnlock" (UI thread).
func (a *phoneAPI) lanHello(phone string, c lanConn, snap netSnap) bool {
	return a.d.lanCheck(phone, c, snap, a.d.pcLock.now())
}

type bufResponse struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (b *bufResponse) Header() http.Header { return b.header }
func (b *bufResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.buf.Write(p)
}
func (b *bufResponse) WriteHeader(s int) {
	if b.status == 0 {
		b.status = s
	}
}

// unlockRoute serves POST /phone/unlock: {password} goes to the password
// unlock (pclock.go), {nonce, sig} to the key proof.
func (a *phoneAPI) unlockRoute(w http.ResponseWriter, r *http.Request, phone string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, folders.APIBodyMax))
	var in struct {
		Password *string `json:"password"`
		Nonce    string  `json:"nonce"`
		Sig      string  `json:"sig"`
	}
	if err != nil || json.Unmarshal(body, &in) != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "body must be JSON {password} or {nonce, sig}")
		return
	}
	c := lanConnOf(r)
	if in.Nonce != "" || in.Sig != "" {
		if in.Password != nil {
			folders.WriteError(w, http.StatusBadRequest, "bad_request", "send either {password} or {nonce, sig}")
			return
		}
		a.keyUnlock(w, phone, in.Nonce, in.Sig, c)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := &bufResponse{header: http.Header{}}
	a.unlockAPI(rec, r, "unlock", phone)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status == http.StatusOK {
		var out struct {
			Token  string `json:"token"`
			Needed bool   `json:"needed"`
		}
		if json.Unmarshal(rec.buf.Bytes(), &out) == nil && out.Token != "" && out.Needed {
			a.ui(func() { a.notePasswordUnlock(phone, out.Token, c) })
		}
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.buf.Bytes())
}

// notePasswordUnlock (UI thread): restarts the 30 days and ties the waiver
// to the current device password. It never changes the home network; the
// token and connection are kept so that turning the switch on can record it.
func (a *phoneAPI) notePasswordUnlock(phone, tok string, c lanConn) {
	d := a.d
	p, ok := d.device(phone)
	if !ok || p.Kind != "phone" {
		return
	}
	a.lan.mu.Lock()
	a.lan.pwTok[phone], a.lan.pwConn[phone] = tok, c
	a.lan.mu.Unlock()
	m := d.lanDevices()
	st := m[phone]
	st.PwAt = d.pcLock.now().UnixMilli()
	st.PwHash = lanPwHash(d.app.Preferences().String(prefPCPassword))
	m[phone] = st
	d.saveLanDevices(m)
}

func (a *phoneAPI) keyUnlock(w http.ResponseWriter, phone, nonce, sig string, c lanConn) {
	d := a.d
	if locked, msg := d.pcLock.lockedOut(phone); locked {
		folders.WriteError(w, http.StatusForbidden, "locked_out", msg)
		return
	}
	now := d.pcLock.now()
	fresh := a.lan.take(nonce, phone, now)
	snap := lanSnapshot()
	var ok bool
	var fp, name, keyB64 string
	a.ui(func() {
		ok = d.lanCheck(phone, c, snap, now)
		fp = d.phone().fp
		p, _ := d.device(phone)
		name = p.Name
		keyB64 = d.lanDevices()[phone].Key
	})
	if !ok {
		folders.WriteError(w, http.StatusForbidden, "lan_unlock_off", "this device must unlock with the PC's password")
		return
	}
	if !fresh {
		code, msg := d.pcLock.failProof(phone, "bad_nonce", "unknown, used or expired nonce")
		folders.WriteError(w, http.StatusForbidden, code, msg)
		return
	}
	key, kerr := parseLanKey(keyB64)
	raw, serr := base64.StdEncoding.DecodeString(sig)
	sum := sha256.Sum256([]byte(lanSignedString(phone, nonce, fp)))
	if fp == "" || kerr != nil || serr != nil || !ecdsa.VerifyASN1(key, sum[:], raw) {
		code, msg := d.pcLock.failProof(phone, "bad_signature", "signature does not verify")
		folders.WriteError(w, http.StatusForbidden, code, msg)
		return
	}
	tok := d.pcLock.grant(phone)
	if tok == "" {
		_, msg := d.pcLock.lockedOut(phone)
		folders.WriteError(w, http.StatusForbidden, "locked_out", msg)
		return
	}
	a.lan.mu.Lock()
	delete(a.lan.pwTok, phone)
	a.lan.mu.Unlock()
	var idle int
	a.ui(func() {
		idle = int(d.pcIdle().Minutes())
		a.lanNotice(phone, name, c.String())
	})
	folders.WriteJSON(w, http.StatusOK, map[string]any{"token": tok, "idleMin": idle, "needed": true, "method": "key"})
}

// unlockChallenge serves POST /phone/unlock/challenge (passes the lock gate).
func (a *phoneAPI) unlockChallenge(w http.ResponseWriter, r *http.Request, phone string) {
	d := a.d
	if locked, msg := d.pcLock.lockedOut(phone); locked {
		folders.WriteError(w, http.StatusForbidden, "locked_out", msg)
		return
	}
	c, snap, now := lanConnOf(r), lanSnapshot(), d.pcLock.now()
	var ok bool
	a.ui(func() { ok = d.lanCheck(phone, c, snap, now) })
	if !ok {
		folders.WriteError(w, http.StatusForbidden, "lan_unlock_off", "this device must unlock with the PC's password")
		return
	}
	n, exp := a.lan.issue(phone, now)
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "nonce": n, "expiresMs": exp.UnixMilli(), "ttlMs": lanNonceTTL.Milliseconds()})
}

// unlockKey serves POST /phone/unlock/key {publicKey}: only from a session
// unlocked with the password (not one unlocked with a key proof).
func (a *phoneAPI) unlockKey(w http.ResponseWriter, r *http.Request, phone string) {
	d := a.d
	var in struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, folders.APIBodyMax)).Decode(&in); err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "body must be JSON {publicKey}")
		return
	}
	key, err := parseLanKey(in.PublicKey)
	if err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	der, _ := x509.MarshalPKIXPublicKey(key)
	tok := r.Header.Get(unlockHeader)
	c, snap := lanConnOf(r), lanSnapshot()
	code, msg := "", ""
	var lan bool
	a.ui(func() {
		p, ok := d.device(phone)
		if !ok || p.Kind != "phone" {
			code, msg = "not_a_phone", "only phones can unlock with a key"
			return
		}
		a.lan.mu.Lock()
		pw := a.lan.pwTok[phone]
		a.lan.mu.Unlock()
		if !p.NeedsUnlock() || tok == "" || pw == "" || subtle.ConstantTimeCompare([]byte(pw), []byte(tok)) != 1 || !d.pcLock.valid(phone, tok, d.pcIdle(), false) {
			code, msg = "password_session_required", "register the key from a session unlocked with the password"
			return
		}
		m := d.lanDevices()
		st := m[phone]
		st.Key, st.KeyAt = base64.StdEncoding.EncodeToString(der), d.pcLock.now().UnixMilli()
		m[phone] = st
		d.saveLanDevices(m)
		lan = d.lanCheck(phone, c, snap, d.pcLock.now())
	})
	if code != "" {
		folders.WriteError(w, http.StatusForbidden, code, msg)
		return
	}
	folders.WriteJSON(w, http.StatusOK, map[string]any{"v": 1, "ok": true, "lanUnlock": lan})
}

// lanUnlockCheck is the per-phone switch in Quill Desktop → Devices (off by
// default; greyed out without the Password switch or off Linux).
func (d *desk) lanUnlockCheck(ph folders.Phone) *widget.Check {
	var c *widget.Check
	c = widget.NewCheck("No password on this home network", func(on bool) {
		if err := d.setLanUnlock(ph.ID, on); err != nil && on {
			c.SetChecked(false)
			if d.win != nil {
				dialog.ShowInformation("Home network not recorded", err.Error(), d.win)
			}
		}
	})
	c.Checked = d.lanDevices()[ph.ID].On
	if !lanUnlockSupported || !ph.NeedsUnlock() {
		c.Disable()
	}
	return c
}
