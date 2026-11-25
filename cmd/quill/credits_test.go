package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/codex"

	"github.com/katachinosekai/quill/internal/protocol"
)

func TestUsageCreditsInStrip(t *testing.T) {
	var ev protocol.Event
	raw := `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1795162800,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"out_of_credits","isUsingOverage":false}}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil || ev.RateLimit == nil {
		t.Fatalf("parse: %v", err)
	}
	h := newHarness(t)
	h.ui(func() {
		h.d.usage.update()
		if got := h.d.usage.strip[3].value.Text; got != "not reported yet" && got != "" {
			t.Fatalf("no guess before Claude reports it: %q", got)
		}
		h.d.setRateLimit(ev.RateLimit)
		h.d.usage.update()
		if got := h.d.usage.strip[3].value.Text; got != "out of credits" {
			t.Fatalf("credits come from Claude's report: %q", got)
		}
	})
	for _, c := range []struct {
		r    protocol.RateLimit
		want string
	}{
		{protocol.RateLimit{OverageStatus: "allowed", UsingOverage: true}, "in use"},
		{protocol.RateLimit{OverageStatus: "allowed"}, "available"},
		{protocol.RateLimit{OverageStatus: "rejected", OverageReason: "org_level_disabled"}, "turned off"},
	} {
		if got := creditsText(&c.r); got != c.want {
			t.Fatalf("%+v: %q", c.r, got)
		}
	}
}

func TestCodexSessionTokensMatchClaudeLayout(t *testing.T) {
	h := newDeskHarness(t, false)
	h.ui(func() {
		d := h.d
		session := d.addSessionWithBackend(h.proj, "default", "default", false, true)
		d.current = session
		session.conv.Stats.ContextUsed, session.conv.Stats.ContextMax = 32, 128
		session.conv.Stats.CodexUsage = &protocol.CodexUsage{
			Total: protocol.CodexTokenBreakdown{Total: 300, Input: 200, Output: 100, CachedInput: 75, ReasoningOutput: 45},
			Last:  protocol.CodexTokenBreakdown{Total: 32, Input: 24, Output: 8, CacheWriteInput: 3},
		}
		u := d.usage
		for _, mode := range []string{layoutHorizontal, layoutVertical} {
			d.setLayoutMode(mode)
			u.update()
			wantStrip := mode == layoutHorizontal
			if d.usageRow.Visible() != wantStrip || !u.stripCodex.Visible() || !u.stripDetails.Visible() || u.stripClaude.Visible() {
				t.Fatalf("%s: incorrect usage display", mode)
			}
			label := u.codexSessionRow.(*fyne.Container).Objects[0].(*fyne.Container).Objects[0].(*canvas.Text)
			if label.Text != "This session" || u.codexSession.value.Text != "300 tok" {
				t.Fatalf("%s: compact session %q %q", mode, label.Text, u.codexSession.value.Text)
			}
			for _, value := range []string{"300 tokens", "125 input", "100 output", "75 cached read", "context 32 / 128"} {
				if !strings.Contains(u.sess.Text, value) {
					t.Fatalf("%s: missing session usage %q in %q", mode, value, u.sess.Text)
				}
			}
			for _, extra := range []string{"reasoning output", "Last request", "All sessions", "32 tokens", " cached write"} {
				if strings.Contains(u.details, extra) {
					t.Fatalf("%s: unexpected token field %q in %q", mode, extra, u.details)
				}
			}
		}
		d.setLayoutMode(layoutHorizontal)
		d.current = d.addSession(h.proj, "default", "default", false)
		u.update()
		if u.stripCodex.Visible() || !u.stripClaude.Visible() || !u.stripDetails.Visible() {
			t.Fatal("switching to Claude should restore its usage display")
		}
	})
}

func TestCodexUsageBarsFollowSelectedProvider(t *testing.T) {
	h := newDeskHarness(t, false)
	h.ui(func() {
		d := h.d
		claude := d.addSession(h.proj, "default", "default", false)
		chatgpt := d.addSessionWithBackend(h.proj, "default", "default", false, true)
		u := d.usage
		reset := time.Now().Add(time.Hour).Unix()
		d.rate = &protocol.RateLimit{Type: "five_hour", Utilization: .19, ResetsAt: reset}
		d.codexAuth.ready = true
		d.codexAuth.snapshot = codex.AccountSnapshot{
			Account: &codex.Account{Type: "chatgpt"},
			Limits: map[string]codex.AccountLimit{
				"codex": {
					Primary:   &codex.LimitWindow{UsedPercent: 42, WindowDurationMins: 300, ResetsAt: reset},
					Secondary: &codex.LimitWindow{UsedPercent: 7, WindowDurationMins: 10080},
				},
			},
		}
		d.current = chatgpt
		u.update()
		if !u.stripCodex.Visible() || !u.codexPanelWindows.Visible() {
			t.Fatal("Codex usage bars should be visible")
		}
		assertFullBar := func(row fyne.CanvasObject, title, value string, fill float64) {
			t.Helper()
			parts := row.(*fyne.Container).Objects
			if parts[0].(*canvas.Text).Text != title || parts[1].(*widget.ProgressBar).Value != fill || parts[2].(*canvas.Text).Text != value {
				t.Fatalf("unexpected usage row for %s, expected %s and fill %v", title, value, fill)
			}
		}
		assertFullBar(u.codexPanelWindows.Objects[0], "5-hour session", "42% used · "+resetText(reset), .42)
		details := u.usageDetailsContent().(*fyne.Container)
		assertFullBar(details.Objects[2], "Weekly", "7% used", .07)

		compact := u.codexStripWindows.Objects[0].(*fyne.Container)
		compactText := compact.Objects[2].(*fyne.Container).Objects[0].(*canvas.Text)
		if compactText.Text != "42% · "+resetText(reset) {
			t.Fatalf("unexpected compact usage: %q", compactText.Text)
		}

		d.current = claude
		u.update()
		if u.stripCodex.Visible() || u.codexPanelWindows.Visible() || u.strip[0].bar.Value != .19 {
			t.Fatal("Claude should show only its own 19% usage")
		}
		d.codexAuth.snapshot.Limits = nil
		d.current = chatgpt
		u.update()
		assertFullBar(u.codexPanelWindows.Objects[0], "5-hour session", "no data yet (updates after a reply)", 0)
		assertFullBar(u.codexPanelWindows.Objects[1], "Weekly", "no data yet (updates after a reply)", 0)
	})
}
