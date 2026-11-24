package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// kmemSwitchView: the "Switch to kmem" dialog (kmemswitch.go has the steps).
type kmemSwitchView struct {
	dlg      *dialog.CustomDialog
	body     *fyne.Container
	buttons  *fyne.Container
	check    kmemSwitchCheck
	projects []memoryProject
	ticks    []*widget.Check
	topics   []*widget.Entry
	hooks    map[string]map[string]any
	newHooks []string // events the switch adds a hook to
	backup   string   // where the backup goes (preview) or went (result)
	status   *widget.Label
	result   *kmemSwitchResult
}

// showKmemSwitch opens the dialog: detection and the MEMORY.md scan run in
// the background, then the preview replaces "Checking kmem…".
func (d *desk) showKmemSwitch() {
	v := &kmemSwitchView{body: container.NewVBox(wrapLabel("Checking kmem…")), buttons: container.NewHBox()}
	d.kmemSwitchUI = v
	cancel := widget.NewButton("Cancel", func() { v.dlg.Hide() })
	v.buttons.Objects = []fyne.CanvasObject{cancel}
	v.dlg = dialog.NewCustomWithoutButtons("Switch to kmem", container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), v.buttons), nil, nil, container.NewVScroll(v.body)), d.win)
	v.dlg.Resize(fyne.NewSize(760, 560))
	v.dlg.Show()
	home := homeDir()
	go func() {
		check := d.kmemSwitchDetect(home)
		var projects []memoryProject
		if len(check.Problems) == 0 {
			projects = scanMemoryProjects(d.cfg.ProjectsRoot)
		}
		d.do(func() { d.fillKmemSwitch(v, check, projects, home) })
	}()
}

// kmemSwitchHooks: the hooks the switch writes for the detected kmem.
func kmemSwitchHooks(c kmemSwitchCheck) map[string]map[string]any {
	python := ""
	if runtime.GOOS == "windows" {
		python = c.Python
	}
	return kmemHooksForPaths(runtime.GOOS, c.Bin, c.Guard, python)
}

