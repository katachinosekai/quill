package main

import (
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
	if s != nil && d.kmemWin.session() == s {
		return true // shown in the open kmem window (its own window, so not tied to d.hidden)
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
	if isHiddenSession(s) {
		// The kmem window's assistant: desktop only, never the phone (§4 #25).
		if d.notifyOn() && !d.seen(s) {
			d.desktopNotify(s.id, "kmem assistant"+alertTitles[kind], body)
		}
		return
	}
	title, _, _ := d.rowText(s.id)
	d.phoneEvent(s.id, kind, title, body)
	if !d.notifyOn() || d.seen(s) {
		return
	}
	d.desktopNotify(s.id, title+alertTitles[kind], body)
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
	if d.notifySink != nil {
		d.notifySink(sid, title, body)
		return
	}
	n := d.notifier
	if n == nil {
		return
	}
	go n.send(d, sid, title, body)
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
			d.do(func() {
				if d.hiddenID(sid, "") {
					d.openKmemWindow() // raises the kmem window
					return
				}
				d.showWindow()
				if d.session(sid) != nil {
					d.open(sid)
				}
			})
		}
	}()
}

func (n *notifier) send(d *desk, sid, title, body string) {
	n.mu.Lock()
	replace := n.last[sid]
	n.mu.Unlock()
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
	n.mu.Lock()
	if replace != 0 && replace != id {
		delete(n.session, replace)
	}
	n.session[id] = sid
	n.last[sid] = id
	n.mu.Unlock()
}
