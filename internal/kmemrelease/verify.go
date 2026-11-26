package kmemrelease

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	Manifest   = "kmem-release.json"
	Signature  = "kmem-release.sig"
	domain     = "kmem-release-v1"
	managed    = "managed"
	maxPayload = 150 * 1024 * 1024
)

// Set by the release build, independently of the portable payload.
var portablePublicKey string

var versionName = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

var releaseFiles = map[string][]string{
	managed:            {"kmem", "kmem-guard", "kmem-launch", "rules/core.md", "rules/core-details.md"},
	"portable":         {"kmem", "kmem-guard", "rules/core.md", "rules/core-details.md"},
	"portable-windows": {"kmem.exe", "kmem-guard.exe", "rules/core.md", "rules/core-details.md"},
}

type Release struct {
	Format  string            `json:"format"`
	Version string            `json:"version"`
	Layout  string            `json:"layout"`
	Files   map[string]string `json:"files"`
}

func parsePublicKey(raw []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected a single Ed25519 public PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("expected an Ed25519 public key")
	}
	return key, nil
}

func installation(path string) (dir, layout string, err error) {
	if !filepath.IsAbs(path) {
		return "", "", errors.New("expected an absolute kmem path")
	}
	dir, err = filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", "", err
	}
	switch strings.ToLower(filepath.Base(path)) {
	case "kmem.exe":
		return dir, "portable-windows", nil
	case "kmem":
		_, manifestErr := os.Lstat(filepath.Join(dir, Manifest))
		_, signatureErr := os.Lstat(filepath.Join(dir, Signature))
		if errors.Is(manifestErr, os.ErrNotExist) && errors.Is(signatureErr, os.ErrNotExist) {
			return dir, managed, nil
		}
		return dir, "portable", nil
	default:
		return "", "", errors.New("unexpected kmem executable name")
	}
}

// VerifyInstalled checks portable bytes against the build pin. A managed
// install uses a separately provisioned key shared with its signed launcher.
func VerifyInstalled(path string) (Release, error) {
	dir, layout, err := installation(path)
	if err != nil {
		return Release{}, err
	}
	if layout != managed {
		key, err := base64.StdEncoding.Strict().DecodeString(portablePublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return Release{}, errors.New("this Quill build has no trusted key for portable kmem")
		}
		return verify(dir, layout, ed25519.PublicKey(key))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Release{}, err
	}
	config, err := filepath.EvalSymlinks(filepath.Join(home, ".config", "kmem"))
	if err != nil {
		return Release{}, fmt.Errorf("read ~/.config/kmem/release.pub: %w", err)
	}
	raw, err := readFile(config, "release.pub", 4096)
	if err != nil {
		return Release{}, fmt.Errorf("read ~/.config/kmem/release.pub: %w", err)
	}
	key, err := parsePublicKey(raw)
	if err != nil {
		return Release{}, fmt.Errorf("read ~/.config/kmem/release.pub: %w", err)
	}
	return verify(dir, layout, key)
}

func verify(dir, layout string, key ed25519.PublicKey) (Release, error) {
	if layout == managed {
		return verifyManaged(dir, key)
	}
	release, err := readManifest(dir, layout, key)
	if err != nil {
		return Release{}, err
	}
	for _, name := range releaseFiles[layout] {
		digest, err := fileDigest(dir, name, maxPayload)
		if err != nil {
			return Release{}, err
		}
		if digest != release.Files[name] {
			return Release{}, fmt.Errorf("kmem file %q does not match the signed manifest", name)
		}
	}
	return release, nil
}

func verifyManaged(dir string, key ed25519.PublicKey) (Release, error) {
	var release Release
	for _, name := range []string{"kmem-guard", "kmem"} {
		digest, err := fileDigest(dir, name, 1024*1024)
		if err != nil {
			return Release{}, err
		}
		release, err = readManifest(filepath.Join(dir, ".kmem-signatures", digest), managed, key)
		if err != nil {
			return Release{}, err
		}
		if release.Files["kmem-launch"] != digest {
			return Release{}, fmt.Errorf("kmem launcher %q does not match the signed manifest", name)
		}
	}
	return release, nil
}

func readManifest(dir, layout string, key ed25519.PublicKey) (Release, error) {
	raw, err := readFile(dir, Manifest, 16*1024)
	if err != nil {
		return Release{}, err
	}
	signature, err := readFile(dir, Signature, ed25519.SignatureSize)
	if err != nil {
		return Release{}, err
	}
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, raw, signature) {
		return Release{}, errors.New("invalid kmem release signature")
	}
	var release Release
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&release) != nil || dec.Decode(new(any)) != io.EOF || release.Format != domain ||
		release.Layout != layout || !versionName.MatchString(release.Version) {
		return Release{}, errors.New("invalid kmem release manifest")
	}
	names := releaseFiles[layout]
	if len(release.Files) != len(names) {
		return Release{}, errors.New("invalid kmem release file list")
	}
	for _, name := range names {
		sum := release.Files[name]
		decoded, err := hex.DecodeString(sum)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != sum {
			return Release{}, fmt.Errorf("invalid signed digest for kmem file %q", name)
		}
	}
	return release, nil
}

func openRegular(dir, name string) (*os.File, error) {
	path := filepath.Join(dir, filepath.FromSlash(name))
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if resolved != path || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("kmem file %q must be regular and not a symbolic link", name)
	}
	return os.Open(path)
}

func readFile(dir, name string, limit int64) ([]byte, error) {
	f, err := openRegular(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("kmem file %q is too large", name)
	}
	return raw, nil
}

func fileDigest(dir, name string, limit int64) (string, error) {
	f, err := openRegular(dir, name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if size > limit {
		return "", fmt.Errorf("kmem file %q is too large", name)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
