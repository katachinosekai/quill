package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/updater"
)

const updateEvery = 6 * time.Hour

func (d *desk) updateButton() fyne.CanvasObject {
	d.updBtn = widget.NewButtonWithIcon("", theme.DownloadIcon(), d.showUpdate)
	d.updBtn.Importance = widget.HighImportance
	d.updBtn.Hide()
	return d.updBtn
}

func (d *desk) updateKind() (updater.Kind, string) {
	exe, err := os.Executable()
	if err != nil {
		return updater.KindNone, ""
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return updater.InstallKind(exe)
}

func (d *desk) watchUpdates(ctx context.Context) {
	if _, ok := updater.Parse(version); !ok {
		return
	}
	if k, _ := d.updateKind(); k == updater.KindNone {
		return
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if d.app.Preferences().BoolWithFallback("checkUpdates", true) {
			cctx, cancel := context.WithTimeout(ctx, time.Minute)
			r, err := updater.Latest(cctx, updater.Client())
			cancel()
			if err == nil && updater.Newer(r.Tag, version) {
				d.do(func() {
					d.updRelease = r
					d.updBtn.SetText("Update to " + strings.TrimPrefix(r.Tag, "v"))
					d.updBtn.Show()
				})
			}
		}
		timer.Reset(updateEvery)
	}
}

func (d *desk) showUpdate() {
	r := d.updRelease
	if r == nil {
		return
	}
	kind, appImage := d.updateKind()
	notes := widget.NewLabel(oneLineKeep(r.Body, 2000))
	notes.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(notes)
	scroll.SetMinSize(fyne.NewSize(520, 260))
	how := "The AppImage file is replaced and Quill restarts into it; your sessions are handed over."
	if kind == updater.KindRPM {
		how = "The new package is installed with dnf (your system asks for your password), then Quill switches to it; your sessions are handed over."
	}
	info := widget.NewLabel("Quill " + strings.TrimPrefix(r.Tag, "v") + " is available (you have " + version + "). " + how +
		" The download must match Quill's release signature, or nothing is installed.")
	info.Wrapping = fyne.TextWrapWord
	bar := widget.NewProgressBar()
	bar.Hide()
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var dlg dialog.Dialog
	update := widget.NewButtonWithIcon("Update", theme.DownloadIcon(), nil)
	update.Importance = widget.HighImportance
	page := widget.NewButton("Release page", func() {
		if u, err := url.Parse(r.Page); err == nil && u.Scheme == "https" && u.Host == "github.com" {
			d.app.OpenURL(u)
		}
	})
	later := widget.NewButton("Later", func() { dlg.Hide() })
	update.OnTapped = func() {
		update.Disable()
		bar.Show()
		status.SetText("Checking the release signature...")
		go func() {
			err := d.applyUpdate(r, kind, appImage, func(f float64) { d.do(func() { bar.SetValue(f) }) }, func(s string) { d.do(func() { status.SetText(s) }) })
			d.do(func() {
				if err != nil {
					update.Enable()
					status.SetText("Not updated: " + err.Error())
					return
				}
				dlg.Hide()
				d.updBtn.Hide()
			})
		}()
	}
	body := container.NewBorder(info, container.NewVBox(bar, status, container.NewHBox(update, page, later)), nil, nil, scroll)
	dlg = dialog.NewCustomWithoutButtons("Update Quill", body, d.win)
	dlg.Resize(fyne.NewSize(600, 480))
	dlg.Show()
}

func oneLineKeep(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func (d *desk) applyUpdate(r *updater.Release, kind updater.Kind, appImage string, progress func(float64), say func(string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	c := updater.Client()
	sums, err := r.SignedSums(ctx, c)
	if err != nil {
		return err
	}
	pkg, err := r.Package(kind)
	if err != nil {
		return err
	}
	want, ok := sums[pkg.Name]
	if !ok {
		return errors.New(pkg.Name + " is not in the signed checksum list")
	}
	dir := privateDir()
	if kind == updater.KindAppImage {
		dir = filepath.Dir(appImage)
	}
	say("Downloading " + pkg.Name + "...")
	file, err := updater.Download(ctx, c, pkg, want, dir, progress)
	if err != nil {
		return err
	}
	switch kind {
	case updater.KindAppImage:
		say("Installing...")
		if err := updater.ReplaceAppImage(appImage, file); err != nil {
			os.Remove(file)
			return err
		}
		d.do(func() {
			d.swapTo = appImage
			d.forceSwap = true
			d.trySwap()
		})
		return nil
	case updater.KindRPM:
		defer os.Remove(file)
		rpm := file + ".rpm"
		if err := os.Rename(file, rpm); err != nil {
			return err
		}
		defer os.Remove(rpm)
		say("Installing (enter your password when asked)...")
		out, err := exec.CommandContext(ctx, "pkexec", "dnf", "install", "-y", "--", rpm).CombinedOutput()
		if err != nil {
			return fmt.Errorf("dnf: %s", strings.TrimSpace(tailLines(string(out), 4)))
		}
		say("Installed. Quill switches to the new version in a moment.")
		return nil
	}
	return errors.New("this install can't update itself")
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
