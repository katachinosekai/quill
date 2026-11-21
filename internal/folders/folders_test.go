package folders

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildAndAtomicWrite(t *testing.T) {
	f := Build("box", []string{"Active", "Zomboid"}, map[string]string{"session_A1": "Active"}, time.UnixMilli(1700000000000))
	dir := t.TempDir()
	if err := WriteAtomic(dir, f.JSON()); err != nil {
		t.Fatal(err)
	}
	var back File
	b, _ := os.ReadFile(filepath.Join(dir, FileName))
	if err := json.Unmarshal(b, &back); err != nil || back.Version != 1 || back.UpdatedMs != 1700000000000 || back.Sessions["session_A1"] != "Active" || back.Order[1] != "Zomboid" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if !f.SameContent(back) {
		t.Fatal("same content")
	}
	back.Sessions["session_B2"] = "Archive"
	if f.SameContent(back) {
		t.Fatal("changed content must differ")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".quill-folders-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestSessionID(t *testing.T) {
	for in, want := range map[string]string{
		"https://claude.ai/code/session_01AR9b92m2zB5xtDLD8JipgM": "session_01AR9b92m2zB5xtDLD8JipgM",
		"https://claude.ai/code?environment=":                     "",
		"https://claude.ai/code/session_x/../../evil":             "",
		"": "",
	} {
		if got := SessionID(in); got != want {
			t.Errorf("SessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServerKeyAndPinnedCert(t *testing.T) {
	dir := t.TempDir()
	cert, fp, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := LoadOrCreateCert(dir)
	if err != nil || fp2 != fp || len(fp) != 64 {
		t.Fatalf("cert must persist: %q %q %v", fp, fp2, err)
	}
	s, err := Listen(cert, 0, []Phone{{ID: "aaaa0001", KeyHash: HashKey("secret-key-123456")}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pinned := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if Fingerprint(cs.PeerCertificates[0].Raw) != fp {
				t.Fatal("fingerprint mismatch")
			}
			return nil
		},
	}}}
	get := func(path, key string) (int, string) {
		req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(s.Port)+path, nil)
		if key != "" {
			req.Header.Set("X-Quill-Key", key)
		}
		resp, err := pinned.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := get("/folders.json", "secret-key-123456"); code != http.StatusServiceUnavailable {
		t.Fatalf("no data yet: %d", code)
	}
	s.Set([]byte(`{"version":1}`))
	if code, _ := get("/folders.json", ""); code != http.StatusForbidden {
		t.Fatalf("missing key: %d", code)
	}
	if code, _ := get("/folders.json", "wrong"); code != http.StatusForbidden {
		t.Fatalf("wrong key: %d", code)
	}
	if code, _ := get("/other", "secret-key-123456"); code != http.StatusNotFound {
		t.Fatalf("other path: %d", code)
	}
	if code, body := get("/folders.json", "secret-key-123456"); code != 200 || body != `{"version":1}` {
		t.Fatalf("ok: %d %q", code, body)
	}
	s.SetPhones([]Phone{{ID: "aaaa0001", KeyHash: HashKey("secret-key-123456")}, {ID: "bbbb0002", KeyHash: HashKey("second-phone-key-7890")}})
	if code, _ := get("/folders.json", "second-phone-key-7890"); code != 200 {
		t.Fatalf("second phone's own key: %d", code)
	}
	s.SetPhones([]Phone{{ID: "bbbb0002", KeyHash: HashKey("second-phone-key-7890")}})
	if code, _ := get("/folders.json", "secret-key-123456"); code != http.StatusForbidden {
		t.Fatalf("revoked phone: %d", code)
	}
	if code, _ := get("/folders.json", "second-phone-key-7890"); code != 200 {
		t.Fatalf("the other phone keeps working: %d", code)
	}
}

func TestPairLinkAndGist(t *testing.T) {
	l := Pair{Hosts: []string{"192.168.1.5", "box.local"}, Port: 41234, Key: "k_k", Fingerprint: strings.Repeat("a", 64), File: FileName, Gist: "me/0123456789abcdef0123", Name: "box"}.Link()
	for _, want := range []string{"quillpair://pair?", "h=192.168.1.5%2Cbox.local", "p=41234", "k=k_k", "fp=" + strings.Repeat("a", 64), "g=me%2F0123456789abcdef0123", "n=box", "f=quill-folders.json"} {
		if !strings.Contains(l, want) {
			t.Errorf("link %q lacks %q", l, want)
		}
	}
	if strings.Contains(l, "i=") || strings.Contains(l, "v=") {
		t.Errorf("a link without a phone id is the old kind: %q", l)
	}
	if l := (Pair{Hosts: []string{"192.168.1.5"}, Port: 1, Key: "k", Fingerprint: "f", ID: "a1b2c3d4"}).Link(); !strings.Contains(l, "i=a1b2c3d4") || !strings.Contains(l, "v=2") {
		t.Errorf("per-phone link: %q", l)
	}
	if l := (Pair{Gist: "me/abc", Name: "box"}).Link(); strings.Contains(l, "k=") || !strings.Contains(l, "g=me%2Fabc") {
		t.Errorf("gist-only link: %q", l)
	}
	if g, err := GistFromURL("https://gist.github.com/me/0123abc\n"); err != nil || g != "me/0123abc" {
		t.Errorf("gist url: %q %v", g, err)
	}
	if _, err := GistFromURL("https://example.com/me/x"); err == nil {
		t.Error("non-gist URL accepted")
	}
}
