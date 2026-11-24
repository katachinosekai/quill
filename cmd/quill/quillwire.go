package main

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// The Quill operator in the assistant box (design §3.2, §5.1, §3.7): its
// chat and own message box (attachAssistBox), the reason strip with "Move it
// here", and the settings change cards with Undo.

// attachQuillBox registers the operator's box content and the change cards.
func (d *desk) attachQuillBox() {
	d.quillChat = d.attachAssistBox(quillRole, "Quill operator", "Message the Quill operator")
	c := d.quillChat
	c.mirrors = newPlanMirrors(func() float32 { return c.root.Size().Height })
	c.top.Add(c.mirrors.wrap)
	d.syncPlanMirrors()
	d.setSettingsCard(d.quillSettingsCard)
}

// quillBoxReason: the role's reason line and action (assist.go hook).
func quillBoxReason(d *desk, a *assistant, reason, button string, tap func()) {
	if c := d.quillChat; c != nil {
		c.tap = tap
		d.syncReasonStrip(c)
	}
	d.refreshBoxHeader()
}

// quillBoxAttach: the session is there: show its chat, drop the reason.
func quillBoxAttach(d *desk, a *assistant) {
	c := d.quillChat
	if c == nil {
		return
	}
	c.tap = nil
	if a.s != nil && c.view.s != a.s {
		c.view.show(a.s)
	}
	d.offerQuillClone(a.s)
	d.syncReasonStrip(c)
	d.refreshBoxHeader()
}

func quillBoxRefresh(d *desk, a *assistant) {
	if c := d.quillChat; c != nil {
		d.syncReasonStrip(c)
	}
	d.refreshBoxHeader()
}

// syncReasonStrip shows the strip while the assistant has a reason and isn't
// running (the strip follows assistant state, so it never goes stale).
func (d *desk) syncReasonStrip(c *assistChat) {
	a := c.a
	on := a.reason != "" && !d.assistRunning(a)
	defer c.syncTop()
	if !on {
		c.reasonBox.Hide()
		return
	}
	c.reasonLbl.SetText(a.reason)
	if a.button != "" && c.tap != nil {
		c.reasonBtn.SetText(a.button)
		c.reasonBtn.Show()
	} else {
		c.reasonBtn.Hide()
	}
	c.reasonBox.Show()
	c.reasonBox.Refresh()
}

// ---- Change cards.

const settingCardMark = "​setting-change:"

// settingCard: one card in the chat and what it needs to stay current.
type settingCard struct {
	n     int
	undo  *widget.Button
	state *widget.Label
}

// quillSettingsCard is the settings change card hook: a card for a change, a
// plain notice for an undo; either goes to the operator's chat.
func (d *desk) quillSettingsCard(owner *deskSession, c settingChange, undo bool) {
	if owner == nil {
		owner = d.operatorSession()
	}
	if owner == nil {
		return // the log keeps it
	}
	text := settingsCardText(c, true)
	if !undo {
		text = fmt.Sprintf("%s%d", settingCardMark, c.N)
	}
	d.chatFor(owner, []*chat.Item{owner.conv.AddNotice(text)})
	d.syncSettingCards()
}

// settingCardN: the change number a card notice shows, 0 if it is no card.
func settingCardN(text string) int {
	var n int
	if rest, ok := strings.CutPrefix(text, settingCardMark); ok {
		fmt.Sscanf(rest, "%d", &n)
	}
	return n
}

func (d *desk) settingChangeByN(n int) (settingChange, bool) {
	for _, c := range d.settingsChanges() {
		if c.N == n {
			return c, true
		}
	}
	return settingChange{}, false
}

// settingCardView builds the card of change n.
func (d *desk) settingCardView(n int) fyne.CanvasObject {
	c, ok := d.settingChangeByN(n)
	if !ok {
		return caption("Settings change " + fmt.Sprint(n) + " is no longer in the log.")
	}
	card := &settingCard{n: n, state: caption("")}
	card.undo = widget.NewButton("Undo", func() {
		if _, err := d.undoSettingChange(nil, n); err != nil {
			card.state.SetText(err.Error())
		}
		d.syncSettingCards()
	})
	head := fmt.Sprintf("%s: %s → %s", c.Label, shownValue(c.Old), shownValue(c.New))
	rows := []fyne.CanvasObject{boldLabel(head)}
	if c.Why != "" {
		rows = append(rows, caption("“"+c.Why+"”"))
	}
	when := time.Unix(c.Time, 0).Format("15:04")
	rows = append(rows, container.NewBorder(nil, nil, caption("Change "+fmt.Sprint(c.N)+" · "+when), card.undo), card.state)
	d.settingCards = append(d.settingCards, card)
	d.syncSettingCards()
	return bgPad(color.NRGBA{0x2a, 0x33, 0x3d, 0xff}, 8, 4, 10, container.NewVBox(rows...))
}

// syncSettingCards: each card's Undo is enabled while its change is the
// latest one of its setting that isn't undone (the value is still the one it
// set); a later change or an undo disables it.
func (d *desk) syncSettingCards() {
	for _, card := range d.settingCards {
		c, ok := d.settingChangeByN(card.n)
		s, found := settingByID(c.ID)
		switch {
		case !ok || !found:
			card.undo.Disable()
		case c.Undone:
			card.undo.Disable()
			card.state.SetText("Undone")
		case s.get(d) != c.New:
			card.undo.Disable()
			card.state.SetText("Changed since; can't undo")
		default:
			card.undo.Enable()
			card.state.SetText("")
		}
	}
}
