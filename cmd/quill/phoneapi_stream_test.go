package main

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

// A reply that was still streaming (empty) at one fetch must come back with
// its finished text in the next incremental fetch (epoch + since), as a
// phone that reconnects after a dropped connection asks for it.
func TestPhoneItemsFinishedStreamComesBack(t *testing.T) {
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

	var it *chat.Item
	h.ui(func() {
		it = h.d.sessions[editsID].conv.AddNotice("")
		it.Kind, it.Streaming = chat.Assistant, true
	})
	_, m, _ := a.do("GET", "/phone/session/"+editsID+"/items", "", nil)
	epoch, rev := int(m["epoch"].(float64)), int(m["rev"].(float64))
	find := func(m map[string]any) map[string]any {
		for _, x := range m["items"].([]any) {
			if xm := x.(map[string]any); int(xm["seq"].(float64)) == it.Seq {
				return xm
			}
		}
		return nil
	}
	if got := find(m); got == nil || got["text"] != "" || got["streaming"] != true {
		t.Fatalf("the streaming reply is in the window, still empty: %v", got)
	}
	since := func() map[string]any {
		_, m, _ := a.do("GET", "/phone/session/"+editsID+"/items?epoch="+strconv.Itoa(epoch)+"&since="+strconv.Itoa(rev), "", nil)
		epoch, rev = int(m["epoch"].(float64)), int(m["rev"].(float64))
		return m
	}
	h.ui(func() { it.Text = "Partial" })
	if got := find(since()); got == nil || got["text"] != "Partial" {
		t.Fatalf("streamed text arrives incrementally: %v", got)
	}
	// Finished with a different text of the same length and the streaming flag off.
	h.ui(func() { it.Text, it.Streaming = "Final!!", false })
	if got := find(since()); got == nil || got["text"] != "Final!!" || got["streaming"] != false {
		t.Fatalf("the finished reply comes back in the next incremental fetch: %v", got)
	}
	// Same length, same flags, different text (a final message that replaces the streamed one).
	h.ui(func() { it.Text = "Fixed!!" })
	if got := find(since()); got == nil || got["text"] != "Fixed!!" {
		t.Fatalf("a same-length text change is not missed: %v", got)
	}
}
