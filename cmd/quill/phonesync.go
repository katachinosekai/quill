package main

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	phoneRCMax     = 4000
	phoneDebounce  = time.Second
	phoneGistEvery = 60 * time.Second
)

type phoneSync struct {
	d       *desk
	rc      map[string]string
	server  *folders.Server
	fp      string
	last    folders.File
	dirty   *time.Timer
	gistMu  sync.Mutex
	gistAt  time.Time
	gistDue *time.Timer
	status  *widget.Label
	ghPath  string
}

func (d *desk) phone() *phoneSync {
	if d.phoneSync == nil {
		p := &phoneSync{d: d, rc: map[string]string{}}
		_ = json.Unmarshal([]byte(d.app.Preferences().String("phoneRC")), &p.rc)
		if p.rc == nil {
			p.rc = map[string]string{}
		}
		p.ghPath, _ = exec.LookPath(phoneGH())
		d.phoneSync = p
	}
	return d.phoneSync
}

func phoneGH() string {
	if v := os.Getenv("QUILL_GH"); v != "" {
		return v
	}
	return "gh"
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
	return prefs.Bool("phoneServe") || prefs.String("phoneDir") != "" || prefs.String("phoneGist") != ""
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
	if d.app.Preferences().String("phoneGist") != "" {
		d.scheduleGist(data)
	}
}

func (d *desk) scheduleGist(data []byte) {
	p := d.phone()
	p.gistMu.Lock()
	defer p.gistMu.Unlock()
	if p.gistDue != nil {
		p.gistDue.Stop()
	}
	wait := time.Until(p.gistAt.Add(phoneGistEvery))
	if wait < 0 {
		wait = 0
	}
	p.gistDue = time.AfterFunc(wait, func() {
		p.gistMu.Lock()
		p.gistAt = time.Now()
		p.gistMu.Unlock()
		err := d.pushGist(data)
		d.do(func() { d.phoneStatus(err, "Gist") })
	})
}

func (d *desk) pushGist(data []byte) error {
	p := d.phone()
	gist := d.app.Preferences().String("phoneGist")
	if gist == "" || p.ghPath == "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "quill-gist-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, folders.FileName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id := gist[strings.Index(gist, "/")+1:]
	out, err := exec.CommandContext(ctx, p.ghPath, "gist", "edit", id, "--filename", folders.FileName, path).CombinedOutput()
	if err != nil {
		return &phoneErr{strings.TrimSpace(string(out)), err}
	}
	return nil
}

func (d *desk) createGist() (string, error) {
	p := d.phone()
	dir, err := os.MkdirTemp("", "quill-gist-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, folders.FileName)
	if err := os.WriteFile(path, d.phoneFile().JSON(), 0o600); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p.ghPath, "gist", "create", "--desc", "Quill folders for Quill for Android", path).CombinedOutput()
	if err != nil {
		return "", &phoneErr{strings.TrimSpace(string(out)), err}
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return "", &phoneErr{"gh printed no gist URL", nil}
	}
	return folders.GistFromURL(lines[len(lines)-1])
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
	key := prefs.String("phoneKey")
	if key == "" {
		key = folders.NewKey()
		prefs.SetString("phoneKey", key)
	}
	port := prefs.Int("phonePort")
	if port == 0 {
		port = 20000 + rand.IntN(40000)
	}
	s, err := folders.Listen(cert, port, key)
	if err != nil {
		s, err = folders.Listen(cert, 0, key)
		if err != nil {
			return err
		}
	}
	prefs.SetInt("phonePort", s.Port)
	prefs.SetBool("phoneServe", true)
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
	}
}

func (d *desk) pairLink() string {
	p := d.phone()
	prefs := d.app.Preferences()
	host, _ := os.Hostname()
	pair := folders.Pair{File: folders.FileName, Gist: prefs.String("phoneGist"), Name: host}
	if p.server != nil {
		pair.Hosts = folders.LocalHosts()
		pair.Port = p.server.Port
		pair.Key = prefs.String("phoneKey")
		pair.Fingerprint = p.fp
	}
	return pair.Link()
}

