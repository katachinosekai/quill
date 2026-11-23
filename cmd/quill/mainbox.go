package main

import (
	"strings"

	"fyne.io/fyne/v2"
)

// The main message box while the assistant box is open over the chat
// (td1e074, user 2026-11-23): the overlay has no message box of its own; the
// main one (the focused pane's in split view) belongs to the box's assistant
// until the box closes or docks. Its placeholder and label say who it
// messages. Drafts are kept per target: the tab's draft comes back when the
// box closes, the assistant's when it opens again. Docked, the box keeps its
// own message box and the main one stays with the tab.

// mainTarget: who the main message box messages while it isn't the tab.
type mainTarget struct {
	role string     // the box role it messages ("" = the tab, as always)
	in   *sendEntry // the entry it took over
	key  string     // the tab draft to put back (the tab the entry belongs to)
}

// assistDraftKey: the draft key of role's main-box draft (never a session id).
func assistDraftKey(role string) string { return "\x00assist:" + role }

// tabDraftKey: tab id's draft key; no tab keeps its draft too.
func tabDraftKey(id string) string {
	if id == "" {
		return "\x00notab"
	}
	return id
}

// targetLabel: "Message the Quill operator" for role.
func targetLabel(role string) string {
	if role == boxKmem {
		return "Message the kmem assistant"
	}
	return "Message the " + roleDef(role).title
}

// mainEntry: the main message box (the focused pane's in split view).
func (d *desk) mainEntry() (*sendEntry, string) {
	if d.split() {
		if p := d.focused; p != nil && p.input != nil {
			id := ""
			if p.view.s != nil {
				id = p.view.s.id
			}
			return p.input, id
		}
		return nil, ""
	}
	id := ""
	if d.strip != nil {
		id = d.strip.lastCur
	} else if d.current != nil {
		id = d.current.id
	}
	return d.input, id
}

// syncMainTarget hands the main message box to the overlay's assistant, or
// back to the tab, keeping each one's draft. Called when the box opens,
// closes, docks or undocks.
func (d *desk) syncMainTarget() {
	want := ""
	if d.assistOverlayOpen() {
		want = d.abox.role
	}
	in, key := d.mainEntry()
	if want == d.main.role && (want == "" || d.main.in == in) {
		d.syncBoxInputs()
		return
	}
	// Give the entry back first.
	if old := d.main; old.role != "" && old.in != nil && d.strip != nil {
		d.strip.keepBox(assistDraftKey(old.role), old.in)
		d.strip.fillBox(tabDraftKey(old.key), old.in)
		old.in.hint = "Message Claude"
		old.in.setSuggestion("")
		old.in.SetPlaceHolder(old.in.hint)
		if old.in == d.input && d.strip != nil {
			d.strip.lastCur = old.key
		}
	}
	d.main = mainTarget{}
	if want != "" && in != nil && d.strip != nil {
		d.strip.keepBox(tabDraftKey(key), in)
		d.strip.fillBox(assistDraftKey(want), in)
		in.hint = targetLabel(want)
		in.setSuggestion("")
		in.SetPlaceHolder(in.hint)
		d.main = mainTarget{role: want, in: in, key: key}
		if d.cmds != nil {
			d.cmds.hide()
		}
	}
	d.syncBoxInputs()
}

// syncBoxInputs: an assistant's own message box shows unless the main one
// messages it (the overlay); the main box's label says who it messages.
func (d *desk) syncBoxInputs() {
	if b := d.abox; b != nil {
		for role, v := range b.views {
			if v.inRow == nil {
				continue
			}
			if o := v.inRow(); o != nil {
				setShown(o, d.main.role != role)
			}
		}
	}
	if d.targetLbl != nil {
		if d.main.role != "" && d.main.in == d.input {
			setLabel(d.targetLbl, "To the "+roleDef(d.main.role).title+" · Esc closes the box and gives this box back to the tab")
			d.targetLbl.Show()
		} else {
			d.targetLbl.Hide()
		}
	}
}

func setShown(o fyne.CanvasObject, on bool) {
	if o.Visible() == on {
		return
	}
	if on {
		o.Show()
	} else {
		o.Hide()
	}
}

// targeted: in messages an assistant now (its role), "" when it is the tab's.
func (d *desk) targeted(in *sendEntry) string {
	if in != nil && d.main.in == in {
		return d.main.role
	}
	return ""
}

// targetSession: the session the targeted assistant runs, or nil.
func (d *desk) targetSession(role string) *deskSession {
	if a := d.assistByRole(role); a != nil {
		return a.s
	}
	return nil
}

// sendTargeted sends in's text to the assistant it messages; false when in
// is the tab's (the caller sends as usual).
func (d *desk) sendTargeted(in *sendEntry, text string, now bool) bool {
	role := d.targeted(in)
	if role == "" {
		return false
	}
	if strings.TrimSpace(text) == "" {
		return true
	}
	a := d.assistByRole(role)
	if a == nil {
		return true
	}
	if now {
		if s := a.s; s != nil && s.running() {
			d.interrupt(s)
		}
	}
	d.assistDeliver(a, text)
	in.SetText("")
	d.strip.keepDraft(assistDraftKey(role), "")
	if role == boxQuill {
		if c := d.quillChat; c != nil && a.s != nil && c.view.s != a.s {
			c.view.show(a.s)
		}
	}
	d.assistRefresh(a)
	return true
}
