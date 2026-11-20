package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersions(t *testing.T) {
	cases := []struct {
		latest, cur string
		want        bool
	}{
		{"v0.1.2", "0.1.1", true},
		{"v0.2.0", "0.1.9", true},
		{"v1.0.0", "0.9.9", true},
		{"v0.1.1", "0.1.1", false},
		{"v0.1.0", "0.1.1", false},
		{"v0.1.2-rc1", "0.1.1", false},
		{"0.1.2; rm -rf", "0.1.1", false},
		{"v0.1.2", "dev", false},
	}
	for _, c := range cases {
		if Newer(c.latest, c.cur) != c.want {
			t.Errorf("Newer(%q, %q) != %v", c.latest, c.cur, c.want)
		}
	}
}

func TestSignatureChecks(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pk := base64.StdEncoding.EncodeToString(pub)
	sums := []byte(strings.Repeat("a", 64) + "  quill-0.1.2-1.fc44.x86_64.rpm\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)))
	if Verify(sums, sig, pk) != nil {
		t.Fatal("a valid signature verifies")
	}
	tampered := append([]byte{}, sums...)
	tampered[0] = 'b'
	if Verify(tampered, sig, pk) == nil {
		t.Fatal("changed checksums must fail")
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	forged := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(other, sums)))
	if Verify(sums, forged, pk) == nil {
		t.Fatal("a signature from another key must fail")
	}
	if Verify(sums, forged, PublicKey) == nil {
		t.Fatal("the built-in key rejects foreign signatures")
	}
	m, err := ParseSums(sums)
	if err != nil || m["quill-0.1.2-1.fc44.x86_64.rpm"] == "" {
		t.Fatal("sums parse")
	}
	if _, err := ParseSums([]byte(strings.Repeat("a", 64) + "  ../../etc/passwd\n")); err == nil {
		t.Fatal("paths in SHA256SUMS are refused")
	}
}

func TestUnsignedReleaseRefused(t *testing.T) {
	r := &Release{Tag: "v0.1.2", Assets: []Asset{{Name: "quill-0.1.2-1.fc44.x86_64.rpm", Size: 10, URL: "https://github.com/x"}}}
	if _, err := r.SignedSums(context.Background(), Client()); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("unsigned releases are refused: %v", err)
	}
}

func TestPackagePickAndHosts(t *testing.T) {
	r := &Release{Tag: "v0.1.2", Assets: []Asset{
		{Name: "quill-0.1.2-1.fc44.x86_64.rpm", Size: 100},
		{Name: "Quill-0.1.2-x86_64.AppImage", Size: 100},
		{Name: "Quill-0.1.3-x86_64.AppImage", Size: 100},
	}}
	if a, err := r.Package(KindAppImage); err != nil || a.Name != "Quill-0.1.2-x86_64.AppImage" {
		t.Fatal("picks the AppImage for the release's own version")
	}
	if a, err := r.Package(KindRPM); err != nil || !strings.HasSuffix(a.Name, ".rpm") {
		t.Fatal("picks the RPM")
	}
	if _, err := r.Package(KindNone); err == nil {
		t.Fatal("dev builds don't update")
	}
	for raw, ok := range map[string]bool{
		"https://github.com/x":                    true,
		"https://objects.githubusercontent.com/x": true,
		"http://github.com/x":                     false,
		"https://github.com.evil.example/x":       false,
		"https://evilgithubusercontent.com/x":     false,
	} {
		u, _ := url.Parse(raw)
		if allowedHost(u) != ok {
			t.Errorf("allowedHost(%s) != %v", raw, ok)
		}
	}
}

func TestDigestMismatchStopsBeforeDownload(t *testing.T) {
	a := Asset{Name: "x", URL: "https://github.com/x", Size: 1, Digest: "sha256:" + strings.Repeat("b", 64)}
	if _, err := Download(context.Background(), Client(), a, strings.Repeat("a", 64), t.TempDir(), nil); err == nil {
		t.Fatal("GitHub's digest must match the signed checksum")
	}
}

func TestReplaceAppImage(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Quill.AppImage")
	os.WriteFile(target, []byte("old"), 0o755)
	dl := filepath.Join(dir, ".quill-update-1")
	os.WriteFile(dl, []byte("new"), 0o600)
	if err := ReplaceAppImage(target, dl); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(target)
	st, _ := os.Stat(target)
	old, _ := os.ReadFile(target + ".old")
	if string(b) != "new" || st.Mode().Perm()&0o100 == 0 || string(old) != "old" {
		t.Fatal("AppImage is swapped, executable, and the old one kept")
	}
}
