package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/transcript"
)

// kmem is optional (user 2026-11-24): it is not part of Quill Desktop and is
// distributed to Patreon patrons. Without it Quill Desktop uses Claude Code's
// auto memory: the toolbar's kmem button becomes "Memory" and opens the
// current project's MEMORY.md (showClaudeMemory); kmem-only features (tasks,
// topics, end-check, the kmem assistant, badges, kmem prompt hints) stay off
// because kmemAvailable/kmemMemory are false. A small dismissible advert
// (kmemAdvert) sits in Settings → General → Memory, the Memory tab, the
// Claude memory panel and the Welcome; never when kmem is installed. With
// kmem installed but sessions on Claude's memory, a card in the header offers
// once to switch to kmem (kmemSwitchBanner).

const (
	prefKmemAdvertOff     = "kmemAdvertDismissed"
	prefKmemSwitchOffered = "kmemSwitchOffered"
	kmemAdvertText        = "kmem: shared memory, tasks and topics across sessions — available on Patreon"
	kmemSwitchText        = "kmem is installed. Use it as the memory of Quill Desktop sessions (shared memory, tasks and topics)?"
)

// kmemAdvertShown: no kmem installed and the advert not dismissed.
func (d *desk) kmemAdvertShown() bool {
	return !d.kmemAvailable() && !d.app.Preferences().Bool(prefKmemAdvertOff)
}

// kmemAdvert is the advert card; dismissing one hides them all, for good.
func (d *desk) kmemAdvert() *fyne.Container {
	card := container.NewStack(cardBg(), kmemAdvertRow(d))
	d.trackKmemAdvert(card)
	return card
}

// kmemAdvertRow: the advert's text, Learn more and dismiss (not tracked).
func kmemAdvertRow(d *desk) *fyne.Container {
	lbl := widget.NewLabel(kmemAdvertText)
	lbl.Wrapping = fyne.TextWrapWord
	learn := widget.NewButton("Learn more", d.openKmemPatreon)
	learn.Importance = widget.LowImportance
	dismiss := widget.NewButtonWithIcon("", theme.CancelIcon(), d.dismissKmemAdvert)
	dismiss.Importance = widget.LowImportance
	return container.NewBorder(nil, nil, nil, container.NewHBox(container.NewCenter(learn), container.NewCenter(dismiss)), lbl)
}

// kmemStatusCard: Settings → General → Memory (kmem)'s card, the kmem
// status on the first line and, without kmem, the advert on the second; the
// dismiss hides only the advert (user 2026-11-24).
func (d *desk) kmemStatusCard(status *widget.Label) *fyne.Container {
	row := kmemAdvertRow(d)
	d.trackKmemAdvert(row)
	return container.NewStack(cardBg(), container.NewVBox(status, row))
}

// trackKmemAdvert registers c with syncKmemAdverts and sets its visibility.
func (d *desk) trackKmemAdvert(c *fyne.Container) {
	c.Hidden = !d.kmemAdvertShown()
	if len(d.kmemAdverts) >= 16 { // old Settings/Welcome views
		d.kmemAdverts = d.kmemAdverts[len(d.kmemAdverts)-15:]
	}
	d.kmemAdverts = append(d.kmemAdverts, c)
}

func cardBg() *canvas.Rectangle {
	bg := canvas.NewRectangle(colCard)
	bg.CornerRadius = 8
	return bg
}

func (d *desk) dismissKmemAdvert() {
	d.app.Preferences().SetBool(prefKmemAdvertOff, true)
	d.syncKmemAdverts()
}

// syncKmemAdverts shows or hides every advert card (UI thread).
func (d *desk) syncKmemAdverts() {
	show := d.kmemAdvertShown()
	for _, c := range d.kmemAdverts {
		if c.Visible() != show {
			c.Hidden = !show
			c.Refresh()
		}
	}
}

// openKmemPatreon opens where patrons get kmem.
func (d *desk) openKmemPatreon() {
	if u, err := url.Parse(kmemPatreonURL); err == nil {
		_ = d.app.OpenURL(u)
	}
}

// syncKmemButton: with kmem the toolbar's and sidebar's button is "kmem" (the
// kmem box); without it "Memory" (Claude's auto memory). Also updates the
// adverts and the switch offer (UI thread).
func (d *desk) syncKmemButton() {
	on := d.kmemAvailable()
	label, icon := "Memory", theme.DocumentIcon()
	if on {
		label, icon = "kmem", theme.StorageIcon()
	}
	if bt := d.box().btns[boxKmem]; bt != nil {
		for _, b := range []*widget.Button{bt.btn, bt.side} {
			if b != nil && b.Text != label {
				b.SetText(label)
				b.SetIcon(icon)
			}
		}
		if !on {
			if bt.bubble != nil {
				bt.bubble.box.Hide()
			}
			if bt.sideBub != nil {
				bt.sideBub.box.Hide()
			}
		}
	}
	if !on && d.assistBoxRole() == boxKmem {
		d.closeAssistBox()
	}
	d.syncKmemAdverts()
	d.syncKmemOnlySettings()
	d.syncKmemSwitchBanner()
}

