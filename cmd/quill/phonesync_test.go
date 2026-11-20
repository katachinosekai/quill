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

func TestPhoneSyncFileServerAndGist(t *testing.T) {
	dir := t.TempDir()
	ghLog := filepath.Join(dir, "gh.log")
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\necho \"$@\" >> " + ghLog + "\nif [ \"$2\" = create ]; then echo https://gist.github.com/me/0123456789abcdef0123; fi\n"
	if err := os.WriteFile(gh, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_GH", gh)
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
	h.eventually("edits session in a folder", func() bool {
		h.d.rebuildRows()
		for _, g := range h.d.groups {
			for _, id := range g.ids {
				if id == editsID {
					want = g.title
				}
			}
		}
		return want != ""
	})
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

	h.ui(func() { h.d.toggleGist(true) })
	h.eventually("gist created", func() bool {
		return h.d.app.Preferences().String("phoneGist") == "me/0123456789abcdef0123"
	})
	h.ui(func() {
		if !strings.Contains(h.d.pairLink(), "g=me%2F0123456789abcdef0123") {
			t.Fatal("pair link should carry the gist")
		}
		h.d.setPlacement(editsID, placeInactive)
	})
	h.eventually("gist edited", func() bool {
		b, _ := os.ReadFile(ghLog)
		return strings.Contains(string(b), "gist edit 0123456789abcdef0123 --filename "+folders.FileName)
	})
	h.ui(h.d.stopPhone)
}
