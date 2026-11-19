package protocol

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func readFixture(t *testing.T, name string) []*Event {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var out []*Event
	for sc.Scan() {
		ev, err := ParseLine(sc.Bytes())
		if err != nil {
			t.Fatalf("parse %q: %v", sc.Text()[:min(80, len(sc.Text()))], err)
		}
		out = append(out, ev)
	}
	return out
}

func TestParseRealRun(t *testing.T) {
	evs := readFixture(t, "run1.jsonl")
	counts := map[string]int{}
	var deltas string
	var denied, rc, interruptOK, results int
	for _, ev := range evs {
		counts[ev.Type]++
		if ev.Type == "stream_event" && ev.Event.Delta != nil && ev.Event.Delta.Type == "text_delta" {
			deltas += ev.Event.Delta.Text
		}
		if ev.Type == "system" && ev.Subtype == "permission_denied" {
			denied++
			if ev.MessageText == "" || ev.ToolName != "Write" {
				t.Errorf("permission_denied fields: %+v", ev)
			}
		}
		if ev.Type == "control_response" {
			if info, ok := ev.RemoteControlInfo(); ok {
				rc++
				if info.SessionURL == "" {
					t.Error("empty session url")
				}
			}
			if ev.Response.RequestID == "int-1" && ev.Response.Subtype == "success" {
				interruptOK++
			}
		}
		if ev.Type == "result" {
			results++
			used, window := ev.LastContext()
			if results == 1 && (used == 0 || window != 1000000 || ev.TotalCostUSD <= 0) {
				t.Errorf("result usage: used=%d window=%d cost=%v", used, window, ev.TotalCostUSD)
			}
		}
		if ev.Type == "system" && ev.Subtype == "init" && (ev.SessionID == "" || ev.Model == "" || ev.PermissionMode != "default") {
			t.Errorf("init: %+v", ev)
		}
	}
	if counts["stream_event"] < 10 || counts["assistant"] < 2 || results != 2 {
		t.Errorf("counts %v results %d", counts, results)
	}
	if denied != 1 || rc != 1 || interruptOK != 1 {
		t.Errorf("denied=%d rc=%d interrupt=%d", denied, rc, interruptOK)
	}
	if len(deltas) < 20 {
		t.Errorf("text deltas too short: %q", deltas)
	}
}

func TestParseCanUseTool(t *testing.T) {
	var req *Event
	for _, ev := range readFixture(t, "run2.jsonl") {
		if ev.Type == "control_request" && ev.Request.Subtype == "can_use_tool" {
			req = ev
		}
	}
	if req == nil {
		t.Fatal("no can_use_tool in run2")
	}
	if req.RequestID == "" || req.Request.ToolName != "Write" || req.Request.ToolUseID == "" || len(req.Request.PermissionSuggestions) != 1 {
		t.Fatalf("bad request %+v", req.Request)
	}
	var resp struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string `json:"subtype"`
			RequestID string `json:"request_id"`
			Response  struct {
				Behavior           string           `json:"behavior"`
				UpdatedInput       map[string]any   `json:"updatedInput"`
				UpdatedPermissions []map[string]any `json:"updatedPermissions"`
				Message            string           `json:"message"`
			} `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(PermissionResponse(req.RequestID, AllowAlways, req.Request, ""), &resp); err != nil {
		t.Fatal(err)
	}
	r := resp.Response
	if resp.Type != "control_response" || r.Subtype != "success" || r.RequestID != req.RequestID || r.Response.Behavior != "allow" {
		t.Fatalf("bad response %+v", resp)
	}
	if r.Response.UpdatedInput["content"] != "hi" || len(r.Response.UpdatedPermissions) != 1 || r.Response.UpdatedPermissions[0]["destination"] != "session" {
		t.Fatalf("bad allow-always body %+v", r.Response)
	}
	if err := json.Unmarshal(PermissionResponse(req.RequestID, Deny, req.Request, ""), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Response.Response.Behavior != "deny" || resp.Response.Response.Message == "" {
		t.Fatalf("bad deny %+v", resp)
	}
}

func TestStringContent(t *testing.T) {
	bs := ParseContent(json.RawMessage(`"plain"`))
	if len(bs) != 1 || bs[0].Text != "plain" {
		t.Fatal(bs)
	}
	if ContentText(json.RawMessage(`[{"type":"text","text":"a"},{"type":"image"}]`)) != "a\n[image]" {
		t.Fatal("content text")
	}
}

func TestRateLimitEvent(t *testing.T) {
	ev, err := ParseLine([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1795113000,"rateLimitType":"five_hour","utilization":0.99,"unifiedWindows":{"five_hour":{"utilization":0.99,"resetsAt":1795113000,"status":"allowed_warning"},"seven_day":{"utilization":0.08,"resetsAt":1795701600,"status":"allowed"}}},"session_id":"s"}`))
	if err != nil {
		t.Fatal(err)
	}
	five, ok := ev.RateLimit.Window("five_hour")
	week, ok2 := ev.RateLimit.Window("seven_day")
	if !ok || !ok2 || five.Utilization != 0.99 || week.ResetsAt != 1795701600 {
		t.Fatalf("%+v", ev.RateLimit)
	}
}
