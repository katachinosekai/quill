package folders

import (
	"compress/gzip"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAcceptsGzip(t *testing.T) {
	for h, want := range map[string]bool{
		"":                     false,
		"gzip":                 true,
		"gzip, deflate":        true,
		"deflate, GZIP;q=0.5":  true,
		"gzip;q=0":             false,
		"gzip; q=0.0":          false,
		"br":                   false,
		"x-gzip":               false,
		"identity, gzip;q=1.0": true,
	} {
		r := httptest.NewRequest("GET", "/", nil)
		if h != "" {
			r.Header.Set("Accept-Encoding", h)
		}
		if got := acceptsGzip(r); got != want {
			t.Errorf("acceptsGzip(%q) = %v", h, got)
		}
	}
}

// slowServer: a server with one phone and a raw client (no transparent
// decompression, so the test sees what goes over the wire).
func slowServer(t *testing.T, h2 bool) (*Server, func(path string, gz bool) (*http.Response, string)) {
	cert, _, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := Listen(cert, 0, []Phone{{ID: "aaaa0001", KeyHash: HashKey("secret-key-123456")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableCompression: true, ForceAttemptHTTP2: h2}
	c := &http.Client{Timeout: 10 * time.Second, Transport: tr}
	get := func(path string, gz bool) (*http.Response, string) {
		req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(s.Port)+path, nil)
		req.Header.Set("X-Quill-Key", "secret-key-123456")
		if gz {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body io.Reader = resp.Body
		if resp.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			body = zr
		}
		b, _ := io.ReadAll(body)
		return resp, string(b)
	}
	return s, get
}

func TestGzipOnlyWhenAskedAndLarge(t *testing.T) {
	s, get := slowServer(t, false)
	big := `{"version":1,"folders":[` + strings.Repeat(`{"name":"a folder with a fairly long name","sessions":[]},`, 200) + `{}]}`
	s.Set([]byte(big))
	resp, body := get("/folders.json", true)
	if resp.Header.Get("Content-Encoding") != "gzip" || body != big {
		t.Fatalf("large reply with gzip asked: encoding %q, body ok %v", resp.Header.Get("Content-Encoding"), body == big)
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatal("gzipped reply says Vary: Accept-Encoding")
	}
	resp, body = get("/folders.json", false)
	if resp.Header.Get("Content-Encoding") != "" || body != big {
		t.Fatal("a client that doesn't ask gets the plain reply")
	}
	s.Set([]byte(`{"version":1}`))
	resp, body = get("/folders.json", true)
	if resp.Header.Get("Content-Encoding") != "" || body != `{"version":1}` {
		t.Fatalf("small reply stays plain: %q %q", resp.Header.Get("Content-Encoding"), body)
	}
	if resp.ContentLength != int64(len(body)) {
		t.Fatalf("small reply keeps its length: %d", resp.ContentLength)
	}
	// Errors are small and stay plain too.
	resp, _ = get("/phone/nothing", true)
	if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Content-Encoding") != "" {
		t.Fatalf("error reply: %d %q", resp.StatusCode, resp.Header.Get("Content-Encoding"))
	}
}

func TestGzipLeavesLiveEndpointsAlone(t *testing.T) {
	for p, live := range map[string]bool{"/events": true, "/phone/stream": true, "/phone/sessions": false, "/folders.json": false, "/phone/session/x/items": false} {
		if liveEndpoint(p) != live {
			t.Errorf("liveEndpoint(%q) = %v", p, !live)
		}
	}
}

func TestSlowLinkTimeouts(t *testing.T) {
	s, _ := slowServer(t, false)
	if s.srv.IdleTimeout != idleTimeout || idleTimeout < 60*time.Second {
		t.Fatalf("IdleTimeout %v: phones reuse connections between requests", s.srv.IdleTimeout)
	}
	for p, slow := range map[string]bool{"/phone/sessions": true, "/phone/session/abc/items": true, "/phone/stream": false, "/folders.json": false, "/phone/session/abc": false} {
		r := httptest.NewRequest("GET", p, nil)
		if slowReply(r) != slow {
			t.Errorf("slowReply(%q) = %v", p, !slow)
		}
	}
	if slowReply(httptest.NewRequest("POST", "/phone/sessions", nil)) {
		t.Error("only GETs are slow replies")
	}
}

func TestHTTP2Offered(t *testing.T) {
	s, get := slowServer(t, true)
	s.Set([]byte(`{"version":1}`))
	resp, body := get("/folders.json", true)
	if resp.ProtoMajor != 2 || body != `{"version":1}` {
		t.Fatalf("h2 client: %s %q", resp.Proto, body)
	}
	_, get1 := slowServer(t, false)
	if resp, _ := get1("/folders.json", false); resp.ProtoMajor != 1 {
		t.Fatalf("HTTP/1.1 clients keep working: %s", resp.Proto)
	}
}
