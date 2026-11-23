package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/session"
)

// The settings command (docs/design/quill-operator.md §3.8).

func testSessionKey(t *testing.T) {
	t.Helper()
	setSessionKey(bytes.Repeat([]byte{7}, 32))
	t.Cleanup(func() {
		sessionKey.Lock()
		sessionKey.k = nil
		sessionKey.Unlock()
		session.TokenFor = nil
	})
}

func TestSettingsRegistryExcludesSecurity(t *testing.T) {
	mustSecurity := []string{"planFirst", "buildGate", "autoApprove", "newMode", "extraReadOnly", "permissions",
		"checkUpdates", "autoRemoteControl", "rcLocal", "phoneServe", "phoneControl", "phoneNotify", "phoneFilesDeny",
		"pcLock", "remoteAccess", "remotePCs", "memoryMode", "kmemSetup", "kmemAssistModel", "operatorModel"}
	// Preference keys of security settings no writable entry may touch.
	securityPrefs := []string{"planFirst", prefBuildGate, prefNewMode, "extraReadOnly", "checkUpdates", "autoRemoteControl",
		"rcLocal", "phoneServe", "phoneControl", "phoneNotify", "phones", prefPhoneFilesDeny, prefPCPassword, prefPCIdle,
		prefLanUnlock, prefRemoteAccess, "remotePCs", memoryModeKey, prefKmemAssistModel}
	seen := map[string]bool{}
	for _, s := range settingsRegistry() {
		if seen[s.id] {
			t.Fatalf("duplicate id %s", s.id)
		}
		seen[s.id] = true
		if s.security {
			if s.where == "" || s.set != nil || s.get != nil {
				t.Fatalf("%s: a security entry has a place and no get/set", s.id)
			}
			continue
		}
		if s.get == nil || s.set == nil || s.label == "" || s.section == "" {
			t.Fatalf("%s: incomplete", s.id)
		}
		if slices.Contains(securityPrefs, s.id) {
			t.Fatalf("%s is a security setting but writable", s.id)
		}
	}
	for _, id := range mustSecurity {
		s, ok := settingByID(id)
		if !ok || !s.security {
			t.Fatalf("%s must be a registered security setting", id)
		}
	}

	h := newHarness(t)
	h.ui(func() {
		before := h.d.app.Preferences().BoolWithFallback("planFirst", true)
		_, err := h.d.changeSetting(nil, "planFirst", boolStr(!before), "test", byOperator)
		if err == nil || !strings.Contains(err.Error(), "security setting") || !strings.Contains(err.Error(), "Settings → General") {
			t.Fatalf("planFirst: %v", err)
		}
		if h.d.app.Preferences().BoolWithFallback("planFirst", true) != before {
			t.Fatal("planFirst changed")
		}
		for _, in := range h.d.settingsInfo() {
			if in.Security && in.Value != "" {
				t.Fatalf("%s: a security setting's value is never shown", in.ID)
			}
		}
		h.d.app.Preferences().SetString(prefPCPassword, "secret-placeholder")
		rep := h.d.settingsRequest(settingsReq{Op: "list"}, nil)
		b, _ := json.Marshal(rep)
		if strings.Contains(string(b), "secret-placeholder") {
			t.Fatal("list leaks the device password")
		}
		if _, err := h.d.changeSetting(nil, "noSuchSetting", "on", "x", byOperator); err == nil {
			t.Fatal("unknown ids don't exist")
		}
	})
}

// operatorSession starts a Quill operator session (fake Claude) for h.
func startOperator(t *testing.T, h *harness) *deskSession {
	t.Helper()
	var s *deskSession
	h.ui(func() {
		s = h.d.addSession(h.proj, "default", "default", false)
		h.d.markHiddenAs(s, roleQuill)
		if err := h.d.ensureProcess(s); err != nil {
			t.Fatal(err)
		}
	})
	return s
}

