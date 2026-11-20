package main

import (
	"encoding/json"
	"testing"

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
