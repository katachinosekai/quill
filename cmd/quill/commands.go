package main

import (
	"image/color"
	"os"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/protocol"
	"github.com/katachinosekai/quill/internal/session"
)

const maxCmdRows = 8

type cmdMenu struct {
	d       *desk
	panel   *fyne.Container
	box     *fyne.Container
	matches []protocol.Command
	sel     int
}

type tapRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
	icon    *widget.Icon
}

func newTapRow(c fyne.CanvasObject, onTap func()) *tapRow {
	r := &tapRow{content: c, onTap: onTap}
	r.ExtendBaseWidget(r)
	return r
}

func (r *tapRow) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(r.content) }
func (r *tapRow) Tapped(*fyne.PointEvent)             { r.onTap() }

func (d *desk) fetchCommands() {
	home, _ := os.UserHomeDir()
	cmds, agents, err := session.FetchInit(d.cfg.ClaudeBin, home, 30*time.Second)
	if err != nil || len(cmds) == 0 {
		return
	}
	d.do(func() { d.cmdList, d.agentList = cmds, agents })
}

func (d *desk) commandsFor(s *deskSession) []protocol.Command {
	list := d.cmdList
	if s != nil && s.proc != nil {
		if c := s.proc.Commands(); len(c) > 0 {
			list = c
		}
	}
	seen := map[string]bool{}
	out := make([]protocol.Command, 0, len(list)+len(localCommands))
	for _, c := range localCommands {
		seen[c.Name] = true
		out = append(out, c)
	}
	for _, c := range list {
		if !seen[c.Name] && terminalOnly[c.Name] == "" {
			out = append(out, c)
		}
	}
	return out
}

func matchCommands(all []protocol.Command, text string) []protocol.Command {
	if !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\n") {
		return nil
	}
	q := strings.ToLower(text[1:])
	var prefix, inside []protocol.Command
	for _, c := range all {
		n := strings.ToLower(c.Name)
		switch {
		case strings.HasPrefix(n, q):
			prefix = append(prefix, c)
		case q != "" && strings.Contains(n, q):
			inside = append(inside, c)
		}
	}
	byName := func(l []protocol.Command) {
		sort.SliceStable(l, func(i, j int) bool { return l[i].Name < l[j].Name })
	}
	byName(prefix)
	byName(inside)
	out := append(prefix, inside...)
	if len(out) > maxCmdRows {
		out = out[:maxCmdRows]
	}
	return out
}

func (m *cmdMenu) visible() bool {
	return m.panel != nil && m.panel.Visible() && len(m.matches) > 0
}

func (m *cmdMenu) hide() {
	m.matches = nil
	if m.panel != nil && m.panel.Visible() {
		m.panel.Hide()
		relayout(m.d.mainPane)
	}
}

func (m *cmdMenu) update(text string) {
	d := m.d
	if d.input.Disabled() {
		m.hide()
		return
	}
	matches := matchCommands(d.commandsFor(d.current), text)
	if len(matches) == 0 || (len(matches) == 1 && "/"+matches[0].Name == text) {
		m.hide()
		return
	}
	m.matches = matches
	if m.sel >= len(matches) {
		m.sel = 0
	}
	m.render()
}

func (m *cmdMenu) render() {
	d := m.d
	m.box.RemoveAll()
	for i, c := range m.matches {
		name := canvas.NewText("/"+c.Name, colText)
		name.TextStyle.Bold = true
		name.TextSize = fontHeading
		left := container.NewHBox(name)
		if c.ArgumentHint != "" {
			left.Add(mutedText(c.ArgumentHint, fontMeta))
		}
		desc := widget.NewLabel(oneLine(c.Description, 160))
		desc.Truncation = fyne.TextTruncateEllipsis
		desc.SizeName = theme.SizeNameCaptionText
		row := container.NewBorder(nil, nil, container.NewCenter(left), nil, desc)
		hl := canvas.NewRectangle(color.Transparent)
		hl.CornerRadius = 6
		if i == m.sel {
			hl.FillColor = color.NRGBA{0xd9, 0x77, 0x57, 0x40}
		}
		idx := i
		m.box.Add(newTapRow(container.NewStack(hl, container.NewPadded(row)), func() {
			m.sel = idx
			m.accept()
		}))
	}
	m.box.Refresh()
	if !m.panel.Visible() {
		m.panel.Show()
		relayout(d.mainPane)
	}
}

func newCmdMenu(d *desk) *cmdMenu {
	m := &cmdMenu{d: d, box: container.NewVBox()}
	bgRect := canvas.NewRectangle(colCard)
	bgRect.CornerRadius = 10
	bgRect.StrokeColor = color.NRGBA{0x44, 0x44, 0x40, 0xff}
	bgRect.StrokeWidth = 1
	m.panel = container.NewStack(bgRect, container.NewPadded(m.box))
	m.panel.Hide()
	return m
}

func (m *cmdMenu) accept() {
	if len(m.matches) == 0 {
		return
	}
	c := m.matches[m.sel]
	m.hide()
	text := "/" + c.Name + " "
	m.d.input.SetText(text)
	m.d.input.CursorColumn = len([]rune(text))
	m.d.input.CursorRow = 0
	m.d.input.Refresh()
	m.d.win.Canvas().Focus(m.d.input)
}

func (m *cmdMenu) key(k *fyne.KeyEvent) bool {
	if !m.visible() {
		return false
	}
	switch k.Name {
	case fyne.KeyDown:
		m.sel = (m.sel + 1) % len(m.matches)
		m.render()
	case fyne.KeyUp:
		m.sel = (m.sel - 1 + len(m.matches)) % len(m.matches)
		m.render()
	case fyne.KeyTab:
		m.accept()
	case fyne.KeyReturn, fyne.KeyEnter:
		if "/"+m.matches[m.sel].Name == strings.TrimSpace(m.d.input.Text) {
			m.hide()
			return false
		}
		m.accept()
	case fyne.KeyEscape:
		m.hide()
	default:
		return false
	}
	return true
}
