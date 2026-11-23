package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
)

// GET /phone/session/{id}/file?path=... (docs/design/phone-links.md §6-7,
// decisions f2ab8af): a phone reads one file that Claude mentioned in that
// session. Every rule holds here; Quill Android's checks are display only.

const (
	filesPhoneAPI = 1         // hello "files.api"
	fileTextMax   = 256 << 10 // text sent, cut at a line boundary
	fileImageMax  = 8 << 20   // larger images: too_large
	filePathMax   = 4096
	fileSniff     = 8 << 10 // text: valid UTF-8, no NUL in the first 8 KiB

	fileAuditName = "phone-files.log"
	fileAuditKeep = 2000

	prefPhoneFilesDeny = "phoneFilesDeny" // the user's own deny list, one entry per line
)

// Rate limits per phone (§7.7).
var (
	fileRateMinGap = 500 * time.Millisecond // 2 per second
	fileRateWindow = 10 * time.Minute
	fileRateCount  = 60
	fileRateBytes  = int64(64 << 20)
)

// fileErr is a refusal: HTTP status, error code, message.
type fileErr struct {
	status    int
	code, msg string
}

var errFileNotFound = &fileErr{http.StatusNotFound, "not_found", "Quill Desktop can't find this file"}

func badFile(msg string) *fileErr { return &fileErr{http.StatusBadRequest, "bad_request", msg} }
func deniedFile(rule string) *fileErr {
	return &fileErr{http.StatusForbidden, "file_denied", "Quill Desktop doesn't open this file on phones: " + rule}
}

// ---- deny list (§7.2) ----

// denyHomeDirs: folders under home that are never served.
var denyHomeDirs = []string{
	".ssh", ".gnupg", ".aws", ".azure", ".kube", ".docker", ".config/gcloud", ".config/gh", ".password-store",
	".local/share/keyrings", ".pki",
	// browser and mail profiles
	".mozilla", ".thunderbird", ".librewolf", ".waterfox", ".config/google-chrome", ".config/google-chrome-beta",
	".config/chromium", ".config/BraveSoftware", ".config/vivaldi", ".config/microsoft-edge", ".config/opera",
	// kmem's data (kmem has its own phone routes) and Quill Desktop's own
	// preferences, phone keys and TLS key
	".local/share/kmem", ".config/kmem", ".local/state/kmem", ".config/quill", ".config/fyne/" + appID,
}

// denyHomeFiles: single files under home.
var denyHomeFiles = []string{".claude/.credentials.json"}

// denyFlatpak: ~/.var/app/<id> of browsers (and mail), by part of the id.
var denyFlatpak = []string{"firefox", "mozilla", "chrom", "brave", "vivaldi", "edge", "opera", "librewolf", "torbrowser", "zen", "waterfox", "floorp", "thunderbird"}

// denyNames: file names (case-insensitive globs).
var denyNames = []string{
	".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*", ".netrc", ".pgpass", ".git-credentials", ".npmrc", ".pypirc",
	"*credentials*", "*secret*", "*token*", "local.properties",
}

// systemDirs are never served, even under an allowed root; under runDir
// only the current user's mounted drives (runDir/media/<user>) are allowed
// (user decision 2026-11-23: projects live on mounted drives).
var (
	systemDirs = []string{"/proc", "/sys", "/dev"}
	runDir     = "/run"
	mediaUser  = currentUserName // tests: a fixed name
)

func currentUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

// systemFolder: the rule p (clean, absolute) falls under, "" = none.
func systemFolder(p string) string {
	for _, s := range systemDirs {
		if under(p, s) {
			return "system folder " + s
		}
	}
	if !under(p, runDir) {
		return ""
	}
	name := mediaUser()
	if name != "" && !strings.ContainsAny(name, "/\\") && name != "." && name != ".." {
		media := filepath.Join(runDir, "media", name)
		if under(p, media) && p != media {
			return ""
		}
	}
	return "system folder " + runDir
}

var privateKeyRe = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)

// fileRules: what one request may read.
type fileRules struct {
	home     string   // the user's home
	folder   string   // the session's folder
	extraDen []string // more denied folders (Quill Desktop's config, preferences)
	userDeny []string // the user's own entries (Quill Desktop → Devices)
}

