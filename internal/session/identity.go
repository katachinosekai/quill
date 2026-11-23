package session

import (
	"os"
	"strings"
)

// Session identity for Quill Desktop's local socket (the settings command,
// docs/design/quill-operator.md §3.8). Every Claude process Quill Desktop
// starts gets its session id and a token that only this launch of Quill
// Desktop can make (TokenFor); the commands the session runs inherit both,
// so `quill settings set` can say which session asks. Values inherited from
// Quill Desktop's own environment are never passed on.
const (
	SessionIDEnv = "QUILL_SESSION_ID"
	TokenEnv     = "QUILL_SESSION_TOKEN"
)

// TokenFor, when set, makes the token of a session id (set once at start).
var TokenFor func(id string) string

// identityEnv: the session id and token entries for o ("" when TokenFor is
// unset or o has no id).
func (o Options) identityEnv() []string {
	id := o.SessionID
	if id == "" {
		id = o.Resume
	}
	if TokenFor == nil || id == "" {
		return nil
	}
	tok := TokenFor(id)
	if tok == "" {
		return nil
	}
	return []string{SessionIDEnv + "=" + id, TokenEnv + "=" + tok}
}

// childEnv: the full environment of o's Claude process. An identity
// inherited from Quill Desktop's own environment (started from a session's
// shell) or put in o.Env is dropped; only identityEnv sets one.
func (o Options) childEnv() []string {
	env := append(append(os.Environ(), LocalEnv(o.Model)...), o.Env...)
	var own []string
	for _, kv := range env {
		if strings.HasPrefix(kv, SessionIDEnv+"=") || strings.HasPrefix(kv, TokenEnv+"=") {
			continue
		}
		own = append(own, kv)
	}
	return append(own, o.identityEnv()...)
}
