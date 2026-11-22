package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestPhoneSettings(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		if h.d.buildChk == nil || h.d.coordChk == nil {
			t.Fatal("Settings → General checkboxes are kept on the desk")
		}
	})
	defer h.ui(h.d.stopPhone)
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})
	n := 0
	key := func() string {
		n++
		time.Sleep(110 * time.Millisecond)
		return "settings-key-" + strconv.Itoa(1000000+n)
	}
	want := func(what string, got any, build, coord bool) {
		t.Helper()
		m, _ := got.(map[string]any)
		if m == nil || m["buildGate"] != build || m["coordinator"] != coord || len(m) != 2 {
			t.Fatalf("%s: settings %v, want buildGate %v coordinator %v", what, got, build, coord)
		}
	}

	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	want("GET /phone/sessions", m["settings"], true, true)
	cursor := int(m["cursor"].(float64))
	_, st, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(cursor)+"&wait=0", "", nil)
	want("every stream reply", st["settings"], true, true)

	if code, m, _ := a.do("POST", "/phone/settings", key(), map[string]bool{"buildGate": false}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("setting needs phone control: %d %v", code, m)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })

	for name, body := range map[string]any{
		"empty":       map[string]any{},
		"unknown key": map[string]any{"buildGate": true, "theme": true},
		"non-bool":    map[string]any{"coordinator": "no"},
		"null":        map[string]any{"coordinator": nil},
		"not object":  []bool{true},
	} {
		if code, m, _ := a.do("POST", "/phone/settings", key(), body); code != 400 || errCode(m) != "bad_request" {
			t.Fatalf("%s body: %d %v", name, code, m)
		}
	}

	code, m, _ := a.do("POST", "/phone/settings", key(), map[string]bool{"buildGate": false})
	if code != 200 || m["v"] != float64(1) {
		t.Fatalf("set buildGate: %d %v", code, m)
	}
	want("POST reply", m["settings"], false, true)
	h.ui(func() {
		if h.d.buildGateOn() || h.d.buildChk.Checked || !h.d.coordChk.Checked {
			t.Fatal("a phone change reaches the pref and the Settings → General checkboxes")
		}
	})
	code, m, _ = a.do("POST", "/phone/settings", key(), map[string]bool{"buildGate": true, "coordinator": false})
	if code != 200 {
		t.Fatalf("set both: %d %v", code, m)
	}
	want("POST both", m["settings"], true, false)
	h.ui(func() {
		if !h.d.buildChk.Checked || h.d.coordChk.Checked || h.d.app.Preferences().BoolWithFallback(prefCoordinator, true) {
			t.Fatal("both checkboxes follow the phone")
		}
	})

	_, m, _ = a.do("GET", "/phone/sessions", "", nil)
	want("GET after POST", m["settings"], true, false)
	cursor = int(m["cursor"].(float64))
	done := make(chan map[string]any, 1)
	go func() {
		_, m, _ := a.do("GET", "/phone/stream?cursor="+strconv.Itoa(cursor)+"&wait=10", "", nil)
		done <- m
	}()
	time.Sleep(300 * time.Millisecond)
	h.ui(func() { h.d.coordChk.SetChecked(true) })
	select {
	case st = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("stream did not answer")
	}
	found := false
	for _, x := range st["changes"].([]any) {
		if c := x.(map[string]any); c["type"] == "settings" {
			want("stream change", c["settings"], true, true)
			found = true
		}
	}
	if !found {
		t.Fatalf("a PC toggle is a settings stream change: %v", st)
	}
	want("stream reply after the toggle", st["settings"], true, true)
}