func under(p, dir string) bool {
	if dir == "" {
		return false
	}
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// resolveDir: dir with symlinks resolved when it exists.
func resolveDir(dir string) string {
	if dir == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return filepath.Clean(dir)
}

func tildeRule(rel string) string { return "~/" + rel }

// denied returns the rule p falls under ("" = none); p is clean and absolute.
func (fr fileRules) denied(p string) string {
	for _, home := range uniq(filepath.Clean(fr.home), resolveDir(fr.home)) {
		if home == "" || home == "/" {
			continue
		}
		for _, d := range denyHomeDirs {
			if under(p, filepath.Join(home, d)) {
				return tildeRule(d)
			}
		}
		for _, f := range denyHomeFiles {
			if p == filepath.Join(home, f) {
				return tildeRule(f)
			}
		}
		apps := filepath.Join(home, ".var", "app")
		if under(p, apps) && p != apps {
			id := strings.ToLower(strings.SplitN(strings.TrimPrefix(p, apps+string(filepath.Separator)), string(filepath.Separator), 2)[0])
			for _, b := range denyFlatpak {
				if strings.Contains(id, b) {
					return "browser profile (~/.var/app/" + id + ")"
				}
			}
		}
	}
	for _, d := range fr.extraDen {
		for _, dd := range uniq(filepath.Clean(d), resolveDir(d)) {
			if dd != "" && dd != "/" && under(p, dd) {
				return "Quill Desktop's own settings"
			}
		}
	}
	for _, seg := range strings.Split(p, string(filepath.Separator)) {
		if seg == ".git" {
			return ".git internals"
		}
	}
	name := strings.ToLower(filepath.Base(p))
	for _, g := range denyNames {
		if ok, _ := filepath.Match(g, name); ok {
			return "file name " + g
		}
	}
	for _, e := range fr.userDeny {
		if strings.HasPrefix(e, "/") || strings.HasPrefix(e, "~/") {
			ep := filepath.Clean(homeJoin(e, fr.home))
			for _, dd := range uniq(ep, resolveDir(ep)) {
				if under(p, dd) {
					return "your deny list (" + e + ")"
				}
			}
		} else if ok, _ := filepath.Match(strings.ToLower(e), name); ok {
			return "your deny list (" + e + ")"
		}
	}
	return ""
}

func uniq(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

func homeJoin(p, home string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// parseUserDeny: the user's deny list text, one entry per line; blank lines
// and lines starting with # are skipped.
func parseUserDeny(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// ---- the path a phone asks for ----

// filePathOf checks the requested path (as the phone found it: with ~, or a
// file:// URL) and returns it expanded and cleaned.
func filePathOf(raw, home string) (string, *fileErr) {
	switch {
	case raw == "":
		return "", badFile("path is empty")
	case len(raw) > filePathMax:
		return "", badFile("path is too long")
	case strings.IndexByte(raw, 0) >= 0 || !utf8.ValidString(raw):
		return "", badFile("path is not valid text")
	}
	p := raw
	if strings.HasPrefix(strings.ToLower(p), "file://") {
		fp, ok := fileURLPath(p)
		if !ok {
			return "", badFile("not a local file URL")
		}
		p = fp
	}
	// A .. segment is refused, never cleaned away (§7.3).
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return "", badFile("path must not contain ..")
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home == "" {
			return "", errFileNotFound
		}
		p = filepath.Join(home, p[2:])
	} else if !strings.HasPrefix(p, "/") {
		return "", badFile("path must be absolute or start with ~/")
	}
	if strings.IndexByte(p, 0) >= 0 {
		return "", badFile("path is not valid text")
	}
	return filepath.Clean(p), nil
}

// ---- reading ----

var imageExts = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp"}

var fileLangs = map[string]string{
	".go": "go", ".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".py": "python", ".js": "js", ".mjs": "js", ".cjs": "js",
	".jsx": "js", ".ts": "ts", ".tsx": "ts", ".sh": "sh", ".bash": "sh", ".zsh": "sh", ".json": "json", ".yaml": "yaml",
	".yml": "yaml", ".toml": "toml", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".hpp": "cpp", ".md": "markdown",
	".markdown": "markdown", ".lua": "lua", ".rs": "rust", ".rb": "ruby", ".html": "html", ".htm": "html", ".css": "css",
	".xml": "xml", ".svg": "svg", ".sql": "sql", ".gradle": "gradle", ".txt": "text", ".log": "text", ".ini": "ini",
	".properties": "properties", ".mod": "gomod", ".diff": "diff", ".patch": "diff",
}

// readPhoneFile applies §7.1-7.3 to the clean absolute path p and reads it.
// It returns the reply body, or a refusal; size is the file size for the
// audit (0 when unknown).
func readPhoneFile(p string, fr fileRules) (body map[string]any, size int64, ferr *fileErr) {
	if rule := fr.denied(p); rule != "" {
		return nil, 0, deniedFile(rule)
	}
	if rule := systemFolder(p); rule != "" {
		return nil, 0, deniedFile(rule)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, 0, errFileNotFound
	}
	if rule := fr.denied(real); rule != "" {
		return nil, 0, deniedFile(rule)
	}
	if rule := systemFolder(real); rule != "" {
		return nil, 0, deniedFile(rule)
	}
	inRoot := false
	for _, root := range []string{fr.home, fr.folder} {
		if root == "" || filepath.Clean(root) == "/" {
			continue
		}
		for _, r := range uniq(filepath.Clean(root), resolveDir(root)) {
			inRoot = inRoot || under(real, r) && real != r
		}
	}
	if !inRoot {
		return nil, 0, deniedFile("outside the session's folder and your home folder")
	}
	lst, err := os.Lstat(real)
	if err != nil || !lst.Mode().IsRegular() {
		return nil, 0, errFileNotFound
	}
	f, err := openNoFollow(real)
	if err != nil {
		return nil, 0, errFileNotFound
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(lst, st) {
		return nil, 0, errFileNotFound
	}
	size = st.Size()
	body = map[string]any{"v": 1, "path": p, "name": filepath.Base(p), "size": size, "mtimeMs": st.ModTime().UnixMilli()}
	ext := strings.ToLower(filepath.Ext(p))
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if mime, ok := imageExts[ext]; ok && http.DetectContentType(head) == mime {
		if size > fileImageMax {
			body["kind"] = "too_large"
			return body, size, nil
		}
		data, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(head), f), fileImageMax+1))
		if err != nil {
			return nil, size, errFileNotFound
		}
		if len(data) > fileImageMax {
			body["kind"] = "too_large"
			return body, size, nil
		}
		body["kind"], body["mime"], body["data"] = "image", mime, base64.StdEncoding.EncodeToString(data)
		return body, size, nil
	}
	data, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(head), f), fileTextMax+1))
	if err != nil {
		return nil, size, errFileNotFound
	}
	if !looksText(data) {
		body["kind"] = "binary"
		return body, size, nil
	}
	if privateKeyRe.Match(data) {
		return nil, size, deniedFile("looks like a private key")
	}
	truncated := len(data) > fileTextMax || int64(len(data)) < size
	if len(data) > fileTextMax {
		data = data[:fileTextMax]
		if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
			data = data[:i+1]
		} else {
			for len(data) > 0 && !utf8Start(data[len(data)-1]) {
				data = data[:len(data)-1]
			}
			if len(data) > 0 && !utf8.FullRune(data[len(data)-1:]) {
				data = data[:len(data)-1]
			}
		}
	}
	lang := fileLangs[ext]
	if lang == "" {
		lang = "text"
	}
	body["kind"], body["lang"], body["text"], body["truncated"] = "text", lang, strings.ToValidUTF8(string(data), "�"), truncated
	return body, size, nil
}

