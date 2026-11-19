package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
)

type Options struct {
	Binary    string
	Cwd       string
	Resume    string
	SessionID string
	Model     string
	Mode      string
	ExtraArgs []string
	Env       []string
}

func (o Options) Args() []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--prompt-suggestions", "true",
		"--permission-prompt-tool", "stdio",
	}
	if o.Resume != "" {
		args = append(args, "--resume", o.Resume)
	} else if o.SessionID != "" {
		args = append(args, "--session-id", o.SessionID)
	}
	if o.Model != "" && o.Model != "default" {
		args = append(args, "--model", o.Model)
	}
	if o.Mode != "" && o.Mode != "default" {
		args = append(args, "--permission-mode", o.Mode)
	}
	return append(args, o.ExtraArgs...)
}

func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type Process struct {
	opts    Options
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	onEvent func(*protocol.Event)
	done    chan struct{}
	exitErr error
	stderr  *tailBuffer
	seq     atomic.Int64
	mu      sync.Mutex
	waiters map[string]chan *protocol.Event
	busy    atomic.Bool
	closed  atomic.Bool
}

var ErrExited = errors.New("session: process has exited")

func Start(opts Options, onEvent func(*protocol.Event)) (*Process, error) {
	bin := opts.Binary
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.Command(bin, opts.Args()...)
	cmd.Dir = opts.Cwd
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &Process{
		opts:    opts,
		cmd:     cmd,
		stdin:   stdin,
		onEvent: onEvent,
		done:    make(chan struct{}),
		stderr:  &tailBuffer{max: 8192},
		waiters: map[string]chan *protocol.Event{},
	}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		p.read(stdout)
	}()
	go func() {
		<-readDone
		err := cmd.Wait()
		p.closed.Store(true)
		p.busy.Store(false)
		p.exitErr = err
		p.mu.Lock()
		for id, ch := range p.waiters {
			close(ch)
			delete(p.waiters, id)
		}
		p.mu.Unlock()
		close(p.done)
	}()
	if err := p.write(protocol.Initialize(p.nextID("init"))); err != nil {
		return p, err
	}
	return p, nil
}

func (p *Process) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		ev, err := protocol.ParseLine(sc.Bytes())
		if err != nil {
			continue
		}
		switch ev.Type {
		case "control_response":
			if ev.Response != nil {
				p.mu.Lock()
				ch := p.waiters[ev.Response.RequestID]
				delete(p.waiters, ev.Response.RequestID)
				p.mu.Unlock()
				if ch != nil {
					ch <- ev
					close(ch)
				}
			}
		case "control_request":
			if ev.Request != nil && ev.Request.Subtype != "can_use_tool" {
				_ = p.write(protocol.ControlErrorLine(ev.RequestID, "unsupported by Quill: "+ev.Request.Subtype))
			}
		case "result":
			p.busy.Store(false)
		}
		if p.onEvent != nil {
			p.onEvent(ev)
		}
	}
}

func (p *Process) nextID(prefix string) string {
	return fmt.Sprintf("desk-%s-%d", prefix, p.seq.Add(1))
}

func (p *Process) write(b []byte) error {
	if p.closed.Load() {
		return ErrExited
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.stdin.Write(b)
	return err
}

func (p *Process) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) Options() Options      { return p.opts }
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) Busy() bool            { return p.busy.Load() }

func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *Process) ExitError() error {
	<-p.done
	return p.exitErr
}

func (p *Process) Stderr() string { return p.stderr.String() }

func (p *Process) Send(text string) error {
	if err := p.write(protocol.UserMessage(text)); err != nil {
		return err
	}
	p.busy.Store(true)
	return nil
}

func (p *Process) Request(ctx context.Context, build func(id string) []byte) (*protocol.Event, error) {
	id := p.nextID("req")
	ch := make(chan *protocol.Event, 1)
	p.mu.Lock()
	p.waiters[id] = ch
	p.mu.Unlock()
	if err := p.write(build(id)); err != nil {
		p.mu.Lock()
		delete(p.waiters, id)
		p.mu.Unlock()
		return nil, err
	}
	select {
	case ev, ok := <-ch:
		if !ok {
			return nil, ErrExited
		}
		if ev.Response.Subtype == "error" {
			return ev, errors.New(ev.Response.Error)
		}
		return ev, nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.waiters, id)
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (p *Process) Interrupt(ctx context.Context) error {
	_, err := p.Request(ctx, protocol.Interrupt)
	if err != nil && !errors.Is(err, ErrExited) && p.cmd.Process != nil {
		_ = p.cmd.Process.Signal(syscall.SIGINT)
	}
	return err
}

func (p *Process) SetMode(ctx context.Context, mode string) error {
	_, err := p.Request(ctx, func(id string) []byte { return protocol.SetPermissionMode(id, mode) })
	return err
}

func (p *Process) SetModel(ctx context.Context, model string) error {
	_, err := p.Request(ctx, func(id string) []byte { return protocol.SetModel(id, model) })
	return err
}

func (p *Process) RemoteControl(ctx context.Context, enabled bool) (protocol.RemoteControlInfo, error) {
	ev, err := p.Request(ctx, func(id string) []byte { return protocol.RemoteControl(id, enabled) })
	if err != nil {
		return protocol.RemoteControlInfo{}, err
	}
	info, _ := ev.RemoteControlInfo()
	return info, nil
}

func (p *Process) Respond(requestID string, d protocol.Decision, req *protocol.ControlRequest) error {
	return p.write(protocol.PermissionResponse(requestID, d, req, ""))
}

func (p *Process) Stop(grace time.Duration) error {
	if p.Exited() {
		return p.exitErr
	}
	if p.busy.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), grace/2)
		_ = p.Interrupt(ctx)
		cancel()
	}
	p.writeMu.Lock()
	_ = p.stdin.Close()
	p.writeMu.Unlock()
	select {
	case <-p.done:
		return p.exitErr
	case <-time.After(grace):
	}
	_ = syscall.Kill(-p.PID(), syscall.SIGTERM)
	select {
	case <-p.done:
		return p.exitErr
	case <-time.After(3 * time.Second):
	}
	_ = syscall.Kill(-p.PID(), syscall.SIGKILL)
	<-p.done
	return p.exitErr
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(b), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
