package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

func sessionFiles(root, id, transcriptPath string) ([]string, error) {
	if !sessionIDRe.MatchString(id) {
		return nil, fmt.Errorf("not a session id: %q", id)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []string
	if transcriptPath != "" {
		p, err := filepath.Abs(transcriptPath)
		if err != nil {
			return nil, err
		}
		if filepath.Base(p) != id+".jsonl" || !within(root, p) {
			return nil, errors.New("transcript is outside the Claude projects folder")
		}
		out = append(out, p)
		if dir := filepath.Join(filepath.Dir(p), id); within(root, dir) {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				out = append(out, dir)
			}
		}
	}
	return out, nil
}

func (d *desk) askDelete(id string) {
	title, _, _ := d.rowText(id)
	if d.app.Preferences().Bool("skipDeleteWarning") {
		d.deleteSession(id)
		return
	}
	msg := widget.NewLabel(fmt.Sprintf("“%s” will be permanently deleted: its whole conversation is removed from disk and cannot be recovered. If it is running, it is stopped first.", title))
	msg.Wrapping = fyne.TextWrapWord
	skip := widget.NewCheck("Don't warn me again", nil)
	body := container.NewVBox(msg, skip)
	dlg := dialog.NewCustomConfirm("Delete session permanently?", "Delete", "Cancel", body, func(ok bool) {
		if !ok {
			return
		}
		if skip.Checked {
			d.app.Preferences().SetBool("skipDeleteWarning", true)
		}
		d.deleteSession(id)
	}, d.win)
	dlg.Resize(fyne.NewSize(480, 200))
	dlg.Show()
}

func (d *desk) deleteSession(id string) {
	path := ""
	if info, ok := d.infos[id]; ok {
		path = info.Path
	}
	files, err := sessionFiles(d.cfg.ProjectsRoot, id, path)
	if err != nil {
		dialog.ShowError(err, d.win)
		return
	}
	s := d.sessions[id]
	if s != nil && s.terminal != nil && !s.running() {
		dialog.ShowError(errors.New("this session is open in a terminal; end it there first"), d.win)
		return
	}
	if p := d.paneFor(s); s != nil && p != nil && len(d.panes) > 1 {
		d.closePane(p)
	}
	d.closeTab(id)
	if d.current != nil && d.current.id == id {
		d.current = nil
		d.chat.show(nil)
	}
	var proc interface{ Stop(time.Duration) error }
	if s != nil && s.running() {
		s.stopping = true
		proc = s.proc
	}
	delete(d.sessions, id)
	delete(d.infos, id)
	delete(d.created, id)
	delete(d.org.Place, id)
	delete(d.org.Titles, id)
	delete(d.org.Kmem, id)
	delete(d.sorted.Place, id)
	d.unkeepSession(id)
	d.saveOrg()
	go func() {
		if proc != nil {
			_ = proc.Stop(5 * time.Second)
		}
		var errs []string
		for _, f := range files {
			if err := os.RemoveAll(f); err != nil {
				errs = append(errs, err.Error())
			}
		}
		d.refresh()
		if len(errs) > 0 {
			d.do(func() { dialog.ShowError(errors.New(strings.Join(errs, "\n")), d.win) })
		}
	}()
	d.updateChrome()
}
