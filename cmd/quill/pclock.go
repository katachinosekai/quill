package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/folders"
)

// Paired-PC lock (user 2026-11-21): another Quill Desktop that pairs with
// this one must unlock with this PC's password before it gets anything
// (sessions, chats, folders, events, usage). The unlock gives a token kept in
// memory on both sides; it expires after the idle time without activity from
// that device, and after 5 wrong passwords the device waits 15 minutes.
// Phones are not locked unless their Lock switch says so.

const (
	pcPassIter     = 600000
	pcMaxFails     = 5
	pcLockout      = 15 * time.Minute
	pcIdleDefault  = 15
	unlockHeader   = "X-Quill-Unlock"
	prefPCPassword = "pcPassword"
	prefPCIdle     = "pcIdleMin"
)

var pcIdleChoices = []int{5, 15, 30, 60, 240}

func hashPCPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key, _ := pbkdf2.Key(sha256.New, pw, salt, pcPassIter, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pcPassIter, hex.EncodeToString(salt), hex.EncodeToString(key))
}

func checkPCPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	salt, err2 := hex.DecodeString(parts[2])
	want, err3 := hex.DecodeString(parts[3])
	if err != nil || err2 != nil || err3 != nil || iter < 1 || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

type pcToken struct {
	tok  string
	last time.Time
}

type pcFails struct {
	n     int
	until time.Time
}

type pcLock struct {
	mu     sync.Mutex
	tokens map[string]pcToken
	fails  map[string]pcFails
	now    func() time.Time
}

func newPCLock() *pcLock {
	return &pcLock{tokens: map[string]pcToken{}, fails: map[string]pcFails{}, now: time.Now}
}

func (l *pcLock) reset() {
	l.mu.Lock()
	l.tokens = map[string]pcToken{}
	l.mu.Unlock()
}

func (l *pcLock) forget(id string) {
	l.mu.Lock()
	delete(l.tokens, id)
	l.mu.Unlock()
}

// valid checks a device's token and, when touch is set, counts the request
// as activity.
func (l *pcLock) valid(id, tok string, idle time.Duration, touch bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tokens[id]
	if !ok || tok == "" || subtle.ConstantTimeCompare([]byte(t.tok), []byte(tok)) != 1 {
		return false
	}
	now := l.now()
	if now.Sub(t.last) > idle {
		delete(l.tokens, id)
		return false
	}
	if touch {
		t.last = now
		l.tokens[id] = t
	}
	return true
}

// unlock checks the password; ok gives a fresh token, else code and msg
// say why (no_password, locked_out, wrong_password).
func (l *pcLock) unlock(id, stored, pw string) (tok, code, msg string) {
	l.mu.Lock()
	f := l.fails[id]
	now := l.now()
	if f.n >= pcMaxFails && now.Before(f.until) {
		l.mu.Unlock()
		return "", "locked_out", fmt.Sprintf("too many wrong passwords; try again in %d min", int(f.until.Sub(now).Minutes())+1)
	}
	l.mu.Unlock()
	if stored == "" {
		return "", "no_password", "Quill Desktop on that PC has no device password set yet: set one there (Settings → Sessions → Devices → Set password)"
	}
	ok := checkPCPassword(stored, pw)
	l.mu.Lock()
	defer l.mu.Unlock()
	now = l.now()
	if !ok {
		n, already := l.failLocked(id, now)
		if already || n >= pcMaxFails {
			return "", "locked_out", fmt.Sprintf("wrong password; locked for %d min", int(pcLockout.Minutes()))
		}
		return "", "wrong_password", fmt.Sprintf("wrong password (%d of %d tries)", n, pcMaxFails)
	}
	// A concurrent failure may have locked the device while the password
	// was being checked: the lock-out wins.
	if f := l.fails[id]; f.n >= pcMaxFails && now.Before(f.until) {
		return "", "locked_out", fmt.Sprintf("too many wrong passwords; try again in %d min", int(f.until.Sub(now).Minutes())+1)
	}
	delete(l.fails, id)
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok = hex.EncodeToString(b)
	l.tokens[id] = pcToken{tok: tok, last: now}
	return tok, "", ""
}

// failLocked counts one failed unlock (l.mu held). During a lock-out it
// changes nothing (already=true): the count is reset only once the lock-out
// time has passed, so concurrent failures can never lower it or lift a
// lock-out.
func (l *pcLock) failLocked(id string, now time.Time) (n int, already bool) {
	f := l.fails[id]
	if f.n >= pcMaxFails {
		if now.Before(f.until) {
			return f.n, true
		}
		f.n = 0
	}
	f.n++
	f.until = now.Add(pcLockout)
	l.fails[id] = f
	return f.n, false
}

func (d *desk) pcIdle() time.Duration {
	m := d.app.Preferences().IntWithFallback(prefPCIdle, pcIdleDefault)
	if m < 1 {
		m = pcIdleDefault
	}
	return time.Duration(m) * time.Minute
}

func (d *desk) device(id string) (folders.Phone, bool) {
	for _, p := range d.phones() {
		if p.ID == id {
			return p, true
		}
	}
	return folders.Phone{}, false
}

