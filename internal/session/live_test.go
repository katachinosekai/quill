//go:build live

package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/transcript"
)

const checkpointEnv = "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=1"

func liveModel() string {
	if m := os.Getenv("QUILL_LIVE_MODEL"); m != "" {
		return m
	}
	return "haiku"
}

func liveTurnTimeout(model string) time.Duration {
	if LocalModel(model) != "" {
		return 8 * time.Minute
	}
	return 3 * time.Minute
}

type denyProxy struct {
	addr  string
	mu    sync.Mutex
	hosts []string
}

func startDenyProxy(t *testing.T) *denyProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	px := &denyProxy{addr: "http://" + ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				line, _ := bufio.NewReader(c).ReadString('\n')
				px.mu.Lock()
				px.hosts = append(px.hosts, strings.TrimSpace(line))
				px.mu.Unlock()
				_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		px.mu.Lock()
		defer px.mu.Unlock()
		if len(px.hosts) > 0 {
			t.Errorf("LIVE outside requests in local mode: %v", px.hosts)
		} else {
			t.Logf("LIVE outside requests: 0")
		}
	})
	return px
}

func ollamaWindow(t *testing.T, model string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"model": LocalModel(model)})
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Post(OllamaHost()+"/api/show", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Skipf("Ollama is not reachable at %s: %v", OllamaHost(), err)
	}
	defer resp.Body.Close()
	var show struct {
		Parameters string `json:"parameters"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&show)
	if m := regexp.MustCompile(`num_ctx\s+(\d+)`).FindStringSubmatch(show.Parameters); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

type live struct {
	t   *testing.T
	p   *Process
	ch  chan *protocol.Event
	dir string
	id  string
}

func liveDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(transcript.DefaultRoot(), transcript.Sanitize(dir))) })
	return dir
}

func startLive(t *testing.T, dir string, opts Options) *live {
	t.Helper()
	opts.Binary = "claude"
	opts.Cwd = dir
	if opts.Resume == "" && opts.SessionID == "" {
		opts.SessionID = NewUUID()
	}
	if LocalModel(opts.Model) != "" {
		if w := ollamaWindow(t, opts.Model); w > 0 {
			ws := strconv.Itoa(w)
			opts.Env = append(opts.Env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS="+ws, "CLAUDE_CODE_AUTO_COMPACT_WINDOW="+ws)
		}
		px := startDenyProxy(t)
		opts.Env = append(opts.Env, "HTTPS_PROXY="+px.addr, "HTTP_PROXY="+px.addr, "NO_PROXY=localhost,127.0.0.1")
	}
	l := &live{t: t, ch: make(chan *protocol.Event, 8192), dir: dir, id: opts.SessionID}
	if opts.Resume != "" {
		l.id = opts.Resume
	}
	p, err := Start(opts, func(ev *protocol.Event) { l.ch <- ev })
	if err != nil {
		t.Fatal(err)
	}
	l.p = p
	t.Cleanup(func() { l.stop() })
	return l
}

func (l *live) stop() {
	if l.p == nil {
		return
	}
	_ = l.p.Stop(5 * time.Second)
	if s := strings.TrimSpace(l.p.Stderr()); s != "" {
		l.t.Logf("stderr: %s", s)
	}
	l.p = nil
}

func (l *live) turn(text string) *protocol.Event {
	l.t.Helper()
	if err := l.p.Send(text); err != nil {
		l.t.Fatal(err)
	}
	deadline := time.After(liveTurnTimeout(l.p.Options().Model))
	for {
		select {
		case ev := <-l.ch:
			switch {
			case ev.Type == "system" && ev.Subtype == "init":
				l.t.Logf("LIVE init: model=%s permissionMode=%s", ev.Model, ev.PermissionMode)
			case ev.Type == "control_request" && ev.Request != nil && ev.Request.Subtype == "can_use_tool":
				l.t.Logf("LIVE permission asked: %s (allowed)", ev.Request.ToolName)
				if err := l.p.Respond(ev.RequestID, protocol.Allow, ev.Request); err != nil {
					l.t.Fatal(err)
				}
			case ev.Type == "result":
				win := 0
				for _, mu := range ev.ModelUsage {
					win = max(win, mu.ContextWindow)
				}
				l.t.Logf("LIVE result: is_error=%v subtype=%s turns=%d ms=%d window=%d errors=%v text=%q", ev.IsError, ev.Subtype, ev.NumTurns, ev.DurationMS, win, ev.Errors, clipLive(ev.Result))
				return ev
			}
		case <-deadline:
			l.t.Fatalf("timeout waiting for the result of %q", text)
		}
	}
}

func (l *live) control(req map[string]any) json.RawMessage {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := l.p.Control(ctx, req)
	if err != nil {
		l.t.Fatalf("%v: %v", req["subtype"], err)
	}
	l.t.Logf("LIVE %v -> %s", req["subtype"], clipLive(string(raw)))
	return raw
}

func clipLive(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

type tline struct {
	Type       string          `json:"type"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Message    json.RawMessage `json:"message"`
	text       string
}

