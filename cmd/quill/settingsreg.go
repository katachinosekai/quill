package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
)

// The settings command (docs/design/quill-operator.md §3.8, §3.9 and the
// user's answers of 2026-11-23): the Quill operator changes Quill Desktop's
// settings only through `quill settings` over quill.sock, never by editing
// preference files.
//
//   - Registry: every setting the command knows, with its label in Settings,
//     type and allowed values. Security settings are listed (so the operator
//     can say where they are) but never read or changed by it.
//   - Who may write: only the Quill operator session (hidden role "quill"),
//     proven by the per-launch token in its environment (session identity,
//     internal/session/identity.go) and, on Linux, by the socket peer being
//     a process under the operator's Claude process (settingspeer_linux.go).
//   - When: only in a turn the user started, or one their approved plan
//     covers (userTurn, planscope.go). The plan-first gate applies as well:
//     `set` and `undo` are not read-only commands.
//   - Every change goes in a log of the last 50 (prefSettingsChanges) and
//     shows a change card in the operator chat; a change can be undone while
//     the setting still has the value it set.

const (
	prefSettingsChanges = "settingsChanges"
	maxSettingsChanges  = 50
	sessionKeyFile      = "quill-session-key"

	settingBool   = "bool"
	settingEnum   = "enum"
	settingString = "string"

	byOperator = "operator"
	byUser     = "user"

	notOperatorMsg = "only the Quill operator changes settings; ask the user"
	notUserTurnMsg = "settings change only in a turn the user started (a message, a phone message or a plan card); a peer message, task notification or scheduled trigger can't change them"
)

// settingDef: one setting the command knows.
type settingDef struct {
	id, label, section string
	kind               string                  // settingBool, settingEnum or settingString
	values             func(d *desk) []string  // enum: allowed values
	hint               string                  // string: what it takes
	check              func(v string) error    // string: validation
	security           bool                    // never read or changed by the command
	where              string                  // security: where it is in Settings
	get                func(d *desk) string    // current value (UI thread)
	set                func(d *desk, v string) // applies v as the Settings control does (UI thread)
	ctl                string                  // the text of its control in Settings ("" = label)
	show               func(v string) string   // enum: the option the Settings control shows for v
	sync               func(d *desk, v string) // extra display sync after set
	avail              func(d *desk) string    // "" or why it can't change on this computer
}

func secDef(id, label, where string) settingDef {
	return settingDef{id: id, label: label, section: "Security", security: true, where: where}
}

