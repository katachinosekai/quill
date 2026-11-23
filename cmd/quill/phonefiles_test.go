package main

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
)

// A temp home with placeholder files only (kmem rule 22).
type fileFixture struct {
	t                *testing.T
	home, proj, away string
	fr               fileRules
}

func newFileFixture(t *testing.T) *fileFixture {
	root := t.TempDir()
	f := &fileFixture{t: t, home: filepath.Join(root, "home", "user"), away: filepath.Join(root, "elsewhere")}
	f.proj = filepath.Join(f.home, "Projects", "demo")
	for _, d := range []string{f.proj, f.away} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.fr = fileRules{home: f.home, folder: f.proj, extraDen: []string{filepath.Join(root, "quill-config")}}
	return f
}

func (f *fileFixture) write(p string, data []byte) string {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fileFixture) read(p string) (map[string]any, string) {
	f.t.Helper()
	body, _, ferr := readPhoneFile(p, f.fr)
	if ferr != nil {
		return nil, ferr.code
	}
	return body, "ok"
}

func (f *fileFixture) want(p, code string) map[string]any {
	f.t.Helper()
	body, got := f.read(p)
	if got != code {
		f.t.Errorf("%s: got %s, want %s", p, got, code)
	}
	return body
}

func TestPhoneFileAllowedText(t *testing.T) {
	f := newFileFixture(t)
	p := f.write(filepath.Join(f.proj, "main.go"), []byte("package main\n\nfunc main() {}\n"))
	b := f.want(p, "ok")
	if b["kind"] != "text" || b["lang"] != "go" || b["text"] != "package main\n\nfunc main() {}\n" || b["truncated"] != false || b["name"] != "main.go" || b["path"] != p || b["size"] != int64(29) {
		t.Fatalf("text body: %v", b)
	}
	// home is a root too, the session's folder outside home as well
	f.want(f.write(filepath.Join(f.home, "notes", "todo.md"), []byte("- a\n")), "ok")
	f.fr.folder = f.away
	f.want(f.write(filepath.Join(f.away, "x.txt"), []byte("x\n")), "ok")
}

func TestPhoneFileRoots(t *testing.T) {
	f := newFileFixture(t)
	f.want(f.write(filepath.Join(f.away, "x.txt"), []byte("x\n")), "file_denied") // neither root
	f.want("/proc/self/environ", "file_denied")
	f.want("/etc/hostname", "file_denied")
	// a symlink in the project that leaves both roots
	out := f.write(filepath.Join(f.away, "secret-free.txt"), []byte("x\n"))
	link := filepath.Join(f.proj, "out.txt")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	f.want(link, "file_denied")
	// a session folder of "/" is not a root
	f.fr.folder = "/"
	f.want(out, "file_denied")
}

func TestPhoneFileSymlinks(t *testing.T) {
	f := newFileFixture(t)
	key := f.write(filepath.Join(f.home, ".ssh", "id_ed25519"), []byte("placeholder\n"))
	for name, target := range map[string]string{"key-link": key, "dir-link": filepath.Join(f.home, ".ssh")} {
		if err := os.Symlink(target, filepath.Join(f.proj, name)); err != nil {
			t.Fatal(err)
		}
	}
	f.want(filepath.Join(f.proj, "key-link"), "file_denied")
	f.want(filepath.Join(f.proj, "dir-link", "id_ed25519"), "file_denied")
	// a link to an allowed file is served under the name asked for
	real := f.write(filepath.Join(f.proj, "real.txt"), []byte("ok\n"))
	os.Symlink(real, filepath.Join(f.proj, "alias.txt"))
	if b := f.want(filepath.Join(f.proj, "alias.txt"), "ok"); b != nil && b["name"] != "alias.txt" {
		t.Fatalf("alias: %v", b)
	}
	// a loop and a dangling link: not found
	os.Symlink(filepath.Join(f.proj, "loop2"), filepath.Join(f.proj, "loop1"))
	os.Symlink(filepath.Join(f.proj, "loop1"), filepath.Join(f.proj, "loop2"))
	f.want(filepath.Join(f.proj, "loop1"), "not_found")
	os.Symlink(filepath.Join(f.proj, "gone"), filepath.Join(f.proj, "dangling"))
	f.want(filepath.Join(f.proj, "dangling"), "not_found")
	// O_NOFOLLOW: a symlink swapped in after the check is not opened
	if fh, err := openNoFollow(filepath.Join(f.proj, "alias.txt")); err == nil {
		fh.Close()
		t.Fatal("openNoFollow followed a symlink")
	}
}

