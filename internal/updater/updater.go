package updater

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	Repo        = "katachinosekai/quill"
	PublicKey   = "2MdoGNUUn7Q6TwIf6iryK9l6mxLnTtQ2lKprL4v3eUw="
	apiURL      = "https://api.github.com/repos/" + Repo + "/releases/latest"
	maxJSON     = 2 << 20
	maxSums     = 64 << 10
	maxAsset    = 300 << 20
	userAgent   = "quill-updater"
	sumsName    = "SHA256SUMS"
	sigName     = "SHA256SUMS.sig"
	httpTimeout = 10 * time.Minute
)

var (
	tagRe   = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	digests = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
)

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Name   string  `json:"name"`
	Body   string  `json:"body"`
	Page   string  `json:"html_url"`
	Draft  bool    `json:"draft"`
	Pre    bool    `json:"prerelease"`
	Assets []Asset `json:"assets"`
}

type Kind int

const (
	KindNone Kind = iota
	KindAppImage
	KindRPM
)

func Parse(v string) ([3]int, bool) {
	var out [3]int
	m := tagRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		m = tagRe.FindStringSubmatch("v" + strings.TrimSpace(v))
	}
	if m == nil {
		return out, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil || n > 100000 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func Newer(latest, current string) bool {
	l, ok1 := Parse(latest)
	c, ok2 := Parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func allowedHost(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "github.com" || h == "api.github.com" || strings.HasSuffix(h, ".githubusercontent.com")
}

func Client() *http.Client {
	return &http.Client{
		Timeout: httpTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !allowedHost(req.URL) {
				return fmt.Errorf("refusing redirect to %s", req.URL.Host)
			}
			return nil
		},
	}
}

func get(ctx context.Context, c *http.Client, raw string, limit int64, w io.Writer) error {
	u, err := url.Parse(raw)
	if err != nil || !allowedHost(u) {
		return fmt.Errorf("refusing to download from %q", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u.Host, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("download is larger than allowed")
	}
	return nil
}

func Latest(ctx context.Context, c *http.Client) (*Release, error) {
	var b strings.Builder
	if err := get(ctx, c, apiURL, maxJSON, &b); err != nil {
		return nil, err
	}
	var r Release
	if err := json.Unmarshal([]byte(b.String()), &r); err != nil {
		return nil, err
	}
	if r.Draft || r.Pre {
		return nil, errors.New("latest release is not a final release")
	}
	if _, ok := Parse(r.Tag); !ok {
		return nil, fmt.Errorf("unexpected release tag %q", r.Tag)
	}
	return &r, nil
}

func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

func (r *Release) Package(k Kind) (Asset, error) {
	ver := strings.TrimPrefix(r.Tag, "v")
	var want *regexp.Regexp
	switch k {
	case KindAppImage:
		want = regexp.MustCompile(`^Quill-` + regexp.QuoteMeta(ver) + `-x86_64\.AppImage$`)
	case KindRPM:
		want = regexp.MustCompile(`^quill-` + regexp.QuoteMeta(ver) + `-\d+\.fc\d+\.x86_64\.rpm$`)
	default:
		return Asset{}, errors.New("this install can't update itself")
	}
	for _, a := range r.Assets {
		if want.MatchString(a.Name) {
			if a.Size <= 0 || a.Size > maxAsset {
				return Asset{}, errors.New("package size is out of range")
			}
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s has no package for this install", r.Tag)
}

func ParseSums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || len(f[0]) != 64 {
			return nil, errors.New("malformed SHA256SUMS")
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			return nil, errors.New("malformed SHA256SUMS")
		}
		name := strings.TrimPrefix(f[1], "*")
		if name != filepath.Base(name) {
			return nil, errors.New("malformed SHA256SUMS")
		}
		out[name] = f[0]
	}
	return out, nil
}

func Verify(sums, sig []byte, pubKey string) error {
	pub, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("bad built-in public key")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return errors.New("bad release signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), sums, s) {
		return errors.New("release signature does not match Quill's signing key")
	}
	return nil
}

func (r *Release) SignedSums(ctx context.Context, c *http.Client) (map[string]string, error) {
	sa, ok1 := r.asset(sumsName)
	ga, ok2 := r.asset(sigName)
	if !ok1 || !ok2 {
		return nil, errors.New("release is not signed (no SHA256SUMS.sig); refusing to update")
	}
	var sums, sig strings.Builder
	if err := get(ctx, c, sa.URL, maxSums, &sums); err != nil {
		return nil, err
	}
	if err := get(ctx, c, ga.URL, 4096, &sig); err != nil {
		return nil, err
	}
	if err := Verify([]byte(sums.String()), []byte(sig.String()), PublicKey); err != nil {
		return nil, err
	}
	return ParseSums([]byte(sums.String()))
}

func Download(ctx context.Context, c *http.Client, a Asset, want string, dir string, progress func(float64)) (string, error) {
	if m := digests.FindStringSubmatch(a.Digest); m == nil || m[1] != want {
		return "", errors.New("GitHub's checksum for the package doesn't match the signed checksum")
	}
	f, err := os.CreateTemp(dir, ".quill-update-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(name)
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{total: a.Size, fn: progress}
	if err := get(ctx, c, a.URL, maxAsset, io.MultiWriter(f, h, pw)); err != nil {
		return fail(err)
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fail(errors.New("downloaded package doesn't match the signed checksum"))
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

type progressWriter struct {
	total, done int64
	fn          func(float64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.fn != nil && p.total > 0 {
		p.fn(float64(p.done) / float64(p.total))
	}
	return len(b), nil
}

func InstallKind(exe string) (Kind, string) {
	if ai := os.Getenv("APPIMAGE"); ai != "" && filepath.IsAbs(ai) {
		if st, err := os.Stat(ai); err == nil && st.Mode().IsRegular() {
			return KindAppImage, ai
		}
	}
	if strings.HasPrefix(exe, "/usr/libexec/quill/") {
		return KindRPM, ""
	}
	return KindNone, ""
}

func ReplaceAppImage(target, downloaded string) error {
	if err := os.Chmod(downloaded, 0o755); err != nil {
		return err
	}
	if filepath.Dir(downloaded) != filepath.Dir(target) {
		return errors.New("download must be next to the AppImage")
	}
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Link(target, backup); err != nil {
		_ = os.Rename(target, backup)
	}
	return os.Rename(downloaded, target)
}
