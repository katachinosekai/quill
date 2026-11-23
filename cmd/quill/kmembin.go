package main

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// binView is kmem's recycle bin in its own dialog, opened from Settings →
// General → Memory (kmem) and from the kmem panel's "Waiting for you" (user
// 2026-11-22; it was a tab of the kmem panel). Restoring and emptying refresh
// the kmem button's bubble and an open kmem window.
type binView struct {
	d      *desk
	parent fyne.Window // the window its dialogs open on
	box    *fyne.Container
	dlg    dialog.Dialog
	item   dialog.Dialog // the open bin item window (tests)
}

// showBinDialog opens the bin on the main window (Settings).
func (d *desk) showBinDialog() { d.showBinDialogOn(d.win) }

// showBinDialogOn opens the bin on parent (the kmem window when opened from
// it; nil means the main window).
func (d *desk) showBinDialogOn(parent fyne.Window) {
	if parent == nil {
		parent = d.win
	}
	if !d.kmemAvailable() {
		dialog.ShowInformation("kmem", "kmem is not available on this computer. Set it up in Settings → General → Memory (kmem).", parent)
		return
	}
	b := d.newBinView()
	b.parent = parent
	b.dlg = dialog.NewCustom("kmem recycle bin", "Close", container.NewVScroll(b.box), parent)
	b.dlg.Resize(fyne.NewSize(860, 560))
	b.dlg.Show()
	b.reload()
}

func (d *desk) newBinView() *binView {
	b := &binView{d: d, parent: d.win, box: container.NewVBox(widget.NewLabel("Loading from kmem..."))}
	d.kmemBinNow = b
	return b
}

// reload reads the bin in the background, fills the list and refreshes the
// bubble.
func (b *binView) reload() {
	d := b.d
	go func() {
		out, _ := d.runKmemShared("trash", "list")
		bin := parseBin(out)
		d.do(func() { b.fill(bin) })
	}()
	d.refreshKmemNeeds()
}

// changed: after a restore or purge, the bin, the bubble and an open kmem
// window.
func (b *binView) changed() {
	b.reload()
	if p := b.d.kmemPanelNow; p != nil {
		p.reload()
	}
}

// binLines turns bin items back into kmem's head lines for parseBinWait.
func binLines(bin []binItem) string {
	var sb strings.Builder
	for _, it := range bin {
		sb.WriteString("[" + it.ID + "] " + it.Head + "\n")
	}
	return sb.String()
}

func (b *binView) fill(bin []binItem) {
	empty := widget.NewButtonWithIcon("Empty bin...", theme.DeleteIcon(), b.emptyBin)
	empty.Importance = widget.DangerImportance
	rows := []fyne.CanvasObject{
		caption("Facts and tasks moved to the bin are hidden everywhere. Click an item to read it in full and why it is in the bin, and to restore or delete it. Deleting is kmem's final purge: it blanks the text in kmem's history and backups and cannot be undone; only you can do it, in a terminal where kmem asks you to type the confirmation."),
	}
	if len(bin) == 0 {
		rows = append(rows, widget.NewLabel("The recycle bin is empty."))
		empty.Disable()
	} else {
		// kmem trash empty purges only items binned 7+ days, unless you
		// override at its prompt: say what that means right now.
		w := parseBinWait(binLines(bin), kmemNow())
		rows = append(rows, boldLabel(w.binLine(kmemNow())+"."))
	}
	for _, it := range bin {
		it := it
		label := "[" + it.ID + "] " + it.binMeta() + "  ·  " + oneLine(firstNonEmpty(it.binReason(), it.Preview), 90)
		row := widget.NewButton(label, func() { b.showBinItem(it) })
		row.Alignment = widget.ButtonAlignLeading
		row.Importance = widget.LowImportance
		rows = append(rows, row)
	}
	rows = append(rows, widget.NewSeparator(), container.NewHBox(layout.NewSpacer(), empty))
	setBox(b.box, rows...)
}

// showBinItem opens one bin item in its own window: the full reason and
// text (scrolling when long), with Restore and Delete for good.
func (b *binView) showBinItem(it binItem) {
	d := b.d
	text := wrapLabel("Loading the full text from kmem…")
	body := container.NewVBox(
		boldLabel("["+it.ID+"] "+it.binMeta()),
		widget.NewSeparator(),
		boldLabel("Why it is in the bin"),
		wrapLabel(firstNonEmpty(it.binReason(), "(no reason given)")),
		widget.NewSeparator(),
		boldLabel("Full text"),
		text,
	)
	go func() {
		out, err := d.runKmem("kmem", "show", it.ID)
		d.do(func() {
			if err != nil || strings.TrimSpace(out) == "" {
				text.SetText(firstNonEmpty(it.Preview, "(kmem didn't return the text)"))
				return
			}
			text.SetText(strings.TrimSpace(out))
		})
	}()
	var win dialog.Dialog
	restore := widget.NewButtonWithIcon("Restore", theme.ContentUndoIcon(), func() {
		go func() {
			out, err := d.runKmem("kmem", "trash", "restore", it.ID)
			d.do(func() {
				if err != nil {
					dialog.ShowError(fmt.Errorf("kmem: %s", oneLine(firstNonEmpty(strings.TrimSpace(out), err.Error()), 300)), b.parent)
					return
				}
				win.Hide()
				b.changed()
			})
		}()
	})
	purge := widget.NewButtonWithIcon("Delete for good...", theme.DeleteIcon(), func() {
		dialog.ShowConfirm("Delete ["+it.ID+"] for good?",
			"A terminal opens and runs kmem's final purge for this item (kmem trash empty "+it.ID+"). kmem asks you to type the confirmation there. Purged text is gone for good, also from backups.",
			func(ok bool) {
				if !ok {
					return
				}
				cmd := terminalCmd(firstNonEmpty(kmemBin(), "kmem"), it.ID)
				if err := cmd.Start(); err != nil {
					dialog.ShowError(fmt.Errorf("could not open a terminal (%v); run `kmem trash empty %s` in a terminal yourself", err, it.ID), b.parent)
					return
				}
				go func() {
					_ = cmd.Wait()
					d.do(func() {
						win.Hide()
						b.changed()
					})
				}()
			}, b.parent)
	})
	purge.Importance = widget.DangerImportance
	closeBtn := widget.NewButton("Close", func() { win.Hide() })
	content := container.NewBorder(nil, container.NewHBox(restore, purge, layout.NewSpacer(), closeBtn), nil, nil, container.NewVScroll(body))
	win = dialog.NewCustomWithoutButtons("Recycle bin item", content, b.parent)
	win.Resize(fyne.NewSize(720, 520))
	win.Show()
	b.item = win
}

func (b *binView) emptyBin() {
	d := b.d
	dialog.ShowConfirm("Empty the recycle bin?",
		"A terminal opens and runs kmem's final purge (kmem trash empty). kmem purges only items binned 7+ days (you can override at its prompt), lists what will be purged and asks you to type the confirmation there. Purged text is gone for good, also from backups.",
		func(ok bool) {
			if !ok {
				return
			}
			path := firstNonEmpty(kmemBin(), "kmem")
			cmd := terminalCmd(path)
			if err := cmd.Start(); err != nil {
				dialog.ShowError(fmt.Errorf("could not open a terminal (%v); run `kmem trash empty` in a terminal yourself", err), b.parent)
				return
			}
			go func() {
				_ = cmd.Wait()
				d.do(b.changed)
			}()
		}, b.parent)
}