func (l *live) transcript() []tline {
	l.t.Helper()
	b, err := os.ReadFile(filepath.Join(transcript.DefaultRoot(), transcript.Sanitize(l.dir), l.id+".jsonl"))
	if err != nil {
		l.t.Fatal(err)
	}
	var out []tline
	for _, line := range bytes.Split(b, []byte("\n")) {
		var tl tline
		if json.Unmarshal(line, &tl) != nil || tl.UUID == "" {
			continue
		}
		var m struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(tl.Message, &m)
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			tl.text = s
		} else {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(m.Content, &blocks)
			for _, bl := range blocks {
				if bl.Type == "text" {
					tl.text += bl.Text
				}
			}
		}
		out = append(out, tl)
	}
	return out
}

func requireOK(t *testing.T, ev *protocol.Event, want string) {
	t.Helper()
	if ev.IsError || !strings.Contains(strings.ToLower(ev.Result), want) {
		t.Fatalf("expected %q in a successful result", want)
	}
}

func TestLiveTurn(t *testing.T) {
	model := liveModel()
	l := startLive(t, liveDir(t), Options{Model: model})
	ev := l.turn("Reply with exactly the word pong and nothing else.")
	requireOK(t, ev, "pong")
	if LocalModel(model) != "" {
		want := ollamaWindow(t, model)
		for _, mu := range ev.ModelUsage {
			if want > 0 && mu.ContextWindow != want {
				t.Errorf("CLI reports window %d, Ollama num_ctx is %d", mu.ContextWindow, want)
			}
		}
	}
}

func TestLiveToolPermission(t *testing.T) {
	dir := liveDir(t)
	l := startLive(t, dir, Options{Model: liveModel()})
	l.turn("Use the Write tool to create the file hello.txt in the current directory containing exactly: hi")
	b, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil || strings.TrimSpace(string(b)) != "hi" {
		t.Fatalf("hello.txt = %q, %v", b, err)
	}
}

func TestLiveAutoMode(t *testing.T) {
	l := startLive(t, liveDir(t), Options{Model: liveModel(), Mode: "auto"})
	l.turn("Reply with exactly the word pong and nothing else.")
}

func TestLiveResumeAcrossBackends(t *testing.T) {
	model := liveModel()
	if LocalModel(model) == "" {
		t.Skip("needs QUILL_LIVE_MODEL=ollama:<tag>; starts on haiku and resumes on it")
	}
	dir := liveDir(t)
	first := startLive(t, dir, Options{Model: "haiku"})
	requireOK(t, first.turn("Remember the word marmalade. Reply with just OK."), "ok")
	first.stop()
	second := startLive(t, dir, Options{Model: model, Resume: first.id})
	requireOK(t, second.turn("What word did I ask you to remember? Reply with just that word."), "marmalade")
}

