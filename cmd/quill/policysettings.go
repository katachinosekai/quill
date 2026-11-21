package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Settings → Sessions → Auto-handoff: edits the kmem handoff policy
// (`kmem handoff policy set`, kmem 1.4.0) that checkAutoHandoff reads.

var policyModes = []struct{ label, mode, note string }{
	{"Automatic", "auto", "asks at the first value, rotates on its own at the second"},
	{"Ask only", "ask", "only asks; never rotates on its own"},
	{"Off", "off", "never asks and never rotates"},
}

func policyPcts(extra ...int) []string {
	var out []string
	seen := map[int]bool{}
	for n := 10; n <= 100; n += 5 {
		seen[n] = true
	}
	for _, n := range extra {
		seen[n] = n >= 1 && n <= 100
	}
	for n := 1; n <= 100; n++ {
		if seen[n] {
			out = append(out, strconv.Itoa(n)+"%")
		}
	}
	return out
}

func pctOf(v string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(v, "%"))
	return n
}

// kmemPolicySource reads the policy and where it comes from ("default" or a saved fact).
func (d *desk) kmemPolicySource() (handoffPolicy, string, bool) {
	var r struct {
		Source string `json:"source"`
	}
	p, ok := d.kmemPolicy()
	if ok && d.kmemJSON(&r, "handoff", "policy") {
		return p, r.Source, true
	}
	return p, "", ok
}

type policyRow struct {
	box              *fyne.Container
	soft, hard, mode *widget.Select
	status           *widget.Label
	save             *widget.Button
}

func (d *desk) handoffSettings() fyne.CanvasObject {
	d.policyUI = d.newPolicyRow()
	return d.policyUI.box
}

func (d *desk) newPolicyRow() *policyRow {
	r := &policyRow{}
	title := widget.NewLabel("Auto-handoff")
	title.TextStyle = fyne.TextStyle{Bold: true}
	var labels []string
	for _, m := range policyModes {
		labels = append(labels, m.label)
	}
	r.soft = widget.NewSelect(policyPcts(), nil)
	r.hard = widget.NewSelect(policyPcts(), nil)
	r.mode = widget.NewSelect(labels, nil)
	r.status = widget.NewLabel("")
	r.status.Wrapping = fyne.TextWrapWord
	r.status.SizeName = theme.SizeNameCaptionText
	note := widget.NewLabel("When a session's context reaches the first value, Quill offers to rotate it to a fresh session through its kmem handoff; at the second value it rotates on its own at a safe point. Automatic " +
		policyModes[0].note + "; Ask only " + policyModes[1].note + "; Off " + policyModes[2].note + ". Saved in kmem, so every Quill Desktop uses it.")
	note.Wrapping = fyne.TextWrapWord
	note.SizeName = theme.SizeNameCaptionText
	r.save = widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() { d.savePolicy(r) })
	r.save.Importance = widget.MediumImportance
	rowLabel := func(t string) fyne.CanvasObject { return container.NewCenter(widget.NewLabel(t)) }
	r.box = container.NewVBox(title,
		container.NewHBox(rowLabel("Ask at"), boxed(r.soft), rowLabel("Rotate at"), boxed(r.hard), rowLabel("Mode"), boxed(r.mode), r.save),
		note, r.status)
	d.showPolicy(r, d.handoffPolicy(), "")
	return r
}

// loadPolicyRow refreshes the row from kmem each time Settings opens, so a
// policy set on the command line or by another Quill shows up.
func (d *desk) loadPolicyRow() {
	r := d.policyUI
	if r == nil {
		return
	}
	on := d.kmemAvailable()
	for _, w := range []fyne.Disableable{r.soft, r.hard, r.mode, r.save} {
		if on {
			w.Enable()
		} else {
			w.Disable()
		}
	}
	if !on {
		r.status.SetText("kmem is not available, so auto-handoff is off.")
		return
	}
	r.status.SetText("Loading the current policy from kmem...")
	go func() {
		p, src, ok := d.kmemPolicySource()
		d.do(func() {
			if !ok {
				r.status.SetText("Could not read the policy from kmem; showing the defaults.")
				return
			}
			d.showPolicy(r, p, src)
		})
	}()
}

func (d *desk) showPolicy(r *policyRow, p handoffPolicy, src string) {
	r.soft.Options = policyPcts(p.Soft, p.Hard)
	r.hard.Options = r.soft.Options
	r.soft.SetSelected(fmt.Sprintf("%d%%", p.Soft))
	r.hard.SetSelected(fmt.Sprintf("%d%%", p.Hard))
	for _, m := range policyModes {
		if m.mode == p.Mode {
			r.mode.SetSelected(m.label)
		}
	}
	switch src {
	case "":
	case "default":
		r.status.SetText("Using kmem's defaults (nothing saved yet).")
	default:
		r.status.SetText("Using the policy saved in kmem.")
	}
}

func (d *desk) savePolicy(r *policyRow) {
	soft, hard := pctOf(r.soft.Selected), pctOf(r.hard.Selected)
	mode := ""
	for _, m := range policyModes {
		if m.label == r.mode.Selected {
			mode = m.mode
		}
	}
	if soft < 1 || hard < 1 || mode == "" {
		r.status.SetText("Pick both values and a mode.")
		return
	}
	if soft > hard {
		r.status.SetText("“Ask at” can't be higher than “Rotate at”.")
		return
	}
	r.save.Disable()
	r.status.SetText("Saving...")
	go func() {
		out, err := d.runKmem("kmem", "handoff", "policy", "set", fmt.Sprintf("soft=%d", soft), fmt.Sprintf("hard=%d", hard), "mode="+mode)
		d.do(func() {
			r.save.Enable()
			if err != nil {
				msg := strings.TrimSpace(out)
				if msg == "" {
					msg = err.Error()
				}
				r.status.SetText("kmem refused it: " + oneLine(msg, 200))
				return
			}
			p := d.handoffPolicy()
			p.Soft, p.Hard, p.Mode = soft, hard, mode
			d.policy, d.policyAt = p, time.Now()
			r.status.SetText(fmt.Sprintf("Saved: ask at %d%%, rotate at %d%%, %s.", soft, hard, strings.ToLower(r.mode.Selected)))
		})
	}()
}