// looksText: no NUL and valid UTF-8 in the first fileSniff bytes (a rune cut
// by the window's end is fine).
func looksText(b []byte) bool {
	if len(b) > fileSniff {
		b = b[:fileSniff]
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return false
	}
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n <= 1 {
			return len(b) < utf8.UTFMax && !utf8.FullRune(b)
		}
		b = b[n:]
	}
	return true
}

// ---- mentions (§7.1.1, decision 1: Claude's output only) ----

// fileMentions: the paths Claude mentioned in the items (assistant and plan
// text, tool and permission summaries, diff paths), expanded and cleaned.
func fileMentions(s *deskSession, home string) map[string]bool {
	out := map[string]bool{}
	addLinks := func(text string) {
		for _, l := range findLinks(text) {
			if l.Kind != linkPath && l.Kind != linkFile {
				continue
			}
			if p, ferr := filePathOf(l.Target, home); ferr == nil {
				out[p] = true
			}
		}
	}
	for _, it := range s.conv.Items {
		switch it.Kind {
		case chat.Assistant:
			addLinks(it.Text)
		case chat.Plan:
			addLinks(s.conv.PlanText(it))
		case chat.Tool, chat.Permission:
			addLinks(it.Summary)
			if it.DiffPath != "" {
				if p, ferr := filePathOf(it.DiffPath, home); ferr == nil {
					out[p] = true
				}
			}
		}
	}
	return out
}

// ---- rate limits and file_busy (§7.7) ----

type fileHit struct {
	at    time.Time
	bytes int64
}

type fileGuard struct {
	mu   sync.Mutex
	busy map[string]bool
	hits map[string][]fileHit
}

func newFileGuard() *fileGuard {
	return &fileGuard{busy: map[string]bool{}, hits: map[string][]fileHit{}}
}

