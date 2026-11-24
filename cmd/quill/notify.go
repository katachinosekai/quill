package main

import (
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/folders"
	"github.com/katachinosekai/quill/internal/protocol"
)

const (
	notifyPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	notifyIface = "org.freedesktop.Notifications"
)

type notifier struct {
	conn    *dbus.Conn
	mu      sync.Mutex
	session map[uint32]string
	last    map[string]uint32
}

func (d *desk) notifyOn() bool {
	return d.app.Preferences().BoolWithFallback("desktopNotify", true)
}

func (d *desk) watchFocus() {
	d.foreground = true
	lc := d.app.Lifecycle()
	lc.SetOnEnteredForeground(func() { d.foreground = true })
	lc.SetOnExitedForeground(func() { d.foreground = false })
}

func (d *desk) seen(s *deskSession) bool {
	if !d.foreground {
		return false
	}
	if d.hostShown(s) {
		return true // shown in its open host, e.g. the kmem window (its own window, so not tied to d.hidden)
	}
	if d.hidden {
		return false
	}
	if d.current == s {
		return true
	}
	return d.split() && d.paneFor(s) != nil
}

func (d *desk) attention(s *deskSession, ev *protocol.Event, items []*chat.Item) {
	kind, body := attentionKind(s, ev, items)
	if kind == "" {
		return
	}
	d.alert(s, kind, body)
}

func attentionKind(s *deskSession, ev *protocol.Event, items []*chat.Item) (string, string) {
	for _, it := range items {
		if it.PermState != chat.PermPending {
			continue
		}
		switch {
		case it.Kind == chat.Plan:
			return "plan", lastReply(s.conv)
		case it.Kind == chat.Permission && isQuestion(it):
			return "question", it.Summary
		case it.Kind == chat.Permission:
			body := it.ToolName
			if it.Summary != "" {
				body += ": " + it.Summary
			}
			return "approval", body
		}
	}
	if ev.Type != "result" || s.compacting || len(s.conv.PendingPermissions()) > 0 {
		return "", ""
	}
	if ev.IsError {
		return "error", ev.Result
	}
	return "finished", lastReply(s.conv)
}

var alertTitles = map[string]string{
	"approval": " needs approval",
	"question": " has a question",
	"plan":     " has a plan to approve",
	"finished": " finished",
	"error":    " stopped with an error",
}

func (d *desk) alert(s *deskSession, kind, body string) {
	body = clip(body, 200)
	if role := hiddenRole(s); role != "" {
		// A hidden session: never a session event on the phone (kmem-window
		// §4 #25); its role's event when it waits for the user, unseen
		// (kmem: docs/design/kmem-android.md §4.5), titled by its role.
		if d.seen(s) {
			return
		}
		if kind == "finished" && s.conv.AsksApproval() {
			kind = "question" // waits for approval without a card (plan-first off): as the button shows it
		}
		def := roleDef(role)
		if def.phone != nil && waitKind(kind) {
			def.phone(d, strings.TrimSpace(alertTitles[kind])+": "+body)
		}
		if d.notifyOn() {
			d.desktopNotifyAs(s.id, def.title+alertTitles[kind], body, waitKind(kind))
		}
		return
	}
	title, _, _ := d.rowText(s.id)
	d.phoneEvent(s.id, kind, title, body)
	if !d.notifyOn() || d.seen(s) {
		return
	}
	d.desktopNotifyAs(s.id, title+alertTitles[kind], body, waitKind(kind))
}

func (d *desk) phoneEvent(sid, kind, title, body string) {
	p := d.phoneSync
	if p == nil || p.server == nil {
		return
	}
	rc := ""
	for k, v := range p.rc {
		if v == sid && k > rc {
			rc = k
		}
	}
	folder := ""
	for _, g := range d.groups {
		for _, id := range g.ids {
			if id == sid {
				folder = g.title
			}
		}
	}
	p.server.Emit(folders.Event{Type: kind, RC: rc, Quill: sid, Title: title, Folder: folder, Body: body})
}

// kmemPhoneEvent: the phone event for the kmem assistant waiting for the
// user (type "kmem", no session id; old Quill Android drops unknown types).
func (d *desk) kmemPhoneEvent(body string) {
	p := d.phoneSync
	if p == nil || p.server == nil {
		return
	}
	body, _ = capText(kmemScrubber()(body), 300)
	p.server.Emit(folders.Event{Type: "kmem", Title: "kmem assistant", Body: body})
}

