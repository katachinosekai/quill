package account

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlan(t *testing.T) {
	cases := map[Status]string{
		{Subscription: "pro"}:                                 "Pro",
		{Subscription: "pro", Tier: "default_claude_max_5x"}:  "Max 5x",
		{Subscription: "max", Tier: "default_claude_max_20x"}: "Max 20x",
		{}: "",
	}
	for s, want := range cases {
		if got := s.Plan(); got != want {
			t.Errorf("%+v: got %q want %q", s, got, want)
		}
	}
}

func TestProfileTier(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude.json")
	os.WriteFile(p, []byte(`{"oauthAccount":{"organizationRateLimitTier":"default_claude_max_5x","userRateLimitTier":null}}`), 0o600)
	if got := profileTier(p); got != "default_claude_max_5x" {
		t.Fatalf("got %q", got)
	}
}
