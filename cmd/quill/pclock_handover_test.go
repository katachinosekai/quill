//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestPCLockHandover(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	idle := 15 * time.Minute
	old := newPCLock()
	old.now = clock
	tokA := old.grant("devA")
	tokOld := old.grant("devOld")
	now = now.Add(10 * time.Minute)
	if !old.valid("devA", tokA, idle, false) {
		t.Fatal("devA valid before the reload")
	}
	now = now.Add(6 * time.Minute) // devOld idle 16 min; devA's last stays at 0
	tokA = old.grant("devA")       // fresh token at 16 min
	now = now.Add(5 * time.Minute) // devA idle 5 min
	for range pcMaxFails {
		old.failProof("devB", "bad_signature", "x")
	}
	old.failProof("devC", "bad_signature", "x")
	b, err := json.Marshal(old.handOver(idle))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > unlocksMax {
		t.Fatalf("handover too big: %d", len(b))
	}

	now = now.Add(time.Minute) // the switch itself takes time
	nl := newPCLock()
	nl.now = clock
	var h handedLock
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Tokens["devOld"]; ok {
		t.Fatal("an expired token is not handed over")
	}
	nl.takeOver(h)
	if nl.valid("devOld", tokOld, idle, false) {
		t.Fatal("expired token stays expired")
	}
	if got := nl.left("devA", idle); got != 9*time.Minute {
		t.Fatalf("idle clock keeps running across the reload: left %v, want 9m", got)
	}
	if !nl.valid("devA", tokA, idle, false) || nl.valid("devA", tokOld, idle, false) {
		t.Fatal("devA's token works after the reload, no other")
	}
	if locked, _ := nl.lockedOut("devB"); !locked {
		t.Fatal("a lock-out survives the reload")
	}
	if code, _ := nl.failProof("devC", "bad_signature", "x"); code != "bad_signature" || nl.fails["devC"].n != 2 {
		t.Fatalf("fail count kept: %+v", nl.fails["devC"])
	}
	now = now.Add(9*time.Minute + time.Second)
	if nl.valid("devA", tokA, idle, true) {
		t.Fatal("no free extension: devA locks 15 min after its last activity")
	}

	// A last activity in the future (clock set back) counts as now.
	nl2 := newPCLock()
	nl2.now = clock
	nl2.takeOver(handedLock{Tokens: map[string]handedToken{"devF": {Tok: tokA, Last: now.Add(time.Hour).UnixMilli()}}})
	if got := nl2.left("devF", idle); got != idle {
		t.Fatalf("future last clamped to now: left %v", got)
	}
}

func TestUnlocksPipe(t *testing.T) {
	data := []byte(`{"tokens":{"devA":{"tok":"x","last":1}}}`)
	kv, undo, ok := passUnlocks(data)
	if !ok || undo == nil {
		t.Fatal("passUnlocks")
	}
	// Exec keeps the fd: it must not be close-on-exec.
	v := kv[len(unlocksEnv)+1:]
	fd, _ := strconv.Atoi(v)
	flags, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if e != 0 || flags&syscall.FD_CLOEXEC != 0 {
		t.Fatalf("fd %d inheritable: flags %d err %v", fd, flags, e)
	}
	syscall.ForkLock.RUnlock() // the "exec" happened
	t.Setenv(unlocksEnv, v)
	if got := takeUnlocks(); string(got) != string(data) {
		t.Fatalf("takeUnlocks: %q", got)
	}
	if os.Getenv(unlocksEnv) != "" {
		t.Fatal("env entry removed")
	}
	if takeUnlocks() != nil {
		t.Fatal("read once")
	}
	if _, _, ok := passUnlocks(make([]byte, unlocksMax+1)); ok {
		t.Fatal("too big: nothing passed")
	}

	// A non-pipe fd named by a stray env entry is left alone.
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	t.Setenv(unlocksEnv, strconv.Itoa(int(f.Fd())))
	if takeUnlocks() != nil {
		t.Fatal("a regular file is not read")
	}
	if _, err := f.Write([]byte("still open")); err != nil {
		t.Fatal("the file stays open:", err)
	}
}

