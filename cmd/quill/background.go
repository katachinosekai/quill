package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

func instanceSocket() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "quill.sock")
}

func signalRunning(path string) bool {
	return sendCommand(path, "show")
}

func sendCommand(path, cmd string) bool {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(cmd + "\n")); err != nil {
		return false
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	return err == nil && strings.TrimSpace(line) == "ok"
}

// commandReply sends cmd and returns the lines after the "ok".
func commandReply(path, cmd string) ([]string, bool) {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(cmd + "\n")); err != nil {
		return nil, false
	}
	r := bufio.NewReader(c)
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ok" {
		return nil, false
	}
	var out []string
	for {
		l, err := r.ReadString('\n')
		if l = strings.TrimRight(l, "\r\n"); l != "" {
			out = append(out, l)
		}
		if err != nil {
			return out, true
		}
	}
}

// reloadCLI is `quill --reload`: it asks the running Quill Desktop to switch
// to the new build and says what happened. A switch replaces the program in
// the same process (same PID and start time; the keeper process above it
// keeps the old file until Quill Desktop quits), so it checks the build the
// process runs instead.
func reloadCLI(sock string, out io.Writer) int {
	lines, ok := commandReply(sock, "reload")
	if !ok {
		fmt.Fprintln(os.Stderr, "no running Quill answered")
		return 1
	}
	self := buildID(runningExe())
	if len(lines) == 0 {
		fmt.Fprintln(out, "Asked Quill Desktop to switch to the new build.")
		return 0
	}
	status, detail, _ := strings.Cut(lines[0], "\t")
	switch status {
	case reloadWaiting:
		fmt.Fprintln(out, "Quill Desktop switches to the new build when this clears: "+detail+".")
		if typingHolds(strings.Split(detail, "; ")) {
			fmt.Fprintln(out, "Its window has a \"Reload now\" button to switch anyway.")
		}
		return 0
	case reloadNoBuild:
		if id := runningID(sock); id != "" && id == self {
			fmt.Fprintln(out, "Quill Desktop already runs this build.")
			return 0
		}
		fmt.Fprintln(out, "Quill Desktop found no new build: "+detail+" hasn't changed since it started.")
		if exe := runningExe(); exe != detail {
			fmt.Fprintln(out, "This command is "+exe+"; Quill Desktop switches only to a new build of its own program file.")
		}
		return 1
	}
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if id := runningID(sock); id != "" && id == self {
			fmt.Fprintln(out, "Quill Desktop switched to this build; its sessions were handed over.")
			return 0
		}
	}
	fmt.Fprintln(out, "Quill Desktop is switching to the new build.")
	return 0
}

// runningID: the build the running Quill Desktop runs ("" if it can't say).
func runningID(sock string) string {
	if l, ok := commandReply(sock, "build"); ok && len(l) > 0 {
		return l[0]
	}
	return ""
}

func (d *desk) listenInstance(path string) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return
	}
	_ = os.Chmod(path, 0o600)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				cmdLine := strings.TrimSpace(line)
				if name, ok := strings.CutPrefix(cmdLine, "resume "); ok {
					if !validKmemName(name) {
						return
					}
					d.do(func() { d.resumeNamedFromCLI(name) })
					_, _ = c.Write([]byte("ok\n"))
					return
				}
				switch cmdLine {
				case "show":
					d.do(d.showWindow)
				case "kmem-window":
					// `quill --kmem-window` (the Monday check-in timer, docs/design/kmem-window.md §10).
					d.do(d.openKmemWindow)
				case "profile":
					go writeProfile()
				case "heap":
					go writeHeap()
				case "resume-all", "resume-all " + placeArchive:
					key := placeInactive
					if strings.HasSuffix(strings.TrimSpace(line), placeArchive) {
						key = placeArchive
					}
					d.do(func() { d.resumeAll(d.resumableIn(key)) })
				case "reload":
					// `quill --reload` is the user asking. The line after "ok"
					// says whether it switches or what it waits for.
					st := make(chan string, 1)
					d.do(func() { st <- d.reloadRequest() })
					reply := "ok\n"
					select {
					case s := <-st:
						reply += s + "\n"
					case <-time.After(1500 * time.Millisecond):
					}
					_, _ = c.Write([]byte(reply))
					return
				case "build":
					_, _ = c.Write([]byte("ok\n" + runningBuildID() + "\n"))
					return
				default:
					return
				}
				_, _ = c.Write([]byte("ok\n"))
			}()
		}
	}()
}

func (d *desk) setupBackground() bool {
	da, ok := d.app.(desktop.App)
	if !ok {
		return false
	}
	da.SetSystemTrayIcon(appIcon)
	da.SetSystemTrayMenu(fyne.NewMenu("Quill",
		fyne.NewMenuItem("Show Quill", d.showWindow),
		fyne.NewMenuItem("Quit", func() {
			d.showWindow()
			d.confirmQuit()
		}),
	))
	da.SetSystemTrayWindow(d.win)
	d.background = true
	return true
}

func (d *desk) showWindow() {
	d.hidden = false
	d.win.Show()
	d.win.RequestFocus()
}

func (d *desk) hideWindow() {
	d.hidden = true
	if d.cmds != nil {
		d.cmds.hide()
	}
	d.win.Hide()
}

func writeProfile() {
	path := filepath.Join(privateDir(), fmt.Sprintf("quill-cpu-%d.pprof", os.Getpid()))
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if pprof.StartCPUProfile(f) != nil {
		return
	}
	time.Sleep(10 * time.Second)
	pprof.StopCPUProfile()
}

func writeHeap() {
	path := filepath.Join(privateDir(), fmt.Sprintf("quill-heap-%d.pprof", os.Getpid()))
	_ = os.Remove(path)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	runtime.GC()
	_ = pprof.WriteHeapProfile(f)
}