func TestPhoneFileNotRegular(t *testing.T) {
	f := newFileFixture(t)
	os.MkdirAll(filepath.Join(f.proj, "dir"), 0o755)
	f.want(filepath.Join(f.proj, "dir"), "not_found")
	f.want(filepath.Join(f.proj, "missing.go"), "not_found")
	fifo := filepath.Join(f.proj, "pipe")
	if err := exec.Command("mkfifo", fifo).Run(); err == nil {
		f.want(fifo, "not_found")
	}
}

func TestPhoneFileDenyList(t *testing.T) {
	f := newFileFixture(t)
	for _, d := range denyHomeDirs {
		f.want(f.write(filepath.Join(f.home, d, "inside.txt"), []byte("x\n")), "file_denied")
	}
	for _, p := range denyHomeFiles {
		f.want(f.write(filepath.Join(f.home, p), []byte("{}\n")), "file_denied")
	}
	for _, g := range denyNames {
		name := strings.ReplaceAll(g, "*", "x")
		f.want(f.write(filepath.Join(f.proj, "d", name), []byte("x\n")), "file_denied")
		f.want(f.write(filepath.Join(f.proj, "u", strings.ToUpper(name)), []byte("x\n")), "file_denied")
	}
	for _, id := range []string{"org.mozilla.firefox", "com.google.Chrome", "com.brave.Browser", "org.chromium.Chromium"} {
		f.want(f.write(filepath.Join(f.home, ".var", "app", id, "data", "x.txt"), []byte("x\n")), "file_denied")
	}
	f.want(f.write(filepath.Join(f.home, ".var", "app", "org.example.Editor", "x.txt"), []byte("x\n")), "ok")
	f.want(f.write(filepath.Join(f.proj, ".git", "config"), []byte("[core]\n")), "file_denied")
	f.want(f.write(filepath.Join(f.fr.extraDen[0], "phone", "x.txt"), []byte("x\n")), "file_denied")
	// contents: a fake private key header
	f.want(f.write(filepath.Join(f.proj, "notes.txt"), []byte("hello\n-----BEGIN OPENSSH PRIVATE KEY-----\nplaceholder\n-----END OPENSSH PRIVATE KEY-----\n")), "file_denied")
	f.want(f.write(filepath.Join(f.proj, "cert.txt"), []byte("-----BEGIN CERTIFICATE-----\nplaceholder\n")), "ok")
	// the message names the rule
	if _, _, e := readPhoneFile(filepath.Join(f.home, ".ssh", "inside.txt"), f.fr); e == nil || !strings.Contains(e.msg, "~/.ssh") {
		t.Fatalf("rule in message: %+v", e)
	}
}

func TestPhoneFileUserDenyList(t *testing.T) {
	f := newFileFixture(t)
	f.fr.userDeny = parseUserDeny("# mine\n~/private\n\n" + f.proj + "/build\n*.SQLITE\n")
	if len(f.fr.userDeny) != 3 {
		t.Fatalf("parse: %q", f.fr.userDeny)
	}
	f.want(f.write(filepath.Join(f.home, "private", "a.txt"), []byte("x\n")), "file_denied")
	f.want(f.write(filepath.Join(f.proj, "build", "out.txt"), []byte("x\n")), "file_denied")
	f.want(f.write(filepath.Join(f.proj, "data.sqlite"), []byte("x\n")), "file_denied")
	f.want(f.write(filepath.Join(f.proj, "privateer.txt"), []byte("x\n")), "ok")
}