// fillKmemSwitch shows the problems, or the preview (UI thread).
func (d *desk) fillKmemSwitch(v *kmemSwitchView, c kmemSwitchCheck, projects []memoryProject, home string) {
	v.check, v.projects = c, projects
	closeBtn := widget.NewButton("Close", func() { v.dlg.Hide() })
	if len(c.Problems) > 0 {
		objs := []fyne.CanvasObject{boldLabel("kmem isn't ready for the switch yet")}
		for _, p := range c.Problems {
			objs = append(objs, wrapLabel("• "+p))
		}
		objs = append(objs, caption("Nothing was changed. Fix the above and open this again."))
		v.body.Objects = objs
		v.body.Refresh()
		v.buttons.Objects = []fyne.CanvasObject{closeBtn}
		v.buttons.Refresh()
		return
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	v.hooks = kmemSwitchHooks(c)
	m, err := readSettings(settingsTarget(settings))
	if errors.Is(err, os.ErrNotExist) {
		m, err = map[string]any{}, nil
	}
	if err == nil {
		v.newHooks, _, err = mergeKmemHookGroups(m, v.hooks)
	}
	if err != nil {
		v.body.Objects = []fyne.CanvasObject{boldLabel("kmem isn't ready for the switch yet"),
			wrapLabel("• " + tildePath(settings) + " can't be changed: " + err.Error() + ". Fix the file and open this again."),
			caption("Nothing was changed.")}
		v.body.Refresh()
		v.buttons.Objects = []fyne.CanvasObject{closeBtn}
		v.buttons.Refresh()
		return
	}
	v.backup = filepath.Join(home, ".local", "share", "quill", "kmem-switch-backup-"+time.Now().Format("20060102-1504"))

	objs := []fyne.CanvasObject{wrapLabel(fmt.Sprintf("kmem %s (%s) is installed. Switching makes it the memory of Quill Desktop sessions. Nothing happens before \"Switch\"; this is everything it changes:", c.Version, tildePath(c.Bin)))}

	objs = append(objs, widget.NewSeparator(), boldLabel("1. Import Claude Code's memory into kmem (one topic per project)"))
	v.ticks, v.topics = nil, nil
	if len(projects) == 0 {
		objs = append(objs, caption("No project has notes in Claude Code's auto memory (~/.claude/projects/*/memory/MEMORY.md): nothing to import."))
	}
	for _, p := range projects {
		name := tildePath(p.Cwd)
		if p.Cwd == "" {
			name = p.Slug
		}
		facts := fmt.Sprintf("%d facts", p.Facts)
		if p.Facts == 1 {
			facts = "1 fact"
		}
		tick := widget.NewCheck(fmt.Sprintf("%s — %s (%s)", name, facts, tildePath(p.File)), nil)
		tick.SetChecked(true)
		topic := widget.NewEntry()
		topic.SetText(p.Topic)
		topic.SetPlaceHolder("kmem topic")
		tick.OnChanged = func(on bool) {
			if on {
				topic.Enable()
			} else {
				topic.Disable()
			}
		}
		v.ticks, v.topics = append(v.ticks, tick), append(v.topics, topic)
		objs = append(objs, tick, container.NewBorder(nil, nil, caption("    into topic"), nil, topic))
	}
	if len(projects) > 0 {
		objs = append(objs, caption("One fact per note (MEMORY.md's linked note files and plain bullets), marked as imported from MEMORY.md; facts already in the topic are skipped. MEMORY.md and CLAUDE.md files stay as they are."))
	}

	objs = append(objs, widget.NewSeparator(), boldLabel("2. Claude Code settings (~/.claude/settings.json; your other settings and hooks stay)"))
	if len(v.newHooks) == 0 {
		objs = append(objs, wrapLabel("• kmem hooks: already there, not changed"))
	}
	for _, e := range v.newHooks {
		what := "kmem brief: sessions start with the kmem briefing"
		if e == "PreToolUse" {
			what = "kmem-guard (Bash, PowerShell, Agent, Task, Write, Edit): kmem's guards"
		}
		objs = append(objs, wrapLabel("• add hook "+e+": "+what))
	}
	if autoMemoryOn() {
		objs = append(objs, wrapLabel("• turn Claude Code's auto memory off (autoMemoryEnabled: false), so notes aren't kept in two places; Undo turns it back on"))
	} else {
		objs = append(objs, wrapLabel("• Claude Code's auto memory: already off, not changed"))
	}

	objs = append(objs, widget.NewSeparator(), boldLabel("3. Quill Desktop"),
		wrapLabel("• memory mode: Claude's auto memory → kmem. Running sessions keep going unchanged; sessions started or resumed afterwards get kmem briefings, tasks, topics and handoffs. Nothing restarts."))

	objs = append(objs, widget.NewSeparator(), boldLabel("Backup first"),
		wrapLabel(tildePath(v.backup)+"/ gets settings.json, the MEMORY.md files above and Quill Desktop's preferences. If any step fails, everything is restored from it. Settings → General → Memory (kmem) → \"Switch back to Claude memory\" undoes the switch."))
	v.status = caption("")
	v.status.Wrapping = fyne.TextWrapWord
	objs = append(objs, v.status)
	v.body.Objects = objs
	v.body.Refresh()

	cancel := widget.NewButton("Cancel", func() { v.dlg.Hide() })
	sw := widget.NewButtonWithIcon("Switch", theme.ConfirmIcon(), func() { d.startKmemSwitch(v, home) })
	sw.Importance = widget.HighImportance
	v.buttons.Objects = []fyne.CanvasObject{cancel, sw}
	v.buttons.Refresh()
}

// startKmemSwitch checks the topics and runs the switch in the background.
func (d *desk) startKmemSwitch(v *kmemSwitchView, home string) {
	plan := kmemSwitchPlan{Home: home, Settings: filepath.Join(home, ".claude", "settings.json"), PrefsFile: d.prefsFile(),
		BackupRoot: filepath.Join(home, ".local", "share", "quill"), Hooks: v.hooks, Now: time.Now()}
	for i, p := range v.projects {
		if !v.ticks[i].Checked {
			continue
		}
		t := switchTopic(v.topics[i].Text)
		if t == "" {
			v.status.SetText("Give every ticked project a kmem topic (letters, digits and dashes), or untick it.")
			return
		}
		p.Topic = t
		plan.Projects = append(plan.Projects, p)
	}
	v.body.Objects = []fyne.CanvasObject{wrapLabel("Switching to kmem…")}
	v.body.Refresh()
	v.buttons.Objects = nil
	v.buttons.Refresh()
	prefs := d.app.Preferences()
	go func() {
		res := d.runKmemSwitch(plan, prefs)
		d.do(func() { d.showKmemSwitchResult(v, res) })
	}()
}

// showKmemSwitchResult: what was done, or what failed and was restored.
func (d *desk) showKmemSwitchResult(v *kmemSwitchView, res kmemSwitchResult) {
	v.result = &res
	v.backup = res.Backup
	var objs []fyne.CanvasObject
	if res.Err == nil {
		objs = append(objs, boldLabel("Switched to kmem."))
		for _, p := range res.Projects {
			objs = append(objs, wrapLabel(fmt.Sprintf("• %s → topic %s: %d imported, %d already there", tildePath(p.File), p.Topic, p.Imported, p.Skipped)))
			if len(p.Missing) > 0 {
				objs = append(objs, wrapLabel("    Linked notes not found: "+strings.Join(p.Missing, ", ")))
			}
			if len(p.Refused) > 0 {
				objs = append(objs, wrapLabel("    Not imported (kmem refused): "+strings.Join(p.Refused, ", ")))
			}
		}
		objs = append(objs, wrapLabel("Sessions started or resumed from now on use kmem; running sessions keep going unchanged. Claude Code's auto memory is off."))
		d.refreshKmemVer()
		d.syncKmemButton()
		if d.kmemSettingsRefresh != nil {
			d.kmemSettingsRefresh()
		}
	} else {
		objs = append(objs, boldLabel("The switch stopped; nothing was switched."), wrapLabel(res.Err.Error()))
		if res.RolledBack && res.RestoreErr == nil {
			objs = append(objs, wrapLabel("settings.json and Quill Desktop's preferences are as they were before (restored from the backup)."))
		}
		if res.RestoreErr != nil {
			objs = append(objs, wrapLabel("Could not restore everything: "+res.RestoreErr.Error()+". The backup folder below has the files as they were."))
		}
		done := 0
		for _, p := range res.Projects {
			if p.Done {
				done++
			}
		}
		if done > 0 {
			objs = append(objs, wrapLabel(fmt.Sprintf("Facts kmem already imported (%d project(s)) stay in kmem, marked as imported from MEMORY.md; trying again skips them.", done)))
		}
	}
	if res.Backup != "" {
		open := widget.NewButtonWithIcon("Open folder", theme.FolderOpenIcon(), func() { d.openPath(res.Backup) })
		objs = append(objs, widget.NewSeparator(), container.NewBorder(nil, nil, nil, open, wrapLabel("Backup: "+tildePath(res.Backup))))
	}
	v.body.Objects = objs
	v.body.Refresh()
	v.buttons.Objects = []fyne.CanvasObject{widget.NewButton("Close", func() { v.dlg.Hide() })}
	v.buttons.Refresh()
}

// kmemUndoDialog: Settings → Memory "Switch back to Claude memory".
func (d *desk) kmemUndoDialog(after func()) {
	dir := d.kmemSwitchUndoDir()
	if dir == "" {
		return
	}
	msg := wrapLabel("Switch back to Claude memory:\n" +
		"• the kmem hooks the switch added are removed from ~/.claude/settings.json (your other settings stay)\n" +
		"• Claude Code's auto memory is turned back on as it was\n" +
		"• Quill Desktop's memory mode goes back to Claude's auto memory\n" +
		"Running sessions keep going; sessions started or resumed afterwards use Claude's memory. Facts imported into kmem stay there (marked), so switching again doesn't duplicate them.\n" +
		"Backup of the switch: " + tildePath(dir))
	dialog.ShowCustomConfirm("Switch back to Claude memory", "Switch back", "Cancel", msg, func(ok bool) {
		if !ok {
			return
		}
		if err := undoKmemSwitch(dir, d.app.Preferences()); err != nil {
			dialog.ShowError(err, d.win)
		} else {
			d.refreshKmemVer()
			dialog.ShowInformation("Switch back to Claude memory", "Quill Desktop sessions started or resumed from now on use Claude's auto memory.", d.win)
		}
		if after != nil {
			after()
		}
	}, d.win)
}

// kmemRemoveHooksDialog: kmem is gone but its hooks are still there.
func (d *desk) kmemRemoveHooksDialog(after func()) {
	dialog.ShowConfirm("Remove kmem hooks", "kmem is no longer installed, but its hooks (kmem brief, kmem-guard) are still in ~/.claude/settings.json. Remove them? Your other hooks stay.", func(ok bool) {
		if !ok {
			return
		}
		if err := removeLeftKmemHooks(homeDir()); err != nil {
			dialog.ShowError(err, d.win)
		}
		if after != nil {
			after()
		}
	}, d.win)
}

// kmemSwitchButtons: Settings → General → Memory (kmem)'s switch, undo and
// remove-hooks buttons; refresh shows the ones that apply.
func (d *desk) kmemSwitchButtons(after func()) (fyne.CanvasObject, func()) {
	sw := widget.NewButtonWithIcon("Switch to kmem...", theme.StorageIcon(), d.showKmemSwitch)
	sw.Importance = widget.MediumImportance
	undo := widget.NewButtonWithIcon("Switch back to Claude memory...", theme.ContentUndoIcon(), nil)
	undo.Importance = widget.LowImportance
	rm := widget.NewButtonWithIcon("Remove kmem hooks...", theme.DeleteIcon(), nil)
	rm.Importance = widget.LowImportance
	undo.OnTapped = func() { d.kmemUndoDialog(after) }
	rm.OnTapped = func() { d.kmemRemoveHooksDialog(after) }
	refresh := func() {
		show := func(b *widget.Button, on bool) {
			if b.Hidden == on {
				b.Hidden = !on
				b.Refresh()
			}
		}
		show(sw, d.kmemAvailable() && !d.kmemMemory())
		show(undo, d.kmemSwitchUndoDir() != "")
		show(rm, !d.kmemAvailable() && kmemHooksAnyPresent(homeDir()))
	}
	refresh()
	return container.NewVBox(container.NewHBox(sw), container.NewHBox(undo), container.NewHBox(rm)), refresh
}
