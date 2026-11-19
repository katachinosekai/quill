package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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
	os.Exit(m.Run())
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