// pcGate is the phone server's gate (runs on the HTTP goroutine). Writes and
// /phone/unlock/touch count as activity; reads (polling, the stream) don't,
// so an idle viewer is locked again after the idle time.
func (d *desk) pcGate(id string, r *http.Request) (bool, string) {
	p, ok := d.device(id)
	if !ok || !p.NeedsUnlock() {
		return true, ""
	}
	touch := r.Method == http.MethodPost
	if d.pcLock.valid(id, r.Header.Get(unlockHeader), d.pcIdle(), touch) {
		return true, ""
	}
	return false, "this device must unlock with the PC's password"
}

// unlockAPI serves POST /phone/unlock {password} and /phone/unlock/touch.
func (a *phoneAPI) unlockAPI(w http.ResponseWriter, r *http.Request, path, phone string) {
	d := a.d
	if path == "unlock/touch" {
		folders.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "idleMin": int(d.pcIdle().Minutes())})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "body must be JSON {password}")
		return
	}
	p, ok := d.device(phone)
	if !ok {
		folders.WriteError(w, http.StatusForbidden, "forbidden", "unknown device")
		return
	}
	if !p.NeedsUnlock() {
		folders.WriteJSON(w, http.StatusOK, map[string]any{"token": "", "idleMin": int(d.pcIdle().Minutes()), "needed": false})
		return
	}
	tok, code, msg := d.pcLock.unlock(phone, d.app.Preferences().String(prefPCPassword), in.Password)
	if tok == "" {
		folders.WriteError(w, http.StatusForbidden, code, msg)
		return
	}
	folders.WriteJSON(w, http.StatusOK, map[string]any{"token": tok, "idleMin": int(d.pcIdle().Minutes()), "needed": true})
}

// setDevicePasswordDialog asks for a new device password; done runs after
// it was saved.
func (d *desk) setDevicePasswordDialog(why string, done func()) {
	pw, again := widget.NewPasswordEntry(), widget.NewPasswordEntry()
	items := []*widget.FormItem{widget.NewFormItem("Password", pw), widget.NewFormItem("Again", again)}
	if why != "" {
		items = append([]*widget.FormItem{widget.NewFormItem("", caption(why))}, items...)
	}
	items = append(items, widget.NewFormItem("", caption("At least 8 characters. Every paired device (phones and other PCs) asks for it when it connects; it is stored here only as a salted hash.")))
	dlg := dialog.NewForm("Device password", "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		if len([]rune(pw.Text)) < 8 || pw.Text != again.Text {
			dialog.ShowInformation("Password not saved", "The password must have at least 8 characters and both fields must match.", d.win)
			return
		}
		d.app.Preferences().SetString(prefPCPassword, hashPCPassword(pw.Text))
		d.pcLock.reset()
		if done != nil {
			done()
		}
	}, d.win)
	dlg.Resize(fyne.NewSize(480, 300))
	dlg.Show()
}

// pcLockSettings: Settings → Sessions → Devices: the password every paired
// device must enter, and the idle time (also how long this PC stays
// connected to other PCs without activity).
func (d *desk) pcLockSettings() fyne.CanvasObject {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	btn := widget.NewButton("", nil)
	btn.Importance = widget.LowImportance
	btn.Alignment = widget.ButtonAlignLeading
	clear := widget.NewButton("Remove", nil)
	clear.Importance = widget.LowImportance
	var refresh func()
	refresh = func() {
		if d.app.Preferences().String(prefPCPassword) == "" {
			status.SetText("⚠ No device password set: no paired device (phone or PC) can connect until you set one.")
			status.Importance = widget.WarningImportance
			btn.SetText("Set password...")
			btn.Importance = widget.HighImportance
			clear.Hide()
		} else {
			status.SetText("Device password: set. Paired phones and PCs ask for it when they connect (turn it off for one device with its Password switch).")
			status.Importance = widget.MediumImportance
			btn.SetText("Change password...")
			btn.Importance = widget.LowImportance
			clear.Show()
		}
		status.Refresh()
		btn.Refresh()
	}
	btn.OnTapped = func() { d.setDevicePasswordDialog("", refresh) }
	clear.OnTapped = func() {
		dialog.ShowConfirm("Remove the password?", "No paired device can connect without it (except devices whose Password switch is off).", func(ok bool) {
			if ok {
				d.app.Preferences().SetString(prefPCPassword, "")
				d.pcLock.reset()
				refresh()
			}
		}, d.win)
	}
	d.pcPassRefresh = refresh
	refresh()
	var labels []string
	for _, m := range pcIdleChoices {
		labels = append(labels, idleLabel(m))
	}
	idle := widget.NewSelect(labels, func(v string) {
		for _, m := range pcIdleChoices {
			if idleLabel(m) == v {
				d.app.Preferences().SetInt(prefPCIdle, m)
			}
		}
	})
	idle.SetSelected(idleLabel(int(d.pcIdle().Minutes())))
	idleRow := container.NewBorder(nil, nil, widget.NewLabel("Devices unlock again after no activity for"), nil, container.NewHBox(idle))
	return container.NewVBox(status, container.NewHBox(btn, clear), idleRow)
}

func idleLabel(m int) string {
	if m >= 60 {
		return plural(m/60, "hour")
	}
	return strconv.Itoa(m) + " min"
}

// setDeviceLock sets whether one paired device must unlock with the password.
func (d *desk) setDeviceLock(id string, on bool) {
	list := d.phones()
	for i := range list {
		if list[i].ID == id && list[i].NeedsUnlock() != on {
			v := on
			list[i].Lock = &v
			d.pcLock.forget(id)
			d.setPhones(list)
			return
		}
	}
}
