package main

// Hidden-session roles (docs/design/quill-operator.md §5.2). A hidden
// session runs without a tab, sidebar row or phone list entry and shows in
// its role's host (the kmem window today; the assistant box later). Roles:
// "kmem" (the kmem assistant, kmem-lock-audit) and "quill" (the Quill
// operator, phase 2). "" = not hidden.
//
// Storage keeps today's formats so old org and restore files load, and older
// builds still read new ones: org.Hidden[id] = true marks an id hidden and
// org.HiddenRoles[id] names its role; a hidden id without a role is "kmem".
// The same holds for Hidden + Role in restore and reload state.

const (
	roleKmem  = "kmem"
	roleQuill = "quill"
)

// hiddenRoleDef: the static text of a hidden role.
type hiddenRoleDef struct {
	title string // notification title ("kmem assistant finished")
	owner string // ownerLabel: where a live handoff or topic runs
	// phone: the phone event when the role's session waits for the user,
	// unseen; nil = none yet (older phones hide the role).
	phone func(d *desk, text string)
}

var hiddenRoles = map[string]hiddenRoleDef{
	roleKmem:       {title: "kmem assistant", owner: "live in the kmem window", phone: (*desk).kmemPhoneEvent},
	roleCodexQuill: {title: "Quill operator", owner: "live in the Quill window"},
	roleCodexKmem:  {title: "kmem assistant", owner: "live in the kmem window"},
	roleQuill:      {title: "Quill operator", owner: "live in the Quill operator", phone: (*desk).quillPhoneEvent},
}

// hiddenRoleOr: role, or "kmem" for a hidden entry stored without one.
func hiddenRoleOr(role string) string {
	if role == "" {
		return roleKmem
	}
	return role
}

// roleDef: the definition of role, kmem's for an unknown role (a newer
// build's role read by this one stays hidden and labelled).
func roleDef(role string) hiddenRoleDef {
	if r, ok := hiddenRoles[role]; ok {
		return r
	}
	return hiddenRoles[roleKmem]
}

// hiddenRole: s's hidden role, "" for a visible session. The kmem window's
// folder alone marks the kmem assistant.
func hiddenRole(s *deskSession) string {
	switch {
	case s == nil:
		return ""
	case s.hidden:
		return hiddenRoleOr(s.role)
	case isKmemWindowDir(s.cwd):
		return roleKmem
	}
	return ""
}

// isHiddenSession: s runs hidden in any role.
func isHiddenSession(s *deskSession) bool {
	return hiddenRole(s) != ""
}

// hiddenRoleOf: the hidden role of id (with working folder cwd, may be
// empty), by its session's flag, the org's Hidden list or its folder; "" if
// visible. Transcripts with no session are only known by the org or folder.
func (d *desk) hiddenRoleOf(id, cwd string) string {
	if r := hiddenRole(d.sessions[id]); r != "" {
		return r
	}
	if d.org.Hidden[id] {
		return hiddenRoleOr(d.org.HiddenRoles[id])
	}
	if isKmemWindowDir(cwd) {
		return roleKmem
	}
	if info, ok := d.infos[id]; ok && isKmemWindowDir(info.Cwd) {
		return roleKmem
	}
	return ""
}

// hiddenID: id is a hidden session of any role.
func (d *desk) hiddenID(id, cwd string) bool {
	return d.hiddenRoleOf(id, cwd) != ""
}

// markHidden makes s the kmem window's assistant.
func (d *desk) markHidden(s *deskSession) {
	d.markHiddenAs(s, roleKmem)
}

// markHiddenAs makes s a hidden session of role: flagged, remembered in the
// org so its id stays out of the sidebar after it ends, and out of the tabs.
func (d *desk) markHiddenAs(s *deskSession, role string) {
	if s == nil {
		return
	}
	role = hiddenRoleOr(role)
	s.hidden, s.role = true, role
	if d.org.Hidden == nil {
		d.org.Hidden = map[string]bool{}
	}
	if d.org.HiddenRoles == nil {
		d.org.HiddenRoles = map[string]string{}
	}
	if !d.org.Hidden[s.id] || hiddenRoleOr(d.org.HiddenRoles[s.id]) != role {
		d.org.Hidden[s.id] = true
		if role == roleKmem {
			delete(d.org.HiddenRoles, s.id) // today's format
		} else {
			d.org.HiddenRoles[s.id] = role
		}
		d.saveOrgQuiet()
	}
	for i, t := range d.tabs {
		if t == s.id {
			d.tabs = append(d.tabs[:i:i], d.tabs[i+1:]...)
			d.saveTabs()
			d.tabSig = ""
			d.syncTabs()
			break
		}
	}
}