func TestLiveRewind(t *testing.T) {
	model := liveModel()
	dir := liveDir(t)
	env := []string{checkpointEnv}
	l := startLive(t, dir, Options{Model: model, Mode: "acceptEdits", Env: env})
	requireOK(t, l.turn("Reply with exactly the word one and nothing else."), "one")
	l.turn("Use the Write tool to create the file note.txt in the current directory containing exactly: two")
	if _, err := os.Stat(filepath.Join(dir, "note.txt")); err != nil {
		t.Fatalf("note.txt was not written: %v", err)
	}
	var a1, u2 string
	for _, tl := range l.transcript() {
		switch {
		case tl.Type == "assistant" && u2 == "":
			a1 = tl.UUID
		case tl.Type == "user" && u2 == "" && strings.Contains(tl.text, "note.txt"):
			u2 = tl.UUID
		}
	}
	if a1 == "" || u2 == "" {
		t.Fatalf("could not find the first answer (%q) or the second prompt (%q) in the transcript", a1, u2)
	}
	raw := l.control(map[string]any{"subtype": "rewind_files", "user_message_id": u2})
	var reply struct {
		CanRewind bool `json:"canRewind"`
	}
	if json.Unmarshal(raw, &reply) != nil || !reply.CanRewind {
		t.Errorf("rewind_files reply is not {canRewind:true,...}: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "note.txt")); err == nil {
		t.Error("rewind_files left note.txt in place")
	}
	l.stop()
	r := startLive(t, dir, Options{Model: model, Mode: "acceptEdits", Env: env, Resume: l.id, ResumeAt: a1})
	requireOK(t, r.turn("Reply with exactly the word three and nothing else."), "three")
	files, _ := filepath.Glob(filepath.Join(transcript.DefaultRoot(), transcript.Sanitize(dir), "*.jsonl"))
	if len(files) != 1 {
		t.Errorf("the rewound session should stay in one transcript file, found %d", len(files))
	}
	lines := r.transcript()
	byID := map[string]tline{}
	var u3 string
	for _, tl := range lines {
		byID[tl.UUID] = tl
		if tl.Type == "user" && strings.Contains(tl.text, "three") {
			u3 = tl.UUID
		}
	}
	if u3 == "" {
		t.Fatal("the new prompt is not in the original transcript file")
	}
	hitA1 := false
	for id, n := u3, 0; id != "" && n < 1000; id, n = byID[id].ParentUUID, n+1 {
		if id == u2 {
			t.Fatal("the new branch still descends from the rewound prompt")
		}
		if id == a1 {
			hitA1 = true
			break
		}
	}
	if !hitA1 {
		t.Error("the new branch does not fork from the chosen answer")
	}
}

func TestLiveMCPStatus(t *testing.T) {
	cfg := `{"mcpServers":{"livetest":{"command":"/bin/false"}}}`
	l := startLive(t, liveDir(t), Options{Model: liveModel(), ExtraArgs: []string{"--mcp-config", cfg, "--strict-mcp-config"}})
	status := func() string {
		var r struct {
			Servers []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"mcpServers"`
		}
		_ = json.Unmarshal(l.control(map[string]any{"subtype": "mcp_status"}), &r)
		for _, s := range r.Servers {
			if s.Name == "livetest" {
				return s.Status
			}
		}
		t.Fatal("mcp_status does not list the configured server (Quill reads mcpServers[].name/status)")
		return ""
	}
	before := status()
	l.control(map[string]any{"subtype": "mcp_toggle", "serverName": "livetest", "enabled": false})
	if got := status(); got != "disabled" {
		t.Errorf("after mcp_toggle off the status is %q, want disabled", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := l.p.Control(ctx, map[string]any{"subtype": "mcp_toggle", "serverName": "livetest", "enabled": true})
	t.Logf("LIVE mcp re-enable of a server that exits at once: err=%v; status before=%s after=%s", err, before, status())
}
