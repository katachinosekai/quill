package main

import (
	"crypto/tls"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestParseHave(t *testing.T) {
	m, err := parseHave("12:5:0a1b2c3d, 14:0:0")
	if err != nil || len(m) != 2 || m[12].n != 5 || m[12].crc != 0x0a1b2c3d || m[14].n != 0 {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"12", "12:5", "x:5:00", "12:-1:00", "12:5:zz", "12:5:1ffffffff"} {
		if _, err := parseHave(bad); err == nil {
			t.Errorf("parseHave(%q) accepted", bad)
		}
	}
	many := ""
	for i := 0; i <= maxHave; i++ {
		many += fmt.Sprintf("%d:1:0,", i)
	}
	if _, err := parseHave(many[:len(many)-1]); err == nil {
		t.Error("more than maxHave entries are refused")
	}
}

func haveFor(seq int, text string) string {
	runes := []rune(text)
	return fmt.Sprintf("%d:%d:%08x", seq, len(runes), crc32.ChecksumIEEE([]byte(text)))
}

// A streaming reply sends only its new end to a phone that opts in; others
// (and a phone whose copy differs) get the whole text.
func TestPhoneItemsStreamingDelta(t *testing.T) {
	t.Setenv("QUILL_PHONE_DIR", filepath.Join(t.TempDir(), "phone"))
	h := newHarness(t)
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		h.d.open(editsID)
	})
	defer h.ui(h.d.stopPhone)
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded })
	u, _ := url.Parse(link)
	q := u.Query()
	a := unlockedClient(h, &apiClient{t: t, base: "https://127.0.0.1:" + q.Get("p"), key: q.Get("k"),
		c: &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}})

	first := "Streaming wörld: the first part 🙂 "
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind = chat.Assistant
		it.Streaming = true
		it.Text = first
	})
	base := "/phone/session/" + editsID + "/items"
	_, m, _ := a.do("GET", base, "", nil)
	epoch, rev := int(m["epoch"].(float64)), int(m["rev"].(float64))
	h.ui(func() { it.Text = first + "and the new end." })
	since := base + "?epoch=" + strconv.Itoa(epoch) + "&since=" + strconv.Itoa(rev)
	find := func(m map[string]any) map[string]any {
		for _, x := range m["items"].([]any) {
			if xm := x.(map[string]any); int(xm["seq"].(float64)) == it.Seq {
				return xm
			}
		}
		t.Fatalf("the streaming item is not in %v", m["items"])
		return nil
	}

	_, m, _ = a.do("GET", since+"&delta=1&have="+haveFor(it.Seq, first), "", nil)
	got := find(m)
	if got["textFrom"] != float64(len([]rune(first))) || got["text"] != "and the new end." || got["textLen"] != float64(len([]rune(it.Text))) {
		t.Fatalf("delta: %v", got)
	}
	// Without delta=1: the whole text, as before.
	_, m, _ = a.do("GET", since, "", nil)
	if got = find(m); got["text"] != it.Text || got["textFrom"] != nil {
		t.Fatalf("old clients get the whole text: %v", got)
	}
	// A copy that isn't a prefix (wrong crc): the whole text, no textFrom.
	_, m, _ = a.do("GET", since+"&delta=1&have="+haveFor(it.Seq, "Something else entirely here, same length!!"), "", nil)
	if got = find(m); got["text"] != it.Text || got["textFrom"] != nil {
		t.Fatalf("a mismatched copy gets the whole text: %v", got)
	}
	// Longer than the text: whole text.
	_, m, _ = a.do("GET", since+"&delta=1&have="+haveFor(it.Seq, it.Text+" more"), "", nil)
	if got = find(m); got["text"] != it.Text || got["textFrom"] != nil {
		t.Fatalf("a longer copy gets the whole text: %v", got)
	}
	// The full window (no since) never sends deltas.
	_, m, _ = a.do("GET", base+"?delta=1&have="+haveFor(it.Seq, first), "", nil)
	if got = find(m); got["text"] != it.Text || got["textFrom"] != nil {
		t.Fatalf("the window sends whole items: %v", got)
	}
	if code, m, _ := a.do("GET", since+"&delta=1&have=nonsense", "", nil); code != 400 || errCode(m) != "bad_request" {
		t.Fatalf("bad have: %d %v", code, m)
	}
}
