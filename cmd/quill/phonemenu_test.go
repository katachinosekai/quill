package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPhoneSessionMenu(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link, id, other string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		id = h.d.createSession(h.proj, "default", "default").id
		other = h.d.createSession(h.proj, "default", "default").id
		h.d.setProject(other, "Work")
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
		return "menu-key-" + strconv.Itoa(1000000+n)
	}
	base := "/phone/session/" + id
	find := func(want string) map[string]any {
		_, m, _ := a.do("GET", "/phone/sessions", "", nil)
		list, _ := m["sessions"].([]any)
		for _, x := range list {
			if s, _ := x.(map[string]any); s["id"] == want {
				return s
			}
		}
		return nil
	}

	if code, m, _ := a.do("POST", base+"/rename", key(), map[string]string{"title": "x"}); code != 403 || errCode(m) != "control_off" {
		t.Fatalf("menu writes need the control setting: %d %v", code, m)
	}
	h.ui(func() { h.d.app.Preferences().SetBool("phoneControl", true) })

	_, m, _ := a.do("GET", "/phone/sessions", "", nil)
	if acts, _ := m["actions"].([]any); len(acts) != len(menuActions) {
		t.Fatalf("sessions lists the menu actions: %v", m["actions"])
	}
	if pl, _ := m["projects"].([]any); len(pl) != 2 || m["places"] != nil {
		t.Fatalf("sessions lists the projects, no folders: %v %v", m["projects"], m["places"])
	}
	if code, _, _ := a.do("POST", base+"/place", key(), map[string]string{"place": "inactive"}); code != 404 {
		t.Fatal("there are no folders to place sessions in any more")
	}
	if code, m, _ := a.do("POST", "/phone/handoffs/nope/resume", key(), map[string]any{}); code != 409 && code != 404 {
		t.Fatalf("handoff actions without kmem or for unknown names fail: %d %v", code, m)
	}
	if code, _, _ := a.do("POST", base+"/kmem", key(), map[string]string{"name": "x"}); code != 404 {
		t.Fatal("there is no separate kmem action: rename covers it")
	}

	// rename and restore
	if code, m, _ := a.do("POST", base+"/rename", key(), map[string]string{}); code != 400 || errCode(m) != "bad_request" {
		t.Fatalf("rename needs a title: %d %v", code, m)
	}
	if code, _, _ := a.do("POST", base+"/rename", key(), map[string]string{"title": strings.Repeat("x", 201)}); code != 400 {
		t.Fatal("titles are capped at 200 characters")
	}
	code, m, _ := a.do("POST", base+"/rename", key(), map[string]string{"title": "  Phone name "})
	if s, _ := m["session"].(map[string]any); code != 200 || s["title"] != "Phone name" || s["renamed"] != true {
		t.Fatalf("rename: %d %v", code, m)
	}
	code, m, _ = a.do("POST", base+"/rename", key(), map[string]string{"title": ""})
	if s, _ := m["session"].(map[string]any); code != 200 || s["renamed"] != nil || s["title"] == "Phone name" {
		t.Fatalf("an empty title restores the original name: %d %v", code, m)
	}

	// project
	if code, _, _ := a.do("POST", base+"/project", key(), map[string]string{}); code != 400 {
		t.Fatal("project is required")
	}
	if code, m, _ := a.do("POST", base+"/project", key(), map[string]string{"project": "Work"}); code != 200 || m["session"].(map[string]any)["project"] != "Work" {
		t.Fatalf("move to a project: %d %v", code, m)
	}
	if s := find(id); s == nil || s["project"] != "Work" || s["group"] != "Work" {
		t.Fatalf("the session shows its project (group too, for older phones): %v", s)
	}

	// resume
	h.ui(func() { h.d.sessions[id].takeover = true })
	if code, m, _ := a.do("POST", base+"/resume", key(), map[string]any{}); code != 409 || errCode(m) != "blocked" {
		t.Fatalf("resume during a takeover is blocked: %d %v", code, m)
	}
	h.ui(func() { h.d.sessions[id].takeover = false })
	if code, m, _ := a.do("POST", base+"/resume", key(), map[string]any{}); code != 200 || m["session"] == nil {
		t.Fatalf("resume: %d %v", code, m)
	}
	running := false
	h.ui(func() { running = h.d.sessions[id].running() })
	if !running {
		t.Fatal("resume starts the session's Claude process")
	}

	// end
	if code, _, _ := a.do("POST", base+"/end", key(), map[string]any{}); code != 200 {
		t.Fatal("end")
	}
	h.ui(func() {
		for _, x := range h.d.tabs {
			if x == id {
				t.Fatal("end closes the session's tab")
			}
		}
	})

	// delete
	if code, m, _ := a.do("POST", "/phone/session/"+other+"/delete", key(), map[string]any{}); code != 200 || m["removed"] != other {
		t.Fatalf("delete: %d %v", code, m)
	}
	h.ui(func() {
		if h.d.sessions[other] != nil {
			t.Fatal("delete removes the session")
		}
	})
	if code, _, _ := a.do("POST", "/phone/session/"+other+"/rename", key(), map[string]string{"title": "x"}); code != 404 {
		t.Fatal("a deleted session is not found")
	}
}
