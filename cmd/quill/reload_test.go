package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBinWatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "quill")
	old := time.Now().Add(-time.Minute)
	os.WriteFile(p, []byte("\x7fELFold"), 0o755)
	os.Chtimes(p, old, old)
	st, _ := os.Stat(p)
	b := &binWatch{path: p, mod: st.ModTime(), size: st.Size()}
	if b.changed() {
		t.Fatal("unchanged file reported")
	}
	os.WriteFile(p, []byte("\x7fELFnewer"), 0o755)
	if b.changed() {
		t.Fatal("must wait until the file settles")
	}
	os.Chtimes(p, old.Add(time.Second), old.Add(time.Second))
	os.WriteFile(p+".x", nil, 0o644)
	if !b.changed() || b.changed() {
		t.Fatal("settled new build should be reported once")
	}
	os.WriteFile(p, []byte("#!/bin/sh"), 0o755)
	os.Chtimes(p, old, old)
	if b.changed() {
		t.Fatal("non-ELF must be ignored")
	}
}

func TestTakeRestore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`{"session":"abc","draft":"hi","width":900,"height":700,"scrollY":40}`), 0o600)
	t.Setenv(restoreEnv, p)
	st := takeRestore()
	if st == nil || st.Session != "abc" || st.Draft != "hi" || st.Width != 900 || st.ScrollY != 40 {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(p); err == nil || os.Getenv(restoreEnv) != "" {
		t.Fatal("restore file and env should be consumed")
	}
}
