package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/fakeclaude"
	"github.com/katachinosekai/quill/internal/protocol"
)

func TestMain(m *testing.M) {
	if os.Getenv("CLAUDE_DESK_FAKE") == "1" {
		fakeclaude.Run()
		os.Exit(0)
	}
	if h := os.Getenv("QUILL_EXEC_STAGE2"); h != "" {
		os.Exit(execStage2(h))
	}
	os.Exit(m.Run())
}

func execStage2(raw string) int {
	var h Handoff
	if json.Unmarshal([]byte(raw), &h) != nil {
		return 3
	}
	h.FromExec = true
	got := make(chan *protocol.Event, 256)
	p, err := Attach(h, func(ev *protocol.Event) { got <- ev })
	if err != nil {
		return 4
	}
	if os.Getenv("QUILL_EXEC_ROUND") == "" {
		select {
		case <-got:
		case <-time.After(5 * time.Second):
			return 7
		}
		done := make(chan Handoff, 1)
		go func() { h2, _ := p.Handoff(); done <- h2 }()
		var h2 Handoff
		select {
		case h2 = <-done:
		case <-time.After(5 * time.Second):
			return 8
		}
		b, _ := json.Marshal(h2)
		exe, _ := os.Executable()
		env := append(os.Environ(), "QUILL_EXEC_STAGE2="+string(b), "QUILL_EXEC_ROUND=2")
		_ = syscall.Exec(exe, []string{exe}, env)
		return 9
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !p.Busy() || p.Interrupt(ctx) != nil {
		return 5
	}
	_ = p.Send("after exec")
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ev := <-got:
			if ev.Type == "assistant" && strings.Contains(string(ev.MessageRaw), "after exec") {
				_ = p.Stop(3 * time.Second)
				return 0
			}
		case <-deadline:
			return 6
		}
	}
}

func TestHandoffAcrossExec(t *testing.T) {
	if os.Getenv("QUILL_EXEC_CHILD") == "" {
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "-test.run=^TestHandoffAcrossExec$", "-test.v")
		cmd.Env = append(os.Environ(), "QUILL_EXEC_CHILD=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("exec handoff failed: %v\n%s", err, out)
		}
		return
	}
	r := newRecorder()
	exe, _ := os.Executable()
	p, err := Start(Options{Binary: exe, Cwd: t.TempDir(), SessionID: "exec-1", Env: []string{"CLAUDE_DESK_FAKE=1"}}, r.on)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Send("slow"); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, isType("stream_event"))
	h, err := p.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(h)
	env := append(os.Environ(), "QUILL_EXEC_STAGE2="+string(b))
	t.Fatal(syscall.Exec(exe, []string{exe}, env))
}

type recorder struct {
	mu   sync.Mutex
	conv *chat.Conversation
	evs  []*protocol.Event
	ch   chan *protocol.Event
}

func newRecorder() *recorder {
	return &recorder{conv: chat.New(), ch: make(chan *protocol.Event, 1024)}
}

func (r *recorder) on(ev *protocol.Event) {
	r.mu.Lock()
	r.conv.Apply(ev)
	r.evs = append(r.evs, ev)
	r.mu.Unlock()
	r.ch <- ev
}

func (r *recorder) waitFor(t *testing.T, pred func(*protocol.Event) bool) *protocol.Event {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev := <-r.ch:
			if pred(ev) {
				return ev
			}
		case <-timeout:
			t.Fatal("timeout waiting for event")
			return nil
		}
	}
}

func isType(typ string) func(*protocol.Event) bool {
	return func(ev *protocol.Event) bool { return ev.Type == typ }
}

func start(t *testing.T, o Options, r *recorder, env ...string) *Process {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	o.Binary = exe
	o.Env = append([]string{"CLAUDE_DESK_FAKE=1"}, env...)
	if o.Cwd == "" {
		o.Cwd = t.TempDir()
	}
	p, err := Start(o, r.on)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop(2 * time.Second) })
	return p
}

func TestArgs(t *testing.T) {
	args := strings.Join(Options{Resume: "abc", Model: "haiku", Mode: "acceptEdits"}.Args(), " ")
	for _, want := range []string{"-p", "--input-format stream-json", "--output-format stream-json", "--verbose", "--include-partial-messages", "--prompt-suggestions true", "--permission-prompt-tool stdio", "--resume abc", "--model haiku", "--permission-mode acceptEdits"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	args = strings.Join(Options{SessionID: "id1", Model: "default", Mode: "default"}.Args(), " ")
	if !strings.Contains(args, "--session-id id1") || strings.Contains(args, "--model") || strings.Contains(args, "--permission-mode") {
		t.Error(args)
	}
	if u := NewUUID(); len(u) != 36 || u[14] != '4' {
		t.Error(u)
	}
}

func TestLocalModel(t *testing.T) {
	args := strings.Join(Options{SessionID: "id1", Model: "ollama:qwen3.8:27b-80k"}.Args(), " ")
	if !strings.Contains(args, "--model qwen3.8:27b-80k ") && !strings.HasSuffix(args, "--model qwen3.8:27b-80k") {
		t.Error(args)
	}
	t.Setenv("QUILL_OLLAMA_URL", "")
	env := strings.Join(LocalEnv("ollama:qwen3.8:27b-80k"), "\n")
	for _, want := range []string{"ANTHROPIC_BASE_URL=http://localhost:11434", "ANTHROPIC_AUTH_TOKEN=ollama", "ANTHROPIC_API_KEY=\n", "ANTHROPIC_SMALL_FAST_MODEL=qwen3.8:27b-80k"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in %s", want, env)
		}
	}
	if LocalEnv("opus") != nil || LocalModel("opus") != "" {
		t.Error("claude model treated as local")
	}
}

func TestPermissionRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		d    protocol.Decision
		want string
	}{{protocol.Allow, "done allow"}, {protocol.AllowAlways, "done allow+always"}, {protocol.Deny, "done deny"}} {
		t.Run(tc.want, func(t *testing.T) {
			r := newRecorder()
			argsFile := t.TempDir() + "/args"
			p := start(t, Options{SessionID: "s-1"}, r, "FAKE_ARGS_OUT="+argsFile)
			if err := p.Send("write"); err != nil {
				t.Fatal(err)
			}
			if !p.Busy() {
				t.Error("should be busy after send")
			}
			req := r.waitFor(t, isType("control_request"))
			r.mu.Lock()
			perm := r.conv.Permission(req.RequestID)
			r.mu.Unlock()
			if perm == nil || perm.PermState != chat.PermPending || perm.ToolName != "Write" {
				t.Fatalf("perm item %+v", perm)
			}
			if err := p.Respond(req.RequestID, tc.d, req.Request); err != nil {
				t.Fatal(err)
			}
			r.waitFor(t, isType("result"))
			r.mu.Lock()
			defer r.mu.Unlock()
			last := r.conv.Items[len(r.conv.Items)-1]
			if last.Kind != chat.Assistant || last.Text != tc.want {
				t.Fatalf("last %+v", last)
			}
			if p.Busy() {
				t.Error("still busy after result")
			}
			if r.conv.SessionID != "s-1" || r.conv.Stats.ContextUsed != 1000 || r.conv.Stats.ContextMax != 200000 {
				t.Errorf("conv %q %+v", r.conv.SessionID, r.conv.Stats)
			}
			b, _ := os.ReadFile(argsFile)
			if !strings.Contains(string(b), "--permission-prompt-tool stdio") {
				t.Errorf("args %s", b)
			}
		})
	}
}

