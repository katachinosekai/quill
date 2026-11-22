package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/folders"
)

// Example addresses only (RFC 5737 / RFC 3849 / private ranges), fake MACs.
const (
	testGwMAC    = "02:00:00:00:00:01"
	testOtherMAC = "02:00:00:00:00:99"
)

func testSnap(gw string) netSnap {
	up := net.FlagUp | net.FlagBroadcast
	pfx := netip.MustParsePrefix
	return netSnap{Ifaces: []netIface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, Addrs: []netip.Prefix{pfx("127.0.0.1/8"), pfx("::1/128")}},
		{Name: "eth0", Flags: up, GwMAC: gw, Addrs: []netip.Prefix{pfx("192.0.2.10/24"), pfx("2001:db8:1::10/64"), pfx("fe80::10/64")}},
		{Name: "wg0", Flags: up, Addrs: []netip.Prefix{pfx("198.51.100.1/24")}},
		{Name: "tun0", Flags: up, Addrs: []netip.Prefix{pfx("10.8.0.1/24")}},
		{Name: "ztabcdef", Flags: up, Addrs: []netip.Prefix{pfx("10.9.0.1/24")}},
		{Name: "tailscale0", Flags: up, Addrs: []netip.Prefix{pfx("100.100.1.1/32"), pfx("fd7a:115c:a1e0::1/128")}},
		{Name: "eth1", Flags: up, GwMAC: testOtherMAC, Addrs: []netip.Prefix{pfx("100.64.1.2/10")}}, // CGNAT range on a plain NIC
		{Name: "ppp0", Flags: net.FlagUp | net.FlagPointToPoint, Addrs: []netip.Prefix{pfx("172.16.5.1/24")}},
		{Name: "eth2", Flags: net.FlagBroadcast, Addrs: []netip.Prefix{pfx("203.0.113.1/24")}}, // down
	}}
}

func TestLanLocalSubnets(t *testing.T) {
	s := testSnap(testGwMAC)
	for ip, want := range map[string]bool{
		"192.0.2.50":        true,
		"::ffff:192.0.2.50": true,
		"2001:db8:1::50":    true,
		"192.0.2.10":        false, // this PC itself
		"2001:db8:1::10":    false,
		"fe80::5":           false, // link-local
		"127.0.0.1":         false,
		"::1":               false,
		"198.51.100.7":      false, // wg*
		"10.8.0.5":          false, // tun*
		"10.9.0.5":          false, // zt*
		"100.100.1.7":       false, // tailscale / CGNAT
		"100.64.1.9":        false, // 100.64.0.0/10 even on a plain interface
		"fd7a:115c:a1e0::5": false,
		"172.16.5.9":        false, // point-to-point
		"203.0.113.9":       false, // interface down
		"198.18.0.1":        false, // not connected at all
		"2001:db8:2::1":     false,
		"0.0.0.0":           false,
		"ff02::1":           false,
	} {
		_, _, ok := s.local(netip.MustParseAddr(ip))
		if ok != want {
			t.Errorf("local(%s) = %v, want %v", ip, ok, want)
		}
	}
	if _, _, ok := s.local(netip.Addr{}); ok {
		t.Error("no address is not local")
	}
}