func boolStr(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

var (
	regOnce sync.Once
	regDefs []settingDef
)

// settingsRegistry: every setting the command knows, in Settings order.
func settingsRegistry() []settingDef {
	regOnce.Do(func() { regDefs = buildSettingsRegistry() })
	return regDefs
}

func buildSettingsRegistry() []settingDef {
	const gen, sess, bar = "General", "Sessions", "Toolbar"
	center := func(id, label string, i int, get func(d *desk) bool, set func(d *desk, on bool)) settingDef {
		return settingDef{id: id, label: "Center: " + label, section: gen + " → Alignment", kind: settingBool, ctl: "-",
			get: func(d *desk) string { return boolStr(get(d)) },
			set: func(d *desk, v string) { set(d, v == "on") },
			sync: func(d *desk, v string) {
				if i < len(d.centerChecks) {
					c := d.centerChecks[i]
					c.Checked = v == "on"
					c.Refresh()
				}
				d.syncCenterAll()
			}}
	}
	pct := func(v string) string { return v + "%" }
	defs := []settingDef{
		// Security settings: Settings only (decision 7; §3.8).
		secDef("planFirst", "Plan first, then approve all", "Settings → General → Plan first, then approve all"),
		secDef("buildGate", "Ask before building", "Settings → General → Ask before building"),
		secDef("autoApprove", "Auto-approve", "the Auto-approve chip under a tab's message box"),
		secDef("newMode", "New sessions start in mode", "Settings → General → New sessions"),
		secDef("extraReadOnly", "Read-only commands", "Settings → Permissions → Read-only commands"),
		secDef("permissions", "Permission rules", "Settings → Permissions"),
		secDef("checkUpdates", "Check for updates", "Settings → General → Check for updates"),
		secDef("autoRemoteControl", "Remote Control for every session", "Settings → Sessions → Remote Control"),
		secDef("rcLocal", "Remote Control for local sessions", "Settings → Sessions → Remote Control"),
		secDef("phoneServe", "Phone pairing", "Settings → Sessions → Devices"),
		secDef("phoneControl", "Let paired devices control sessions", "Settings → Sessions → Devices"),
		secDef("phoneNotify", "Phone notifications", "Settings → Sessions → Devices"),
		secDef("phoneFilesDeny", "Files paired devices can't open", "Settings → Sessions → Devices"),
		secDef("pcLock", "Device lock, password and home network", "Settings → Sessions → Devices"),
		secDef("remoteAccess", "Remote access (VPN)", "Settings → Sessions → Other PCs"),
		secDef("remotePCs", "Other PCs", "Settings → Sessions → Other PCs"),
		secDef("memoryMode", "Memory mode", "Settings → General → Memory (kmem)"),
		secDef("kmemSetup", "kmem setup", "Settings → Setup"),
		secDef("kmemAssistModel", "kmem assistant model", "Settings → General → Memory (kmem)"),
		secDef("operatorModel", "Quill operator model", "Settings (the Quill operator's own model)"),

		{id: "coordinator", label: coordinatorTitle, section: gen, kind: settingBool,
			get: func(d *desk) string { return boolStr(prefBool(d.app.Preferences(), prefCoordinator)) },
			set: func(d *desk, v string) {
				setUserPref(d.app.Preferences(), prefCoordinator, v == "on")
				d.settingsChanged()
			}},
		{id: "animateText", label: "Animate new text", section: gen, kind: settingBool,
			ctl: "Animate new text (replies appear character by character while they stream)",
			get: func(d *desk) string { return boolStr(d.animateText()) },
			set: func(d *desk, v string) { d.app.Preferences().SetBool(prefAnimateText, v == "on") }},
		{id: "animateSpeed", label: "Text animation speed", section: gen, kind: settingEnum,
			values: func(*desk) []string {
				var out []string
				for _, n := range animSpeedNames {
					out = append(out, speedKey(n))
				}
				return out
			},
			get:  func(d *desk) string { return d.animateSpeed() },
			set:  func(d *desk, v string) { d.app.Preferences().SetString(prefAnimateSpeed, v) },
			show: speedName},
		{id: "collapseChips", label: "New tool chips start collapsed", section: gen, kind: settingBool,
			get: func(d *desk) string { return boolStr(d.collapseChips) },
			set: func(d *desk, v string) {
				if d.chat == nil {
					d.collapseChips = v == "on"
					d.app.Preferences().SetBool("collapseChips", d.collapseChips)
					return
				}
				d.setCollapseChips(v == "on")
			}},
		{id: "desktopNotify", label: "Notify me about approvals and finished sessions", section: gen, kind: settingBool,
			get: func(d *desk) string { return boolStr(d.notifyOn()) },
			set: func(d *desk, v string) { d.app.Preferences().SetBool("desktopNotify", v == "on") }},
		{id: "kwinRules", label: kwinSwitch, section: gen, kind: settingBool,
			get: func(d *desk) string { return boolStr(d.app.Preferences().Bool(prefKwinRules)) },
			set: func(d *desk, v string) {
				d.setKwinRules(v == "on")
				d.settingsChanged()
			},
			avail: func(d *desk) string {
				if !d.kwin.onKDE() {
					return kwinNotKDE
				}
				return ""
			}},
		center("centerText", "Chat text", 0, func(d *desk) bool { return d.center }, (*desk).setCenter),
		center("centerInput", "Typing", 1, func(d *desk) bool { return d.app.Preferences().Bool("centerInput") }, (*desk).setInputCenter),
		center("centerStatus", "Status", 2, func(d *desk) bool { return d.app.Preferences().Bool("centerStatus") }, (*desk).setStatusCenter),
		center("centerTabs", "Tabs", 3, func(d *desk) bool { return d.app.Preferences().Bool("centerTabs") }, (*desk).setTabCenter),
		{id: "newModel", label: "New sessions use", section: gen + " → New sessions", kind: settingEnum, ctl: "-",
			values: func(d *desk) []string { return append([]string{"default"}, d.localModelList()...) },
			get: func(d *desk) string {
				if m := d.app.Preferences().StringWithFallback("newModel", "default"); isLocal(m) {
					return m
				}
				return "default"
			},
			set:  func(d *desk, v string) { d.app.Preferences().SetString("newModel", v) },
			sync: func(d *desk, _ string) { d.syncNewSessionSetting() }},
		{id: "showMsgTimes", label: "Show message times", section: gen + " → Message times", kind: settingBool,
			get: func(d *desk) string { return boolStr(d.showTimes()) },
			set: func(d *desk, v string) {
				d.app.Preferences().SetBool(prefShowTimes, v == "on")
				d.redrawChats()
			}},
		{id: "msgTimeFormat", label: "Message time format", section: gen + " → Message times", kind: settingString, ctl: "Format",
			hint: "empty for the system format, or a pattern like HH:mm, h:mm a, ddd d MMM HH:mm",
			check: func(v string) error {
				if _, ok := patternLayout(v); v != "" && !ok {
					return errors.New("not a time format: use tokens like HH, h, mm, a, ddd, d, MMM, yyyy")
				}
				return nil
			},
			get: func(d *desk) string { return d.app.Preferences().String(prefTimeFormat) },
			set: func(d *desk, v string) {
				d.app.Preferences().SetString(prefTimeFormat, v)
				d.redrawChats()
			}},
		{id: "voiceInput", label: "Voice input", section: gen + " → Voice input", kind: settingBool,
			ctl: "Voice input (the mic button under the message box)",
			get: func(d *desk) string { return boolStr(!d.app.Preferences().Bool(prefSTTOff)) },
			set: func(d *desk, v string) {
				d.app.Preferences().SetBool(prefSTTOff, v != "on")
				if d.stt != nil {
					d.stt.syncOff()
				}
			}},
		{id: "sttLang", label: "Voice input language", section: gen + " → Voice input", kind: settingEnum, ctl: "-",
			values: func(*desk) []string {
				var out []string
				for _, l := range languages {
					out = append(out, l.code)
				}
				return out
			},
			get: func(d *desk) string { return d.app.Preferences().StringWithFallback("sttLang", autoLang) },
			set: func(d *desk, v string) { d.app.Preferences().SetString("sttLang", v) }},
		{id: "sttModelSize", label: "Voice model size", section: gen + " → Voice input", kind: settingEnum, ctl: "-",
			values: func(*desk) []string {
				var out []string
				for _, m := range modelSizes {
					out = append(out, m.id)
				}
				return out
			},
			get: func(d *desk) string {
				return sizeInfo(d.app.Preferences().StringWithFallback(prefSTTModel, "base")).id
			},
			set: func(d *desk, v string) { d.app.Preferences().SetString(prefSTTModel, sizeInfo(v).id) }},
		{id: "autocorrect", label: "Autocorrect the message box", section: gen + " → Typing", kind: settingBool,
			get: func(d *desk) string { return boolStr(d.autocorrectOn()) },
			set: func(d *desk, v string) { d.app.Preferences().SetBool("autocorrect", v == "on") }},
		{id: "autoFormat", label: "Auto formatting", section: gen + " → Typing", kind: settingBool,
			ctl: "Auto formatting (capital letters, — dashes, list continuation with Shift+Enter)",
			get: func(d *desk) string { return boolStr(d.autoFormatOn()) },
			set: func(d *desk, v string) { d.app.Preferences().SetBool(prefAutoFormat, v == "on") }},
		{id: "kmemBadge", label: "Badge on the kmem button", section: gen + " → Memory (kmem)", kind: settingBool,
			get: func(d *desk) string { return boolStr(d.kmemBadgeOn()) },
			set: func(d *desk, v string) {
				d.app.Preferences().SetBool(prefKmemBadge, v == "on")
				d.updateKmemBadge()
			},
			avail: func(d *desk) string {
				if !d.kmemAvailable() {
					return "kmem is not installed"
				}
				return ""
			}},
		{id: "usagePause", label: "Pause sessions near the usage limit", section: sess, kind: settingBool,
			get: func(d *desk) string { return boolStr(d.pauseEnabled()) },
			set: func(d *desk, v string) { d.app.Preferences().SetBool("usagePause", v == "on") }},
		{id: "usagePauseAt", label: "Pause at (share of the 5-hour window)", section: sess, kind: settingEnum,
			ctl:    "Pause sessions near the usage limit",
			values: func(*desk) []string { return []string{"80", "85", "90", "95", "98"} },
			get:    func(d *desk) string { return strconv.Itoa(d.pauseThreshold()) },
			set: func(d *desk, v string) {
				n, _ := strconv.Atoi(v)
				d.app.Preferences().SetInt("usagePauseAt", n)
			},
			show: pct},
		{id: "tabLayout", label: "Tab layout (Landscape or Portrait)", section: bar, kind: settingEnum,
			values: func(*desk) []string { return []string{layoutHorizontal, layoutVertical} },
			get:    func(d *desk) string { return d.layoutMode() },
			set: func(d *desk, v string) {
				if d.layoutBtn == nil {
					d.app.Preferences().SetString("tabLayout", v)
					return
				}
				d.setLayoutMode(v)
			}},
		{id: "autoSplit", label: "Auto split", section: bar, kind: settingBool,
			get: func(d *desk) string { return boolStr(d.app.Preferences().Bool("autoSplit")) },
			set: func(d *desk, v string) {
				if d.autoChk != nil {
					d.autoChk.SetChecked(v == "on") // its own handler splits or leaves
					return
				}
				d.app.Preferences().SetBool("autoSplit", v == "on")
			}},
	}
	return defs
}

// settingByID: the registered setting id (exact, then any case).
func settingByID(id string) (settingDef, bool) {
	for _, s := range settingsRegistry() {
		if s.id == id {
			return s, true
		}
	}
	for _, s := range settingsRegistry() {
		if strings.EqualFold(s.id, id) {
			return s, true
		}
	}
	return settingDef{}, false
}

// securityMsg: the refusal for a security setting.
func securityMsg(s settingDef) string {
	return "this is a security setting; it's in " + s.where + " (the user changes it there)"
}

// settingInfo: a setting as `quill settings list/get` show it.
type settingInfo struct {
	ID       string   `json:"id"`
	Label    string   `json:"label"`
	Section  string   `json:"section"`
	Type     string   `json:"type,omitempty"`
	Values   []string `json:"values,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Value    string   `json:"value,omitempty"`
	Security bool     `json:"security,omitempty"`
	Where    string   `json:"where,omitempty"`
	Note     string   `json:"note,omitempty"` // why it can't change on this computer
}

func (d *desk) settingInfo(s settingDef) settingInfo {
	in := settingInfo{ID: s.id, Label: s.label, Section: s.section, Security: s.security, Where: s.where}
	if s.security {
		return in // never its value: a password, a pairing
	}
	in.Type, in.Hint, in.Value = s.kind, s.hint, s.get(d)
	switch s.kind {
	case settingBool:
		in.Values = []string{"on", "off"}
	case settingEnum:
		in.Values = s.values(d)
	}
	in.Note = d.settingUnavailable(s)
	return in
}

// settingsInfo: every registered setting (UI thread).
func (d *desk) settingsInfo() []settingInfo {
	var out []settingInfo
	for _, s := range settingsRegistry() {
		out = append(out, d.settingInfo(s))
	}
	return out
}

// normalizeSetting: v as s stores it, or why it isn't allowed.
func (d *desk) normalizeSetting(s settingDef, v string) (string, error) {
	v = strings.TrimSpace(v)
	switch s.kind {
	case settingBool:
		switch strings.ToLower(v) {
		case "on", "true", "yes", "1":
			return "on", nil
		case "off", "false", "no", "0":
			return "off", nil
		}
		return "", fmt.Errorf("%s takes on or off", s.id)
	case settingEnum:
		vals := s.values(d)
		for _, a := range vals {
			if a == v {
				return a, nil
			}
		}
		for _, a := range vals {
			if strings.EqualFold(a, v) || (s.show != nil && strings.EqualFold(s.show(a), v)) {
				return a, nil
			}
		}
		return "", fmt.Errorf("%s takes one of: %s", s.id, strings.Join(vals, ", "))
	case settingString:
		if strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("%s takes one line", s.id)
		}
		if s.check != nil {
			if err := s.check(v); err != nil {
				return "", err
			}
		}
		return v, nil
	}
	return "", errors.New("unknown setting type")
}

// settingUnavailable: why s can't change on this computer ("" = it can):
// its own reason, or its control in Settings is greyed out.
func (d *desk) settingUnavailable(s settingDef) string {
	if s.avail != nil {
		if why := s.avail(d); why != "" {
			return why
		}
	}
	if s.kind == settingBool && s.ctl != "-" {
		if c := findCheck(d.settingsRoots(), s.control()); c != nil && c.Disabled() {
			return "it is greyed out in Settings on this computer"
		}
	}
	return ""
}

func (s settingDef) control() string {
	if s.ctl != "" {
		return s.ctl
	}
	return s.label
}

// applySetting sets s to v (normalized) and puts Settings in line.
func (d *desk) applySetting(s settingDef, v string) {
	s.set(d, v)
	d.syncSettingControl(s, v)
}

// ---- The Settings dialog follows changes live. Its panes are built once
// (buildSidebar), so updating their controls updates an open dialog.

func (d *desk) settingsRoots() []fyne.CanvasObject {
	var out []fyne.CanvasObject
	if d.setGeneral != nil {
		out = append(out, d.setGeneral)
	}
	if d.setSessions != nil {
		out = append(out, d.setSessions)
	}
	return out
}

// walkSettingsTree calls f on o and everything inside it until f returns true.
func walkSettingsTree(o fyne.CanvasObject, f func(fyne.CanvasObject) bool) bool {
	if o == nil {
		return false
	}
	if f(o) {
		return true
	}
	switch c := o.(type) {
	case *fyne.Container:
		for _, k := range c.Objects {
			if walkSettingsTree(k, f) {
				return true
			}
		}
	case *container.Scroll:
		return walkSettingsTree(c.Content, f)
	case *container.ThemeOverride:
		return walkSettingsTree(c.Content, f)
	}
	return false
}

func findCheck(roots []fyne.CanvasObject, text string) *widget.Check {
	var out *widget.Check
	for _, r := range roots {
		if walkSettingsTree(r, func(o fyne.CanvasObject) bool {
			if c, ok := o.(*widget.Check); ok && c.Text == text {
				out = c
				return true
			}
			return false
		}) {
			break
		}
	}
	return out
}

func objText(o fyne.CanvasObject) string {
	switch w := o.(type) {
	case *widget.Label:
		return w.Text
	case *widget.Check:
		return w.Text
	}
	return ""
}

// findNear: the first widget match finds in the container that has a label
// or check with text among its own objects.
func findNear[T fyne.CanvasObject](roots []fyne.CanvasObject, text string) T {
	var out T
	for _, r := range roots {
		if walkSettingsTree(r, func(o fyne.CanvasObject) bool {
			c, ok := o.(*fyne.Container)
			if !ok || !slices.ContainsFunc(c.Objects, func(k fyne.CanvasObject) bool { return objText(k) == text }) {
				return false
			}
			return walkSettingsTree(c, func(k fyne.CanvasObject) bool {
				if w, ok := k.(T); ok {
					out = w
					return true
				}
				return false
			})
		}) {
			break
		}
	}
	return out
}

// syncSettingControl shows v in s's Settings control, without running the
// control's handler (the change is already applied).
func (d *desk) syncSettingControl(s settingDef, v string) {
	roots := d.settingsRoots()
	if s.ctl != "-" {
		switch s.kind {
		case settingBool:
			if c := findCheck(roots, s.control()); c != nil && c.Checked != (v == "on") {
				c.Checked = v == "on"
				c.Refresh()
			}
		case settingEnum:
			if sel := findNear[*widget.Select](roots, s.control()); sel != nil {
				shown := v
				if s.show != nil {
					shown = s.show(v)
				}
				if sel.Selected != shown {
					sel.Selected = shown
					sel.Refresh()
				}
			}
		case settingString:
			if e := findNear[*widget.Entry](roots, s.control()); e != nil && e.Text != v {
				e.SetText(v) // its handler applies the same value
			}
		}
	}
	switch s.id {
	case "sttLang", "sttModelSize":
		d.syncVoiceControls()
	}
	if s.sync != nil {
		s.sync(d, v)
	}
}

// syncVoiceControls: the language button and the size select of Settings →
// Voice input show the stored values.
func (d *desk) syncVoiceControls() {
	p := d.app.Preferences()
	cur := sizeInfo(p.StringWithFallback(prefSTTModel, "base"))
	langText := languageLabel(languageFor(p.StringWithFallback("sttLang", autoLang)))
	isLang := func(t string) bool {
		for _, l := range languages {
			if t == languageLabel(l) {
				return true
			}
		}
		return false
	}
	for _, r := range d.settingsRoots() {
		walkSettingsTree(r, func(o fyne.CanvasObject) bool {
			switch w := o.(type) {
			case *widget.Button:
				if w.Text != langText && isLang(w.Text) {
					w.SetText(langText)
				}
			case *widget.Select:
				want := cur.label + " (" + cur.mb + ")"
				if slices.Contains(w.Options, want) && w.Selected != want {
					w.Selected = want
					w.Refresh()
				}
			}
			return false
		})
	}
}

// ---- Change log.

// settingChange: one entry of the change log (prefSettingsChanges).
type settingChange struct {
	N      int    `json:"n"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Time   int64  `json:"time"`
	Why    string `json:"why,omitempty"`
	By     string `json:"by"`
	Undone bool   `json:"undone,omitempty"`
}

func (d *desk) settingsChanges() []settingChange {
	var out []settingChange
	_ = json.Unmarshal([]byte(d.app.Preferences().String(prefSettingsChanges)), &out)
	return out
}

func (d *desk) saveSettingsChanges(list []settingChange) {
	if len(list) > maxSettingsChanges {
		list = list[len(list)-maxSettingsChanges:]
	}
	b, _ := json.Marshal(list)
	d.app.Preferences().SetString(prefSettingsChanges, string(b))
}

// changeSetting applies id = value for by and logs it; the caller checked
// who asks (UI thread).
func (d *desk) changeSetting(owner *deskSession, id, value, why, by string) (settingChange, error) {
	s, ok := settingByID(id)
	if !ok {
		return settingChange{}, fmt.Errorf("no setting %q (quill settings list shows them)", id)
	}
	if s.security {
		return settingChange{}, errors.New(securityMsg(s))
	}
	if why := d.settingUnavailable(s); why != "" {
		return settingChange{}, fmt.Errorf("%s can't change on this computer: %s", s.id, why)
	}
	v, err := d.normalizeSetting(s, value)
	if err != nil {
		return settingChange{}, err
	}
	old := s.get(d)
	if old == v {
		return settingChange{ID: s.id, Label: s.label, Old: old, New: v}, nil // N 0: nothing changed
	}
	d.applySetting(s, v)
	list := d.settingsChanges()
	n := 1
	if len(list) > 0 {
		n = list[len(list)-1].N + 1
	}
	c := settingChange{N: n, ID: s.id, Label: s.label, Old: old, New: s.get(d), Time: time.Now().Unix(),
		Why: strings.TrimSpace(why), By: by}
	d.saveSettingsChanges(append(list, c))
	d.settingsCard(owner, c, false)
	return c, nil
}

// undoSettingChange restores change n's old value while the setting still
// has the value n set (n 0: the newest change not undone). UI thread.
func (d *desk) undoSettingChange(owner *deskSession, n int) (settingChange, error) {
	list := d.settingsChanges()
	i := -1
	for j := len(list) - 1; j >= 0; j-- {
		if (n == 0 && !list[j].Undone) || (n != 0 && list[j].N == n) {
			i = j
			break
		}
	}
	if i < 0 {
		if n == 0 {
			return settingChange{}, errors.New("no change to undo")
		}
		return settingChange{}, fmt.Errorf("no change %d in the log (it keeps the last %d)", n, maxSettingsChanges)
	}
	c := list[i]
	if c.Undone {
		return c, fmt.Errorf("change %d was already undone", c.N)
	}
	s, ok := settingByID(c.ID)
	if !ok || s.security {
		return c, fmt.Errorf("%s can't be changed by this build", c.ID)
	}
	if cur := s.get(d); cur != c.New {
		return c, fmt.Errorf("%s changed again since (now %s); nothing undone", c.Label, cur)
	}
	if why := d.settingUnavailable(s); why != "" {
		return c, fmt.Errorf("%s can't change on this computer: %s", s.id, why)
	}
	old, err := d.normalizeSetting(s, c.Old)
	if err != nil {
		return c, fmt.Errorf("the old value is no longer allowed: %v", err)
	}
	d.applySetting(s, old)
	list[i].Undone = true
	d.saveSettingsChanges(list)
	d.settingsCard(owner, list[i], true)
	return list[i], nil
}

// ---- Change cards. Phase C/E (the operator's chat in the assistant box)
// replace the default card with one that has an Undo button: that button
// calls d.undoSettingChange(nil, c.N) (the user clicked; no token needed).

// settingsHooks: per-desk replaceable parts.
type settingsHooks struct {
	card   func(owner *deskSession, c settingChange, undo bool) // nil: settingsNotice
	peerOK func(chain []int, s *deskSession) bool               // nil: peerUnder
}

var settingsHookMap sync.Map // *desk -> *settingsHooks

func (d *desk) settingsHooks() *settingsHooks {
	h, _ := settingsHookMap.LoadOrStore(d, &settingsHooks{})
	return h.(*settingsHooks)
}

// SetSettingsCard installs the change card renderer (phase C/E): it is
// called on the UI thread after every change and undo.
func (d *desk) setSettingsCard(f func(owner *deskSession, c settingChange, undo bool)) {
	d.settingsHooks().card = f
}

// settingsCardText: the card's text ("Changed Text animation speed: normal → fast").
func settingsCardText(c settingChange, undo bool) string {
	if undo {
		return fmt.Sprintf("Undid change %d: %s is %s again.", c.N, c.Label, c.Old)
	}
	t := fmt.Sprintf("Changed %s: %s → %s.", c.Label, shownValue(c.Old), shownValue(c.New))
	if c.Why != "" {
		t += " (" + c.Why + ")"
	}
	return t + fmt.Sprintf(" Change %d; Undo: ask the Quill operator, or quill settings undo %d.", c.N, c.N)
}

func shownValue(v string) string {
	if v == "" {
		return "(empty)"
	}
	return v
}

func (d *desk) settingsCard(owner *deskSession, c settingChange, undo bool) {
	if f := d.settingsHooks().card; f != nil {
		f(owner, c, undo)
		return
	}
	if owner == nil {
		owner = d.operatorSession()
	}
	if owner == nil {
		return // the log keeps it
	}
	d.chatFor(owner, []*chat.Item{owner.conv.AddNotice(settingsCardText(c, undo))})
}

// operatorSession: the running Quill operator session, or nil.
func (d *desk) operatorSession() *deskSession {
	for _, s := range d.sessions {
		if hiddenRole(s) == roleQuill {
			return s
		}
	}
	return nil
}

// ---- Who may write.

var sessionKey struct {
	sync.Mutex
	k []byte
}

// initSessionKey makes this launch's key for session tokens. On Linux a
// handover (reload, or a restart after a crash, with sessions still running)
// keeps the key the sessions' tokens were made with, from a 0600 file in the
// private runtime folder; reading that file doesn't help another session,
// as the socket peer must also run under the operator (settingspeer_linux.go).
// Without peer credentials the key is never written down: the token is all
// there is, so a reload ends the operator's right to write until it restarts.
func initSessionKey(handover bool) {
	path := filepath.Join(privateDir(), sessionKeyFile)
	var k []byte
	if handover && peerCredsSupported {
		if b, err := os.ReadFile(path); err == nil && len(b) == 64 {
			k, _ = hex.DecodeString(string(b))
		}
	}
	if len(k) != 32 {
		k = make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return
		}
		_ = os.Remove(path)
		if peerCredsSupported {
			tmp := path + ".tmp"
			_ = os.Remove(tmp)
			if f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
				_, werr := f.WriteString(hex.EncodeToString(k))
				if f.Close() == nil && werr == nil {
					_ = os.Rename(tmp, path)
				}
			}
		}
	}
	setSessionKey(k)
}

