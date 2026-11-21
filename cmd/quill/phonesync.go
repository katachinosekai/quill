package main

import (
	"context"
	"encoding/json"
	"image"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"rsc.io/qr"

	"github.com/katachinosekai/quill/internal/folders"
)

const (
	phoneRCMax    = 4000
	phoneDebounce = time.Second
)

type phoneSync struct {
	d       *desk
	rc      map[string]string
	server  *folders.Server
	fp      string
	last    folders.File
	dirty   *time.Timer
	status  *widget.Label
	pairing *pendingPair
	api     *phoneAPI
	list    *fyne.Container
}

type pendingPair struct {
	id  string
	key string
}

func (d *desk) phone() *phoneSync {
	if d.phoneSync == nil {
		p := &phoneSync{d: d, rc: map[string]string{}}
		_ = json.Unmarshal([]byte(d.app.Preferences().String("phoneRC")), &p.rc)
		if p.rc == nil {
			p.rc = map[string]string{}
		}
		d.phoneSync = p
	}
	return d.phoneSync
}

func phoneCertDir() string {
	if v := os.Getenv("QUILL_PHONE_DIR"); v != "" {
		return v
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "quill", "phone")
}

func (d *desk) rememberRC(id, sessionURL string) {
	rcID := folders.SessionID(sessionURL)
	if rcID == "" {
		return
	}
	p := d.phone()
	if p.rc[rcID] == id {
		return
	}
	p.rc[rcID] = id
	if len(p.rc) > phoneRCMax {
		for k := range p.rc {
			delete(p.rc, k)
			if len(p.rc) <= phoneRCMax {
				break
			}
		}
	}
	if b, err := json.Marshal(p.rc); err == nil {
		d.app.Preferences().SetString("phoneRC", string(b))
	}
	d.phoneChanged()
}

func (d *desk) phoneActive() bool {
	prefs := d.app.Preferences()
	return prefs.Bool("phoneServe") || prefs.String("phoneDir") != ""
}

func (d *desk) phoneChanged() {
	if !d.phoneActive() {
		return
	}
	p := d.phone()
	if p.dirty != nil {
		p.dirty.Stop()
	}
	p.dirty = time.AfterFunc(phoneDebounce, func() { d.do(func() { d.publishPhone(false) }) })
}

func (d *desk) phoneFile() folders.File {
	p := d.phone()
	place := map[string]string{}
	var order []string
	for _, g := range d.groups {
		order = append(order, g.title)
		for _, id := range g.ids {
			place[id] = g.title
		}
	}
	sessions := map[string]string{}
	for rcID, id := range p.rc {
		if f, ok := place[id]; ok {
			sessions[rcID] = f
		}
	}
	host, _ := os.Hostname()
	return folders.Build(host, order, sessions, time.Now())
}

func (d *desk) publishPhone(force bool) {
	p := d.phone()
	f := d.phoneFile()
	if !force && f.SameContent(p.last) && p.last.Version != 0 {
		return
	}
	p.last = f
	data := f.JSON()
	if p.server != nil {
		p.server.Set(data)
	}
	if dir := d.app.Preferences().String("phoneDir"); dir != "" {
		go func() {
			err := folders.WriteAtomic(dir, data)
			d.do(func() { d.phoneStatus(err, "Folders file") })
		}()
	}
}

type phoneErr struct {
	msg string
	err error
}

func (e *phoneErr) Error() string {
	if e.msg != "" {
		return e.msg
	}
	if e.err != nil {
		return e.err.Error()
	}
	return "failed"
}

func (d *desk) phoneStatus(err error, what string) {
	p := d.phone()
	if p.status == nil {
		return
	}
	if err != nil {
		p.status.SetText(what + " failed: " + err.Error())
		return
	}
	p.status.SetText(what + " updated " + time.Now().Format("15:04"))
}

func (d *desk) startPhoneServer() error {
	p := d.phone()
	prefs := d.app.Preferences()
	if p.server != nil {
		return nil
	}
	cert, fp, err := folders.LoadOrCreateCert(phoneCertDir())
	if err != nil {
		return err
	}
	phones := d.phones()
	port := prefs.Int("phonePort")
	if port == 0 {
		port = 20000 + rand.IntN(40000)
	}
	s, err := folders.Listen(cert, port, phones)
	if err != nil {
		s, err = folders.Listen(cert, 0, phones)
		if err != nil {
			return err
		}
	}
	prefs.SetInt("phonePort", s.Port)
	prefs.SetBool("phoneServe", true)
	s.SetEvents(prefs.BoolWithFallback("phoneNotify", true))
	s.SetUsageSource(d.phoneUsageJSON)
	s.SetSeen(func(id string) { d.do(func() { d.phoneSeen(id) }) })
	p.api = newPhoneAPI(d, s)
	s.SetAPI(p.api)
	s.SetGate(d.pcGate)
	p.server, p.fp = s, fp
	d.publishPhone(true)
	return nil
}

func (d *desk) initPhone() {
	if d.app.Preferences().Bool("phoneServe") {
		if err := d.startPhoneServer(); err != nil {
			d.phone().last = folders.File{}
		}
	}
	if d.phoneActive() {
		d.publishPhone(true)
	}
}

