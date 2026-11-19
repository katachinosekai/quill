package account

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func fake(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "claude")
	os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755)
	return p
}

func TestGet(t *testing.T) {
	bin := fake(t, `echo '{"loggedIn":true,"email":"a@b.c","subscriptionType":"pro"}'`)
	s, err := Get(bin)
	if err != nil || !s.LoggedIn || s.Email != "a@b.c" || s.Subscription != "pro" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = Get(fake(t, `echo '{"loggedIn":false}'; exit 1`))
	if err != nil || s.LoggedIn {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestLoginURL(t *testing.T) {
	bin := fake(t, `echo "Opening browser..."; echo "If it did not open: https://example.test/oauth?x=1 "; echo again https://example.test/oauth?x=1`)
	var urls []string
	if err := Login(context.Background(), bin, func(u string) { urls = append(urls, u) }); err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0] != "https://example.test/oauth?x=1" {
		t.Fatalf("%q", urls)
	}
}
