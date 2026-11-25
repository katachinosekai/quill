// Package codex adapts the Codex app server to Quill's session event stream.
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/katachinosekai/quill/internal/protocol"
)

const Entrypoint = "quill-codex"

type Options struct {
	Binary, Cwd, SessionID                              string
	Instructions, SettingsBinary, Sandbox               string
	ComposerSettings, Coordinate                        bool
	KmemCommand                                         []string
	KmemTopic, KmemTopicTitle                           string
	Ephemeral, Steer, SteerOnlyWhileWaiting, ForceSteer bool
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type approval struct {
	id        json.RawMessage
	questions map[string]string
	run       func() error
}

type queuedInput struct {
	text     string
	force    bool
	settings *protocol.CodexTurnSettings
}

type bridge struct {
	opts                        Options
	toServer                    *json.Encoder
	toQuill                     *json.Encoder
	sequence                    int
	pending                     map[string]func(rpcMessage) error
	approvals                   map[string]approval
	streaming                   map[string]bool
	fileChanges                 map[string]json.RawMessage
	activeItems                 map[string]bool
	waitingForAgents            bool
	workers                     map[string]bool
	threadID                    string
	turnID                      string
	announcedTurn               string
	model                       string
	defaultModel, defaultEffort string
	turnSettings                *protocol.CodexTurnSettings
	ready                       bool
	briefSent                   bool
	starting                    bool
	interrupt                   string
	queued                      []queuedInput
	steering                    bool
	steerBlocked                bool
	completion                  *protocol.Event
}

func supportsMultiAgentV2(ctx context.Context, opts Options) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, opts.Binary, "features", "list")
	probe.Dir, probe.Env = opts.Cwd, serverEnv()
	out, err := probe.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "multi_agent_v2" && fields[1] == "stable" {
			return true
		}
	}
	return false
}

func Run(opts Options, in io.Reader, out, stderr io.Writer) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []string{"app-server", "--listen", "stdio://"}
	if opts.Coordinate && supportsMultiAgentV2(ctx, opts) {
		args = append(args, "--enable", "multi_agent_v2")
	}
	cmd := exec.CommandContext(ctx, opts.Binary, args...)
	cmd.Dir, cmd.Stderr, cmd.Env = opts.Cwd, stderr, serverEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			cancel()
			<-done
		}
	}()
	b := &bridge{
		opts: opts, toServer: json.NewEncoder(stdin), toQuill: json.NewEncoder(out),
		pending: map[string]func(rpcMessage) error{}, approvals: map[string]approval{},
		streaming: map[string]bool{}, fileChanges: map[string]json.RawMessage{},
	}
	server, quill := lines(ctx, stdout), lines(ctx, in)
	for {
		select {
		case line, ok := <-server:
			if !ok {
				return errors.New("Codex app server exited")
			}
			if line.err != nil {
				return line.err
			}
			var msg rpcMessage
			if err := json.Unmarshal(line.data, &msg); err == nil {
				if err := b.receive(msg); err != nil {
					return err
				}
			}
		case line, ok := <-quill:
			if !ok {
				return nil
			}
			if line.err != nil {
				return line.err
			}
			if ev, err := protocol.ParseLine(line.data); err == nil {
				if err := b.input(ev); err != nil {
					return err
				}
			}
		}
	}
}

type readResult struct {
	data []byte
	err  error
}

