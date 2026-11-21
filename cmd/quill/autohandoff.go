package main

import (
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

type handoffPolicy struct {
	Soft, Hard int
	Mode       string
}

const (
	policyRefresh = 10 * time.Minute
	rotateRetry   = 5
)

var (
	defaultPolicy = handoffPolicy{Soft: 50, Hard: 75, Mode: "auto"}
	policyField   = regexp.MustCompile(`\b(soft|hard|mode)\s*=\s*(\w+)`)
)

func parsePolicy(out string) (handoffPolicy, bool) {
	p, seen := defaultPolicy, false
	for _, m := range policyField.FindAllStringSubmatch(out, -1) {
		switch m[1] {
		case "soft", "hard":
			n, err := strconv.Atoi(m[2])
			if err != nil || n < 1 || n > 100 {
				return defaultPolicy, false
			}
			if m[1] == "soft" {
				p.Soft = n
			} else {
				p.Hard = n
			}
		case "mode":
			switch m[2] {
			case "auto", "ask", "off":
				p.Mode = m[2]
			default:
				return defaultPolicy, false
			}
		}
		seen = true
	}
	if !seen || p.Soft > p.Hard {
		return defaultPolicy, false
	}
	return p, true
}

func (d *desk) handoffPolicy() handoffPolicy {
	if d.policy.Mode == "" {
		d.policy = defaultPolicy
	}
	if !d.policyBusy && time.Since(d.policyAt) > policyRefresh {
		d.policyBusy = true
		go func() {
			out, err := d.runKmem("kmem", "handoff", "policy")
			p, ok := parsePolicy(out)
			d.do(func() {
				d.policyBusy = false
				d.policyAt = time.Now()
				if err == nil && ok {
					d.policy = p
				} else {
					d.policy = defaultPolicy
				}
			})
		}()
	}
	return d.policy
}

func contextPct(s *deskSession) int {
	st := s.conv.Stats
	if st.ContextMax <= 0 || st.ContextUsed <= 0 {
		return 0
	}
	return st.ContextUsed * 100 / st.ContextMax
}

func (d *desk) safePoint(s *deskSession) bool {
	return s.running() && !s.conv.Busy && len(s.queue) == 0 && s.kmemSave == nil && !s.compacting &&
		len(s.conv.PendingPermissions()) == 0 && !s.conv.AsksApproval() && !s.stopping
}

func (d *desk) checkAutoHandoff(s *deskSession) {
	if !d.kmemAvailable() {
		return
	}
	p := d.handoffPolicy()
	pct := contextPct(s)
	if p.Mode == "off" || pct < p.Soft || !d.safePoint(s) {
		return
	}
	name := d.org.Kmem[s.id]
	hard := pct >= p.Hard
	if hard && p.Mode == "auto" && name != "" {
		if s.rotateTried > 0 && pct < s.rotateTried+rotateRetry {
			return
		}
		s.rotateTried = pct
		d.closeRotateCards(s)
		d.notify(s, fmt.Sprintf("Context is at %d%% (auto-handoff at %d%%): rotating to a fresh session from kmem “%s”.", pct, p.Hard, name))
		d.relaunchViaKmem(s.id, name)
		return
	}
	level := 1
	if hard {
		level = 2
	}
	if s.rotateAsk >= level {
		return
	}
	s.rotateAsk = level
	d.closeRotateCards(s)
	it := s.conv.AddRotate(fmt.Sprintf("Context is at %d%%.", pct))
	d.chatFor(s, []*chat.Item{it})
}

func (d *desk) closeRotateCards(s *deskSession) {
	var out []*chat.Item
	for _, it := range s.conv.Items {
		if it.Kind == chat.Rotate && it.PermState == chat.PermPending {
			it.PermState = chat.PermCancelled
			out = append(out, it)
		}
	}
	if len(out) > 0 {
		d.chatFor(s, out)
	}
}

func (d *desk) answerRotate(s *deskSession, it *chat.Item, rotate bool) {
	if s == nil || it.PermState != chat.PermPending {
		return
	}
	if !rotate {
		it.PermState = chat.PermDenied
		d.chatFor(s, []*chat.Item{it})
		return
	}
	name := d.org.Kmem[s.id]
	if name == "" {
		d.askKmemName(s.id)
		return
	}
	it.PermState = chat.PermAllowed
	d.chatFor(s, []*chat.Item{it})
	d.relaunchViaKmem(s.id, name)
}

func (v *chatView) renderRotate(it *chat.Item) fyne.CanvasObject {
	owner := v.s
	name := ""
	if owner != nil {
		name = v.d.org.Kmem[owner.id]
	}
	title := widget.NewLabel("Rotate to a fresh session?")
	title.Alignment = v.align()
	title.TextStyle = fyne.TextStyle{Bold: true}
	text := it.Text + " "
	if name != "" {
		text += fmt.Sprintf("Rotating saves this session's kmem handoff “%s”, starts a fresh session that resumes from it, and deletes this one.", name)
	} else {
		text += "Give this session a kmem name first; then it can rotate through a kmem handoff."
	}
	msg := widget.NewLabel(text)
	msg.Wrapping = fyne.TextWrapWord
	msg.SizeName = theme.SizeNameCaptionText
	msg.Alignment = v.align()
	parts := []fyne.CanvasObject{title, msg}
	border := color.Color(colAccent)
	if it.PermState == chat.PermPending {
		label, icon := "Rotate now", theme.ViewRefreshIcon()
		if name == "" {
			label, icon = "kmem name...", theme.SettingsIcon()
		}
		yes := glowButton(label, icon, colIdle, owner != nil, func() { v.d.answerRotate(owner, it, true) })
		no := glowButton("Not now", theme.CancelIcon(), colMuted, true, func() { v.d.answerRotate(owner, it, false) })
		parts = append(parts, v.buttonRow(yes, no))
	} else {
		state := "Not now"
		switch it.PermState {
		case chat.PermAllowed:
			state = "Rotating"
		case chat.PermCancelled:
			state = "No longer needed"
		}
		parts = append(parts, mutedText(state, fontBody))
		border = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	}
	r := canvas.NewRectangle(colCard)
	r.CornerRadius = 10
	r.StrokeColor = border
	r.StrokeWidth = 1.5
	return container.NewVBox(padTop(4), container.NewStack(r, container.NewPadded(container.NewVBox(parts...))), padTop(4))
}