func (d *desk) pairDialog() {
	if err := d.startPhoneServer(); err != nil {
		dialog.ShowError(err, d.win)
		return
	}
	img := canvas.NewImageFromResource(nil)
	img.FillMode = canvas.ImageFillContain
	img.ScaleMode = canvas.ImageScalePixels
	linkText := widget.NewLabel("")
	linkText.Wrapping = fyne.TextWrapBreak
	linkText.Hide()
	var link string
	show := func() {
		link = d.pairLink()
		code, err := qr.Encode(link, qr.M)
		if err != nil {
			dialog.ShowError(err, d.win)
			return
		}
		img.Resource = fyne.NewStaticResource("pair.png", code.PNG())
		img.Refresh()
		linkText.SetText(link)
	}
	show()
	help := widget.NewLabel("Scan with your phone's camera and open the link in Quill for Android, or copy the link and use \"Paste pairing link\" in the app's settings. Folders update while the phone is on the same network as this computer.")
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
	newKey := widget.NewButton("New key (unpairs phones)", func() {
		k := folders.NewKey()
		d.app.Preferences().SetString("phoneKey", k)
		d.phone().server.SetKey(k)
		show()
	})
	stop := widget.NewButton("Stop sharing on the network", func() {
		d.stopPhone()
		d.app.Preferences().SetBool("phoneServe", false)
		d.updatePhoneRow()
	})
	body := container.NewVBox(sized(300, 300, img), help, container.NewGridWithColumns(2, copyBtn, showBtn), container.NewGridWithColumns(2, newKey, stop), linkText)
	dlg := dialog.NewCustom("Pair phone", "Close", body, d.win)
	dlg.Resize(fyne.NewSize(460, 600))
	dlg.SetOnClosed(d.updatePhoneRow)
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

func (d *desk) toggleGist(on bool) {
	prefs := d.app.Preferences()
	if !on {
		prefs.SetString("phoneGist", "")
		d.updatePhoneRow()
		return
	}
	if prefs.String("phoneGist") != "" {
		return
	}
	d.phone().status.SetText("Creating secret gist...")
	go func() {
		g, err := d.createGist()
		d.do(func() {
			if err != nil {
				d.phoneStatus(err, "Gist")
				d.updatePhoneRow()
				return
			}
			prefs.SetString("phoneGist", g)
			d.phoneStatus(nil, "Gist")
			d.updatePhoneRow()
		})
	}()
}

func (d *desk) buildPhoneRow() fyne.CanvasObject {
	p := d.phone()
	p.status = widget.NewLabel("")
	p.status.Wrapping = fyne.TextWrapWord
	p.status.TextStyle.Italic = true
	pairBtn := widget.NewButton("Pair phone...", d.pairDialog)
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
	d.phoneGistChk = widget.NewCheck("Publish to a secret gist", nil)
	if p.ghPath == "" {
		d.phoneGistChk.Hide()
	}
	d.updatePhoneRow()
	d.phoneGistChk.OnChanged = d.toggleGist
	head := mutedText("Phone", fontMeta)
	return container.NewVBox(head, pairBtn, container.NewBorder(nil, nil, nil, d.phoneFileOff, d.phoneFileBtn), d.phoneGistChk, p.status)
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
	on := prefs.String("phoneGist") != ""
	if d.phoneGistChk.Checked != on {
		cb := d.phoneGistChk.OnChanged
		d.phoneGistChk.OnChanged = nil
		d.phoneGistChk.SetChecked(on)
		d.phoneGistChk.OnChanged = cb
	}
}

func (d *desk) phoneButton() fyne.CanvasObject {
	b := widget.NewButtonWithIcon("Phone", theme.ComputerIcon(), d.pairDialog)
	b.Importance = widget.LowImportance
	return b
}