func TestLanHomeGateway(t *testing.T) {
	s := testSnap(testGwMAC)
	conn := func(ip, local string) lanConn {
		c := lanConn{IP: netip.MustParseAddr(ip)}
		if local != "" {
			c.Local = netip.MustParseAddr(local)
		}
		return c
	}
	h := s.learn(conn("192.0.2.50", "192.0.2.10"))
	if h == nil || h.Iface != "eth0" || h.GwMAC != testGwMAC || len(h.Prefixes) != 2 {
		t.Fatalf("learn: %+v", h)
	}
	if !s.atHome(*h, conn("192.0.2.77", "192.0.2.10")) || !s.atHome(*h, conn("2001:db8:1::77", "2001:db8:1::10")) {
		t.Fatal("a neighbour on the home network is at home")
	}
	if s.atHome(*h, conn("198.51.100.7", "198.51.100.1")) {
		t.Fatal("VPN peer is not at home")
	}
	// The connection must have arrived on the home interface's own address.
	for _, local := range []string{"", "10.8.0.1", "198.51.100.1", "127.0.0.1"} {
		if s.atHome(*h, conn("192.0.2.77", local)) || s.learn(conn("192.0.2.77", local)) != nil {
			t.Fatalf("local address %q is not the home interface's", local)
		}
	}
	if s.homeState(*h) != homeOK {
		t.Fatal("home ok")
	}
	moved := testSnap(testOtherMAC)
	if moved.homeState(*h) != homeChanged || moved.atHome(*h, conn("192.0.2.77", "192.0.2.10")) {
		t.Fatal("a different gateway MAC is a different network")
	}
	if noGw := testSnap(""); noGw.homeState(*h) != homeUnknown || noGw.atHome(*h, conn("192.0.2.77", "192.0.2.10")) || noGw.learn(conn("192.0.2.50", "192.0.2.10")) != nil {
		t.Fatal("unreadable gateway MAC: no waiver, home kept, nothing learned")
	}
	if s.learn(conn("10.8.0.5", "10.8.0.1")) != nil || s.learn(conn("100.64.1.9", "100.64.1.2")) != nil {
		t.Fatal("never learn a VPN network")
	}
}

func TestLanNonces(t *testing.T) {
	m := newLanMem()
	now := time.Now()
	n, exp := m.issue("dev1", now)
	if len(n) != 43 || exp.Sub(now) != lanNonceTTL {
		t.Fatalf("nonce %q exp %v", n, exp)
	}
	if m.take(n, "dev2", now) || m.take(n, "dev1", now) {
		t.Fatal("other device's nonce is rejected and used up")
	}
	n, _ = m.issue("dev1", now)
	if !m.take(n, "dev1", now) || m.take(n, "dev1", now) {
		t.Fatal("single use")
	}
	n, _ = m.issue("dev1", now)
	if m.take(n, "dev1", now.Add(lanNonceTTL)) {
		t.Fatal("expired")
	}
	var all []string
	for range 10 {
		n, _ := m.issue("dev1", now)
		all = append(all, n)
	}
	if len(m.nonces) != lanNoncesMax || !m.take(all[9], "dev1", now) {
		t.Fatalf("at most %d open nonces per device: %d", lanNoncesMax, len(m.nonces))
	}
}

type lanTest struct {
	t     *testing.T
	h     *harness
	a     *apiClient
	id    string
	priv  *ecdsa.PrivateKey
	shift atomic.Int64
	mu    sync.Mutex
	snap  netSnap
	conn  lanConn
	sink  []string
}

// set: the PC's network and the phone's address; the connection arrives on
// eth0's own address (or local, when given; "-" = missing).
func (l *lanTest) set(snap netSnap, ip string, local ...string) {
	c := lanConn{IP: netip.MustParseAddr(ip), Local: netip.MustParseAddr("192.0.2.10")}
	if len(local) > 0 {
		c.Local = netip.Addr{}
		if local[0] != "-" {
			c.Local = netip.MustParseAddr(local[0])
		}
	}
	l.mu.Lock()
	l.snap, l.conn = snap, c
	l.mu.Unlock()
}