func setSessionKey(k []byte) {
	sessionKey.Lock()
	sessionKey.k = k
	sessionKey.Unlock()
	session.TokenFor = sessionToken
}

// sessionToken: the token of session id under this launch's key ("" none).
func sessionToken(id string) string {
	sessionKey.Lock()
	k := sessionKey.k
	sessionKey.Unlock()
	if len(k) == 0 || id == "" {
		return ""
	}
	m := hmac.New(sha256.New, k)
	m.Write([]byte("quill-session\x00" + id))
	return hex.EncodeToString(m.Sum(nil))
}

func validSessionToken(id, tok string) bool {
	want := sessionToken(id)
	return want != "" && hmac.Equal([]byte(want), []byte(tok))
}

// settingsReq: one `quill settings` request over the socket.
type settingsReq struct {
	Op      string `json:"op"` // list, get, set, undo
	ID      string `json:"id,omitempty"`
	Value   string `json:"value,omitempty"`
	Why     string `json:"why,omitempty"`
	N       int    `json:"n,omitempty"` // undo: the change; 0 = the newest
	Session string `json:"session,omitempty"`
	Token   string `json:"token,omitempty"`
}

type settingsReply struct {
	Error    string         `json:"error,omitempty"`
	Settings []settingInfo  `json:"settings,omitempty"`
	Change   *settingChange `json:"change,omitempty"`
	Same     bool           `json:"same,omitempty"` // set: it already had the value
}

