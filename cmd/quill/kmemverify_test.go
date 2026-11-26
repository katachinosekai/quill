package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func signFixtureKmem(home, bin string) error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	encoded, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return err
	}
	config := filepath.Join(home, ".config", "kmem")
	if err := os.MkdirAll(config, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(config, "release.pub"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}), 0600); err != nil {
		return err
	}
	for _, name := range []string{"kmem", "kmem-guard"} {
		data, err := os.ReadFile(filepath.Join(bin, name))
		if err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(data))
		files := map[string]string{"kmem": digest, "kmem-guard": digest, "kmem-launch": digest, "rules/core.md": digest, "rules/core-details.md": digest}
		raw, err := json.Marshal(map[string]any{"format": "kmem-release-v1", "version": "1.15.4", "layout": "managed", "files": files})
		if err != nil {
			return err
		}
		sidecar := filepath.Join(bin, ".kmem-signatures", digest)
		if err := os.MkdirAll(sidecar, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sidecar, "kmem-release.json"), raw, 0600); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sidecar, "kmem-release.sig"), ed25519.Sign(priv, raw), 0600); err != nil {
			return err
		}
	}
	return nil
}

func TestKmemRuntimeRefusesModifiedModule(t *testing.T) {
	h := newHarness(t)
	h.d.kmemOff = false
	h.d.kmemRun = nil
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	bin := filepath.Join(home, ".local", "bin")
	t.Setenv("PATH", bin)
	installFakeKmem(t, home, "kmem", "kmem-guard")
	entry := filepath.Join(bin, "kmem")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\nprintf SHOULD_NOT_RUN\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := h.d.runKmemRaw("kmem", "version", "--json"); err == nil ||
		!strings.Contains(err.Error(), "signature verification failed") || out != "" {
		t.Fatalf("tampered payload must fail before running: %q %v", out, err)
	}
}
