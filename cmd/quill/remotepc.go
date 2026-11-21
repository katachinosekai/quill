package main

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
	"github.com/katachinosekai/quill/internal/protocol"
)

// Remote PCs (user 2026-11-21): another Quill Desktop (Office PC, Windows)
// serves the phone API; this Quill pairs with it like a phone and shows each
// of its live sessions as a tab with a remote icon and the PC's name. Tabs
// come and go with the sessions there. Everything goes over the paired,
// pinned-TLS link; remote sessions are never re-exported to our own phones.

const remotePrefix = "r~"

type remotePCConf struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Hosts []string `json:"hosts"`
	Port  int      `json:"port"`
	Key   string   `json:"key"`
	FP    string   `json:"fp"`
}

// parsePairLink reads a quillpair:// link as Quill Desktop shows it.
func parsePairLink(link string) (remotePCConf, error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "quillpair" {
		return remotePCConf{}, errors.New("not a Quill pairing link (quillpair://...)")
	}
	q := u.Query()
	port, _ := strconv.Atoi(q.Get("p"))
	c := remotePCConf{Name: q.Get("n"), Port: port, Key: q.Get("k"), FP: strings.ToLower(q.Get("fp"))}
	for _, h := range strings.Split(q.Get("h"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			c.Hosts = append(c.Hosts, h)
		}
	}
	if len(c.Hosts) == 0 || c.Port == 0 || c.Key == "" || len(c.FP) != 64 {
		return remotePCConf{}, errors.New("the link has no address, key or certificate fingerprint; turn on the phone server on that PC and copy its pairing link again")
	}
	if c.Name == "" {
		c.Name = c.Hosts[0]
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	c.ID = hex.EncodeToString(b)
	return c, nil
}

func (d *desk) remotePCs() []remotePCConf {
	var out []remotePCConf
	_ = json.Unmarshal([]byte(d.app.Preferences().String("remotePCs")), &out)
	return out
}

func (d *desk) saveRemotePCs(pcs []remotePCConf) {
	b, _ := json.Marshal(pcs)
	d.app.Preferences().SetString("remotePCs", string(b))
}

// remoteSess is what we know about one session on a remote PC.
type remoteSess struct {
	link    *remoteLink
	rid     string
	title   string
	project string
	status  string
	busy    bool
	errText string
	epoch   int
	rev     int
	fetched int // rev of the items we hold, -1 = none
}

type remoteLink struct {
	d      *desk
	conf   remotePCConf
	client *http.Client
	base   string
	stop   chan struct{}
	once   sync.Once
	online bool
	ctl    bool
	seen   map[string]bool
}

func remoteLocalID(pcID, rid string) string { return remotePrefix + pcID + "~" + rid }

func newRemoteClient(fp string) *http.Client {
	return &http.Client{Timeout: 40 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // the certificate is self-signed; it is pinned below instead
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || folders.Fingerprint(cs.PeerCertificates[0].Raw) != fp {
				return errors.New("certificate does not match the paired fingerprint")
			}
			return nil
		},
	}}}
}

func (d *desk) startRemotePCs() {
	for _, c := range d.remotePCs() {
		d.startRemote(c)
	}
}

func (d *desk) startRemote(c remotePCConf) *remoteLink {
	if d.remotes == nil {
		d.remotes = map[string]*remoteLink{}
	}
	if old := d.remotes[c.ID]; old != nil {
		old.close()
	}
	l := &remoteLink{d: d, conf: c, client: newRemoteClient(c.FP), stop: make(chan struct{}), seen: map[string]bool{}}
	d.remotes[c.ID] = l
	go l.loop()
	return l
}

func (d *desk) stopRemotes() {
	for _, l := range d.remotes {
		l.close()
	}
}

func (l *remoteLink) close() {
	l.once.Do(func() { close(l.stop) })
}

func (l *remoteLink) stopped() bool {
	select {
	case <-l.stop:
		return true
	default:
		return false
	}
}

func idemKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (l *remoteLink) do(method, path string, body any, out any) (int, error) {
	if l.base == "" {
		return 0, errors.New("not connected")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, l.base+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Quill-Key", l.conf.Key)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idemKey())
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return resp.StatusCode, fmt.Errorf("%s: %s", firstNonEmpty(e.Error.Code, resp.Status), e.Error.Message)
	}
	if out != nil {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// connect finds a host that answers with the pinned certificate.
func (l *remoteLink) connect() bool {
	for _, h := range l.conf.Hosts {
		host := h
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			host = "[" + host + "]"
		}
		l.base = "https://" + host + ":" + strconv.Itoa(l.conf.Port)
		c := *l.client
		c.Timeout = 4 * time.Second
		saved := l.client
		l.client = &c
		_, err := l.do("GET", "/phone/sessions", nil, nil)
		l.client = saved
		if err == nil {
			name, _ := os.Hostname()
			_, _ = l.do("POST", "/phone/hello", map[string]string{"name": firstNonEmpty(name, "Quill Desktop"), "app": "quill-desktop " + version}, nil)
			return true
		}
	}
	l.base = ""
	return false
}

type remoteSessionsPage struct {
	Boot     string            `json:"boot"`
	Cursor   int64             `json:"cursor"`
	Control  bool              `json:"control"`
	Sessions []json.RawMessage `json:"sessions"`
}

type remoteSessJSON struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Folder  string `json:"folder"`
	Project string `json:"project"`
	Group   string `json:"group"`
	Status  string `json:"status"`
	Busy    bool   `json:"busy"`
	Error   string `json:"error"`
	Model   string `json:"model"`
	Mode    string `json:"mode"`
	Epoch   int    `json:"epoch"`
	Rev     int    `json:"rev"`
}

