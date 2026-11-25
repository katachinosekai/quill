package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/katachinosekai/quill/internal/chat"
)

type nativeApproval struct {
	Command                         string
	Cwd                             string
	GrantRoot                       string
	AvailableDecisions              []json.RawMessage
	AdditionalPermissions           json.RawMessage
	ProposedExecpolicyAmendment     json.RawMessage
	ProposedNetworkPolicyAmendments json.RawMessage
	NetworkApprovalContext          *struct{ Host, Protocol string }
	Changes                         []struct{ Path, Diff string }
}

func codexCommandPermission(it *chat.Item) bool {
	return it.ToolName == "Codex command" || it.ToolName == "Codex file changes"
}

func nativeSessionApprovalAvailable(it *chat.Item) bool {
	if !codexCommandPermission(it) || len(it.Input) == 0 {
		return true
	}
	var request nativeApproval
	if json.Unmarshal(it.Input, &request) != nil {
		return false
	}
	if request.AvailableDecisions == nil {
		return true
	} // older Codex servers
	for _, choice := range request.AvailableDecisions {
		var decision string
		if json.Unmarshal(choice, &decision) == nil && decision == "acceptForSession" {
			return true
		}
	}
	return false
}

func nativeApprovalLabel(it *chat.Item) string {
	if it.ToolName == "Codex file changes" {
		return "Allow file changes?"
	}
	var data nativeApproval
	_ = json.Unmarshal(it.Input, &data)
	if data.NetworkApprovalContext != nil {
		return "Allow network access?"
	}
	return "Allow command?"
}

func (v *chatView) approvalPreview(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.Alignment = v.align()
	return label
}

func (v *chatView) nativeApprovalBody(it *chat.Item) ([]fyne.CanvasObject, fyne.CanvasObject) {
	var data nativeApproval
	_ = json.Unmarshal(it.Input, &data)
	var preview, full []fyne.CanvasObject
	if it.Request != nil && it.Request.DecisionReason != "" {
		reason := it.Request.DecisionReason
		preview = append(preview, v.approvalPreview(oneLine(reason, 200)))
		if oneLine(reason, 200) != strings.TrimSpace(reason) {
			full = append(full, v.approvalPreview(reason))
		}
	}
	if network := data.NetworkApprovalContext; network != nil {
		preview = append(preview, v.approvalPreview(strings.TrimSpace(network.Host+" "+network.Protocol)))
	}
	if data.GrantRoot != "" {
		preview = append(preview, v.approvalPreview("Write access: "+data.GrantRoot))
	}
	if data.Command != "" {
		if data.NetworkApprovalContext == nil {
			preview = append(preview, v.approvalPreview(oneLine(data.Command, 130)))
		}
		command := mono(data.Command)
		command.Selectable = true
		full = append(full, command)
	}
	if data.Cwd != "" {
		full = append(full, v.approvalPreview("Working folder: "+data.Cwd))
	}
	if len(data.AdditionalPermissions) > 0 && string(data.AdditionalPermissions) != "null" {
		preview = append(preview, v.approvalPreview("Requests additional permissions"))
		var formatted bytes.Buffer
		if json.Indent(&formatted, data.AdditionalPermissions, "", "  ") == nil {
			full = append(full, mono(formatted.String()))
		}
	}
	if n := len(data.Changes); n > 0 {
		summary := data.Changes[0].Path
		if n > 1 {
			summary = fmt.Sprintf("%s and %d more", summary, n-1)
		}
		preview = append(preview, v.approvalPreview(oneLine(summary, 140)))
		for _, change := range data.Changes {
			full = append(full, v.approvalPreview(change.Path))
			if change.Diff != "" {
				full = append(full, mono(change.Diff))
			}
		}
	}
	for _, proposed := range []struct {
		label string
		value json.RawMessage
	}{
		{"Suggested command rule", data.ProposedExecpolicyAmendment},
		{"Suggested network rules", data.ProposedNetworkPolicyAmendments},
	} {
		if len(proposed.value) == 0 || string(proposed.value) == "null" {
			continue
		}
		var formatted bytes.Buffer
		if json.Indent(&formatted, proposed.value, "", "  ") == nil {
			full = append(full, v.approvalPreview(proposed.label), mono(formatted.String()))
		}
	}
	if len(full) == 0 {
		return preview, nil
	}
	scroll := container.NewVScroll(container.NewVBox(full...))
	scroll.SetMinSize(fyne.NewSize(0, 160))
	scroll.Hide()
	panel := container.NewVBox()
	var button *widget.Button
	button = widget.NewButtonWithIcon("Details", theme.NavigateNextIcon(), func() {
		if scroll.Visible() {
			scroll.Hide()
			button.SetIcon(theme.NavigateNextIcon())
		} else {
			scroll.Show()
			button.SetIcon(theme.MenuDropDownIcon())
		}
		panel.Refresh()
		if iv := v.views[it.Seq]; iv != nil {
			v.settleHeight(iv)
		}
	})
	button.Importance = widget.LowImportance
	panel.Objects = []fyne.CanvasObject{button, scroll}
	return preview, panel
}
