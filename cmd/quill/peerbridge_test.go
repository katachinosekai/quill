package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/katachinosekai/quill/internal/codex"
	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
)

func TestPeerMessageWaitsForCodexBridgeUpgrade(t *testing.T) {
	h := newHarness(t)
	d := h.d
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("QUILL_CODEX_BIN", self)
	t.Setenv("QUILL_HANDOFF_FAKE", "1")
	d.cfg.CodexRoot = t.TempDir()
	var s *deskSession
	var receipt operatorMessageReceipt
	h.ui(func() {
		s = d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.org.Titles[s.id] = "Fixture operator"
		opts, err := d.codexProcessOptions(s)
		if err != nil {
			t.Fatal(err)
		}
		var legacy codex.Options
		if err := json.Unmarshal([]byte(opts.BridgeArgs[1]), &legacy); err != nil {
			t.Fatal(err)
		}
		legacy.ForceSteer = false
		payload, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		opts.BridgeArgs[1] = string(payload)
		old, err := session.Start(opts, func(*protocol.Event) {})
		if err != nil {
			t.Fatal(err)
		}
		d.adopt(s, old)
		receipt = d.routeOperatorMessage(operatorMessageRequest{To: "fixture operator", Text: "Fixture peer note"})
		if receipt.Status != "queued" || !s.codexUpgrading || len(s.queue) != 0 {
			t.Fatalf("peer should stay in the durable pool during bridge upgrade: %+v", receipt)
		}
	})
	h.eventually("fresh bridge accepts the peer note", func() bool {
		return !s.codexUpgrading && strings.Contains(lastUser(s), receipt.ID) &&
			d.routeOperatorMessage(operatorMessageRequest{Op: "status", ID: receipt.ID}).Status == "submitted"
	})
}