func TestInterruptAndMultiTurn(t *testing.T) {
	r := newRecorder()
	p := start(t, Options{Resume: "old"}, r)
	if err := p.Send("slow"); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, func(ev *protocol.Event) bool {
		return ev.Type == "stream_event" && ev.Event.Delta != nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	res := r.waitFor(t, isType("result"))
	if res.TerminalReason != "aborted_streaming" {
		t.Fatal(res.TerminalReason)
	}
	if err := p.Send("again"); err != nil {
		t.Fatal(err)
	}
	r.waitFor(t, isType("result"))
	r.mu.Lock()
	var got []string
	for _, it := range r.conv.Items {
		got = append(got, fmt.Sprintf("%d:%s", it.Kind, it.Text))
		if it.Streaming || it.Kind == chat.Error {
			t.Errorf("bad item %+v", it)
		}
	}
	r.mu.Unlock()
	if len(got) != 3 || got[1] != "4:Interrupted" || got[2] != "1:echo: again" {
		t.Fatalf("items %v", got)
	}
	if p.Exited() {
		t.Fatal("process should still run between turns")
	}
}

func TestRemoteControlAndModeRequests(t *testing.T) {
	r := newRecorder()
	p := start(t, Options{}, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := p.RemoteControl(ctx, true)
	if err != nil || info.SessionURL == "" {
		t.Fatal(info, err)
	}
	if _, err := p.RemoteControl(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := p.SetMode(ctx, "plan"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetModel(ctx, "default"); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleStop(t *testing.T) {
	r := newRecorder()
	p := start(t, Options{}, r)
	if p.PID() == 0 || p.Exited() {
		t.Fatal("not running")
	}
	if err := p.Stop(2 * time.Second); err != nil {
		t.Fatalf("clean stop: %v", err)
	}
	if !p.Exited() {
		t.Fatal("not exited")
	}
	if err := p.Send("x"); err == nil {
		t.Fatal("send after exit should fail")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Request(ctx, protocol.Interrupt); err == nil {
		t.Fatal("request after exit should fail")
	}
}

func TestLifecycleStopEscalates(t *testing.T) {
	r := newRecorder()
	p := start(t, Options{}, r, "FAKE_IGNORE_EOF=1")
	t0 := time.Now()
	err := p.Stop(300 * time.Millisecond)
	if err == nil {
		t.Fatal("expected a signal exit error")
	}
	if !p.Exited() || time.Since(t0) > 5*time.Second {
		t.Fatal("did not stop in time")
	}
}

func TestStopWhileBusyInterruptsFirst(t *testing.T) {
	r := newRecorder()
	p := start(t, Options{}, r)
	p.Send("slow")
	r.waitFor(t, func(ev *protocol.Event) bool { return ev.Type == "stream_event" && ev.Event.Delta != nil })
	if err := p.Stop(2 * time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	found := false
	for _, ev := range r.evs {
		if ev.Type == "result" && ev.TerminalReason == "aborted_streaming" {
			found = true
		}
	}
	if !found {
		t.Fatal("busy stop should interrupt the turn")
	}
}

func TestFetchCommands(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_DESK_FAKE", "1")
	cmds, err := FetchCommands(exe, t.TempDir(), 10*time.Second)
	if err != nil || len(cmds) != 3 || cmds[0].Name != "compact" || cmds[0].ArgumentHint != "<instructions>" {
		t.Fatalf("%+v %v", cmds, err)
	}
}

func TestHandoffMidTurn(t *testing.T) {
	r1 := newRecorder()
	p := start(t, Options{SessionID: "hand-1"}, r1)
	if err := p.Send("slow"); err != nil {
		t.Fatal(err)
	}
	r1.waitFor(t, isType("stream_event"))
	h, err := p.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	if p.Exited() {
		t.Fatal("the old handle must not report an exit after a handoff")
	}
	r2 := newRecorder()
	q, err := Attach(h, r2.on)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Stop(2 * time.Second) })
	r2.waitFor(t, isType("stream_event"))
	if !q.Busy() {
		t.Fatal("busy state should carry over")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	r2.waitFor(t, isType("result"))
	if err := q.Send("hello"); err != nil {
		t.Fatal(err)
	}
	r2.waitFor(t, func(ev *protocol.Event) bool {
		return ev.Type == "result"
	})
	if err := q.Stop(3 * time.Second); err != nil && !strings.Contains(err.Error(), "signal") {
		t.Logf("stop: %v", err)
	}
	if !q.Exited() {
		t.Fatal("attached process should report its exit")
	}
}

func TestHandoffTwice(t *testing.T) {
	r1 := newRecorder()
	p := start(t, Options{SessionID: "hand-2"}, r1)
	if err := p.Send("slow"); err != nil {
		t.Fatal(err)
	}
	r1.waitFor(t, isType("stream_event"))
	h, err := p.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	r2 := newRecorder()
	q, err := Attach(h, r2.on)
	if err != nil {
		t.Fatal(err)
	}
	r2.waitFor(t, isType("stream_event"))
	done := make(chan Handoff, 1)
	go func() {
		h2, err := q.Handoff()
		if err != nil {
			t.Error(err)
		}
		done <- h2
	}()
	var h2 Handoff
	select {
	case h2 = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("second handoff hung")
	}
	r3 := newRecorder()
	q2, err := Attach(h2, r3.on)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q2.Stop(2 * time.Second) })
	r3.waitFor(t, isType("stream_event"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := q2.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	r3.waitFor(t, isType("result"))
}

func TestOllamaHostChecks(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "http://localhost:11434",
		"http://gpu-box:11434/": "http://gpu-box:11434",
		"https://ollama.lan":    "https://ollama.lan",
		"file:///etc/passwd":    "http://localhost:11434",
		"gpu-box:11434":         "http://localhost:11434",
		"javascript:alert(1)":   "http://localhost:11434",
	} {
		t.Setenv("QUILL_OLLAMA_URL", in)
		if got := OllamaHost(); got != want {
			t.Errorf("OllamaHost(%q) = %q, want %q", in, got, want)
		}
	}
	for host, want := range map[string]bool{
		"http://localhost:11434": false, "http://127.0.0.1:11434": false, "http://[::1]:11434": false,
		"http://gpu-box:11434": true, "http://192.168.1.5:11434": true, "https://gpu-box": false,
	} {
		if OllamaInsecure(host) != want {
			t.Errorf("OllamaInsecure(%q) != %v", host, want)
		}
	}
	if env := strings.Join(LocalEnv("ollama:qwen3.8:27b-80k"), "\n"); !strings.Contains(env, "CLAUDE_CODE_SUBAGENT_MODEL=qwen3.8:27b-80k") {
		t.Error("subagents use the local model too")
	}
}

func TestStartFlags(t *testing.T) {
	args := strings.Join(Options{Resume: "id1", Chrome: "on", AddDirs: []string{"/a b", "/c"}, ResumeAt: "uuid-9"}.Args(), " ")
	for _, want := range []string{"--chrome", "--add-dir /a b", "--add-dir /c", "--resume-session-at uuid-9"} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q in %s", want, args)
		}
	}
	if a := strings.Join(Options{SessionID: "x", Chrome: "off", ResumeAt: "u"}.Args(), " "); !strings.Contains(a, "--no-chrome") || strings.Contains(a, "--resume-session-at") {
		t.Errorf("no-chrome, and resume-at only with --resume: %s", a)
	}
}