func (g *fileGuard) begin(phone string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.busy[phone] {
		return false
	}
	g.busy[phone] = true
	return true
}

func (g *fileGuard) end(phone string) {
	g.mu.Lock()
	delete(g.busy, phone)
	g.mu.Unlock()
}

// allow counts one request at now, or returns how long to wait.
func (g *fileGuard) allow(phone string, now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	var keep []fileHit
	var sum int64
	for _, h := range g.hits[phone] {
		if now.Sub(h.at) < fileRateWindow {
			keep = append(keep, h)
			sum += h.bytes
		}
	}
	g.hits[phone] = keep
	if n := len(keep); n > 0 {
		if gap := now.Sub(keep[n-1].at); gap < fileRateMinGap {
			return fileRateMinGap - gap
		}
		if n >= fileRateCount || sum >= fileRateBytes {
			return fileRateWindow - now.Sub(keep[0].at)
		}
	}
	g.hits[phone] = append(keep, fileHit{at: now})
	return 0
}

// sent adds the bytes of the last allowed request.
func (g *fileGuard) sent(phone string, n int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if h := g.hits[phone]; len(h) > 0 {
		h[len(h)-1].bytes += n
	}
}

// ---- audit (§7.8, decision 11: the log only, no notification) ----

var fileAuditMu sync.Mutex

func fileAuditPath() string { return filepath.Join(phoneCertDir(), fileAuditName) }

// auditFile appends one line: time, device id, device name, session id, path,
// size, result code. The log keeps the last fileAuditKeep lines and is never
// sent to a phone.
func auditFile(now time.Time, phone, name, session, path string, size int64, code string) {
	line := strings.Join([]string{now.Format(time.RFC3339), phone, strconv.Quote(name), session, strconv.Quote(path), strconv.FormatInt(size, 10), code}, "\t") + "\n"
	fileAuditMu.Lock()
	defer fileAuditMu.Unlock()
	p := fileAuditPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	old, _ := os.ReadFile(p)
	lines := strings.SplitAfter(string(old), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	lines = append(lines, line)
	if len(lines) > fileAuditKeep {
		lines = lines[len(lines)-fileAuditKeep:]
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, []byte(strings.Join(lines, "")), 0o600) == nil {
		_ = os.Rename(tmp, p)
	}
}

// fileAuditFor: the last n audit lines of one device, newest last, shown in
// Quill Desktop → Devices.
func fileAuditFor(phone string, n int) []string {
	fileAuditMu.Lock()
	b, _ := os.ReadFile(fileAuditPath())
	fileAuditMu.Unlock()
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Split(l, "\t")
		if len(f) != 7 || f[1] != phone {
			continue
		}
		name, _ := strconv.Unquote(f[4])
		t, _ := time.Parse(time.RFC3339, f[0])
		out = append(out, fmt.Sprintf("%s  %s  %s", localDate(t, dateMD)+" "+t.Format("15:04"), f[6], tildePath(name)))
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// ---- the route ----

// filesOn: may this device open files right now (hello and the stream)?
func filesOn(p folders.Phone) bool { return p.Kind == "phone" && p.Files }

// quillOwnDirs: Quill Desktop's own preferences and phone keys (also where
// they are moved by QUILL_PHONE_DIR or XDG_CONFIG_HOME).
func (a *phoneAPI) quillOwnDirs() []string {
	dirs := []string{phoneCertDir()}
	if cfg, err := os.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(cfg, "quill"), filepath.Join(cfg, "fyne", appID), filepath.Join(cfg, "kmem"))
	}
	// the app's storage folder (~/.config/fyne/<app id>; a test app's is the
	// temp folder, not Quill Desktop's own)
	if st := a.d.app.Storage(); st != nil && st.RootURI() != nil && strings.Contains(st.RootURI().Path(), appID) {
		dirs = append(dirs, st.RootURI().Path())
	}
	return dirs
}

