package kmemrelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
}

func TestVerify(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{"portable", "portable-windows", managed} {
		t.Run(layout, func(t *testing.T) {
			dir := t.TempDir()
			exe, guard := "kmem", "kmem-guard"
			if layout == "portable-windows" {
				exe += ".exe"
				guard += ".exe"
			}
			manifestDir := dir
			release := Release{Format: domain, Version: "1.15.4", Layout: layout, Files: make(map[string]string)}
			launcher := []byte("fixture launcher")
			for _, name := range releaseFiles[layout] {
				data := []byte("fixture " + name)
				if layout == managed && name == "kmem-launch" {
					data = launcher
				}
				release.Files[name] = fmt.Sprintf("%x", sha256.Sum256(data))
				if layout != managed {
					writeFixture(t, filepath.Join(dir, name), data)
				}
			}
			if layout == managed {
				manifestDir = filepath.Join(dir, ".kmem-signatures", release.Files["kmem-launch"])
				writeFixture(t, filepath.Join(dir, exe), launcher)
				writeFixture(t, filepath.Join(dir, guard), launcher)
			}
			raw, err := json.Marshal(release)
			if err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(manifestDir, Manifest), raw)
			signature := filepath.Join(manifestDir, Signature)
			writeFixture(t, signature, ed25519.Sign(private, raw))
			check := func(key ed25519.PublicKey) (Release, error) {
				dir, detected, err := installation(filepath.Join(dir, exe))
				if err != nil {
					return Release{}, err
				}
				return verify(dir, detected, key)
			}
			if got, err := check(pub); err != nil || got.Layout != layout || got.Version != release.Version {
				t.Fatalf("verify valid %s: %+v, %v", layout, got, err)
			}
			if _, err := check(other); err == nil {
				t.Fatal("accepted wrong signing key")
			}
			if err := os.Remove(signature); err != nil {
				t.Fatal(err)
			}
			if _, err := check(pub); err == nil {
				t.Fatal("accepted release with missing signature")
			}
			writeFixture(t, signature, ed25519.Sign(private, raw))
			writeFixture(t, filepath.Join(dir, guard), []byte("tampered"))
			if _, err := check(pub); err == nil {
				t.Fatal("accepted tampered guard")
			}
		})
	}
}
