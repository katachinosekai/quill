package folders

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func eventServer(t *testing.T) (*Server, func(path, key string) (int, string)) {
	cert, _, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Listen(cert, 0, "secret-key-123456")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	get := func(path, key string) (int, string) {
		req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(s.Port)+path, nil)
		if key != "" {
			req.Header.Set("X-Quill-Key", key)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Error(err)
			return 0, ""
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	return s, get
}

func decode(t *testing.T, body string) EventPage {
	var p EventPage
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("%v: %q", err, body)
	}
	return p
}

func TestEventsCursorAndRing(t *testing.T) {
	s, get := eventServer(t)
	const key = "secret-key-123456"
	if code, _ := get("/events", ""); code != http.StatusForbidden {
		t.Fatalf("key required: %d", code)
	}
	code, body := get("/events?since=0", key)
	p := decode(t, body)
	if code != 200 || p.Version != 1 || p.Boot == "" || p.Next != 1 || p.Events == nil || len(p.Events) != 0 {
		t.Fatalf("empty page: %d %q", code, body)
	}
	s.Emit(Event{Type: "approval", Quill: "q1", Title: "Launcher", Folder: "Active", RC: "session_01A"})
	s.Emit(Event{Type: "bogus", Quill: "q1"})
	s.Emit(Event{Type: "finished", Quill: "q1", Title: "Launcher", Folder: "Active"})
	p = decode(t, func() string { _, b := get("/events?since=0", key); return b }())
	if len(p.Events) != 2 || p.Events[0].ID != 1 || p.Events[1].ID != 2 || p.Next != 3 || p.Events[0].Time == 0 {
		t.Fatalf("two events, ids 1 and 2, unknown types dropped: %+v", p)
	}
	p = decode(t, func() string { _, b := get("/events?since=2", key); return b }())
	if len(p.Events) != 1 || p.Events[0].Type != "finished" {
		t.Fatalf("since is inclusive: %+v", p)
	}
	p = decode(t, func() string { _, b := get("/events?since=3", key); return b }())
	if len(p.Events) != 0 || p.Next != 3 {
		t.Fatalf("since >= next is empty: %+v", p)
	}
	for i := 0; i < EventRing+50; i++ {
		s.Emit(Event{Type: "finished", Quill: "q"})
	}
	p = decode(t, func() string { _, b := get("/events?since=0", key); return b }())
	if len(p.Events) != EventRing || p.Events[0].ID != p.Next-EventRing {
		t.Fatalf("ring keeps the last %d: %d first=%d next=%d", EventRing, len(p.Events), p.Events[0].ID, p.Next)
	}
	var raw map[string]any
	_, body = get("/events?since="+strconv.FormatInt(p.Next-1, 10), key)
	json.Unmarshal([]byte(body), &raw)
	ev := raw["events"].([]any)[0].(map[string]any)
	for k := range ev {
		if !strings.Contains(" id time type rc quill title folder ", " "+k+" ") {
			t.Fatalf("no content fields: %q", k)
		}
	}
	s.SetEvents(false)
	if code, _ := get("/events", key); code != http.StatusNotFound {
		t.Fatalf("off returns 404: %d", code)
	}
}

func TestEventsLongPoll(t *testing.T) {
	s, get := eventServer(t)
	const key = "secret-key-123456"
	start := time.Now()
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.Emit(Event{Type: "plan", Quill: "q1"})
	}()
	_, body := get("/events?since=1&wait=20", key)
	if p := decode(t, body); len(p.Events) != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("wait returns early on an event: %+v after %v", p, time.Since(start))
	}
	start = time.Now()
	code, body := get("/events?since=2&wait=1", key)
	if p := decode(t, body); code != 200 || len(p.Events) != 0 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("wait times out cleanly: %d %+v after %v", code, p, time.Since(start))
	}
	start = time.Now()
	_, body = get("/events?since=2&wait=12", key)
	if time.Since(start) < 11*time.Second || len(decode(t, body).Events) != 0 {
		t.Fatalf("long waits outlive the 10 s server write timeout: %v", time.Since(start))
	}
	var wg sync.WaitGroup
	codes := make(chan int, EventWaiters+1)
	for i := 0; i < EventWaiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _ := get("/events?since=2&wait=3", key)
			codes <- c
		}()
	}
	time.Sleep(500 * time.Millisecond)
	if c, _ := get("/events?since=2&wait=3", key); c != http.StatusTooManyRequests {
		t.Fatalf("a fifth waiter gets 429: %d", c)
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Fatalf("waiters finish with 200: %d", c)
		}
	}
}