// quillPhoneEvent: the phone event for the Quill operator waiting for the
// user (type "quill", titled "Quill operator", no session id; old Quill
// Android drops unknown types).
func (d *desk) quillPhoneEvent(body string) {
	p := d.phoneSync
	if p == nil || p.server == nil {
		return
	}
	body, _ = capText(kmemScrubber()(body), 300)
	p.server.Emit(folders.Event{Type: "quill", Title: "Quill operator", Body: body})
}

func lastReply(c *chat.Conversation) string {
	for i := len(c.Items) - 1; i >= 0; i-- {
		if it := c.Items[i]; it.Kind == chat.Assistant && it.Text != "" {
			return it.Text
		}
		if c.Items[i].Kind == chat.User {
			break
		}
	}
	return ""
}

func (d *desk) desktopNotify(sid, title, body string) {
	d.desktopNotifyAs(sid, title, body, false)
}

// waitKind: an alert kind that waits for the user's approval or answer.
func waitKind(kind string) bool {
	return kind == "approval" || kind == "question" || kind == "plan"
}

// desktopNotifyAs: own = its own notification, replacing none (approvals).
func (d *desk) desktopNotifyAs(sid, title, body string, own bool) {
	if d.notifySink != nil {
		d.notifySink(sid, title, body)
		return
	}
	n := d.notifier
	if n == nil {
		return
	}
	go n.send(d, sid, title, body, own)
}

func (d *desk) startNotifier() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	n := &notifier{conn: conn, session: map[uint32]string{}, last: map[string]uint32{}}
	if conn.AddMatchSignal(dbus.WithMatchObjectPath(notifyPath), dbus.WithMatchInterface(notifyIface)) != nil {
		conn.Close()
		return
	}
	sig := make(chan *dbus.Signal, 16)
	conn.Signal(sig)
	d.notifier = n
	go func() {
		for v := range sig {
			if len(v.Body) < 2 {
				continue
			}
			id, ok := v.Body[0].(uint32)
			if !ok {
				continue
			}
			n.mu.Lock()
			sid, known := n.session[id]
			if v.Name == notifyIface+".NotificationClosed" {
				delete(n.session, id)
				if n.last[sid] == id {
					delete(n.last, sid)
				}
			}
			n.mu.Unlock()
			if !known || v.Name != notifyIface+".ActionInvoked" {
				continue
			}
			d.do(func() { d.notifyClicked(sid) })
		}
	}()
}

// notifyClicked opens what notification sid is about (UI thread): a tab, or
// a hidden assistant's box in the main window (shown first: it may be in the
// tray), switching the box from the other assistant; the popped-out kmem
// window is raised instead.
func (d *desk) notifyClicked(sid string) {
	if d.hiddenID(sid, "") {
		if role := d.hiddenRoleOf(sid, ""); role != roleKmem || !d.kmemPoppedOut() {
			d.showWindow()
		}
		d.openHidden(sid)
		return
	}
	d.showWindow()
	if d.session(sid) != nil {
		d.open(sid)
	}
}

// kmemPoppedOut: the kmem box is in its own window.
func (d *desk) kmemPoppedOut() bool {
	w := d.kmemWin
	return w != nil && w.win != nil && !w.closed
}

func (n *notifier) send(d *desk, sid, title, body string, own bool) {
	replace := n.replaceID(sid, own)
	hints := map[string]dbus.Variant{
		"desktop-entry": dbus.MakeVariant(appID),
		"urgency":       dbus.MakeVariant(byte(1)),
	}
	var id uint32
	err := n.conn.Object(notifyIface, notifyPath).Call(notifyIface+".Notify", 0,
		"Quill", replace, appID, title, body, []string{"default", "Open"}, hints, int32(-1)).Store(&id)
	if err != nil {
		return
	}
	n.sent(sid, replace, id, own)
}

// replaceID: the notification a new one for sid replaces, 0 for none. An
// approval's own notification (own) replaces nothing, so Plasma pops it up
// instead of quietly updating an earlier "finished" one (user 2026-11-23).
func (n *notifier) replaceID(sid string, own bool) uint32 {
	if own {
		return 0
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.last[sid]
}

// sent records notification id for sid (replacing replace). An own one is
// never a replace target: the next other notification replaces the last
// other one and leaves the approval's in place.
func (n *notifier) sent(sid string, replace, id uint32, own bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if replace != 0 && replace != id {
		delete(n.session, replace)
	}
	n.session[id] = sid
	if !own {
		n.last[sid] = id
	}
}
