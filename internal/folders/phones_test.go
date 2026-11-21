package folders

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRevokeEndsLongPoll(t *testing.T) {
	s, get := eventServer(t)
	seen := make(chan string, 4)
	s.SetSeen(func(id string) { seen <- id })
	done := make(chan int, 1)
	start := time.Now()
	go func() {
		code, _ := get("/events?since=999&wait=20", "secret-key-123456")
		done <- code
	}()
	if id := <-seen; id != "aaaa0001" {
		t.Fatalf("seen reports the phone id: %q", id)
	}
	time.Sleep(200 * time.Millisecond)
	s.SetPhones(nil)
	select {
	case code := <-done:
		if code != http.StatusForbidden || time.Since(start) > 5*time.Second {
			t.Fatalf("revoked phone's long poll: %d after %v", code, time.Since(start))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("revoke did not end the long poll")
	}
}

func TestPhonesStoreHashesOnly(t *testing.T) {
	p, key := NewPhone("Pixel", time.Unix(100, 0))
	if len(key) != 43 || len(p.ID) != 8 || p.KeyHash != HashKey(key) || !p.Control || p.LastSeen != 0 {
		t.Fatalf("new phone: %+v %q", p, key)
	}
	js := PhonesJSON([]Phone{p})
	if strings.Contains(js, key) {
		t.Fatal("the key itself is never stored")
	}
	back := ParsePhones(js)
	if len(back) != 1 || back[0] != p {
		t.Fatalf("round trip: %+v", back)
	}
	if matchPhone(back, key) != p.ID || matchPhone(back, "wrong") != "" || matchPhone(back, "") != "" {
		t.Fatal("match by key")
	}
	if l := LegacyPhone("old", time.Unix(5, 0)); l.Name != LegacyPhoneName || l.KeyHash != HashKey("old") || l.LastSeen == 0 {
		t.Fatalf("legacy entry counts as used so pairing cleanup keeps it: %+v", l)
	}
}

func TestNeedsUnlock(t *testing.T) {
	on, off := true, false
	for _, c := range []struct {
		p    Phone
		want bool
	}{{Phone{Kind: "pc"}, true}, {Phone{Kind: "phone"}, true}, {Phone{}, true}, {Phone{Kind: "phone", Lock: &on}, true}, {Phone{Kind: "phone", Lock: &off}, false}, {Phone{Kind: "pc", Lock: &off}, false}} {
		if c.p.NeedsUnlock() != c.want {
			t.Errorf("%+v: want %v", c.p, c.want)
		}
	}
}