func (l *remoteLink) loop() {
	backoff := 2 * time.Second
	for !l.stopped() {
		if !l.connect() {
			l.d.do(func() { l.setOffline() })
			select {
			case <-l.stop:
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = 2 * time.Second
		var page remoteSessionsPage
		if _, err := l.do("GET", "/phone/sessions", nil, &page); err != nil {
			continue
		}
		l.d.do(func() { l.applyList(page) })
		cursor := page.Cursor
		for !l.stopped() {
			var st struct {
				Boot    string           `json:"boot"`
				Cursor  int64            `json:"cursor"`
				Reset   bool             `json:"reset"`
				Changes []map[string]any `json:"changes"`
			}
			if _, err := l.do("GET", "/phone/stream?cursor="+strconv.FormatInt(cursor, 10)+"&wait=25", nil, &st); err != nil {
				break
			}
			if st.Reset || st.Boot != page.Boot {
				break
			}
			cursor = st.Cursor
			if len(st.Changes) > 0 {
				var p2 remoteSessionsPage
				if _, err := l.do("GET", "/phone/sessions", nil, &p2); err != nil {
					break
				}
				page = p2
				l.d.do(func() { l.applyList(p2) })
			}
		}
	}
}

// applyList brings our tabs in line with the PC's live sessions (UI thread).
func (l *remoteLink) applyList(page remoteSessionsPage) {
	d := l.d
	l.online, l.ctl = true, page.Control
	now := map[string]bool{}
	for _, raw := range page.Sessions {
		var rs remoteSessJSON
		if json.Unmarshal(raw, &rs) != nil || rs.ID == "" {
			continue
		}
		id := remoteLocalID(l.conf.ID, rs.ID)
		now[id] = true
		s := d.sessions[id]
		if s == nil {
			s = &deskSession{id: id, cwd: rs.Folder, conv: chat.New(), loaded: true, created: time.Now(),
				remote: &remoteSess{link: l, rid: rs.ID, fetched: -1}}
			d.sessions[id] = s
		}
		r := s.remote
		r.title, r.project, r.status, r.busy, r.errText = rs.Title, firstNonEmpty(rs.Project, rs.Group), rs.Status, rs.Busy, rs.Error
		r.epoch, r.rev = rs.Epoch, rs.Rev
		s.title, s.model, s.mode, s.cwd = rs.Title, rs.Model, rs.Mode, rs.Folder
		if !l.seen[id] {
			l.seen[id] = true
			d.addTab(id)
		}
		if d.remoteVisible(s) && r.fetched != r.rev {
			l.fetchItems(s)
		}
	}
	for id := range l.seen {
		if !now[id] {
			l.drop(id)
		}
	}
	d.rebuildRows()
	d.refreshSidebar()
	d.updateChrome()
}

func (l *remoteLink) drop(id string) {
	d := l.d
	delete(l.seen, id)
	d.closeTab(id)
	if d.current != nil && d.current.id == id {
		d.current = nil
		d.chat.show(nil)
	}
	delete(d.sessions, id)
}

// setOffline: an unreachable PC's sessions leave the tabs; they come back as
// new tabs when the PC is reachable again.
func (l *remoteLink) setOffline() {
	if !l.online && len(l.seen) == 0 {
		return
	}
	l.online = false
	for id := range l.seen {
		l.drop(id)
	}
	l.d.rebuildRows()
}

func (d *desk) remoteVisible(s *deskSession) bool {
	if d.current == s {
		return true
	}
	for _, p := range d.panes {
		if p.view.s == s {
			return true
		}
	}
	return false
}

type remoteItem struct {
	Seq       int    `json:"seq"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Streaming bool   `json:"streaming"`
	InPlan    bool   `json:"inPlan"`
	Tool      *struct {
		Name    string `json:"name"`
		ID      string `json:"id"`
		Summary string `json:"summary"`
		Done    bool   `json:"done"`
		IsError bool   `json:"isError"`
		Result  string `json:"result"`
	} `json:"tool"`
	Request *struct {
		ID        string          `json:"id"`
		State     string          `json:"state"`
		Tool      string          `json:"tool"`
		Detail    string          `json:"detail"`
		Questions json.RawMessage `json:"questions"`
	} `json:"request"`
	Choices []string `json:"choices"`
}

var remoteKinds = map[string]chat.Kind{}
var remoteStates = map[string]chat.PermState{}

func init() {
	for k, v := range apiKinds {
		remoteKinds[v] = k
	}
	remoteKinds["question"] = chat.Permission
	for k, v := range apiStates {
		remoteStates[v] = k
	}
}

func remoteChatItem(ri remoteItem) *chat.Item {
	kind, ok := remoteKinds[ri.Kind]
	if !ok {
		kind = chat.Notice
	}
	it := &chat.Item{Kind: kind, Seq: ri.Seq, Text: ri.Text, Streaming: ri.Streaming, InPlan: ri.InPlan}
	if t := ri.Tool; t != nil {
		it.ToolName, it.ToolID, it.Summary, it.HasResult, it.ResultError, it.Result = t.Name, t.ID, t.Summary, t.Done, t.IsError, t.Result
	}
	if r := ri.Request; r != nil {
		it.RequestID = r.ID
		it.PermState = remoteStates[r.State]
		if r.Tool != "" {
			it.ToolName = r.Tool
		}
		if ri.Kind == "question" {
			it.ToolName = "AskUserQuestion"
			it.Input, _ = json.Marshal(map[string]json.RawMessage{"questions": r.Questions})
		} else if r.Detail != "" {
			it.Summary = r.Detail
		}
	}
	return it
}

// fetchItems loads the latest window of a shown remote session's chat.
func (l *remoteLink) fetchItems(s *deskSession) {
	r := s.remote
	want := r.rev
	go func() {
		var page struct {
			Items []remoteItem `json:"items"`
		}
		if _, err := l.do("GET", "/phone/session/"+url.PathEscape(r.rid)+"/items?limit=200", nil, &page); err != nil {
			return
		}
		l.d.do(func() {
			if l.d.sessions[s.id] != s {
				return
			}
			conv := chat.New()
			for _, ri := range page.Items {
				conv.Items = append(conv.Items, remoteChatItem(ri))
			}
			conv.Busy = r.busy
			s.conv = conv
			r.fetched = want
			for _, v := range l.d.viewsFor(s) {
				v.s = nil
				v.show(s)
			}
			l.d.updateChrome()
		})
	}()
}

// remoteWrite posts one write for a remote session and reports failures in its chat.
func (d *desk) remoteWrite(s *deskSession, op string, body any) {
	r := s.remote
	l := r.link
	go func() {
		_, err := l.do("POST", "/phone/session/"+url.PathEscape(r.rid)+"/"+op, body, nil)
		d.do(func() {
			if err != nil && d.sessions[s.id] == s {
				d.chatFor(s, []*chat.Item{s.conv.AddError(l.conf.Name + ": " + err.Error())})
				return
			}
			r.fetched = -1
			if d.remoteVisible(s) {
				l.fetchItems(s)
			}
		})
	}()
}

func (d *desk) remoteDecision(s *deskSession, it *chat.Item, dec protocol.Decision) {
	action := map[protocol.Decision]string{protocol.Allow: "allow", protocol.AllowAlways: "allowAlways", protocol.Deny: "deny"}[dec]
	d.remoteWrite(s, "answer", map[string]any{"requestId": it.RequestID, "action": action})
}

func (d *desk) remoteAnswers(s *deskSession, it *chat.Item, answers map[string]string) {
	out := map[string][]string{}
	for q, a := range answers {
		out[q] = strings.Split(a, ", ")
	}
	d.remoteWrite(s, "answer", map[string]any{"requestId": it.RequestID, "action": "answer", "answers": out})
}

func (d *desk) remotePlan(s *deskSession, it *chat.Item, ok bool) {
	action := "rejectPlan"
	if ok {
		action = "approvePlan"
	}
	d.remoteWrite(s, "answer", map[string]any{"requestId": it.RequestID, "action": action})
}

func (d *desk) remoteChoice(s *deskSession, it *chat.Item, option string) {
	text := strings.TrimRight(it.Text, " \n")
	_, opts, ok := parseChoices(text[strings.LastIndex(text, "\n")+1:])
	if !ok {
		return
	}
	for i, o := range opts {
		if o == option || choiceReply(i, o) == option {
			d.remoteWrite(s, "answer", map[string]any{"seq": it.Seq, "action": "choice", "index": i})
			return
		}
	}
}

func remoteStatus(r *remoteSess) status {
	switch r.status {
	case "running":
		return stRunning
	case "waiting":
		return stWaiting
	case "idle":
		return stIdle
	case "error":
		return stError
	case "terminal":
		return stTerminal
	}
	return stNone
}

func (d *desk) viewsFor(s *deskSession) []*chatView {
	if p := d.paneFor(s); p != nil {
		return []*chatView{p.view}
	}
	if d.current == s {
		return []*chatView{d.chat}
	}
	return nil
}

// remotePCsRow: Settings → Sessions → Other PCs.
func (d *desk) remotePCsRow() fyne.CanvasObject {
	box := container.NewVBox()
	var fill func()
	fill = func() {
		var objs []fyne.CanvasObject
		pcs := d.remotePCs()
		if len(pcs) == 0 {
			objs = append(objs, caption("No other PCs paired. Pair one to see its live sessions here as tabs with a computer icon."))
		}
		for _, c := range pcs {
			c := c
			state := "connecting..."
			if l := d.remotes[c.ID]; l != nil && l.online {
				state = fmt.Sprintf("online · %d live session(s)", len(l.seen))
				if !l.ctl {
					state += " · read-only (turn on \"Let paired devices control sessions\" there)"
				}
			} else if l != nil && l.base == "" {
				state = "offline"
			}
			rm := widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
				dialog.ShowConfirm("Remove "+c.Name+"?", "Its sessions leave the tabs. Revoke this PC on "+c.Name+" too (Settings → Sessions → Devices).", func(ok bool) {
					if !ok {
						return
					}
					var keep []remotePCConf
					for _, x := range d.remotePCs() {
						if x.ID != c.ID {
							keep = append(keep, x)
						}
					}
					d.saveRemotePCs(keep)
					if l := d.remotes[c.ID]; l != nil {
						l.close()
						for id := range l.seen {
							l.drop(id)
						}
						delete(d.remotes, c.ID)
					}
					d.rebuildRows()
					fill()
				}, d.win)
			})
			rm.Importance = widget.LowImportance
			objs = append(objs, container.NewBorder(nil, nil, widget.NewIcon(theme.ComputerIcon()), rm,
				container.NewVBox(boldLabel(c.Name), caption(strings.Join(c.Hosts, ", ")+" · "+state))))
		}
		add := widget.NewButtonWithIcon("Add a PC...", theme.ContentAddIcon(), func() { d.addRemotePCDialog(fill) })
		add.Importance = widget.MediumImportance
		objs = append(objs, container.NewHBox(add))
		box.Objects = objs
		box.Refresh()
	}
	fill()
	return box
}

func (d *desk) addRemotePCDialog(done func()) {
	link := widget.NewMultiLineEntry()
	link.SetPlaceHolder("quillpair://pair?h=...")
	link.SetMinRowsVisible(3)
	link.Wrapping = fyne.TextWrapBreak
	name := widget.NewEntry()
	name.SetPlaceHolder("Name, e.g. Office PC (optional)")
	help := caption("On the other PC, open Quill Desktop → Devices → Pair, copy the pairing link and paste it here while that pairing window is still open (Quill there forgets a pairing that never connected). Turn on \"Let paired devices control sessions\" there to send messages and answer approvals from here.")
	dlg := dialog.NewForm("Add a PC", "Pair", "Cancel", []*widget.FormItem{
		widget.NewFormItem("Pairing link", link), widget.NewFormItem("Name", name), widget.NewFormItem("", help),
	}, func(ok bool) {
		if !ok {
			return
		}
		c, err := parsePairLink(link.Text)
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		if n := strings.TrimSpace(name.Text); n != "" {
			c.Name = n
		}
		d.saveRemotePCs(append(d.remotePCs(), c))
		d.startRemote(c)
		if done != nil {
			done()
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(620, 360))
	dlg.Show()
}
