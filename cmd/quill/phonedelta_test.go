package main

import (
	"crypto/tls"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
)

func TestParseHave(t *testing.T) {
	m, err := parseHave("12:5:0a1b2c3d, 14:0:0, 15:2:a1b2c3d")
	if err != nil || len(m) != 3 || m[12].n != 5 || m[12].crc != 0x0a1b2c3d || m[14].n != 0 || m[15].crc != 0xa1b2c3d {
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

// androidHave formats have like Quill Android 0.9.23: code points
// (String.codePointCount) and Long.toHexString of java.util.zip.CRC32 over
// the UTF-8 bytes (lower case, no zero padding).
func androidHave(seq int, text string) string {
	return fmt.Sprintf("%d:%d:%s", seq, len([]rune(text)), strconv.FormatUint(uint64(crc32.ChecksumIEEE([]byte(text))), 16))
}

// A streaming reply fetched the way Quill Android does it: every round sends
// have for the copy it holds, appends textFrom replies, and must end with
// exactly the server's text (cross-check asked for by Quill Android,
// 2026-11-22).
func TestPhoneDeltaAndroidRoundTrip(t *testing.T) {
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

	full := []rune("Résumé of the café 🙂 step: 東京で検証中 — naïve façade, emoji 👍🏽 and more text that keeps arriving in uneven bursts until the end. ✅")
	var it *chat.Item
	h.ui(func() {
		s := h.d.sessions[editsID]
		it = s.conv.AddNotice("")
		it.Kind = chat.Assistant
		it.Streaming = true
		it.Text = string(full[:3])
	})
	base := "/phone/session/" + editsID + "/items"
	_, m, _ := a.do("GET", base, "", nil)
	epoch, rev := int(m["epoch"].(float64)), int(m["rev"].(float64))
	phone := ""
	for _, x := range m["items"].([]any) {
		if xm := x.(map[string]any); int(xm["seq"].(float64)) == it.Seq {
			phone = xm["text"].(string)
		}
	}
	deltas, zeroLead := 0, false
	for n, step := 3, 1; n < len(full); step = step%7 + 2 {
		n = min(n+step, len(full))
		server := string(full[:n])
		h.ui(func() { it.Text = server })
		have := androidHave(it.Seq, phone)
		if strings.Split(have, ":")[2][0] != '0' && len(strings.Split(have, ":")[2]) < 8 {
			zeroLead = true // a CRC whose hex would start with 0 when padded
		}
		_, m, _ = a.do("GET", base+"?epoch="+strconv.Itoa(epoch)+"&since="+strconv.Itoa(rev)+"&delta=1&have="+have, "", nil)
		rev = int(m["rev"].(float64))
		for _, x := range m["items"].([]any) {
			xm := x.(map[string]any)
			if int(xm["seq"].(float64)) != it.Seq {
				continue
			}
			if from, ok := xm["textFrom"].(float64); ok {
				if int(from) != len([]rune(phone)) {
					t.Fatalf("textFrom %v, phone holds %d code points", from, len([]rune(phone)))
				}
				phone += xm["text"].(string)
				deltas++
				if int(xm["textLen"].(float64)) != len([]rune(phone)) {
					t.Fatalf("textLen %v, phone now has %d", xm["textLen"], len([]rune(phone)))
				}
			} else {
				phone = xm["text"].(string)
			}
		}
		if phone != server {
			t.Fatalf("after %d code points the phone has %q, server %q", n, phone, server)
		}
	}
	if deltas < 10 {
		t.Fatalf("only %d replies came as deltas", deltas)
	}
	if !zeroLead {
		t.Log("no CRC with a leading zero came up; covered by TestParseHave's unpadded case")
	}
	// Upper-case hex (another client's formatting) is read the same way.
	h.ui(func() { it.Text = string(full) + " end" })
	up := fmt.Sprintf("%d:%d:%X", it.Seq, len([]rune(phone)), crc32.ChecksumIEEE([]byte(phone)))
	_, m, _ = a.do("GET", base+"?epoch="+strconv.Itoa(epoch)+"&since="+strconv.Itoa(rev)+"&delta=1&have="+up, "", nil)
	for _, x := range m["items"].([]any) {
		if xm := x.(map[string]any); int(xm["seq"].(float64)) == it.Seq && (xm["textFrom"] == nil || xm["text"] != " end") {
			t.Fatalf("upper-case crc: %v", xm)
		}
	}
}
