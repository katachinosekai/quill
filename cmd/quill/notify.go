package main

import (
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/katachinosekai/quill/internal/chat"
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
	if !d.foreground || d.hidden {
		return false
	}
	if d.current == s {
		return true
	}
	return d.split() && d.paneFor(s) != nil
}

func (d *desk) attention(s *deskSession, ev *protocol.Event, items []*chat.Item) {
	if !d.notifyOn() || d.seen(s) {
		return
	}
	title, _, _ := d.rowText(s.id)
	for _, it := range items {
		if it.PermState != chat.PermPending {
			continue
		}
		switch {
		case it.Kind == chat.Plan:
			d.desktopNotify(s.id, title+" has a plan to approve", clip(lastReply(s.conv), 200))
			return
		case it.Kind == chat.Permission && isQuestion(it):
			d.desktopNotify(s.id, title+" has a question", clip(it.Summary, 200))
			return
		case it.Kind == chat.Permission:
			body := it.ToolName
			if it.Summary != "" {
				body += ": " + it.Summary
			}
			d.desktopNotify(s.id, title+" needs approval", clip(body, 200))
			return
		}
	}
	if ev.Type != "result" || s.compacting || len(s.conv.PendingPermissions()) > 0 {
		return
	}
	if ev.IsError {
		d.desktopNotify(s.id, title+" stopped with an error", clip(ev.Result, 200))
		return
	}
	d.desktopNotify(s.id, title+" finished", clip(lastReply(s.conv), 200))
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