// settingsWriter: the operator session allowed to write for r, or why not
// (UI thread). chain: the socket peer and its ancestors (nil: unknown).
func (d *desk) settingsWriter(r settingsReq, chain []int) (*deskSession, string) {
	if r.Session == "" || r.Token == "" {
		return nil, notOperatorMsg + " (no session token: run it from the Quill operator session)"
	}
	if !validSessionToken(r.Session, r.Token) {
		return nil, notOperatorMsg + " (the session token isn't valid for this Quill Desktop)"
	}
	s := d.sessions[r.Session]
	if s == nil || hiddenRole(s) != roleQuill {
		return nil, notOperatorMsg
	}
	if s.proc == nil {
		return nil, "the Quill operator isn't running"
	}
	ok := peerUnder
	if f := d.settingsHooks().peerOK; f != nil {
		ok = f
	}
	if !ok(chain, s) {
		return nil, notOperatorMsg + " (the command doesn't run under the Quill operator's process)"
	}
	if !userTurn(s) {
		return nil, notUserTurnMsg
	}
	return s, ""
}

// peerUnder: the socket peer runs under s's Claude process. Without peer
// credentials (Windows) the token alone decides.
func peerUnder(chain []int, s *deskSession) bool {
	if !peerCredsSupported {
		return true
	}
	return s.proc != nil && slices.Contains(chain, s.proc.PID())
}