func (d *desk) stopPhone() {
	if p := d.phoneSync; p != nil && p.server != nil {
		p.server.Close()
		p.server = nil
		if p.api != nil {
			p.api.close()
			p.api = nil
		}
	}
}

func (d *desk) pairLink() string {
	p := d.phone()
	host, _ := os.Hostname()
	pair := folders.Pair{File: folders.FileName, Name: host}
	if p.server != nil {
		if p.pairing == nil {
			d.beginPairing()
		}
		pair.Hosts = folders.LocalHosts()
		if n := tailscaleName(); n != "" {
			pair.Hosts = append(pair.Hosts, n)
		}
		pair.Port = p.server.Port
		pair.Key = p.pairing.key
		pair.ID = p.pairing.id
		pair.Fingerprint = p.fp
	}
	return pair.Link()
}

func tailscaleName() string {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "status", "--json").Output()
	if err != nil {
		return ""
	}
	var st struct {
		Self struct {
			DNSName string
		}
	}
	if json.Unmarshal(out, &st) != nil {
		return ""
	}
	return strings.TrimSuffix(st.Self.DNSName, ".")
}

func (d *desk) phones() []folders.Phone {
	prefs := d.app.Preferences()
	list := folders.ParsePhones(prefs.String("phones"))
	if old := prefs.String("phoneKey"); old != "" {
		list = append(list, folders.LegacyPhone(old, time.Now()))
		prefs.SetString("phones", folders.PhonesJSON(list))
		prefs.SetString("phoneKey", "")
	}
	return list
}

func (d *desk) setPhones(list []folders.Phone) {
	d.app.Preferences().SetString("phones", folders.PhonesJSON(list))
	if p := d.phone(); p.server != nil {
		p.server.SetPhones(list)
	}
	d.updatePhoneList()
}

func (d *desk) beginPairing() {
	ph, key := folders.NewPhone("New device", time.Now())
	d.setPhones(append(d.phones(), ph))
	d.phone().pairing = &pendingPair{id: ph.ID, key: key}
}

func (d *desk) endPairing() {
	p := d.phone()
	if p.pairing == nil {
		return
	}
	id := p.pairing.id
	p.pairing = nil
	list := d.phones()
	for i, ph := range list {
		if ph.ID == id && ph.LastSeen == 0 {
			d.setPhones(append(list[:i:i], list[i+1:]...))
			return
		}
	}
}

func (d *desk) phoneSeen(id string) {
	list := d.phones()
	now := time.Now().UnixMilli()
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if list[i].LastSeen != 0 && now-list[i].LastSeen < time.Minute.Milliseconds() {
			return
		}
		list[i].LastSeen = now
		d.app.Preferences().SetString("phones", folders.PhonesJSON(list))
		d.updatePhoneList()
		return
	}
}

func (d *desk) revokePhone(id string) {
	list := d.phones()
	for i, ph := range list {
		if ph.ID == id {
			d.pcLock.forget(id)
			d.setPhones(append(list[:i:i], list[i+1:]...))
			return
		}
	}
}

func (d *desk) updatePhoneList() {
	p := d.phone()
	if p.list == nil {
		return
	}
	var rows []fyne.CanvasObject
	for _, ph := range d.phones() {
		if p.pairing != nil && ph.ID == p.pairing.id && ph.LastSeen == 0 {
			continue
		}
		seen := "never connected"
		if ph.LastSeen > 0 {
			seen = "last seen " + ago(time.UnixMilli(ph.LastSeen))
		}
		ph := ph
		revoke := widget.NewButton("Revoke", func() {
			dialog.ShowConfirm("Revoke device?", "\""+ph.Name+"\" loses access at once. Pair it again to bring it back.", func(ok bool) {
				if ok {
					d.revokePhone(ph.ID)
				}
			}, d.win)
		})
		revoke.Importance = widget.LowImportance
		kind := map[string]string{"pc": "PC", "phone": "phone"}[ph.Kind]
		if kind != "" {
			seen = kind + " · " + seen
		}
		lock := widget.NewCheck("Password", func(on bool) { d.setDeviceLock(ph.ID, on) })
		lock.Checked = ph.NeedsUnlock()
		rows = append(rows, container.NewBorder(nil, nil, nil, container.NewHBox(lock, revoke), widget.NewLabel(ph.Name+" · "+seen)))
	}
	if len(rows) == 0 {
		rows = append(rows, mutedText("No paired devices", fontMeta))
	}
	p.list.Objects = rows
	p.list.Refresh()
}

