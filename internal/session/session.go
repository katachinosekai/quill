package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
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
	Chrome    string
	AddDirs   []string
	ResumeAt  string
	Prompt    string
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
	if m := LocalModel(o.Model); m != "" {
		args = append(args, "--model", m)
	} else if o.Model != "" && o.Model != "default" {
		args = append(args, "--model", o.Model)
	}
	if o.Mode != "" && o.Mode != "default" {
		args = append(args, "--permission-mode", o.Mode)
	}
	switch o.Chrome {
	case "on":
		args = append(args, "--chrome")
	case "off":
		args = append(args, "--no-chrome")
	}
	for _, dir := range o.AddDirs {
		args = append(args, "--add-dir", dir)
	}
	if o.ResumeAt != "" && o.Resume != "" {
		args = append(args, "--resume-session-at", o.ResumeAt)
	}
	if o.Prompt != "" {
		args = append(args, "--append-system-prompt", o.Prompt)
	}
	return append(args, o.ExtraArgs...)
}

const LocalPrefix = "ollama:"

// ServerPrefix marks a model on another Anthropic-compatible server for
// open-weight models (llama.cpp, vLLM, LM Studio, LiteLLM, ...):
// "local:<server name>/<model>".
const ServerPrefix = "local:"

// LocalServer is one such server, set from Quill's settings.
type LocalServer struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Key  string `json:"key,omitempty"`
}

var localServers atomic.Pointer[map[string]LocalServer]

// SetLocalServers replaces the known local servers.
func SetLocalServers(list []LocalServer) {
	m := map[string]LocalServer{}
	for _, s := range list {
		m[s.Name] = s
	}
	localServers.Store(&m)
}

func serverOf(model string) (LocalServer, string, bool) {
	rest, ok := strings.CutPrefix(model, ServerPrefix)
	if !ok {
		return LocalServer{}, "", false
	}
	name, m, ok := strings.Cut(rest, "/")
	if !ok || m == "" {
		return LocalServer{}, "", false
	}
	if p := localServers.Load(); p != nil {
		if s, ok := (*p)[name]; ok {
			return s, m, true
		}
	}
	return LocalServer{Name: name}, m, true
}

// LocalModel is the model name the local server knows ("" for Claude models).
func LocalModel(model string) string {
	if strings.HasPrefix(model, LocalPrefix) {
		return strings.TrimPrefix(model, LocalPrefix)
	}
	if _, m, ok := serverOf(model); ok {
		return m
	}
	return ""
}

// LocalBaseURL is where a local model's server answers ("" for Claude).
func LocalBaseURL(model string) string {
	if strings.HasPrefix(model, LocalPrefix) {
		return OllamaHost()
	}
	if s, _, ok := serverOf(model); ok {
		return strings.TrimRight(s.URL, "/")
	}
	return ""
}

const defaultOllama = "http://localhost:11434"

func OllamaHost() string {
	host := strings.TrimRight(os.Getenv("QUILL_OLLAMA_URL"), "/")
	if host == "" {
		return defaultOllama
	}
	u, err := url.Parse(host)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return defaultOllama
	}
	return host
}

func OllamaInsecure(host string) bool {
	u, err := url.Parse(host)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return false
	}
	ip := net.ParseIP(h)
	return ip == nil || !ip.IsLoopback()
}