// settingsRequest answers r (UI thread).
func (d *desk) settingsRequest(r settingsReq, chain []int) settingsReply {
	switch r.Op {
	case "list":
		return settingsReply{Settings: d.settingsInfo()}
	case "get":
		s, ok := settingByID(r.ID)
		if !ok {
			return settingsReply{Error: fmt.Sprintf("no setting %q (quill settings list shows them)", r.ID)}
		}
		return settingsReply{Settings: []settingInfo{d.settingInfo(s)}}
	case "set", "undo":
		if s, ok := settingByID(r.ID); r.Op == "set" && ok && s.security {
			return settingsReply{Error: securityMsg(s)}
		}
		owner, why := d.settingsWriter(r, chain)
		if owner == nil {
			return settingsReply{Error: why}
		}
		var c settingChange
		var err error
		if r.Op == "set" {
			if strings.TrimSpace(r.Why) == "" {
				return settingsReply{Error: `add --why "<the user's request>"`}
			}
			c, err = d.changeSetting(owner, r.ID, r.Value, r.Why, byOperator)
		} else {
			c, err = d.undoSettingChange(owner, r.N)
		}
		if err != nil {
			return settingsReply{Error: err.Error()}
		}
		return settingsReply{Change: &c, Same: r.Op == "set" && c.N == 0}
	}
	return settingsReply{Error: "unknown settings request " + strconv.Quote(r.Op)}
}