func TestPhoneFileKinds(t *testing.T) {
	f := newFileFixture(t)
	// text: cut at 256 KiB on a line boundary
	line := strings.Repeat("a", 99) + "\n"
	big := f.write(filepath.Join(f.proj, "big.log"), []byte(strings.Repeat(line, 3000)))
	b := f.want(big, "ok")
	if txt, _ := b["text"].(string); b["truncated"] != true || len(txt) > fileTextMax || !strings.HasSuffix(txt, "\n") || len(txt)%100 != 0 {
		t.Fatalf("cut: truncated=%v len=%d", b["truncated"], len(txt))
	}
	// one long line of 3-byte runes: cut on a rune
	b = f.want(f.write(filepath.Join(f.proj, "one.txt"), []byte(strings.Repeat("€", 100000))), "ok")
	if txt, _ := b["text"].(string); b["truncated"] != true || !strings.HasSuffix(txt, "€") || strings.ContainsRune(txt, '�') {
		t.Fatalf("rune cut: %d", len(txt))
	}
	// UTF-8 edges: a rune cut by the 8 KiB sniff window is text, invalid bytes are binary
	edge := append(bytes.Repeat([]byte("a"), fileSniff-1), []byte("€ ok\n")...)
	f.want(f.write(filepath.Join(f.proj, "edge.txt"), edge), "ok")
	if b := f.want(f.write(filepath.Join(f.proj, "latin1.txt"), []byte("caf\xe9\n")), "ok"); b["kind"] != "binary" {
		t.Fatalf("invalid UTF-8: %v", b)
	}
	// binary: metadata only
	b = f.want(f.write(filepath.Join(f.proj, "prog.bin"), []byte("ELF\x00\x01\x02")), "ok")
	if _, has := b["text"]; b["kind"] != "binary" || has || b["data"] != nil || b["size"] != int64(6) {
		t.Fatalf("binary: %v", b)
	}
	// images: sniffed, base64; over 8 MiB too_large
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...)
	b = f.want(f.write(filepath.Join(f.proj, "shot.png"), png), "ok")
	if data, _ := base64.StdEncoding.DecodeString(b["data"].(string)); b["kind"] != "image" || b["mime"] != "image/png" || !bytes.Equal(data, png) {
		t.Fatalf("image: %v", b["kind"])
	}
	huge := f.write(filepath.Join(f.proj, "huge.png"), png)
	os.Truncate(huge, fileImageMax+1)
	if b = f.want(huge, "ok"); b["kind"] != "too_large" || b["data"] != nil {
		t.Fatalf("too large: %v", b["kind"])
	}
	// a .png that isn't one: text rules; an svg is text
	if b = f.want(f.write(filepath.Join(f.proj, "fake.png"), []byte("not an image\n")), "ok"); b["kind"] != "text" {
		t.Fatalf("fake png: %v", b["kind"])
	}
	if b = f.want(f.write(filepath.Join(f.proj, "icon.svg"), []byte("<svg/>\n")), "ok"); b["kind"] != "text" || b["lang"] != "svg" {
		t.Fatalf("svg: %v", b)
	}
}

func TestPhoneFilePathOf(t *testing.T) {
	home := "/home/user"
	for _, c := range []struct{ in, want, code string }{
		{"/home/user/a/b.go", "/home/user/a/b.go", ""},
		{"~/a/b.go", "/home/user/a/b.go", ""},
		{"file:///home/user/a%20b.go", "/home/user/a b.go", ""},
		{"/home/user/./a//b.go", "/home/user/a/b.go", ""},
		{"", "", "bad_request"},
		{"a/b.go", "", "bad_request"},
		{"/home/user/../root/x", "", "bad_request"},
		{"~/../x", "", "bad_request"},
		{"file:///home/user/%2e%2e/x", "", "bad_request"},
		{"/home/user/a\x00b", "", "bad_request"},
		{"/home/user/\xff", "", "bad_request"},
		{"/" + strings.Repeat("a", filePathMax), "", "bad_request"},
		{"file://host/x/y", "", "bad_request"},
	} {
		got, e := filePathOf(c.in, home)
		code := ""
		if e != nil {
			code = e.code
		}
		if got != c.want || code != c.code {
			t.Errorf("filePathOf(%q) = %q, %s; want %q, %s", c.in, got, code, c.want, c.code)
		}
	}
}

func TestPhoneFileGuard(t *testing.T) {
	g := newFileGuard()
	if !g.begin("p1") || g.begin("p1") || !g.begin("p2") {
		t.Fatal("one file request at a time per phone")
	}
	g.end("p1")
	if !g.begin("p1") {
		t.Fatal("free again after end")
	}
	now := time.Unix(1_800_000_000, 0)
	if g.allow("p1", now) != 0 || g.allow("p1", now.Add(100*time.Millisecond)) == 0 {
		t.Fatal("2 per second")
	}
	at := now
	for i := 1; i < fileRateCount; i++ {
		at = at.Add(time.Second)
		if w := g.allow("p1", at); w != 0 {
			t.Fatalf("request %d refused (%v)", i+1, w)
		}
	}
	if w := g.allow("p1", at.Add(time.Second)); w <= 0 {
		t.Fatal("61st request in 10 minutes allowed")
	}
	if g.allow("p1", now.Add(fileRateWindow+time.Second)) != 0 {
		t.Fatal("window does not slide")
	}
	// bytes: 64 MiB per 10 minutes
	if g.allow("p3", now) != 0 {
		t.Fatal("p3")
	}
	g.sent("p3", fileRateBytes)
	if g.allow("p3", now.Add(time.Second)) == 0 {
		t.Fatal("64 MiB budget")
	}
}