// syncKmemOnlySettings hides the kmem-only controls of Settings → General →
// Memory (kmem) without kmem: they do nothing then (UI thread).
func (d *desk) syncKmemOnlySettings() {
	if c := d.kmemOnlySet; c != nil && c.Hidden == d.kmemAvailable() {
		c.Hidden = !d.kmemAvailable()
		c.Refresh()
	}
}

// kmemSwitchOffer: kmem is installed, sessions still use Claude's memory, and
// the offer was not answered yet.
func (d *desk) kmemSwitchOffer() bool {
	return d.kmemAvailable() && !d.kmemMemory() && !d.app.Preferences().Bool(prefKmemSwitchOffered)
}

func (d *desk) buildKmemSwitchBanner() fyne.CanvasObject {
	lbl := widget.NewLabel(kmemSwitchText)
	lbl.Wrapping = fyne.TextWrapWord
	use := widget.NewButton("Switch to kmem...", func() { d.answerKmemSwitch(true) })
	use.Importance = widget.HighImportance
	no := widget.NewButton("No thanks", func() { d.answerKmemSwitch(false) })
	bg := canvas.NewRectangle(colCard)
	bg.CornerRadius = 8
	d.kmemSwitch = container.NewStack(bg, container.NewPadded(container.NewBorder(nil, nil, nil, container.NewHBox(use, no), lbl)))
	d.kmemSwitch.Hide()
	return d.kmemSwitch
}

func (d *desk) syncKmemSwitchBanner() {
	b := d.kmemSwitch
	if b == nil {
		return
	}
	show := d.kmemSwitchOffer()
	if b.Visible() == show {
		return
	}
	b.Hidden = !show
	b.Refresh()
	if d.headerBox != nil {
		d.headerBox.Refresh()
	}
}

// answerKmemSwitch: "Switch to kmem..." opens the switch's preview
// (kmemswitchui.go; the offer goes away once the switch is done), "No
// thanks" hides the offer for good (Settings → Memory keeps the switch).
func (d *desk) answerKmemSwitch(use bool) {
	if use {
		d.showKmemSwitch()
		return
	}
	d.app.Preferences().SetBool(prefKmemSwitchOffered, true)
	d.syncKmemSwitchBanner()
}

// autoMemoryPath: Claude Code's MEMORY.md for a project folder.
func autoMemoryPath(cwd string) string {
	return filepath.Join(transcript.DefaultRoot(), transcript.Sanitize(cwd), "memory", "MEMORY.md")
}

// claudeMemoryPane: the current project's MEMORY.md, editable, with the
// advert (the Memory button without kmem).
func (d *desk) claudeMemoryPane(cwd string, parent func() fyne.Window) fyne.CanvasObject {
	if cwd == "" {
		return container.NewVBox(widget.NewLabel("Open a session first: its project's auto memory is shown here."), d.kmemAdvert())
	}
	path := autoMemoryPath(cwd)
	head := caption("Claude Code's auto memory for " + folderLabel(cwd) + " (" + tildePath(path) + "). Sessions read it at start; Claude adds to it while it works.")
	head.Wrapping = fyne.TextWrapWord
	ed := widget.NewMultiLineEntry()
	ed.Wrapping = fyne.TextWrapWord
	ed.SetPlaceHolder("No MEMORY.md yet. Write notes for Claude here and save.")
	if b, err := os.ReadFile(path); err == nil {
		ed.SetText(string(b))
	}
	status := caption("")
	save := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() {
		if err := saveAutoMemory(path, ed.Text); err != nil {
			dialog.ShowError(err, parent())
			return
		}
		status.SetText("Saved.")
	})
	save.Importance = widget.HighImportance
	var others []string
	if es, err := os.ReadDir(filepath.Dir(path)); err == nil {
		for _, e := range es {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && e.Name() != "MEMORY.md" {
				others = append(others, e.Name())
			}
		}
	}
	var bottom []fyne.CanvasObject
	if len(others) > 0 {
		c := caption("Also in this folder: " + strings.Join(others, ", "))
		c.Wrapping = fyne.TextWrapWord
		bottom = append(bottom, c)
	}
	bottom = append(bottom, container.NewHBox(save, status), d.kmemAdvert())
	return container.NewBorder(head, container.NewVBox(bottom...), nil, nil, ed)
}

// saveAutoMemory writes MEMORY.md (temp + rename; the folder is created).
func saveAutoMemory(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".quill-tmp"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// showClaudeMemory: the Memory button without kmem.
func (d *desk) showClaudeMemory() {
	dlg := dialog.NewCustom("Memory (Claude's auto memory)", "Close", d.claudeMemoryPane(d.cwdOf(currentID(d)), func() fyne.Window { return d.win }), d.win)
	dlg.Resize(fyne.NewSize(720, 520))
	dlg.Show()
}
