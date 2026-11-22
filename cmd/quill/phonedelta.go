package main

import (
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"

	"github.com/katachinosekai/quill/internal/chat"
)

// Streaming deltas (user 2026-11-22, asked for by Quill Android): while a
// reply streams, the phone used to get the whole text of the item on every
// /items?since. A phone that sends delta=1 and have=<seq>:<n>:<crc> for the
// items it holds gets only the new end: "textFrom": n (Unicode code points
// it already has), "text": the rest, "textLen": the whole length. crc is
// the CRC-32 (IEEE, 8 hex digits) of the UTF-8 of the phone's first n code
// points; when it doesn't match (the text changed, not just grew) the item
// comes whole, without textFrom. Phones that don't send delta=1 get the
// whole text as before.

const maxHave = 16

type haveText struct {
	n   int
	crc uint32
}

// parseHave reads "seq:n:crc,seq:n:crc".
func parseHave(v string) (map[int]haveText, error) {
	out := map[int]haveText{}
	if strings.TrimSpace(v) == "" {
		return out, nil
	}
	for _, part := range strings.Split(v, ",") {
		f := strings.Split(strings.TrimSpace(part), ":")
		if len(f) != 3 {
			return nil, fmt.Errorf("have is seq:n:crc, comma separated")
		}
		seq, err1 := strconv.Atoi(f[0])
		n, err2 := strconv.Atoi(f[1])
		crc, err3 := strconv.ParseUint(f[2], 16, 32)
		if err1 != nil || err2 != nil || err3 != nil || seq < 0 || n < 0 {
			return nil, fmt.Errorf("have is seq:n:crc (crc in hex)")
		}
		if len(out) >= maxHave {
			return nil, fmt.Errorf("at most %d have entries", maxHave)
		}
		out[seq] = haveText{n: n, crc: uint32(crc)}
	}
	return out, nil
}

// textDelta turns an item's text into its new end when the phone's copy is
// a prefix of it (assistant and thinking text only grows while streaming).
func textDelta(m map[string]any, it *chat.Item, h haveText) {
	if it.Kind != chat.Assistant && it.Kind != chat.Thinking || h.n == 0 {
		return
	}
	if t, _ := m["text"].(string); t != it.Text {
		return
	}
	runes := []rune(it.Text)
	if h.n > len(runes) {
		return
	}
	if crc32.ChecksumIEEE([]byte(string(runes[:h.n]))) != h.crc {
		return
	}
	m["textFrom"] = h.n
	m["text"] = string(runes[h.n:])
	m["textLen"] = len(runes)
}
