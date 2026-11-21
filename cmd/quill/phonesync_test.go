package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/folders"
)

func TestPhoneSyncFileAndServer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(dir, "phone"))
	h := newHarness(t)
	out := filepath.Join(dir, "sync")
	os.MkdirAll(out, 0o700)
	readFile := func() folders.File {
		var f folders.File
		b, _ := os.ReadFile(filepath.Join(out, folders.FileName))
		_ = json.Unmarshal(b, &f)
		return f
	}
	var want string
	h.ui(func() {
		h.d.app.Preferences().SetString("phoneDir", out)
		h.d.rememberRC(editsID, "https://claude.ai/code/session_01EditsAAA")
		h.d.rememberRC(editsID, "https://claude.ai/code?environment=")
	})
	found := func() bool {
		ok := false
		h.ui(func() {
			h.d.rebuildRows()
			for _, g := range h.d.groups {
				for _, id := range g.ids {
					if id == editsID {
						want, ok = g.title, true
					}
				}
			}
		})
		return ok
	}
	for deadline := time.Now().Add(30 * time.Second); !found(); {
		if time.Now().After(deadline) {
			h.ui(func() {
				_, inInfos := h.d.infos[editsID]
				t.Fatalf("edits session is in no folder: inInfos=%v order=%d place=%q", inInfos, len(h.d.order), h.d.placement(editsID))
			})
		}
		h.d.refresh()
		time.Sleep(50 * time.Millisecond)
	}
	h.eventually("folders file", func() bool { return readFile().Sessions["session_01EditsAAA"] == want })
	f := readFile()
	if f.Version != 1 || len(f.Order) == 0 || len(f.Sessions) != 1 || f.UpdatedMs == 0 {
		t.Fatalf("file: %+v", f)
	}
	before := f.UpdatedMs
	h.ui(func() { h.d.setPlacement(editsID, placeArchive) })
	h.eventually("moved", func() bool { return readFile().Sessions["session_01EditsAAA"] == "Archive" })
	if readFile().UpdatedMs < before {
		t.Fatal("updatedMs must not go backwards")
	}

	var link string
	h.ui(func() {
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
	})
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "quillpair" || u.Host != "pair" {
		t.Fatalf("pair link %q: %v", link, err)
	}
	q := u.Query()
	fp, key := q.Get("fp"), q.Get("k")
	port, _ := strconv.Atoi(q.Get("p"))
	if len(fp) != 64 || key == "" || port == 0 {
		t.Fatalf("pair link fields: %q", link)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if folders.Fingerprint(cs.PeerCertificates[0].Raw) != fp {
				t.Error("served certificate does not match the pairing fingerprint")
			}
			return nil
		},
	}}}
	req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(port)+"/folders.json", nil)
	req.Header.Set("X-Quill-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var served folders.File
	if resp.StatusCode != 200 || json.Unmarshal(body, &served) != nil || served.Sessions["session_01EditsAAA"] != "Archive" {
		t.Fatalf("served %d %s", resp.StatusCode, body)
	}

	if strings.Contains(link, "g=") {
		t.Fatal("pair links no longer carry a gist")
	}
	h.ui(h.d.stopPhone)
}

func TestPerPhoneKeys(t *testing.T) {
	h := newHarness(t)
	prefs := h.d.app.Preferences()
	var srv *folders.Server
	var first, second url.Values
	h.ui(func() {
		prefs.SetInt("phonePort", 0)
		prefs.SetString("phoneKey", "old-shared-key-0123456789")
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		srv = h.d.phone().server
		if prefs.String("phoneKey") != "" || len(h.d.phones()) != 1 || h.d.phones()[0].Name != folders.LegacyPhoneName {
			t.Fatalf("the shared key becomes one legacy entry: %+v", h.d.phones())
		}
		u, _ := url.Parse(h.d.pairLink())
		first = u.Query()
		h.d.endPairing()
		if len(h.d.phones()) != 1 {
			t.Fatal("a pairing nobody used is dropped when the dialog closes")
		}
		u, _ = url.Parse(h.d.pairLink())
		second = u.Query()
	})
	if second.Get("v") != "2" || len(second.Get("i")) != 8 || second.Get("k") == first.Get("k") {
		t.Fatalf("each pairing gets its own id and key: %v", second)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	get := func(key string) int {
		req, _ := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(srv.Port)+"/usage.json", nil)
		req.Header.Set("X-Quill-Key", key)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if get(first.Get("k")) != http.StatusForbidden {
		t.Fatal("the dropped pairing's key is refused")
	}
	if get("old-shared-key-0123456789") == http.StatusForbidden || get(second.Get("k")) == http.StatusForbidden {
		t.Fatal("the legacy phone and the new phone both get in")
	}
	h.eventually("seen", func() bool {
		ok := false
		for _, p := range h.d.phones() {
			ok = ok || p.ID == second.Get("i") && p.LastSeen > 0
		}
		return ok
	})
	h.ui(func() {
		h.d.endPairing()
		if len(h.d.phones()) != 2 {
			t.Fatal("a pairing that connected is kept")
		}
		h.d.revokePhone(second.Get("i"))
	})
	if get(second.Get("k")) != http.StatusForbidden || get("old-shared-key-0123456789") == http.StatusForbidden {
		t.Fatal("revoking one phone leaves the other")
	}
	h.ui(h.d.stopPhone)
}
