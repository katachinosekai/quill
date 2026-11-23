package session

import (
	"slices"
	"strings"
	"testing"
)

func TestChildEnvIdentity(t *testing.T) {
	t.Setenv(SessionIDEnv, "inherited")
	t.Setenv(TokenEnv, "inherited-token")
	old := TokenFor
	t.Cleanup(func() { TokenFor = old })

	TokenFor = nil
	env := Options{SessionID: "s1", Env: []string{TokenEnv + "=forged"}}.childEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, SessionIDEnv+"=") || strings.HasPrefix(kv, TokenEnv+"=") {
			t.Fatalf("no identity without TokenFor, got %q", kv)
		}
	}

	TokenFor = func(id string) string { return "tok-" + id }
	env = Options{Resume: "s2", Env: []string{TokenEnv + "=forged"}}.childEnv()
	var ids, toks []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, SessionIDEnv+"="); ok {
			ids = append(ids, v)
		}
		if v, ok := strings.CutPrefix(kv, TokenEnv+"="); ok {
			toks = append(toks, v)
		}
	}
	if !slices.Equal(ids, []string{"s2"}) || !slices.Equal(toks, []string{"tok-s2"}) {
		t.Fatalf("identity %v %v", ids, toks)
	}
	if (Options{}).identityEnv() != nil {
		t.Fatal("no id, no identity")
	}
}