// TestPhoneFileEndpoint: the route over the phone server: hello capability,
// the switch, mentions, phones only, lock and revoke, rate limit, audit.
func TestPhoneFileEndpoint(t *testing.T) {
	phoneDir := filepath.Join(t.TempDir(), "phone")
	t.Setenv("QUILL_PHONE_DIR", phoneDir)
	h := newHarness(t)
	var link string
	h.ui(func() {
		h.d.app.Preferences().SetInt("phonePort", 0)
		if err := h.d.startPhoneServer(); err != nil {
			t.Fatal(err)
		}
		link = h.d.pairLink()
		h.d.open(editsID)
		h.d.open(smokeID)
	})
	defer h.ui(h.d.stopPhone)
	h.eventually("history", func() bool { return h.d.sessions[editsID].loaded && h.d.sessions[smokeID].loaded })
	u, _ := url.Parse(link)
	q := u.Query()
	client := &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	base := "https://127.0.0.1:" + q.Get("p")
	a := unlockedClient(h, &apiClient{t: t, base: base, key: q.Get("k"), c: client})
	phone := q.Get("i")
	gap := fileRateMinGap
	fileRateMinGap = 0
	defer func() { fileRateMinGap = gap }()

	// files in the session's folder (a temp dir; HOME is the test's temp home)
	write := func(rel, text string) string {
		p := filepath.Join(h.proj, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	said := write("said.go", "package said\n")
	tool := write("tool.txt", "from a tool summary\n")
	diff := write("diff.txt", "from a diff\n")
	userOnly := write("user.txt", "only the user said this\n")
	unsaid := write("unsaid.txt", "nobody said this\n")
	envFile := write(".env", "KEY=placeholder\n")
	other := filepath.Join(h.proj, "other.txt")
	write("other.txt", "said in the other session\n")
	h.ui(func() {
		s := h.d.sessions[editsID]
		s.conv.Items = append(s.conv.Items,
			&chat.Item{Kind: chat.Assistant, Text: "I edited `" + said + "` and " + envFile + "."},
			&chat.Item{Kind: chat.Tool, ToolName: "Read", Summary: tool},
			&chat.Item{Kind: chat.Tool, ToolName: "Edit", DiffPath: diff},
			&chat.Item{Kind: chat.User, Text: "look at " + userOnly},
			&chat.Item{Kind: chat.Notice, Text: "notice " + unsaid},
			&chat.Item{Kind: chat.Thinking, Text: "thinking " + unsaid})
		o := h.d.sessions[smokeID]
		o.conv.Items = append(o.conv.Items, &chat.Item{Kind: chat.Assistant, Text: "see " + other})
	})
	get := func(c *apiClient, session, p string) (int, map[string]any, http.Header) {
		return c.do("GET", "/phone/session/"+session+"/file?path="+url.QueryEscape(p), "", nil)
	}

	code, hello, _ := a.do("POST", "/phone/hello", "hello-files-00000001", map[string]string{"name": "Test Pixel", "app": "1.0"})
	if fl, _ := hello["files"].(map[string]any); code != 200 || fl == nil || fl["api"] != float64(1) || fl["on"] != false {
		t.Fatalf("hello files (off by default): %d %v", code, hello)
	}
	if code, m, _ := get(a, editsID, said); code != 403 || errCode(m) != "files_off" {
		t.Fatalf("switch off: %d %v", code, m)
	}

	// turning the switch on: a "files" stream change for this phone only
	_, sess, _ := a.do("GET", "/phone/sessions", "", nil)
	cursor := int64(sess["cursor"].(float64))
	h.ui(func() { h.d.setDeviceFiles(phone, true) })
	_, st, _ := a.do("GET", "/phone/stream?wait=0&cursor="+strconv.FormatInt(cursor, 10), "", nil)
	var change map[string]any
	for _, c := range st["changes"].([]any) {
		if m := c.(map[string]any); m["type"] == "files" {
			change = m
		}
	}
	if fl, _ := change["files"].(map[string]any); fl == nil || fl["on"] != true || change["phone"] != nil {
		t.Fatalf("files change: %v", st)
	}
	_, hello, _ = a.do("POST", "/phone/hello", "hello-files-00000002", map[string]string{"name": "Test Pixel", "app": "1.0"})
	if fl, _ := hello["files"].(map[string]any); fl["on"] != true {
		t.Fatalf("hello files on: %v", hello)
	}

	// mentioned by Claude: text (inline code), tool summary, diff path
	for _, p := range []string{said, tool, diff} {
		if code, m, _ := get(a, editsID, p); code != 200 || m["kind"] != "text" || m["path"] != p {
			t.Fatalf("mentioned %s: %d %v", p, code, m)
		}
	}
	// not mentioned, only by the user, notice/thinking, other session, unknown session: all 404
	for _, c := range []struct{ session, path string }{{editsID, unsaid}, {editsID, userOnly}, {editsID, other}, {smokeID, said}, {"no-such-session", said}, {editsID, filepath.Join(h.proj, "missing.go")}} {
		if code, m, _ := get(a, c.session, c.path); code != 404 || errCode(m) != "not_found" {
			t.Fatalf("%s in %s: %d %v", c.path, c.session, code, m)
		}
	}
	// the other session mentions it, but its root is its own folder (not
	// this one's, and not under the test's home)
	if code, m, _ := get(a, smokeID, other); code != 403 || errCode(m) != "file_denied" {
		t.Fatalf("other session's own mention, outside its folder: %d %v", code, m)
	}
	// mentioned but denied; bad request
	if code, m, _ := get(a, editsID, envFile); code != 403 || errCode(m) != "file_denied" {
		t.Fatalf(".env: %d %v", code, m)
	}
	if code, m, _ := get(a, editsID, h.proj+"/../x"); code != 400 || errCode(m) != "bad_request" {
		t.Fatalf("..: %d %v", code, m)
	}
	// rate limit: two in a row within the minimum gap
	fileRateMinGap = time.Hour
	get(a, editsID, said)
	if code, m, hdr := get(a, editsID, said); code != 429 || errCode(m) != "rate_limited" || hdr.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d %v", code, m)
	}
	fileRateMinGap = 0

	// audit: one line per request, nothing of it in replies
	b, _ := os.ReadFile(filepath.Join(phoneDir, fileAuditName))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 15 || !strings.Contains(lines[0], "\tfiles_off") || !strings.Contains(lines[1], phone+"\t\"Test Pixel\"\t"+editsID) || !strings.HasSuffix(lines[1], "\tok") {
		t.Fatalf("audit (%d lines):\n%s", len(lines), b)
	}
	if got := fileAuditFor(phone, 20); len(got) != 15 {
		t.Fatalf("Devices log: %v", got)
	}

	// a locked device (no unlock token) is refused before the route
	locked := *a
	locked.pw, locked.tok = "", ""
	if code, m, _ := get(&locked, editsID, said); code != 401 || errCode(m) != "locked" {
		t.Fatalf("locked: %d %v", code, m)
	}

	// another Quill Desktop PC paired as a device: never, even with the switch on
	off := false
	pc, pcKey := folders.NewPhone("Other PC", time.Now())
	pc.Kind, pc.Files, pc.Lock = "pc", true, &off
	h.ui(func() { h.d.setPhones(append(h.d.phones(), pc)) })
	pcc := &apiClient{t: t, base: base, key: pcKey, c: client}
	if code, m, _ := get(pcc, editsID, said); code != 403 || errCode(m) != "forbidden" {
		t.Fatalf("remote PC: %d %v", code, m)
	}
	_, pcHello, _ := pcc.do("POST", "/phone/hello", "hello-files-pc-00001", map[string]string{"name": "Other PC", "app": "quill-desktop/1"})
	if fl, _ := pcHello["files"].(map[string]any); fl["on"] != false {
		t.Fatalf("PC hello files: %v", pcHello)
	}

	// revoked: 403 forbidden from the server
	h.ui(func() { h.d.revokePhone(phone) })
	if code, m, _ := get(a, editsID, said); code != 403 || errCode(m) != "forbidden" {
		t.Fatalf("revoked: %d %v", code, m)
	}
}
