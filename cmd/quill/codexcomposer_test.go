package main

import (
	"encoding/json"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/katachinosekai/quill/internal/chat"
	"github.com/katachinosekai/quill/internal/codex"
)

func TestCodexComposerLimitsModesToModel(t *testing.T) {
	h := newHarness(t)
	var catalog codex.Catalog
	if err := json.Unmarshal([]byte(`{
        "models":[
            {"model":"test-plan","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]},
            {"model":"test-low","supportedReasoningEfforts":[{"reasoningEffort":"low"}]}
        ],
        "modes":[{"mode":"plan","model":"test-plan","reasoning_effort":"medium"}]
    }`), &catalog); err != nil {
		t.Fatal(err)
	}
	h.ui(func() {
		d := h.d
		s := d.addSessionWithBackend(h.proj, "test-plan", "plan", false, true)
		d.codexCatalogs = map[string]*composerCatalog{s.cwd: {Catalog: catalog}}
		s.codexEffort = "medium"
		if !contains(d.composerModeOptions(s), "plan") || d.codexTurnSettings(s).Mode != "plan" {
			t.Fatal("the model's advertised plan mode should be selectable")
		}
		d.setModel(s, "test-low")
		if contains(d.composerModeOptions(s), "plan") || s.mode != "default" || s.codexEffort != "" {
			t.Fatal("switching to another model should remove incompatible mode and effort")
		}
	})
}

func TestCodexSessionApprovalRequiresServerChoice(t *testing.T) {
	for _, tt := range []struct {
		input   string
		allowed bool
	}{
		{`{}`, true},
		{`{"availableDecisions":["accept","decline"]}`, false},
		{`{"availableDecisions":["accept","acceptForSession","decline"]}`, true},
		{`{"availableDecisions":[]}`, false},
	} {
		item := &chat.Item{ToolName: "Codex command", Input: json.RawMessage(tt.input)}
		if got := nativeSessionApprovalAvailable(item); got != tt.allowed {
			t.Errorf("%s: %t", tt.input, got)
		}
	}
}

func TestCodexInlineReasoningSliderFollowsComposerSession(t *testing.T) {
	h := newDeskHarness(t, false)
	var catalog codex.Catalog
	if err := json.Unmarshal([]byte(`{"models":[
		{"model":"test-many","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}]},
		{"model":"test-one","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}
	]}`), &catalog); err != nil {
		t.Fatal(err)
	}
	h.ui(func() {
		d := h.d
		d.codexCatalogs = map[string]*composerCatalog{h.proj: {Catalog: catalog}}
		primary := d.addSessionWithBackend(h.proj, "test-many", "default", true, true)
		main := d.reasoning
		if !main.root.Visible() || main.label.Text != "Reasoning effort · Default" || main.slider.Max != 2 {
			t.Fatal("the main composer should show a label and a two-level slider plus Default")
		}
		main.slider.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
		if primary.codexEffort != "low" || main.label.Text != "Reasoning effort · low" {
			t.Fatal("the keyboard should apply the visible effort to the current session")
		}
		d.setSplit(2)
		pane := d.paneFor(primary)
		if pane == nil || !pane.reasoning.root.Visible() || pane.reasoning.slider.Value != 1 {
			t.Fatal("the split composer should show the session's current setting")
		}
		pane.reasoning.slider.SetValue(2)
		if primary.codexEffort != "high" {
			t.Fatal("the split slider should write to that pane's session")
		}
		d.setModel(primary, "test-one")
		d.refreshComposerFor(primary)
		if primary.codexEffort != "" || pane.reasoning.slider.Max != 1 || pane.reasoning.slider.Value != 0 {
			t.Fatal("changing the model should reset an unsupported effort and refresh the steps")
		}

		a := d.codexAssistant(codexQuillRole)
		operator := d.addSessionWithBackend(h.proj, "test-one", "default", false, true)
		a.s = operator
		d.markHiddenAs(operator, roleCodexQuill)
		d.openAssistBox(roleCodexQuill)
		if !a.reasoning.root.Visible() || a.reasoning.slider.Max != 1 {
			t.Fatal("the operator composer should use its own model levels")
		}
		a.reasoning.slider.SetValue(1)
		if operator.codexEffort != "medium" || primary.codexEffort != "" {
			t.Fatal("the operator's slider must not change the tab")
		}
		d.closeAssistBox()
		d.setSplit(1)
		d.addSession(h.proj, "default", "default", true)
		if d.reasoning.root.Visible() {
			t.Fatal("Claude's composer should not show the Codex slider")
		}
	})
}
