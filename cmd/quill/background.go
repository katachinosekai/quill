package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
				switch strings.TrimSpace(line) {
				case "show":
					d.do(d.showWindow)
				case "profile":
					go writeProfile()
				case "reload":
					d.do(func() {
						d.forceSwap = true
						d.trySwap()
					})
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
	path := filepath.Join(os.TempDir(), fmt.Sprintf("quill-cpu-%d.pprof", os.Getpid()))
	f, err := os.Create(path)
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