func (a *phoneAPI) file(w http.ResponseWriter, r *http.Request, id string) {
	phone := folders.PhoneID(r)
	raw := r.URL.Query().Get("path")
	home, _ := os.UserHomeDir()
	var dev folders.Phone
	var known bool
	var userDeny string
	a.ui(func() {
		dev, known = a.d.device(phone)
		userDeny = a.d.app.Preferences().String(prefPhoneFilesDeny)
	})
	logPath, _ := capText(raw, filePathMax)
	var size int64
	fail := func(e *fileErr) {
		auditFile(time.Now(), phone, dev.Name, id, logPath, size, e.code)
		folders.WriteError(w, e.status, e.code, e.msg)
	}
	switch {
	case !known || dev.Kind != "phone":
		// Decision 8: phones only; other Quill Desktop PCs never read files.
		fail(&fileErr{http.StatusForbidden, "forbidden", "opening files is for paired phones only"})
		return
	case !dev.Files:
		fail(&fileErr{http.StatusForbidden, "files_off", "Opening files is off in Quill Desktop → Devices"})
		return
	}
	if !a.files.begin(phone) {
		fail(&fileErr{http.StatusConflict, "file_busy", "another file of this phone is still loading"})
		return
	}
	defer a.files.end(phone)
	if wait := a.files.allow(phone, time.Now()); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((wait+time.Second-1)/time.Second)))
		fail(&fileErr{http.StatusTooManyRequests, "rate_limited", "too many files at once; try again in a moment"})
		return
	}
	p, ferr := filePathOf(raw, home)
	if ferr != nil {
		fail(ferr)
		return
	}
	logPath = p
	if !a.preload(id) {
		fail(errFileNotFound)
		return
	}
	var folder string
	mentioned, found := false, false
	a.ui(func() {
		s := a.d.session(id)
		if s == nil || a.d.hiddenID(id, "") {
			return
		}
		found = true
		folder = firstNonEmpty(s.cwd, a.d.infos[id].Cwd)
		mentioned = fileMentions(s, home)[p]
	})
	if !found || !mentioned {
		// The same reply as a missing file: the route can't probe the disk.
		fail(errFileNotFound)
		return
	}
	body, sz, ferr := readPhoneFile(p, fileRules{home: home, folder: folder, extraDen: a.quillOwnDirs(), userDeny: parseUserDeny(userDeny)})
	size = sz
	if ferr != nil {
		fail(ferr)
		return
	}
	var sent int64
	if s, ok := body["text"].(string); ok {
		sent = int64(len(s))
	} else if s, ok := body["data"].(string); ok {
		sent = int64(len(s))
	}
	a.files.sent(phone, sent)
	auditFile(time.Now(), phone, dev.Name, id, p, size, "ok")
	folders.WriteJSON(w, http.StatusOK, body)
}

// ---- Quill Desktop → Devices ----

// deviceFilesRow: a phone's "Let this device open files" switch (off by
// default, decision 10) and its last file reads.
func (d *desk) deviceFilesRow(ph folders.Phone) fyne.CanvasObject {
	chk := widget.NewCheck("Let this device open files", func(on bool) { d.setDeviceFiles(ph.ID, on) })
	chk.Checked = ph.Files
	logBtn := widget.NewButton("Opened files...", func() {
		lines := fileAuditFor(ph.ID, 20)
		text := "No files opened yet."
		if len(lines) > 0 {
			text = strings.Join(lines, "\n")
		}
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapBreak
		help := caption("The last 20 files " + ph.Name + " asked for, newest last, with the result (ok, not_found, file_denied, ...). The full log: " + tildePath(fileAuditPath()))
		dlg := dialog.NewCustom("Files opened by "+ph.Name, "Close", container.NewBorder(help, nil, nil, nil, container.NewVScroll(l)), d.win)
		dlg.Resize(fyne.NewSize(620, 460))
		dlg.Show()
	})
	logBtn.Importance = widget.LowImportance
	return container.NewHBox(widget.NewLabel("    "), chk, logBtn)
}

// setDeviceFiles turns one phone's file switch on or off; the phone hears it
// through the stream's "files" change.
func (d *desk) setDeviceFiles(id string, on bool) {
	list := d.phones()
	for i := range list {
		if list[i].ID == id && list[i].Files != on {
			list[i].Files = on
			d.setPhones(list)
			if p := d.phone(); p.api != nil {
				p.api.scan()
			}
			return
		}
	}
}

// phoneFilesDenyRow: the user's own deny list (decision 6), on top of the
// built-in one.
func (d *desk) phoneFilesDenyRow() fyne.CanvasObject {
	e := widget.NewMultiLineEntry()
	e.SetPlaceHolder("~/private\n*.sqlite")
	e.SetMinRowsVisible(3)
	e.SetText(d.app.Preferences().String(prefPhoneFilesDeny))
	e.OnChanged = func(s string) { d.app.Preferences().SetString(prefPhoneFilesDeny, s) }
	help := caption("Never open these on phones, on top of the built-in list (keys, tokens, .env files, ~/.ssh, browser profiles, Quill Desktop's own settings). One per line: a folder or file (/... or ~/...) or a file name pattern (*.db).")
	return container.NewVBox(mutedText("Files phones may not open", fontMeta), help, e)
}
