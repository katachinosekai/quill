package main

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

// Keep expansion scoped to the parent session. Reusing the strip's listing
// keeps running and recently completed subagents in the same order.
func (d *desk) withSidebarAgents(rows []sideRow) []sideRow {
	out := make([]sideRow, 0, len(rows))
	portrait := d.layoutMode() == layoutVertical && d.agentStrip != nil
	for _, row := range rows {
		if row.agentSession != "" {
			continue
		}
		if row.id == "" {
			out = append(out, row)
			continue
		}
		row.hasAgents, row.open = false, false
		if !portrait {
			out = append(out, row)
			continue
		}
		agents := d.agentStrip.listed(d.sessions[row.id])
		row.hasAgents = len(agents) > 0
		row.open = row.hasAgents && d.openSidebarAgents[row.id]
		out = append(out, row)
		if row.open {
			for _, agent := range agents {
				// Menus read tokens and other live details only when opened.
				label := chat.AgentUse{ID: agent.ID, Name: agent.Name, Type: agent.Type, Status: agent.Status, Done: agent.Done}
				out = append(out, sideRow{depth: row.depth + 1, agentSession: row.id, agent: label})
			}
		}
	}
	return out
}

func (d *desk) syncSidebarAgents() {
	if d.listTheme == nil {
		return
	}
	rows := d.withSidebarAgents(d.rows)
	if !slices.Equal(rows, d.rows) {
		d.drawSidebarRows(rows)
	}
}

func (d *desk) toggleSidebarAgents(session string) {
	if d.openSidebarAgents[session] {
		delete(d.openSidebarAgents, session)
	} else {
		if d.openSidebarAgents == nil {
			d.openSidebarAgents = make(map[string]bool)
		}
		d.openSidebarAgents[session] = true
	}
	d.syncSidebarAgents()
}

func (it *sideItem) showAgentMenu(row sideRow, pos fyne.Position) {
	d := it.d
	session := d.sessions[row.agentSession]
	if session == nil || d.agentStrip == nil {
		return
	}
	for _, agent := range d.agentStrip.listed(session) {
		if agent.ID == row.agent.ID {
			menu := fyne.NewMenu("", d.agentStrip.items(session, agent)...)
			widget.ShowPopUpMenuAtPosition(menu, d.win.Canvas(), pos)
			return
		}
	}
}