func lines(ctx context.Context, r io.Reader) <-chan readResult {
	ch := make(chan readResult)
	go func() {
		defer close(ch)
		s := bufio.NewScanner(r)
		s.Buffer(make([]byte, 64<<10), 64<<20)
		for s.Scan() {
			select {
			case ch <- readResult{data: []byte(s.Text())}:
			case <-ctx.Done():
				return
			}
		}
		if err := s.Err(); err != nil {
			select {
			case ch <- readResult{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}

func (b *bridge) call(method string, params any, reply func(rpcMessage) error) error {
	b.sequence++
	b.pending[strconv.Itoa(b.sequence)] = reply
	return b.toServer.Encode(map[string]any{"id": b.sequence, "method": method, "params": params})
}

func (b *bridge) emit(ev protocol.Event) error {
	return b.toQuill.Encode(ev)
}

func (b *bridge) controlReply(id, message string) error {
	if message != "" {
		return b.toQuill.Encode(json.RawMessage(protocol.ControlErrorLine(id, message)))
	}
	return b.toQuill.Encode(json.RawMessage(protocol.ControlResponseLine(id, map[string]any{})))
}

func (b *bridge) initialize(id string) error {
	init := map[string]any{"clientInfo": map[string]string{"name": "quill", "title": "Quill", "version": "1"}}
	tools := b.tools()
	if len(tools) > 0 || b.opts.ComposerSettings {
		init["capabilities"] = map[string]bool{"experimentalApi": true}
	}
	return b.call("initialize", init, func(msg rpcMessage) error {
		if msg.Error != nil {
			return fmt.Errorf("initialize Codex: %s", msg.Error.Message)
		}
		if err := b.toServer.Encode(map[string]string{"method": "initialized"}); err != nil {
			return err
		}
		method := "thread/start"
		params := map[string]any{"cwd": b.opts.Cwd, "ephemeral": true}
		if b.opts.Instructions != "" {
			params["developerInstructions"] = b.opts.Instructions
		}
		if b.opts.Sandbox != "" {
			params["sandbox"] = b.opts.Sandbox
		}
		if len(tools) > 0 {
			params["dynamicTools"] = tools
		}
		return b.call(method, params, func(msg rpcMessage) error {
			if msg.Error != nil {
				return fmt.Errorf("%s: %s", method, msg.Error.Message)
			}
			var result struct {
				Model, ReasoningEffort string
				Thread                 struct{ ID string }
			}
			if err := json.Unmarshal(msg.Result, &result); err != nil || result.Thread.ID == "" {
				return errors.New("Codex did not return a thread ID")
			}
			b.threadID, b.model, b.ready = result.Thread.ID, result.Model, true
			b.defaultModel, b.defaultEffort = result.Model, result.ReasoningEffort
			if err := b.controlReply(id, ""); err != nil {
				return err
			}
			if err := b.emit(protocol.Event{Type: "system", Subtype: "init", SessionID: b.opts.SessionID, Cwd: b.opts.Cwd, Model: b.model}); err != nil {
				return err
			}
			return b.deliverInput()
		})
	})
}

func (b *bridge) input(ev *protocol.Event) error {
	switch ev.Type {
	case "user":
		if ev.Message == nil {
			return nil
		}
		text := protocol.ContentText(ev.Message.Content)
		if text == "" {
			return nil
		}
		next := queuedInput{text: text, force: b.opts.ForceSteer && ev.Subtype == protocol.CodexSteerNow}
		if b.opts.ComposerSettings {
			next.settings = ev.CodexTurn
		}
		b.queued = append(b.queued, next)
		return b.deliverInput()
	case "control_request":
		if ev.Request == nil {
			return nil
		}
		switch ev.Request.Subtype {
		case "initialize":
			return b.initialize(ev.RequestID)
		case "interrupt":
			if !b.ready && len(b.queued) > 0 {
				b.queued = nil
				if err := b.finish("interrupted", ""); err != nil {
					return err
				}
			}
			if b.starting && b.turnID == "" {
				b.interrupt = ev.RequestID
				return nil
			}
			return b.stopTurn(ev.RequestID)
		case "rename_session":
			var req struct {
				Request struct{ Title string }
			}
			_ = json.Unmarshal(ev.Raw, &req)
			if !b.ready {
				return b.controlReply(ev.RequestID, "Codex thread is still opening")
			}
			return b.call("thread/name/set", map[string]string{"threadId": b.threadID, "name": req.Request.Title}, func(msg rpcMessage) error {
				return b.controlReply(ev.RequestID, rpcError(msg))
			})
		default:
			return b.controlReply(ev.RequestID, "Unavailable for Codex sessions")
		}
	case "control_response":
		return b.answer(ev.Response)
	}
	return nil
}

func rpcError(msg rpcMessage) string {
	if msg.Error != nil {
		return msg.Error.Message
	}
	return ""
}

func (b *bridge) deliverInput() error {
	if b.starting || b.steering {
		return nil
	}
	if b.completion != nil {
		ev := *b.completion
		b.completion = nil
		if len(b.queued) > 0 || b.turnID != "" {
			ev.Subtype = protocol.ResultContinuing
		}
		if err := b.emit(ev); err != nil {
			return err
		}
	}
	if !b.ready || len(b.queued) == 0 {
		return nil
	}
	turn := b.turnID
	next := b.queued[0]
	settingsChanged := next.settings != nil && (b.turnSettings == nil || *next.settings != *b.turnSettings)
	if turn != "" && (settingsChanged || b.steerBlocked || len(b.approvals) != 0 || b.opts.SteerOnlyWhileWaiting && !b.waitingForAgents && !next.force) {
		return nil
	}
	b.queued = b.queued[1:]
	if turn == "" {
		return b.startTurnWithSettings(next.text, next.settings)
	}
	b.steering = true
	return b.call("turn/steer", map[string]any{
		"threadId": b.threadID, "expectedTurnId": turn,
		"input": []map[string]string{{"type": "text", "text": next.text}},
	}, func(msg rpcMessage) error {
		b.steering = false
		if msg.Error != nil {
			// A turn may be finishing or unable to take input. Preserve order
			// and retry as an ordinary message once that turn has finished.
			b.queued = append([]queuedInput{next}, b.queued...)
			b.steerBlocked = b.turnID == turn
		}
		return b.deliverInput()
	})
}

func (b *bridge) startTurn(text string) error {
	return b.startTurnWithSettings(text, nil)
}

func (b *bridge) startTurnWithSettings(text string, options *protocol.CodexTurnSettings) error {
	b.starting = true
	input := []map[string]string{{"type": "text", "text": text}}
	withBrief := !b.briefSent && len(b.opts.KmemCommand) > 0 && b.opts.KmemTopic != ""
	if withBrief {
		// Keep startup context separate from the user message shown in Quill.
		input = []map[string]string{
			{"type": "text", "text": b.kmemBrief()},
			{"type": "text", "text": "\n\nUser message:\n" + text},
		}
	}
	params := map[string]any{"threadId": b.threadID, "input": input}
	model := b.model
	if options != nil {
		model = b.defaultModel
		if options.Model != "" && options.Model != "default" {
			model = options.Model
		}
		effort := options.Effort
		if effort == "" && model == b.defaultModel {
			effort = b.defaultEffort
		}
		if model != "" {
			params["model"] = model
		}
		if effort != "" {
			params["effort"] = effort
		}
		if model != "" && (options.Mode == "plan" || b.turnSettings != nil && b.turnSettings.Mode == "plan") {
			mode := "default"
			if options.Mode == "plan" {
				mode = "plan"
			}
			var reasoning, guidance any
			if effort != "" {
				reasoning = effort
			}
			instructions := b.opts.Instructions
			if options.ModeInstructions != "" {
				instructions = strings.TrimSpace(instructions + "\n" + options.ModeInstructions)
			}
			if instructions != "" {
				guidance = instructions
			}
			params["collaborationMode"] = map[string]any{"mode": mode, "settings": map[string]any{
				"model": model, "reasoning_effort": reasoning, "developer_instructions": guidance,
			}}
		}
	}
	return b.call("turn/start", params, func(msg rpcMessage) error {
		b.starting = false
		if msg.Error != nil {
			if id := b.interrupt; id != "" {
				b.interrupt = ""
				if err := b.controlReply(id, ""); err != nil {
					return err
				}
			}
			return b.finish("failed", msg.Error.Message)
		}
		var result struct{ Turn struct{ ID string } }
		if err := json.Unmarshal(msg.Result, &result); err != nil || result.Turn.ID == "" {
			return errors.New("Codex did not return a turn ID")
		}
		if b.turnID == "" && b.completion == nil {
			b.turnID = result.Turn.ID
		}
		b.turnSettings, b.model = options, model
		b.briefSent = b.briefSent || withBrief
		if id := b.interrupt; id != "" {
			b.interrupt = ""
			if err := b.stopTurn(id); err != nil {
				return err
			}
		}
		return b.deliverInput()
	})
}

func (b *bridge) stopTurn(id string) error {
	if b.turnID == "" {
		return b.controlReply(id, "")
	}
	turn, wasBlocked := b.turnID, b.steerBlocked
	b.steerBlocked = true
	return b.call("turn/interrupt", map[string]string{"threadId": b.threadID, "turnId": turn}, func(msg rpcMessage) error {
		if err := b.controlReply(id, rpcError(msg)); err != nil {
			return err
		}
		if msg.Error != nil && b.turnID == turn {
			b.steerBlocked = wasBlocked
			return b.deliverInput()
		}
		return nil
	})
}

func (b *bridge) receive(msg rpcMessage) error {
	if msg.Method == "" {
		id := string(msg.ID)
		reply := b.pending[id]
		delete(b.pending, id)
		if reply != nil {
			return reply(msg)
		}
		return nil
	}
	if len(msg.ID) > 0 && string(msg.ID) != "null" {
		return b.request(msg)
	}
	return b.notification(msg)
}

func (b *bridge) message(role, id string, blocks []protocol.Block) error {
	content, err := json.Marshal(blocks)
	if err != nil {
		return err
	}
	message, err := json.Marshal(protocol.Message{ID: id, Role: role, Model: b.model, Content: content})
	if err != nil {
		return err
	}
	ev := protocol.Event{
		Type: role, Cwd: b.opts.Cwd, UUID: id, SessionID: b.opts.SessionID,
		Timestamp: time.Now().Format(time.RFC3339Nano), MessageRaw: message,
	}
	return b.emit(ev)
}

func (b *bridge) finish(status, message string) error {
	b.turnID = ""
	b.steerBlocked = false
	for id := range b.activeItems {
		if err := b.activityResult(id, "Activity did not complete", true); err != nil {
			return err
		}
	}
	clear(b.activeItems)
	if err := b.syncAgentWait(); err != nil {
		return err
	}
	// Keep Quill busy while messages already accepted by the bridge remain.
	// A steer reply can still arrive after the completion notification.
	clear(b.approvals)
	clear(b.streaming)
	clear(b.fileChanges)
	ev := protocol.Event{Type: "result", NumTurns: 1}
	switch status {
	case "interrupted":
		ev.TerminalReason = "aborted"
	case "completed":
	default:
		ev.IsError, ev.Result = true, message
		if ev.Result == "" {
			ev.Result = "Codex turn " + status
		}
	}
	b.completion = &ev
	return b.deliverInput()
}

func (b *bridge) notification(msg rpcMessage) error {
	var p struct {
		ThreadID, TurnID, ItemID, Delta string
		Turn                            struct {
			ID, Status string
			Error      *struct{ Message string }
		}
		Changes    json.RawMessage
		Item       activityItem
		TokenUsage *protocol.CodexUsage
	}
	if msg.Method == "serverRequest/resolved" {
		var resolved struct {
			RequestID json.RawMessage
			ThreadID  string
		}
		if json.Unmarshal(msg.Params, &resolved) == nil && resolved.ThreadID == b.threadID {
			id := string(resolved.RequestID)
			if _, ok := b.approvals[id]; ok {
				delete(b.approvals, id)
				if err := b.emit(protocol.Event{Type: "control_cancel_request", RequestID: id}); err != nil {
					return err
				}
				return b.deliverInput()
			}
		}
		return nil
	}
	if json.Unmarshal(msg.Params, &p) != nil || b.threadID == "" {
		return nil
	}
	if p.ThreadID != b.threadID {
		if msg.Method == "turn/completed" && b.workers[p.ThreadID] {
			status := "failed"
			switch p.Turn.Status {
			case "completed":
				status = "completed"
			case "interrupted":
				status = "stopped"
			}
			if err := b.endWorker(p.ThreadID, status); err != nil {
				return err
			}
			return b.emitWorkerActivity(true)
		}
		return nil
	}
	if strings.HasPrefix(msg.Method, "item/") && (b.turnID == "" || p.TurnID != "" && p.TurnID != b.turnID) {
		return nil
	}
	switch msg.Method {
	case "thread/tokenUsage/updated":
		if p.TokenUsage != nil {
			return b.emit(protocol.Event{Type: "codex_token_usage", CodexUsage: p.TokenUsage})
		}
	case "item/agentMessage/delta":
		if !b.streaming[p.ItemID] {
			b.streaming[p.ItemID] = true
			if err := b.emit(protocol.Event{Type: "stream_event", APIMessageID: p.ItemID,
				Event: &protocol.StreamEvent{Type: "content_block_start", ContentBlock: &protocol.Block{Type: "text"}}}); err != nil {
				return err
			}
		}
		return b.emit(protocol.Event{Type: "stream_event", APIMessageID: p.ItemID,
			Event: &protocol.StreamEvent{Type: "content_block_delta", Delta: &protocol.StreamDelta{Type: "text_delta", Text: p.Delta}}})
	case "item/started":
		if p.Item.Type == "fileChange" && hasFileChanges(p.Item.Changes) {
			b.fileChanges[p.Item.ID] = p.Item.Changes
		}
		return b.activity(p.Item, false)
	case "item/fileChange/patchUpdated":
		if hasFileChanges(p.Changes) {
			b.fileChanges[p.ItemID] = p.Changes
		}
	case "item/completed":
		switch p.Item.Type {
		case "agentMessage", "plan":
			delete(b.streaming, p.Item.ID)
			return b.message("assistant", p.Item.ID, []protocol.Block{{Type: "text", Text: p.Item.Text}})
		default:
			return b.activity(p.Item, true)
		}
	case "turn/started":
		if p.Turn.ID != "" && p.Turn.ID != b.announcedTurn && (b.turnID == "" || b.turnID == p.Turn.ID) {
			b.turnID, b.announcedTurn = p.Turn.ID, p.Turn.ID
			return b.emit(protocol.Event{Type: "system", Subtype: protocol.CodexParentTurnStarted})
		}
	case "turn/completed":
		if p.Turn.ID == "" || p.Turn.ID != b.turnID {
			return nil
		}
		message := ""
		if p.Turn.Error != nil {
			message = p.Turn.Error.Message
		}
		return b.finish(p.Turn.Status, message)
	}
	return nil
}

func (b *bridge) request(msg rpcMessage) error {
	if msg.Method == "item/tool/call" {
		return b.dynamicCall(msg)
	}
	var p struct {
		ThreadID, ItemID, Reason string
		Questions                []struct {
			protocol.Question
			ID       string
			IsSecret bool
		}
	}
	if err := json.Unmarshal(msg.Params, &p); err != nil || p.ThreadID == "" || p.ThreadID != b.threadID {
		return b.unsupported(msg)
	}
	a := approval{id: msg.ID}
	req := &protocol.ControlRequest{Subtype: "can_use_tool", ToolUseID: p.ItemID, Input: msg.Params, DecisionReason: p.Reason}
	switch msg.Method {
	case "item/commandExecution/requestApproval":
		req.ToolName = "Codex command"
	case "item/fileChange/requestApproval":
		req.ToolName = "Codex file changes"
		if changes := b.fileChanges[p.ItemID]; len(changes) > 0 {
			var details map[string]any
			if err := json.Unmarshal(msg.Params, &details); err != nil {
				return b.unsupported(msg)
			}
			details["changes"] = changes
			input, err := json.Marshal(details)
			if err != nil {
				return err
			}
			req.Input = input
		}
	case "item/tool/requestUserInput":
		a.questions = map[string]string{}
		qs := make([]protocol.Question, 0, len(p.Questions))
		for _, q := range p.Questions {
			if q.IsSecret || q.ID == "" {
				return b.unsupported(msg)
			}
			if _, exists := a.questions[q.Question.Question]; exists {
				return b.unsupported(msg)
			}
			a.questions[q.Question.Question] = q.ID
			qs = append(qs, q.Question)
		}
		req.ToolName = "AskUserQuestion"
		var err error
		req.Input, err = json.Marshal(map[string]any{"questions": qs})
		if err != nil {
			return err
		}
	default:
		return b.unsupported(msg)
	}
	id := string(msg.ID)
	b.approvals[id] = a
	return b.emit(protocol.Event{Type: "control_request", RequestID: id, Request: req})
}

func (b *bridge) unsupported(msg rpcMessage) error {
	return b.toServer.Encode(map[string]any{"id": msg.ID, "error": map[string]any{
		"code": -32601, "message": "Quill does not support " + msg.Method,
	}})
}

func (b *bridge) answer(response *protocol.ControlResponseBody) error {
	if err := b.respondToApproval(response); err != nil {
		return err
	}
	return b.deliverInput()
}

func (b *bridge) respondToApproval(response *protocol.ControlResponseBody) error {
	if response == nil {
		return nil
	}
	a, ok := b.approvals[response.RequestID]
	if !ok {
		return nil
	}
	delete(b.approvals, response.RequestID)
	var choice struct {
		Behavior           string
		UpdatedInput       struct{ Answers map[string]string }
		UpdatedPermissions []json.RawMessage
	}
	_ = json.Unmarshal(response.Response, &choice)
	if a.run != nil {
		if choice.Behavior == "allow" && len(choice.UpdatedPermissions) == 0 && b.turnID != "" {
			return a.run()
		}
		return b.toolResult(a.id, "", errors.New("The user declined the setting change"))
	}
	var result any
	if a.questions != nil {
		answers := map[string]any{}
		for question, answer := range choice.UpdatedInput.Answers {
			if id, ok := a.questions[question]; ok && strings.TrimSpace(answer) != "" {
				answers[id] = map[string][]string{"answers": {answer}}
			}
		}
		result = map[string]any{"answers": answers}
	} else {
		decision := "decline"
		if choice.Behavior == "allow" {
			decision = "accept"
			if len(choice.UpdatedPermissions) > 0 {
				decision = "acceptForSession"
			}
		}
		result = map[string]string{"decision": decision}
	}
	return b.toServer.Encode(map[string]any{"id": a.id, "result": result})
}