func TestSettingsWriterChecks(t *testing.T) {
	testSessionKey(t)
	h := newHarness(t)
	op := startOperator(t, h)
	var visible *deskSession
	h.ui(func() {
		visible = h.d.addSession(h.proj, "default", "default", false)
		if err := h.d.ensureProcess(visible); err != nil {
			t.Fatal(err)
		}
	})
	req := func(s *deskSession, tok string) settingsReq {
		return settingsReq{Op: "set", ID: "animateSpeed", Value: "fast", Why: "the user asked", Session: s.id, Token: tok}
	}
	h.ui(func() {
		h.d.settingsHooks().peerOK = func([]int, *deskSession) bool { return true }
		cases := []struct {
			name string
			r    settingsReq
			want string
		}{
			{"no token", settingsReq{Op: "set", ID: "animateSpeed", Value: "fast", Why: "x"}, notOperatorMsg},
			{"forged token", req(op, strings.Repeat("0", 64)), notOperatorMsg},
			{"another session's token", req(op, sessionToken(visible.id)), notOperatorMsg},
			{"a visible session", req(visible, sessionToken(visible.id)), notOperatorMsg},
			{"not in a turn", req(op, sessionToken(op.id)), notUserTurnMsg},
		}
		for _, c := range cases {
			if rep := h.d.settingsRequest(c.r, nil); !strings.Contains(rep.Error, c.want) {
				t.Fatalf("%s: %q", c.name, rep.Error)
			}
		}
		// A peer message or a task notification started the turn: refused.
		op.inTurn, op.autoTurn = true, true
		if rep := h.d.settingsRequest(req(op, sessionToken(op.id)), nil); rep.Error != notUserTurnMsg {
			t.Fatalf("notification turn: %q", rep.Error)
		}
		op.autoTurn, op.autoPeer = false, true
		if rep := h.d.settingsRequest(req(op, sessionToken(op.id)), nil); rep.Error != notUserTurnMsg {
			t.Fatalf("peer message in the turn: %q", rep.Error)
		}
		// Auto-approve doesn't make a peer turn the user's.
		op.autoApprove = true
		if rep := h.d.settingsRequest(req(op, sessionToken(op.id)), nil); rep.Error != notUserTurnMsg {
			t.Fatalf("auto-approve, peer turn: %q", rep.Error)
		}
		op.autoApprove = false
		// The user's own turn: allowed. Security settings are still refused.
		op.autoPeer = false
		r := req(op, sessionToken(op.id))
		r.ID = "planFirst"
		r.Value = "off"
		if rep := h.d.settingsRequest(r, nil); !strings.Contains(rep.Error, "security setting") {
			t.Fatalf("security: %q", rep.Error)
		}
		if rep := h.d.settingsRequest(req(op, sessionToken(op.id)), nil); rep.Error != "" || rep.Change == nil || rep.Change.New != "fast" {
			t.Fatalf("user turn: %+v", rep)
		}
		// --why is required.
		r = req(op, sessionToken(op.id))
		r.Value, r.Why = "slow", " "
		if rep := h.d.settingsRequest(r, nil); !strings.Contains(rep.Error, "--why") {
			t.Fatalf("why: %q", rep.Error)
		}
		// A notification turn the approved plan covers: its plan, allowed.
		op.inTurn, op.autoTurn = true, true
		startPlan(op)
		r.Why = "plan step"
		if rep := h.d.settingsRequest(r, nil); rep.Error != "" {
			t.Fatalf("plan-covered turn: %q", rep.Error)
		}
		endPlan(op)
		op.autoTurn = false

		// The real peer check: the caller must run under the operator's process.
		h.d.settingsHooks().peerOK = nil
		if peerCredsSupported {
			r.Value = "fast"
			if rep := h.d.settingsRequest(r, []int{os.Getpid(), 1}); !strings.Contains(rep.Error, "doesn't run under") {
				t.Fatalf("peer outside the operator: %q", rep.Error)
			}
			if rep := h.d.settingsRequest(r, []int{12345, op.proc.PID()}); rep.Error != "" {
				t.Fatalf("peer under the operator: %q", rep.Error)
			}
		}
		op.inTurn = false
	})
}

func TestSessionTokenInEnvironment(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	testSessionKey(t)
	h := newHarness(t)
	op := startOperator(t, h)
	var pid int
	h.ui(func() { pid = op.proc.PID() })
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		t.Skip(err)
	}
	env := strings.Split(string(b), "\x00")
	if !slices.Contains(env, session.SessionIDEnv+"="+op.id) || !slices.Contains(env, session.TokenEnv+"="+sessionToken(op.id)) {
		t.Fatal("the session's process has its id and token")
	}
}

func TestSettingsSetUndoRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		p := h.d.app.Preferences()
		sel := findNear[*widget.Select](h.d.settingsRoots(), "Text animation speed")
		if sel == nil {
			t.Fatal("no speed select in Settings")
		}
		c, err := h.d.changeSetting(nil, "animateSpeed", "Fast", "make it faster", byOperator)
		if err != nil || c.N != 1 || c.Old != "normal" || c.New != "fast" {
			t.Fatalf("set: %+v %v", c, err)
		}
		if p.String(prefAnimateSpeed) != "fast" || sel.Selected != "Fast" {
			t.Fatalf("applied %q, Settings shows %q", p.String(prefAnimateSpeed), sel.Selected)
		}
		// The same value again: nothing logged.
		if c, err := h.d.changeSetting(nil, "animateSpeed", "fast", "again", byOperator); err != nil || c.N != 0 {
			t.Fatalf("same: %+v %v", c, err)
		}
		// A bool with its check in Settings.
		chk := findCheck(h.d.settingsRoots(), "Show message times")
		if chk == nil || !chk.Checked {
			t.Fatal("message times check")
		}
		if _, err := h.d.changeSetting(nil, "showMsgTimes", "off", "hide times", byOperator); err != nil || chk.Checked || h.d.showTimes() {
			t.Fatalf("bool: %v", err)
		}
		if _, err := h.d.changeSetting(nil, "animateSpeed", "warp", "x", byOperator); err == nil {
			t.Fatal("values outside the list are refused")
		}
		if _, err := h.d.changeSetting(nil, "msgTimeFormat", "!!!", "x", byOperator); err == nil {
			t.Fatal("a bad time format is refused")
		}
		log := h.d.settingsChanges()
		if len(log) != 2 || log[0].Why != "make it faster" || log[0].By != byOperator {
			t.Fatalf("log %+v", log)
		}
		u, err := h.d.undoSettingChange(nil, 1)
		if err != nil || !u.Undone || p.String(prefAnimateSpeed) != "normal" || sel.Selected != "Normal" {
			t.Fatalf("undo: %+v %v (%q)", u, err, p.String(prefAnimateSpeed))
		}
		if _, err := h.d.undoSettingChange(nil, 1); err == nil || !strings.Contains(err.Error(), "already undone") {
			t.Fatalf("second undo: %v", err)
		}
		// undo with no number: the newest change not undone (change 2).
		if u, err := h.d.undoSettingChange(nil, 0); err != nil || u.N != 2 || !h.d.showTimes() || !chk.Checked {
			t.Fatalf("undo newest: %+v %v", u, err)
		}
		if _, err := h.d.undoSettingChange(nil, 0); err == nil {
			t.Fatal("nothing left to undo")
		}
	})
}

func TestSettingsUndoRefusedAfterChange(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		c, err := h.d.changeSetting(nil, "animateSpeed", "fast", "faster", byOperator)
		if err != nil {
			t.Fatal(err)
		}
		h.d.app.Preferences().SetString(prefAnimateSpeed, "slow") // the user, in Settings
		_, err = h.d.undoSettingChange(nil, c.N)
		if err == nil || !strings.Contains(err.Error(), "changed again since") {
			t.Fatalf("undo: %v", err)
		}
		if h.d.app.Preferences().String(prefAnimateSpeed) != "slow" {
			t.Fatal("nothing undone")
		}
	})
}

func TestSettingsLogCap(t *testing.T) {
	h := newHarness(t)
	h.ui(func() {
		for i := 0; i < 60; i++ {
			if _, err := h.d.changeSetting(nil, "showMsgTimes", boolStr(i%2 == 1), "toggle", byOperator); err != nil {
				t.Fatal(err)
			}
		}
		log := h.d.settingsChanges()
		if len(log) != maxSettingsChanges || log[0].N != 11 || log[len(log)-1].N != 60 {
			t.Fatalf("log %d entries, %d..%d", len(log), log[0].N, log[len(log)-1].N)
		}
		if _, err := h.d.undoSettingChange(nil, 3); err == nil || !strings.Contains(err.Error(), "no change 3") {
			t.Fatalf("a dropped change: %v", err)
		}
	})
}

func TestSettingsCommandsReadOnly(t *testing.T) {
	h := newHarness(t)
	extra := h.d.readOnlyExtra()
	for cmd, want := range map[string]bool{
		"quill settings list":                                true,
		"quill settings list --json":                         true,
		"quill settings get animateSpeed":                    true,
		"quill settings get animateSpeed --json":             true,
		"quill settings set animateSpeed fast --why x":       false,
		"quill settings undo":                                false,
		"quill settings undo 3":                              false,
		"quill settings list; quill settings undo":           false,
		"quill settings get x && quill settings set x on":    false,
		"quill --reload":                                     false,
		"quill settings list > /home/user/settings-copy.txt": false,
	} {
		if got := readOnlyCommand(cmd, extra); got != want {
			t.Fatalf("%q read-only = %v, want %v", cmd, got, want)
		}
	}
}

func TestUserTurn(t *testing.T) {
	s := &deskSession{}
	if userTurn(s) || userTurn(nil) {
		t.Fatal("idle")
	}
	s.inTurn = true
	if !userTurn(s) {
		t.Fatal("the user's turn")
	}
	s.autoPeer = true
	if userTurn(s) {
		t.Fatal("a peer message arrived")
	}
	s.autoPeer, s.autoTurn = false, true
	if userTurn(s) {
		t.Fatal("a notification's turn")
	}
	s.runAll, s.turnCovered = true, true
	if !userTurn(s) {
		t.Fatal("the approved plan covers it")
	}
}

