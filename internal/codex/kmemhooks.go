package codex

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/katachinosekai/quill/internal/protocol"
)

// KmemHookStatus contains the specific definitions Quill uses, not the
// provider's commands, paths, hashes or unrelated hook configuration.
type KmemHookStatus struct {
	Thread string
	Guard  string
	Lock   string
	Brief  string
}

func (b *bridge) readKmemHooks(requestID string) error {
	if !b.ready || b.opts.KmemTopic == "" {
		return b.controlReply(requestID, "Codex kmem thread is not ready")
	}
	return b.call("hooks/list", map[string]any{"cwds": []string{b.opts.Cwd}}, func(msg rpcMessage) error {
		if msg.Error != nil {
			return b.controlReply(requestID, rpcError(msg))
		}
		status, err := parseKmemHooks(msg.Result, b.opts.Cwd)
		if err != nil {
			return b.controlReply(requestID, "Codex could not read the workspace hooks")
		}
		status.Thread = b.threadID
		return b.toQuill.Encode(json.RawMessage(protocol.ControlResponseLine(requestID, status)))
	})
}

type hookDef struct {
	EventName, Command, Matcher, TrustStatus string
	Enabled, IsManaged                       bool
}

func parseKmemHooks(raw json.RawMessage, cwd string) (KmemHookStatus, error) {
	s := KmemHookStatus{Guard: "missing", Lock: "missing", Brief: "missing"}
	var data struct {
		Data []struct {
			Cwd   string
			Hooks []hookDef
		}
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return s, err
	}
	for _, workspace := range data.Data {
		if filepath.Clean(workspace.Cwd) != filepath.Clean(cwd) {
			continue
		}
		for _, hook := range workspace.Hooks {
			var dest *string
			switch kind := nativeKmemHook(hook); kind {
			case "guard":
				dest = &s.Guard
			case "lock":
				dest = &s.Lock
			case "brief":
				dest = &s.Brief
			default:
				continue
			}
			switch {
			case hook.Enabled && (hook.TrustStatus == "trusted" || hook.IsManaged):
				*dest = "ready"
			case hook.Enabled && *dest != "ready":
				*dest = "review"
			case *dest == "missing":
				*dest = "disabled"
			}
		}
	}
	return s, nil
}

func nativeKmemHook(h hookDef) string {
	parts := strings.Fields(h.Command)
	if len(parts) == 0 {
		return ""
	}
	base := func(word string) string {
		word = strings.Trim(word, "\"'")
		return strings.TrimSuffix(filepath.Base(strings.ReplaceAll(word, "\\", "/")), ".exe")
	}
	if p := base(parts[0]); (p == "py" || strings.HasPrefix(p, "python")) && len(parts) > 1 {
		parts = parts[1:]
	}
	switch base(parts[0]) {
	case "kmem-guard":
		if h.EventName == "preToolUse" {
			match, err := regexp.Compile(h.Matcher)
			if h.Matcher == "*" || err == nil && match.MatchString("Bash") {
				return "guard"
			}
		}
	case "pzlock":
		if h.EventName == "sessionStart" && len(parts) > 1 && parts[1] == "ensure" {
			return "lock"
		}
	case "kmem":
		if h.EventName == "sessionStart" && len(parts) > 1 && parts[1] == "brief" {
			return "brief"
		}
	}
	return ""
}

func kmemTopicEnvironment(env []string, topic string) []string {
	if topic == "" {
		return env
	}
	var result []string
	for _, item := range env {
		if !strings.HasPrefix(item, "KMEM_SESSION=") {
			result = append(result, item)
		}
	}
	return append(result, "KMEM_SESSION="+topic)
}
