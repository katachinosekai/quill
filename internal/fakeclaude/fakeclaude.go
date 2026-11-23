package fakeclaude

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

func Run() {
	args := os.Args[1:]
	if out := os.Getenv("FAKE_ARGS_OUT"); out != "" {
		os.WriteFile(out, []byte(strings.Join(args, " ")), 0o600)
	}
	sid := "fake-session"
	for i, a := range args {
		if (a == "--resume" || a == "--session-id") && i+1 < len(args) {
			sid = args[i+1]
		}
	}
	var mu sync.Mutex
	emit := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		os.Stdout.Write(append(b, '\n'))
		mu.Unlock()
	}
	lines := make(chan map[string]any)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				lines <- m
			}
		}
		close(lines)
	}()
	ack := func(m map[string]any, resp any) {
		emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m["request_id"], "response": resp}})
	}
	result := func(subtype, reason string) {
		emit(map[string]any{"type": "result", "subtype": subtype, "is_error": subtype != "success", "terminal_reason": reason, "session_id": sid, "total_cost_usd": 0.01, "num_turns": 1,
			"usage":      map[string]any{"input_tokens": 1, "iterations": []any{map[string]any{"input_tokens": 100, "cache_read_input_tokens": 900}}},
			"modelUsage": map[string]any{"fake": map[string]any{"contextWindow": 200000}}})
	}
	text := func(id, t string) {
		emit(map[string]any{"type": "assistant", "session_id": sid, "parent_tool_use_id": nil, "message": map[string]any{"id": id, "role": "assistant", "model": "fake", "content": []any{map[string]any{"type": "text", "text": t}}}})
	}
	ignoreEOF := os.Getenv("FAKE_IGNORE_EOF") == "1"
	for {
		m, ok := <-lines
		if !ok {
			if ignoreEOF {
				select {}
			}
			return
		}
		switch m["type"] {
		case "control_request":
			req := m["request"].(map[string]any)
			switch req["subtype"] {
			case "remote_control":
				if req["enabled"] == true {
					ack(m, map[string]any{"session_url": "https://example.invalid/code/session_x"})
				} else {
					ack(m, nil)
				}
			case "rename_session":
				if out := os.Getenv("FAKE_RENAME_OUT"); out != "" {
					if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
						title, _ := req["title"].(string)
						f.WriteString(title + "\n")
						f.Close()
					}
				}
				ack(m, map[string]any{})
			case "initialize":
				ack(m, map[string]any{"commands": []any{
					map[string]any{"name": "compact", "description": "Clear history but keep a summary", "argumentHint": "<instructions>"},
					map[string]any{"name": "clear", "description": "Start a new conversation"},
					map[string]any{"name": "code-review", "description": "Review the current diff", "argumentHint": "[level]"},
				}})
			default:
				ack(m, map[string]any{})
			}
		case "user":
			msg := m["message"].(map[string]any)
			prompt, _ := msg["content"].(string)
			emit(map[string]any{"type": "system", "subtype": "init", "session_id": sid, "model": "fake", "permissionMode": "default", "cwd": "/"})
			switch prompt {
			case "write", "demo":
				tool, input := "Write", map[string]any{"file_path": "/x/hi.txt", "content": "hi"}
				if prompt == "demo" {
					demo(emit, sid)
					tool, input = "Edit", map[string]any{"file_path": "/x/main.go", "old_string": "func main() {\n\tfmt.Println(\"hi\")\n}", "new_string": "func main() {\n\tfmt.Println(\"hello, desk\")\n\tos.Exit(0)\n}"}
				}
				emit(map[string]any{"type": "assistant", "session_id": sid, "message": map[string]any{"id": "m1", "role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "tu1", "name": tool, "input": input}}}})
				emit(map[string]any{"type": "control_request", "request_id": "perm-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": tool, "tool_use_id": "tu1", "input": input, "permission_suggestions": []any{map[string]any{"type": "setMode", "mode": "acceptEdits", "destination": "session"}}}})
				var behavior string
				for behavior == "" {
					r, ok := <-lines
					if !ok {
						return
					}
					if r["type"] == "control_response" {
						resp := r["response"].(map[string]any)
						if resp["request_id"] != "perm-1" {
							continue
						}
						body := resp["response"].(map[string]any)
						behavior = body["behavior"].(string)
						if ups, ok := body["updatedPermissions"].([]any); ok && len(ups) > 0 {
							behavior += "+always"
						}
					}
				}
				isErr := !strings.HasPrefix(behavior, "allow")
				emit(map[string]any{"type": "user", "session_id": sid, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tu1", "content": "result:" + behavior, "is_error": isErr}}}})
				text("m2", "done "+behavior)
				result("success", "completed")
			case "slow":
				emit(map[string]any{"type": "stream_event", "session_id": sid, "api_message_id": "m3", "event": map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}})
				tick := time.NewTicker(10 * time.Millisecond)
			loop:
				for {
					select {
					case <-tick.C:
						emit(map[string]any{"type": "stream_event", "session_id": sid, "api_message_id": "m3", "event": map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "x"}}})
					case r, ok := <-lines:
						if !ok {
							return
						}
						if req, _ := r["request"].(map[string]any); r["type"] == "control_request" && req["subtype"] == "interrupt" {
							ack(r, map[string]any{"still_queued": []any{}})
							kind := "all"
							if req["spare_background_tasks"] == true {
								kind = "spare"
							}
							emit(map[string]any{"type": "system", "subtype": "fake_interrupt", "status": kind, "session_id": sid})
							if out := os.Getenv("FAKE_ARGS_OUT"); out != "" {
								if f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
									f.WriteString("\ninterrupt:" + kind)
									f.Close()
								}
							}
							break loop
						}
					}
				}
				tick.Stop()
				emit(map[string]any{"type": "user", "session_id": sid, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}}})
				result("error_during_execution", "aborted_streaming")
			default:
				text("m9", "echo: "+prompt)
				result("success", "completed")
				emit(map[string]any{"type": "prompt_suggestion", "suggestion": "next after " + prompt, "session_id": sid})
			}
		}
	}
}

func demo(emit func(any), sid string) {
	ev := func(id string, e map[string]any) {
		emit(map[string]any{"type": "stream_event", "session_id": sid, "api_message_id": id, "event": e})
	}
	ev("m0", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
	ev("m0", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "The user wants a friendlier greeting. Check the file first, then edit it."}})
	ev("m0", map[string]any{"type": "content_block_stop", "index": 0})
	emit(map[string]any{"type": "assistant", "session_id": sid, "message": map[string]any{"id": "m0", "role": "assistant", "content": []any{
		map[string]any{"type": "thinking", "thinking": "The user wants a friendlier greeting. Check the file first, then edit it."},
		map[string]any{"type": "text", "text": "Sure. Let me look at **main.go** first, then I'll update the greeting."},
		map[string]any{"type": "tool_use", "id": "tu0", "name": "Bash", "input": map[string]any{"command": "go run .", "description": "Run the program"}},
	}}})
	emit(map[string]any{"type": "user", "session_id": sid, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "tu0", "content": "hi"}}}})
}