func LocalEnv(model string) []string {
	if LocalModel(model) == "" {
		return nil
	}
	token := "ollama"
	if s, _, ok := serverOf(model); ok {
		token = s.Key
		if token == "" {
			token = "local"
		}
	}
	return []string{
		"ANTHROPIC_BASE_URL=" + LocalBaseURL(model),
		"ANTHROPIC_AUTH_TOKEN=" + token,
		"ANTHROPIC_API_KEY=",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=" + LocalModel(model),
		"ANTHROPIC_SMALL_FAST_MODEL=" + LocalModel(model),
		"CLAUDE_CODE_SUBAGENT_MODEL=" + LocalModel(model),
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
}

func NewUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type Process struct {
	opts     Options
	pid      int
	osProc   *os.Process
	stdin    *os.File
	stdout   *os.File
	errPath  string
	readDone chan struct{}
	handing  atomic.Bool
	leftover []byte
	writeMu  sync.Mutex
	onEvent  func(*protocol.Event)
	done     chan struct{}
	exitErr  error
	stderr   string
	seq      atomic.Int64
	mu       sync.Mutex
	waiters  map[string]chan *protocol.Event
	busy     atomic.Bool
	closed   atomic.Bool
	commands atomic.Pointer[[]protocol.Command]
	agents   atomic.Pointer[[]protocol.Agent]
}

var ErrExited = errors.New("session: process has exited")

func Start(opts Options, onEvent func(*protocol.Event)) (*Process, error) {
	bin := opts.Binary
	if bin == "" {
		bin = "claude"
	}
	runBin, runArgs := scoped(bin, opts.Args())
	cmd := exec.Command(runBin, runArgs...)
	cmd.Dir = opts.Cwd
	cmd.Env = append(append(os.Environ(), LocalEnv(opts.Model)...), opts.Env...)
	cmd.SysProcAttr = childAttr()
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	errFile, err := os.CreateTemp("", "quill-claude-*.log")
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errFile
	startErr := cmd.Start()
	inR.Close()
	outW.Close()
	errFile.Close()
	if startErr != nil {
		inW.Close()
		outR.Close()
		os.Remove(errFile.Name())
		return nil, startErr
	}
	p := &Process{
		opts:    opts,
		pid:     cmd.Process.Pid,
		osProc:  cmd.Process,
		stdin:   inW,
		stdout:  outR,
		errPath: errFile.Name(),
		onEvent: onEvent,
		done:    make(chan struct{}),
		waiters: map[string]chan *protocol.Event{},
	}
	p.run(nil)
	if err := p.write(protocol.Initialize(p.nextID("init"))); err != nil {
		return p, err
	}
	return p, nil
}

type Handoff struct {
	PID      int     `json:"pid"`
	In       uintptr `json:"in"`
	Out      uintptr `json:"out"`
	ErrPath  string  `json:"errPath"`
	Leftover []byte  `json:"leftover"`
	Busy     bool    `json:"busy"`
	Seq      int64   `json:"seq"`
	FromExec bool    `json:"-"`
	Opts     Options `json:"opts"`
}

func (p *Process) Keep() (in, out *os.File, h Handoff) {
	return p.stdin, p.stdout, Handoff{PID: p.pid, ErrPath: p.errPath, Seq: p.seq.Load(), Opts: p.opts}
}

func (p *Process) Handoff() (Handoff, error) {
	if !handoffSupported {
		return Handoff{}, errors.New("session: live hand-over is not supported on this OS")
	}
	if p.Exited() {
		return Handoff{}, ErrExited
	}
	p.handing.Store(true)
	_ = p.stdout.SetReadDeadline(time.Now())
	select {
	case <-p.readDone:
	case <-time.After(3 * time.Second):
	}
	h := Handoff{PID: p.pid, ErrPath: p.errPath, Leftover: p.leftover, Busy: p.busy.Load(), Seq: p.seq.Load(), Opts: p.opts}
	var err error
	if h.In, err = inheritFD(p.stdin); err != nil {
		return Handoff{}, err
	}
	if h.Out, err = inheritFD(p.stdout); err != nil {
		return Handoff{}, err
	}
	return h, nil
}

func Attach(h Handoff, onEvent func(*protocol.Event)) (*Process, error) {
	proc, err := os.FindProcess(h.PID)
	if err != nil {
		return nil, err
	}
	if !processExists(proc) {
		return nil, ErrExited
	}
	p := &Process{
		opts:    h.Opts,
		pid:     h.PID,
		osProc:  proc,
		stdin:   pollableFile(h.In, "claude-stdin", h.FromExec),
		stdout:  pollableFile(h.Out, "claude-stdout", h.FromExec),
		errPath: h.ErrPath,
		onEvent: onEvent,
		done:    make(chan struct{}),
		waiters: map[string]chan *protocol.Event{},
	}
	p.seq.Store(h.Seq)
	p.busy.Store(h.Busy)
	p.run(h.Leftover)
	return p, nil
}

func (p *Process) run(leftover []byte) {
	p.readDone = make(chan struct{})
	go func() {
		defer close(p.readDone)
		p.read(leftover)
	}()
	go func() {
		<-p.readDone
		if p.handing.Load() {
			return
		}
		state, err := p.osProc.Wait()
		if err == nil && !state.Success() {
			err = errors.New(state.String())
		}
		if errors.Is(err, syscall.ECHILD) {
			waitForeign(p.pid)
			err = nil
		}
		p.stderr = tailFile(p.errPath, 8192)
		os.Remove(p.errPath)
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
}

func tailFile(path string, max int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max64(0, st.Size()-max)
	b := make([]byte, st.Size()-off)
	n, _ := f.ReadAt(b, off)
	return string(b[:n])
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (p *Process) read(leftover []byte) {
	buf := append([]byte(nil), leftover...)
	chunk := make([]byte, 64<<10)
	for {
		for {
			i := bytes.IndexByte(buf, '\n')
			if i < 0 {
				break
			}
			p.handleLine(buf[:i])
			buf = buf[i+1:]
		}
		n, err := p.stdout.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if err != nil {
			if p.handing.Load() {
				for {
					i := bytes.IndexByte(buf, '\n')
					if i < 0 {
						break
					}
					p.handleLine(buf[:i])
					buf = buf[i+1:]
				}
				p.leftover = append([]byte(nil), buf...)
				return
			}
			if len(bytes.TrimSpace(buf)) > 0 {
				p.handleLine(buf)
			}
			return
		}
	}
}

func (p *Process) handleLine(line []byte) {
	ev, err := protocol.ParseLine(line)
	if err != nil {
		return
	}
	switch ev.Type {
	case "control_response":
		if ev.Response != nil && strings.HasPrefix(ev.Response.RequestID, "desk-init-") {
			if cmds := protocol.ParseCommands(ev.Response.Response); len(cmds) > 0 {
				p.commands.Store(&cmds)
			}
			if ags := protocol.ParseAgents(ev.Response.Response); len(ags) > 0 {
				p.agents.Store(&ags)
			}
		}
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

func (p *Process) nextID(prefix string) string {
	return fmt.Sprintf("desk-%s-%d", prefix, p.seq.Add(1))
}

func (p *Process) write(b []byte) error {
	if p.closed.Load() {
		return ErrExited
	}
	if p.handing.Load() {
		return ErrHandedOff
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err := p.stdin.Write(b)
	return err
}

func (p *Process) PID() int {
	return p.pid
}

func (p *Process) Options() Options { return p.opts }

func (p *Process) Commands() []protocol.Command {
	if c := p.commands.Load(); c != nil {
		return *c
	}
	return nil
}

// Agents: the subagents this session's Claude Code offers (nil until known).
func (p *Process) Agents() []protocol.Agent {
	if a := p.agents.Load(); a != nil {
		return *a
	}
	return nil
}

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

func (p *Process) Stderr() string {
	if p.Exited() {
		return p.stderr
	}
	return tailFile(p.errPath, 8192)
}

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
	if err != nil && !errors.Is(err, ErrExited) && p.osProc != nil {
		interruptProcess(p.osProc)
	}
	return err
}

func (p *Process) SetMode(ctx context.Context, mode string) error {
	_, err := p.Request(ctx, func(id string) []byte { return protocol.SetPermissionMode(id, mode) })
	return err
}

func (p *Process) Control(ctx context.Context, req map[string]any) (json.RawMessage, error) {
	ev, err := p.Request(ctx, func(id string) []byte { return protocol.ControlRequestLine(id, req) })
	if err != nil {
		return nil, err
	}
	if ev == nil || ev.Response == nil {
		return nil, nil
	}
	return ev.Response.Response, nil
}

func (p *Process) SetModel(ctx context.Context, model string) error {
	if m := LocalModel(model); m != "" {
		model = m
	}
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

func (p *Process) RenameSession(ctx context.Context, title string) error {
	_, err := p.Request(ctx, func(id string) []byte { return protocol.RenameSession(id, title) })
	return err
}

func (p *Process) Respond(requestID string, d protocol.Decision, req *protocol.ControlRequest) error {
	return p.write(protocol.PermissionResponse(requestID, d, req, ""))
}

func (p *Process) AllowWithRules(requestID string, req *protocol.ControlRequest, rules []protocol.Rule, destination string) error {
	return p.write(protocol.AllowWithRules(requestID, req, rules, destination))
}

func (p *Process) Answer(requestID string, req *protocol.ControlRequest, answers map[string]string) error {
	return p.write(protocol.AnswerQuestions(requestID, req, answers))
}

func (p *Process) DenyWith(requestID string, req *protocol.ControlRequest, message string) error {
	return p.write(protocol.PermissionResponse(requestID, protocol.Deny, req, message))
}

var ErrHandedOff = errors.New("session: process was handed to another Quill")

func (p *Process) Stop(grace time.Duration) error {
	if p.handing.Load() {
		return ErrHandedOff
	}
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
	terminateGroup(p.PID(), false)
	select {
	case <-p.done:
		return p.exitErr
	case <-time.After(3 * time.Second):
	}
	terminateGroup(p.PID(), true)
	<-p.done
	return p.exitErr
}

func FetchCommands(bin, cwd string, timeout time.Duration) ([]protocol.Command, error) {
	cmds, _, err := FetchInit(bin, cwd, timeout)
	return cmds, err
}

// FetchInit asks a short-lived claude for its slash commands and agents.
func FetchInit(bin, cwd string, timeout time.Duration) ([]protocol.Command, []protocol.Agent, error) {
	if bin == "" {
		bin = "claude"
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose"}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = cwd
	cmd.SysProcAttr = childAttr()
	cmd.Cancel = func() error { terminateGroup(cmd.Process.Pid, true); return nil }
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	defer func() {
		stdin.Close()
		terminateGroup(cmd.Process.Pid, false)
		_ = cmd.Wait()
	}()
	if _, err := stdin.Write(protocol.Initialize("cmds")); err != nil {
		return nil, nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		ev, err := protocol.ParseLine(sc.Bytes())
		if err != nil || ev.Type != "control_response" || ev.Response == nil || ev.Response.RequestID != "cmds" {
			continue
		}
		return protocol.ParseCommands(ev.Response.Response), protocol.ParseAgents(ev.Response.Response), nil
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	return nil, nil, errors.New("no command list from claude")
}