// hiddenHost: where a hidden role's session shows. Every func may be nil.
type hiddenHost struct {
	session func() *deskSession // the session the host is attached to, or nil
	view    func() *chatView    // its chat view (kept while the host is closed), or nil
	shown   func() bool         // the host is open and showing it (for seen)
	open    func()              // brings the host up (a notification click)
}

// setHiddenHost registers the host of role (nil host: none). The kmem
// window is kmem's host until another one is registered.
func (d *desk) setHiddenHost(role string, h *hiddenHost) {
	if d.hiddenHosts == nil {
		d.hiddenHosts = map[string]*hiddenHost{}
	}
	if h == nil {
		delete(d.hiddenHosts, role)
		return
	}
	d.hiddenHosts[role] = h
}

// hostFor: role's host; kmem falls back to the kmem window.
func (d *desk) hostFor(role string) hiddenHost {
	if h := d.hiddenHosts[role]; h != nil {
		return *h
	}
	if role == roleKmem {
		return hiddenHost{
			session: func() *deskSession { return d.kmemWin.session() },
			view: func() *chatView {
				if w := d.kmemWin; w != nil {
					return w.view
				}
				return nil
			},
			shown: func() bool {
				w := d.kmemWin
				return w.assistantSelected() && !w.codexShown && w.session() != nil
			},
			open: d.openKmemAssistant,
		}
	}
	return hiddenHost{}
}

// hostRoles: kmem first, then the other roles in a fixed order.
func hostRoles() []string {
	return []string{roleKmem, roleQuill, roleCodexQuill, roleCodexKmem}
}

// hostSession: role's host session, nil-safe.
func (h hiddenHost) hostSession() *deskSession {
	if h.session == nil {
		return nil
	}
	return h.session()
}

// hostView: the view of the host showing s, or nil.
func (d *desk) hostView(s *deskSession) *chatView {
	if s == nil {
		return nil
	}
	for _, role := range hostRoles() {
		h := d.hostFor(role)
		if h.hostSession() == s && h.view != nil {
			if v := h.view(); v != nil {
				return v
			}
		}
	}
	return nil
}

// hostShown: s is shown in an open host.
func (d *desk) hostShown(s *deskSession) bool {
	if s == nil {
		return false
	}
	for _, role := range hostRoles() {
		h := d.hostFor(role)
		if h.hostSession() == s && h.shown != nil && h.shown() {
			return true
		}
	}
	return false
}

// hostHolds: s is attached to a host (live for handoffOwner even while stopped).
func (d *desk) hostHolds(s *deskSession) bool {
	if s == nil {
		return false
	}
	for _, role := range hostRoles() {
		if d.hostFor(role).hostSession() == s {
			return true
		}
	}
	return false
}

// hostViews: the hosts' chat views other than kmem's (allViews adds kmem's).
func (d *desk) hostViews() []*chatView {
	var out []*chatView
	for _, role := range hostRoles()[1:] {
		if h := d.hostFor(role); h.view != nil {
			if v := h.view(); v != nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// openHiddenRole brings up role's host (its box); an unknown role opens kmem's.
func (d *desk) openHiddenRole(role string) {
	if h := d.hostFor(hiddenRoleOr(role)); h.open != nil {
		h.open()
	}
}

// openHidden brings up the host of id's role.
func (d *desk) openHidden(id string) {
	if h := d.hostFor(d.hiddenRoleOf(id, "")); h.open != nil {
		h.open()
	}
}

// handedRole: the Hidden flag and Role written to restore, reload and keeper
// state. kmem's role stays empty, as older builds wrote it.
func handedRole(s *deskSession) (bool, string) {
	role := hiddenRole(s)
	if role == roleKmem {
		role = ""
	}
	return isHiddenSession(s), role
}