// TestUnlockTouchAndReload: the touch path, idleLeftSec, and a simulated
// live reload of the phone server (same token, idle clock, password session).
func TestUnlockTouchAndReload(t *testing.T) {
	l := newLanTest(t)
	h := l.h
	l.hello("hello-0000000000000001")
	l.a.tok = ""
	code, m := l.post("/phone/unlock", "pw-000000000000000001", map[string]string{"password": phoneTestPassword})
	if code != 200 || !fullLeft(m) || m["idleMin"] != float64(15) {
		t.Fatalf("unlock reply has idleLeftSec: %d %v", code, m)
	}
	l.a.tok, _ = m["token"].(string)
	tok := l.a.tok

	minutes := func(n int) { l.shift.Add(int64(time.Duration(n) * time.Minute)) }
	touch := func(idem string) (int, map[string]any) {
		return l.post("/phone/unlock/touch", idem, map[string]any{})
	}
	minutes(10)
	if code, _, _ := l.a.do("GET", "/phone/sessions", "", nil); code != 200 {
		t.Fatalf("read: %d", code)
	}
	if code, m := touch("touch-00000000000001"); code != 200 || !fullLeft(m) {
		t.Fatalf("a read doesn't count, the touch does: %d %v", code, m)
	}
	minutes(10)
	if code, _, _ := l.a.do("GET", "/phone/sessions", "", nil); code != 200 {
		t.Fatalf("read within the idle time: %d", code)
	}
	if code, m := touch("touch-00000000000002"); code != 200 || !fullLeft(m) {
		t.Fatalf("touch refreshes last: %d %v", code, m)
	}

	// Live reload: hand over, restart the phone server with a fresh pcLock.
	minutes(4)
	var b []byte
	h.ui(func() {
		b, _ = json.Marshal(h.d.pcLock.handOverWith(h.d.phone().api.lan, h.d.pcIdle()))
		now := h.d.pcLock.now
		h.d.stopPhone()
		h.d.pcLock = newPCLock()
		h.d.pcLock.now = now
		handedUnlocks = b
		h.d.takeOverUnlocks()
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
	})
	minutes(1)
	if code, m := touch("touch-00000000000003"); code != 200 || !fullLeft(m) {
		t.Fatalf("token valid after the reload: %d %v", code, m)
	}
	// The password session survives too: key registration needs it.
	if code, m := l.post("/phone/unlock/key", "key-00000000000000001", map[string]string{"publicKey": l.pubKey()}); code != 200 {
		t.Fatalf("key registration after the reload: %d %v", code, m)
	}
	minutes(14)
	var left time.Duration
	h.ui(func() { left = h.d.pcLock.left(l.id, h.d.pcIdle()) })
	if left > time.Minute || left < 50*time.Second {
		t.Fatalf("idle clock not reset by the reload: left %v", left)
	}

	// Expired: refused, and the token is gone for good (no revive).
	minutes(2)
	if code, m := touch("touch-00000000000004"); code != 401 || errCode(m) != "locked" {
		t.Fatalf("touch with an expired token: %d %v", code, m)
	}
	minutes(-10)
	if code, m := touch("touch-00000000000005"); code != 401 {
		t.Fatalf("an expired token never comes back: %d %v", code, m)
	}
	if l.a.tok != tok {
		t.Fatal("same token throughout")
	}
}

// fullLeft: idleLeftSec is the whole idle time (just refreshed; the test
// clock is real time plus a shift, so allow a few seconds).
func fullLeft(m map[string]any) bool {
	v, _ := m["idleLeftSec"].(float64)
	return v <= 15*60 && v >= 15*60-5
}
