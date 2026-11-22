package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/folders"
)

// Phone settings (user 2026-11-22, asked for by Quill Android): two Settings
// → General switches go to the phone API so Quill Android can show and
// toggle them. "buildGate" is "Ask before building" (prefBuildGate),
// "coordinator" is "Coordinate with subagents" (prefCoordinator; the pref
// itself, coordinatorOn also checks the session's model). GET
// /phone/sessions and every /phone/stream reply carry "settings"; a change
// from either side is a {"type":"settings"} stream change; POST
// /phone/settings sets them (needs phone control).

var phoneSettingPrefs = map[string]string{"buildGate": prefBuildGate, "coordinator": prefCoordinator}

// phoneSettings reads only preferences, which are safe off the UI thread.
func (d *desk) phoneSettings() map[string]any {
	return map[string]any{
		"buildGate":   d.buildGateOn(),
		"coordinator": d.app.Preferences().BoolWithFallback(prefCoordinator, true),
	}
}

// settingsChanged runs on the UI thread after either pref changed: it puts
// the Settings → General checkboxes in line and tells the phone streams.
func (d *desk) settingsChanged() {
	set := d.phoneSettings()
	for chk, on := range map[*widget.Check]bool{d.buildChk: set["buildGate"].(bool), d.coordChk: set["coordinator"].(bool)} {
		if chk != nil && chk.Checked != on {
			chk.Checked = on
			chk.Refresh()
		}
	}
	if d.phoneSync != nil && d.phoneSync.api != nil {
		d.phoneSync.api.scan()
	}
}

// parsePhoneSettings reads {"buildGate": bool, "coordinator": bool}, at
// least one, nothing else.
func parsePhoneSettings(body []byte) (map[string]bool, *apiErr) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil || len(raw) == 0 {
		return nil, badReq(`body must be a JSON object with "buildGate" and/or "coordinator"`)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]bool{}
	for _, k := range keys {
		if _, ok := phoneSettingPrefs[k]; !ok {
			return nil, badReq("unknown setting " + k)
		}
		v := strings.TrimSpace(string(raw[k]))
		if v != "true" && v != "false" {
			return nil, badReq(k + " must be true or false")
		}
		out[k] = v == "true"
	}
	return out, nil
}

func (a *phoneAPI) setSettings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		folders.WriteError(w, http.StatusBadRequest, "bad_request", "could not read the body")
		return
	}
	in, fail := parsePhoneSettings(body)
	if fail != nil {
		folders.WriteError(w, fail.status, fail.code, fail.msg)
		return
	}
	phone := folders.PhoneID(r)
	var out map[string]any
	a.ui(func() {
		if !a.control(phone) {
			fail = &apiErr{http.StatusForbidden, "control_off", "Let paired devices control sessions is off"}
			return
		}
		for k, on := range in {
			a.d.app.Preferences().SetBool(phoneSettingPrefs[k], on)
		}
		a.d.settingsChanged()
		out = map[string]any{"v": 1, "settings": a.d.phoneSettings()}
	})
	if fail != nil {
		folders.WriteError(w, fail.status, fail.code, fail.msg)
		return
	}
	folders.WriteJSON(w, http.StatusOK, out)
}
