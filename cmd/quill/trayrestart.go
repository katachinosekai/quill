package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The tray's "Restart Quill Desktop" (t125b67, user 2026-11-23). It leaves
// the sessions alone: Quill Desktop restarts in place with its own program
// file through the live reload's handover (reload.go), so the sessions keep
// running and reconnect, and tabs, drafts, window size and scroll come back.
//
//   - It goes now: typing in the last 15 s does not hold it (the user asked).
//     What can't be handed over still holds it (sign-in, terminal takeover;
//     swapBlockers), and busy turns are handed over as the reload does.
//   - The program file: os.Executable(); if that file was deleted or is not
//     a whole program, the path Quill Desktop was started from; last, the
//     running image itself (/proc/self/exe).
//   - When the handover is not possible (the exec failed), a clean restart:
//     once the running turns end (or "Restart now"), the sessions stop and a
//     new Quill Desktop resumes them, as at a start after an update.

const restartLabel = "Restart Quill Desktop"

// startPath: the program file Quill Desktop was started from (a handover
// keeps os.Args, so this is the first start's path).
var startPath = startedFrom()

func startedFrom() string {
	p := os.Args[0]
	if p == "" {
		return ""
	}
	if !strings.ContainsRune(p, filepath.Separator) {
		if lp, err := exec.LookPath(p); err == nil {
			p = lp
		}
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	return p
}

// selfImage: the running program image, readable even after its file was
// replaced or deleted ("" where there is none).
func selfImage() string {
	if runtime.GOOS == "linux" {
		return "/proc/self/exe"
	}
	return ""
}

// restartTarget: the first of paths that is a whole program file.
func restartTarget(paths ...string) string {
	for _, p := range paths {
		if usableBinary(p) {
			return p
		}
	}
	return ""
}

func usableBinary(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	if st.Mode()&0o111 == 0 {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	_, err = f.Read(head)
	return err == nil && string(head) == "\x7fELF"
}

// restartQuill: the tray's "Restart Quill Desktop" (UI thread).
func (d *desk) restartQuill() {
	if d.swapping || d.restarting || d.restartAsked {
		return
	}
	if d.restartWait != "" { // a clean restart waits for turns: the user asks again
		d.cleanRestart()
		return
	}
	if d.swapTo == "" { // a build the user accepted already is restarted into
		d.swapTo = restartTarget(runningExe(), startPath, selfImage())
	}
	if d.swapTo == "" {
		d.restartFallback(errors.New("Quill Desktop's program file was not found"))
		return
	}
	d.restartAsked = true
	d.forceSwap, d.swapNow = true, true
	d.trySwap()
}

// restartFallback: the handover was not possible (why). The sessions are
// stopped once their turns end and a new Quill Desktop resumes them.
func (d *desk) restartFallback(why error) {
	msg := "the handover failed"
	if why != nil {
		msg = why.Error()
	}
	d.restartWait = msg
	d.waitTurnsThenRestart()
}

func (d *desk) busyTurns() int {
	n := 0
	for _, s := range d.runningSessions() {
		if s.conv.Busy {
			n++
		}
	}
	return n
}

func (d *desk) waitTurnsThenRestart() {
	if d.restartWait == "" || d.restarting {
		return
	}
	n := d.busyTurns()
	if n == 0 {
		d.cleanRestart()
		return
	}
	d.setUpdLine(fmt.Sprintf("Quill Desktop could not hand its sessions over (%s). It restarts and resumes them when %d running turn(s) end.", d.restartWait, n),
		lineBtn("Restart now", d.cleanRestart),
		lineBtn("Cancel", d.cancelRestart))
	if d.restartRetry == nil {
		d.restartRetry = time.AfterFunc(time.Second, func() {
			d.do(func() {
				d.restartRetry = nil
				d.waitTurnsThenRestart()
			})
		})
	}
}

func (d *desk) cancelRestart() {
	d.restartWait = ""
	if d.restartRetry != nil {
		d.restartRetry.Stop()
		d.restartRetry = nil
	}
	d.setUpdLine("")
}

// spawnQuill starts a new Quill Desktop (tests replace it).
var spawnQuill = func(path string, args, env []string) error {
	cmd := exec.Command(path, args[1:]...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// restartEnv: the new Quill Desktop starts its own keeper and restores path.
func restartEnv(env []string, path string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == keeperEnv || k == restoreEnv || k == "QUILL_UNLOCKS_FD" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, restoreEnv+"="+path)
}

// cleanRestart stops the sessions, starts a new Quill Desktop that resumes
// them and quits; if that can't start, the sessions run here again.
func (d *desk) cleanRestart() {
	if d.restarting {
		return
	}
	d.cancelRestart()
	d.restarting = true
	d.setUpdLine("Restarting Quill Desktop…")
	st := d.handoverState()
	running := d.runningSessions()
	for _, s := range running {
		if s.persisted {
			st.Running = append(st.Running, s.id)
		}
	}
	d.rememberTopicsAtQuit()
	target := restartTarget(startPath, runningExe(), selfImage())
	go func() {
		d.stopAll(running)
		err := errors.New("Quill Desktop's program file was not found")
		if target != "" {
			b, _ := json.Marshal(st)
			var path string
			if path, err = writePrivate("quill-restore-*.json", b); err == nil {
				if err = spawnQuill(target, os.Args, restartEnv(os.Environ(), path)); err == nil {
					d.do(d.app.Quit)
					return
				}
				os.Remove(path)
			}
		}
		d.do(func() {
			d.restarting = false
			for _, s := range running {
				if !s.running() && s.terminal == nil {
					if e := d.ensureProcess(s); e != nil {
						d.notify(s, "Could not resume: "+e.Error())
					}
				}
			}
			d.setUpdLine("Quill Desktop could not restart: " + err.Error())
		})
	}()
}