func (d *desk) pairDialog() {
	if err := d.startPhoneServer(); err != nil {
		dialog.ShowError(err, d.win)
		return
	}
	img := canvas.NewImageFromImage(nil)
	img.FillMode = canvas.ImageFillOriginal
	img.ScaleMode = canvas.ImageScalePixels
	linkText := widget.NewLabel("")
	linkText.Wrapping = fyne.TextWrapBreak
	linkText.Hide()
	var link string
	show := func() {
		link = d.pairLink()
		code, err := qr.Encode(link, qr.L)
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		scale := d.win.Canvas().Scale()
		px, side := qrLayout(code.Size, scale)
		img.Image = qrImage(code, px)
		img.SetMinSize(fyne.NewSquareSize(side))
		img.Refresh()
		linkText.SetText(link)
	}
	show()
	help := widget.NewLabel("In Quill Android: ⚙ → Scan pairing QR code, then point it at this code. Or copy the link and use \"Paste pairing link\" in the app's settings. The device reaches this computer on the same network, or anywhere over Tailscale.")
	help.Wrapping = fyne.TextWrapWord
	copyBtn := widget.NewButton("Copy pairing link", func() {
		d.app.Clipboard().SetContent(link)
	})
	showBtn := widget.NewButton("Show link", func() {
		if linkText.Visible() {
			linkText.Hide()
		} else {
			linkText.Show()
		}
	})
	stop := widget.NewButton("Stop sharing on the network", func() {
		d.stopPhone()
		d.app.Preferences().SetBool("phoneServe", false)
		d.updatePhoneRow()
	})
	body := container.NewVBox(container.NewCenter(img), help, container.NewGridWithColumns(2, copyBtn, showBtn), stop, linkText)
	dlg := dialog.NewCustom("Pair device", "Close", body, d.win)
	dlg.Resize(fyne.NewSize(460, 600))
	dlg.SetOnClosed(func() {
		d.endPairing()
		d.updatePhoneRow()
	})
	dlg.Show()
}

func (d *desk) chooseFolderFile() {
	dlg := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		if err != nil || u == nil {
			return
		}
		d.app.Preferences().SetString("phoneDir", u.Path())
		d.publishPhone(true)
		d.updatePhoneRow()
	}, d.win)
	dlg.Resize(fyne.NewSize(720, 520))
	dlg.Show()
}

func (d *desk) buildPhoneRow() fyne.CanvasObject {
	p := d.phone()
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord
	p.status.TextStyle.Italic = true
	pairBtn := widget.NewButton("Pair a device...", d.pairDialog)
	pairBtn.Importance = widget.LowImportance
	pairBtn.Alignment = widget.ButtonAlignLeading
	d.phoneFileBtn = widget.NewButton("", d.chooseFolderFile)
	d.phoneFileBtn.Importance = widget.LowImportance
	d.phoneFileBtn.Alignment = widget.ButtonAlignLeading
	d.phoneFileOff = widget.NewButton("Off", func() {
		d.app.Preferences().SetString("phoneDir", "")
		d.updatePhoneRow()
	})
	d.phoneFileOff.Importance = widget.LowImportance
	d.updatePhoneRow()
	notifyChk := widget.NewCheck("Send notifications to paired devices", func(on bool) {
		d.app.Preferences().SetBool("phoneNotify", on)
		if p.server != nil {
			p.server.SetEvents(on)
		}
	})
	notifyChk.Checked = d.app.Preferences().BoolWithFallback("phoneNotify", true)
	controlChk := widget.NewCheck("Let paired devices control sessions", func(on bool) { d.app.Preferences().SetBool("phoneControl", on) })
	controlChk.Checked = d.app.Preferences().Bool("phoneControl")
	head := mutedText("Devices", fontMeta)
	p.list = container.NewVBox()
	d.updatePhoneList()
	return container.NewVBox(head, pairBtn, p.list, container.NewBorder(nil, nil, nil, d.phoneFileOff, d.phoneFileBtn), notifyChk, controlChk, d.pcLockSettings(), p.status)
}

func (d *desk) updatePhoneRow() {
	if d.phoneFileBtn == nil {
		return
	}
	prefs := d.app.Preferences()
	if dir := prefs.String("phoneDir"); dir != "" {
		d.phoneFileBtn.SetText("Folders file: " + tildePath(dir))
		d.phoneFileOff.Show()
	} else {
		d.phoneFileBtn.SetText("Write folders file to...")
		d.phoneFileOff.Hide()
	}
}

func (d *desk) phoneButton() fyne.CanvasObject {
	b := widget.NewButtonWithIcon("Devices", theme.ComputerIcon(), d.pairDialog)
	b.Importance = widget.LowImportance
	return b
}

const (
	qrQuiet  = 4
	qrTarget = 320
)

func qrLayout(modules int, scale float32) (int, float32) {
	if scale <= 0 {
		scale = 1
	}
	total := modules + 2*qrQuiet
	px := max(2, int(qrTarget*scale)/total)
	return px, float32(total*px) / scale
}

func qrImage(code *qr.Code, px int) image.Image {
	total := code.Size + 2*qrQuiet
	img := image.NewGray(image.Rect(0, 0, total*px, total*px))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if !code.Black(x, y) {
				continue
			}
			x0, y0 := (x+qrQuiet)*px, (y+qrQuiet)*px
			for yy := y0; yy < y0+px; yy++ {
				row := img.Pix[yy*img.Stride:]
				for xx := x0; xx < x0+px; xx++ {
					row[xx] = 0
				}
			}
		}
	}
	return img
}