func (l *lanTest) post(path, idem string, body any) (int, map[string]any) {
	l.t.Helper()
	for {
		code, m, _ := l.a.do("POST", path, idem, body)
		if code != http.StatusTooManyRequests { // 10 writes/s per device
			return code, m
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (l *lanTest) hello(idem string) bool {
	l.t.Helper()
	code, m := l.post("/phone/hello", idem, map[string]string{"name": "Test Pixel", "app": "1.0"})
	if code != 200 {
		l.t.Fatalf("hello: %d %v", code, m)
	}
	v, ok := m["lanUnlock"].(bool)
	if !ok {
		l.t.Fatalf("hello has lanUnlock: %v", m)
	}
	return v
}

func (l *lanTest) passwordUnlock(idem string) {
	l.t.Helper()
	l.a.tok = ""
	code, m := l.post("/phone/unlock", idem, map[string]string{"password": phoneTestPassword})
	tok, _ := m["token"].(string)
	if code != 200 || tok == "" {
		l.t.Fatalf("password unlock: %d %v", code, m)
	}
	l.a.tok = tok
}

func (l *lanTest) challenge(idem string) (int, string, map[string]any) {
	l.t.Helper()
	code, m := l.post("/phone/unlock/challenge", idem, map[string]any{})
	n, _ := m["nonce"].(string)
	return code, n, m
}

func (l *lanTest) sign(nonce string) string {
	var fp string
	l.h.ui(func() { fp = l.h.d.phone().fp })
	sum := sha256.Sum256([]byte("quill-unlock-v1|" + l.id + "|" + nonce + "|" + fp))
	sig, err := ecdsa.SignASN1(rand.Reader, l.priv, sum[:])
	if err != nil {
		l.t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(sig)
}

func (l *lanTest) keyUnlock(idem, nonce, sig string) (int, map[string]any) {
	l.t.Helper()
	saved := l.a.tok
	l.a.tok = ""
	code, m := l.post("/phone/unlock", idem, map[string]string{"nonce": nonce, "sig": sig})
	l.a.tok = saved
	return code, m
}

func newLanTest(t *testing.T) *lanTest {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	l := &lanTest{t: t, h: newHarness(t)}
	h := l.h
	l.set(testSnap(testGwMAC), "192.0.2.50")
	oldSnap, oldConn := lanSnapshot, lanConnOf
	lanSnapshot = func() netSnap { l.mu.Lock(); defer l.mu.Unlock(); return l.snap }
	lanConnOf = func(*http.Request) lanConn { l.mu.Lock(); defer l.mu.Unlock(); return l.conn }
	t.Cleanup(func() { lanSnapshot, lanConnOf = oldSnap, oldConn })
	var link string
	h.ui(func() {
		h.d.pcLock.now = func() time.Time { return time.Now().Add(time.Duration(l.shift.Load())) }
		h.d.notifySink = func(sid, title, body string) { l.mu.Lock(); l.sink = append(l.sink, body); l.mu.Unlock() }
		h.d.app.Preferences().SetString(prefPCPassword, hashPCPassword(phoneTestPassword))
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
	})
	t.Cleanup(func() { h.ui(h.d.stopPhone) })
	u, _ := url.Parse(link)
	q := u.Query()
	l.id = q.Get("i")
	l.a = &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}}
	var err error
	if l.priv, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
		t.Fatal(err)
	}
	return l
}

func (l *lanTest) pubKey() string {
	der, _ := x509.MarshalPKIXPublicKey(&l.priv.PublicKey)
	return base64.StdEncoding.EncodeToString(der)
}

func (l *lanTest) state() lanDevice {
	var st lanDevice
	l.h.ui(func() { st = l.h.d.lanDevices()[l.id] })
	return st
}

func TestLanUnlockFlow(t *testing.T) {
	if !lanUnlockSupported {
		t.Skip("home-network unlock is Linux-only")
	}
	l := newLanTest(t)
	h := l.h
	if l.hello("hello-0000000000000001") {
		t.Fatal("hello: lanUnlock false before anything is set up")
	}

	// Key registration: locked device → gate; Password switch off → no
	// password session; then with a password-unlocked session it works.
	h.ui(func() {
		if err := h.d.setLanUnlock(l.id, true); err == nil {
			t.Fatal("the switch needs a password-unlocked connection to record the home network")
		}
	})
	if code, m := l.post("/phone/unlock/key", "key-00000000000000001", map[string]string{"publicKey": l.pubKey()}); code != 401 || errCode(m) != "locked" {
		t.Fatalf("key while locked: %d %v", code, m)
	}
	h.ui(func() { h.d.setDeviceLock(l.id, false) })
	if code, m := l.post("/phone/unlock/key", "key-00000000000000002", map[string]string{"publicKey": l.pubKey()}); code != 403 || errCode(m) != "password_session_required" {
		t.Fatalf("key without a password session: %d %v", code, m)
	}
	h.ui(func() { h.d.setDeviceLock(l.id, true) })
	l.passwordUnlock("pw-000000000000000001")
	ecKey, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	der384, _ := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	if code, m := l.post("/phone/unlock/key", "key-00000000000000003", map[string]string{"publicKey": base64.StdEncoding.EncodeToString(der384)}); code != 400 {
		t.Fatalf("P-384 key refused: %d %v", code, m)
	}
	if code, m := l.post("/phone/unlock/key", "key-00000000000000004", map[string]string{"publicKey": l.pubKey()}); code != 200 || m["ok"] != true || m["lanUnlock"] != false {
		t.Fatalf("key registration: %d %v", code, m)
	}

	// Switch off by default: no challenge, hello false.
	if st := l.state(); st.On || st.Key == "" || st.PwAt == 0 {
		t.Fatalf("switch off by default, key and password time kept: %+v", st)
	}
	if code, _, m := l.challenge("ch-000000000000000001"); code != 403 || errCode(m) != "lan_unlock_off" {
		t.Fatalf("challenge with the switch off: %d %v", code, m)
	}
	h.ui(func() {
		h.d.updatePhoneList()
		c := h.d.lanUnlockCheck(folders.Phone{ID: l.id, Kind: "phone"})
		if c.Checked || c.Disabled() {
			t.Fatal("Devices switch: shown, off, enabled")
		}
		c.SetChecked(true)
	})
	if st := l.state(); !st.On || st.Home == nil || st.Home.GwMAC != testGwMAC {
		t.Fatalf("switch on remembers the home network: %+v", st)
	}
	if !l.hello("hello-0000000000000002") {
		t.Fatal("hello: lanUnlock true at home")
	}

	// Valid proof → token, notice.
	code, nonce, m := l.challenge("ch-000000000000000002")
	if code != 200 || len(nonce) != 43 || m["expiresMs"] == nil {
		t.Fatalf("challenge: %d %v", code, m)
	}
	code, m = l.keyUnlock("ku-000000000000000001", nonce, l.sign(nonce))
	tok, _ := m["token"].(string)
	if code != 200 || tok == "" || m["method"] != "key" {
		t.Fatalf("key unlock: %d %v", code, m)
	}
	l.a.tok = tok
	if code, _, _ := l.a.do("GET", "/phone/sessions", "", nil); code != 200 {
		t.Fatalf("token works: %d", code)
	}
	l.mu.Lock()
	notice := strings.Join(l.sink, "\n")
	l.mu.Unlock()
	if !strings.Contains(notice, "Phone Test Pixel unlocked without a password from 192.0.2.50") {
		t.Fatalf("notice: %q", notice)
	}
	// A key-unlocked session can't register a key.
	if code, m := l.post("/phone/unlock/key", "key-00000000000000005", map[string]string{"publicKey": l.pubKey()}); code != 403 || errCode(m) != "password_session_required" {
		t.Fatalf("key from a key session: %d %v", code, m)
	}

	// Failures count toward the lock-out: replay, wrong sig, expired, other
	// device's nonce, unknown nonce.
	fails := 0
	expectFail := func(what, idem, nonce, sig, want string) {
		t.Helper()
		fails++
		code, m := l.keyUnlock(idem, nonce, sig)
		if fails >= pcMaxFails {
			want = "locked_out"
		}
		if code != 403 || errCode(m) != want {
			t.Fatalf("%s: %d %v", what, code, m)
		}
	}
	expectFail("replayed nonce", "ku-000000000000000002", nonce, l.sign(nonce), "bad_nonce")
	_, n2, _ := l.challenge("ch-000000000000000003")
	expectFail("wrong signature", "ku-000000000000000003", n2, l.sign("something-else"), "bad_signature")
	_, n3, _ := l.challenge("ch-000000000000000004")
	l.shift.Store(int64(lanNonceTTL + time.Second))
	expectFail("expired nonce", "ku-000000000000000004", n3, l.sign(n3), "bad_nonce")
	l.shift.Store(0)
	var other string
	h.ui(func() { other, _ = h.d.phone().api.lan.issue("0badf00d", time.Now()) })
	expectFail("other device's nonce", "ku-000000000000000005", other, l.sign(other), "bad_nonce")
	expectFail("unknown nonce", "ku-000000000000000006", "AAAA", l.sign("AAAA"), "bad_nonce")
	if code, _, m := l.challenge("ch-000000000000000005"); code != 403 || errCode(m) != "locked_out" {
		t.Fatalf("challenge while locked out: %d %v", code, m)
	}
	h.ui(func() {
		h.d.pcLock.mu.Lock()
		delete(h.d.pcLock.fails, l.id)
		h.d.pcLock.mu.Unlock()
	})

	// VPN source: no waiver.
	l.set(testSnap(testGwMAC), "100.100.1.7")
	if l.hello("hello-0000000000000003") {
		t.Fatal("over Tailscale: no waiver")
	}
	l.set(testSnap(testGwMAC), "192.0.2.50")

	// The connection must arrive on the home interface's own address.
	l.set(testSnap(testGwMAC), "192.0.2.50", "-")
	if l.hello("hello-0000000000000013") {
		t.Fatal("no local address: no waiver")
	}
	l.set(testSnap(testGwMAC), "192.0.2.50", "10.8.0.1")
	if l.hello("hello-0000000000000014") {
		t.Fatal("arrived on a VPN address: no waiver")
	}

	// Gateway MAC unreadable (ARP miss): no waiver, home kept.
	l.set(testSnap(""), "192.0.2.50")
	if l.hello("hello-0000000000000015") {
		t.Fatal("unreadable gateway MAC: no waiver")
	}
	if st := l.state(); !st.On || st.Home == nil {
		t.Fatalf("unreadable gateway MAC keeps the home: %+v", st)
	}
	l.set(testSnap(testGwMAC), "192.0.2.50")
	if !l.hello("hello-0000000000000016") {
		t.Fatal("gateway readable again: waiver back")
	}

	// Home network changed (gateway MAC readable and different): home
	// forgotten, switch off; a password unlock never re-learns it, only
	// turning the switch on again does.
	l.set(testSnap(testOtherMAC), "192.0.2.50")
	if l.hello("hello-0000000000000004") {
		t.Fatal("gateway MAC changed: no waiver")
	}
	if st := l.state(); st.On || st.Home != nil {
		t.Fatalf("network changed: switch off, home forgotten: %+v", st)
	}
	l.passwordUnlock("pw-000000000000000004")
	if l.hello("hello-0000000000000017") || l.state().Home != nil || l.state().On {
		t.Fatal("a password unlock never records the home network")
	}
	l.set(testSnap(testGwMAC), "192.0.2.50")
	l.passwordUnlock("pw-000000000000000002")
	if l.hello("hello-0000000000000005") || l.state().Home != nil {
		t.Fatal("still off after the old gateway comes back and a password unlock")
	}
	h.ui(func() {
		if err := h.d.setLanUnlock(l.id, true); err != nil {
			t.Fatal(err)
		}
	})
	if !l.hello("hello-0000000000000006") {
		t.Fatal("turning the switch on again records the home network")
	}

	// 30 days after the last password unlock: password again.
	l.shift.Store(int64(lanMaxAge + time.Hour))
	if l.hello("hello-0000000000000007") {
		t.Fatal("30 days: password again")
	}
	l.shift.Store(0)

	// A new device password ends the waiver.
	h.ui(func() { h.d.app.Preferences().SetString(prefPCPassword, hashPCPassword("another-password-2")) })
	if l.hello("hello-0000000000000008") {
		t.Fatal("changed device password: password again")
	}
	h.ui(func() { h.d.app.Preferences().SetString(prefPCPassword, hashPCPassword(phoneTestPassword)) })
	l.passwordUnlock("pw-000000000000000003")
	if !l.hello("hello-0000000000000009") {
		t.Fatal("back on after the password")
	}

	// PC devices never.
	h.ui(func() {
		list := h.d.phones()
		for i := range list {
			if list[i].ID == l.id {
				list[i].Kind = "pc"
			}
		}
		h.d.setPhones(list)
	})
	if l.hello("hello-0000000000000010") {
		t.Fatal("PC devices never")
	}
	if code, m := l.post("/phone/unlock/key", "key-00000000000000006", map[string]string{"publicKey": l.pubKey()}); code != 403 || errCode(m) != "not_a_phone" {
		t.Fatalf("PC key registration: %d %v", code, m)
	}
	h.ui(func() {
		if h.d.lanUnlockCheck(folders.Phone{ID: l.id, Kind: "pc"}) == nil {
			t.Fatal("check")
		}
		list := h.d.phones()
		for i := range list {
			if list[i].ID == l.id {
				list[i].Kind = "phone"
			}
		}
		h.d.setPhones(list)
	})
	if !l.hello("hello-0000000000000011") {
		t.Fatal("phone again")
	}

	// Revoke from the notice: switch off, key dropped, session ended.
	_, n4, _ := l.challenge("ch-000000000000000006")
	if code, m := l.keyUnlock("ku-000000000000000007", n4, l.sign(n4)); code != 200 {
		t.Fatalf("key unlock before revoke: %d %v", code, m)
	} else {
		l.a.tok = m["token"].(string)
	}
	h.ui(func() {
		if len(h.d.phone().api.lan.dialogs) != 1 {
			t.Fatal("notice dialog with Revoke shown")
		}
		h.d.revokeLanUnlock(l.id)
	})
	if st := l.state(); st.On || st.Key != "" {
		t.Fatalf("revoke: %+v", st)
	}
	if code, _, _ := l.a.do("GET", "/phone/sessions", "", nil); code != 401 {
		t.Fatalf("revoke ends the session: %d", code)
	}
	if l.hello("hello-0000000000000012") {
		t.Fatal("no waiver after revoke")
	}
	if code, _, m := l.challenge("ch-000000000000000007"); code != 403 || errCode(m) != "lan_unlock_off" {
		t.Fatalf("challenge after revoke: %d %v", code, m)
	}
}

// Concurrent failures never lower the count or lift a lock-out (password
// path and key-proof path).
func TestLanLockoutConcurrent(t *testing.T) {
	stored := hashPCPassword("correct horse")
	l := newPCLock()
	var wg sync.WaitGroup
	for range 3*pcMaxFails + 2 { // not a multiple: the old reset left n=2
		wg.Add(1)
		go func() { defer wg.Done(); l.unlock("dev", stored, "wrong") }()
	}
	wg.Wait()
	if locked, _ := l.lockedOut("dev"); !locked {
		t.Fatalf("password: still locked after %d concurrent failures: %+v", 3*pcMaxFails+2, l.fails["dev"])
	}
	if tok, code, _ := l.unlock("dev", stored, "correct horse"); tok != "" || code != "locked_out" {
		t.Fatalf("password during the lock-out: %q %q", tok, code)
	}

	k := newPCLock()
	for range 20 {
		var wg sync.WaitGroup
		for range 4*pcMaxFails + 1 {
			wg.Add(1)
			go func() { defer wg.Done(); k.failProof("phone", "bad_signature", "x") }()
		}
		wg.Wait()
		if locked, _ := k.lockedOut("phone"); !locked {
			t.Fatalf("key proof: lock-out lifted by concurrent failures: %+v", k.fails["phone"])
		}
		if k.grant("phone") != "" {
			t.Fatal("no token during the lock-out")
		}
	}
	// Once the lock-out time has passed, counting starts again.
	k.now = func() time.Time { return time.Now().Add(pcLockout + time.Minute) }
	if code, _ := k.failProof("phone", "bad_signature", "x"); code != "bad_signature" || k.grant("phone") == "" {
		t.Fatalf("after the lock-out: %q", code)
	}
}

func TestLanConnOf(t *testing.T) {
	r, _ := http.NewRequest("POST", "/phone/hello", nil)
	r.RemoteAddr = "[::ffff:192.0.2.50]:40000"
	if c := lanConnOf(r); c.IP != netip.MustParseAddr("192.0.2.50") || c.Local.IsValid() {
		t.Fatalf("no local address in the context: %+v", c)
	}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 43117}))
	if c := lanConnOf(r); c.Local != netip.MustParseAddr("192.0.2.10") {
		t.Fatalf("local address: %+v", c)
	}
}