func TestPidChain(t *testing.T) {
	if !peerCredsSupported {
		t.Skip("no peer credentials here")
	}
	c := exec.Command("sleep", "30")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Process.Kill(); _ = c.Wait() }()
	chain := pidChain(c.Process.Pid)
	if len(chain) < 2 || chain[0] != c.Process.Pid || !slices.Contains(chain, os.Getpid()) {
		t.Fatalf("chain %v (me %d)", chain, os.Getpid())
	}
}

// The CLI end to end over a test socket.
func TestSettingsCLIEndToEnd(t *testing.T) {
	testSessionKey(t)
	h := newHarness(t)
	op := startOperator(t, h)
	dir, err := os.MkdirTemp("", "qs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "q.sock")
	h.d.listenInstance(sock)
	var cards []string
	h.ui(func() {
		// The CLI runs in this test process: the real peer chain must hold it.
		h.d.settingsHooks().peerOK = func(chain []int, _ *deskSession) bool {
			return !peerCredsSupported || slices.Contains(chain, os.Getpid())
		}
		h.d.setSettingsCard(func(_ *deskSession, c settingChange, undo bool) {
			cards = append(cards, settingsCardText(c, undo))
		})
	})
	run := func(env map[string]string, args ...string) (int, string, string) {
		var out, errw bytes.Buffer
		code := settingsCLI(sock, args, func(k string) string { return env[k] }, &out, &errw)
		return code, out.String(), errw.String()
	}
	opEnv := map[string]string{session.SessionIDEnv: op.id, session.TokenEnv: sessionToken(op.id)}

	code, out, _ := run(nil, "list")
	if code != 0 || !strings.Contains(out, "animateSpeed") || !strings.Contains(out, "planFirst") || !strings.Contains(out, "security") {
		t.Fatalf("list %d:\n%s", code, out)
	}
	code, out, _ = run(nil, "get", "animateSpeed", "--json")
	var in settingInfo
	if code != 0 || json.Unmarshal([]byte(out), &in) != nil || in.Value != "normal" || len(in.Values) != 4 {
		t.Fatalf("get %d: %s", code, out)
	}
	if code, _, _ = run(nil, "set", "animateSpeed"); code != 2 {
		t.Fatal("usage")
	}
	// No token: refused.
	code, _, errText := run(nil, "set", "animateSpeed", "fast", "--why", "the user asked")
	if code != 1 || !strings.Contains(errText, notOperatorMsg) {
		t.Fatalf("no token %d: %s", code, errText)
	}
	// The operator outside a user turn: refused.
	code, _, errText = run(opEnv, "set", "animateSpeed", "fast", "--why", "the user asked")
	if code != 1 || !strings.Contains(errText, "turn the user started") {
		t.Fatalf("no turn %d: %s", code, errText)
	}
	h.ui(func() { op.inTurn = true })
	code, out, errText = run(opEnv, "set", "animateSpeed", "fast", "--why", "the user asked")
	if code != 0 || !strings.Contains(out, "change 1") {
		t.Fatalf("set %d: %s %s", code, out, errText)
	}
	h.ui(func() {
		if h.d.animateSpeed() != "fast" || len(cards) != 1 || !strings.Contains(cards[0], "Text animation speed: normal → fast") {
			t.Fatalf("applied %q, cards %v", h.d.animateSpeed(), cards)
		}
	})
	code, _, errText = run(opEnv, "set", "planFirst", "off", "--why", "x")
	if code != 1 || !strings.Contains(errText, "security setting") {
		t.Fatalf("security %d: %s", code, errText)
	}
	code, out, _ = run(opEnv, "undo", "1")
	if code != 0 || !strings.Contains(out, "Undid change 1") {
		t.Fatalf("undo %d: %s", code, out)
	}
	h.ui(func() {
		if h.d.animateSpeed() != "normal" || len(cards) != 2 {
			t.Fatalf("after undo %q %v", h.d.animateSpeed(), cards)
		}
		op.inTurn = false
		// Without a card hook the card is a notice in the operator chat.
		h.d.setSettingsCard(nil)
		if _, err := h.d.changeSetting(op, "animateText", "off", "calmer", byOperator); err != nil {
			t.Fatal(err)
		}
		last := op.conv.Items[len(op.conv.Items)-1]
		if last.Kind != chat.Notice || !strings.Contains(last.Text, "Changed Animate new text: on → off") {
			t.Fatalf("card %+v", last)
		}
	})
}
