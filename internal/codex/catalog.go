package codex

import "context"

type ModelInfo struct {
	ID, Model, DisplayName, DefaultReasoningEffort string
	Hidden, IsDefault                              bool
	SupportedReasoningEfforts                      []struct{ ReasoningEffort, Description string }
}

type ModeInfo struct {
	Mode, Model, Name     string
	ReasoningEffort       string `json:"reasoning_effort"`
	DeveloperInstructions string `json:"developer_instructions"`
}

type Catalog struct {
	Models []ModelInfo
	Modes  []ModeInfo
}

func ReadCatalog(ctx context.Context, bin, cwd string) (Catalog, error) {
	var catalog Catalog
	err := withAccountRPCAt(ctx, bin, cwd, func(r *accountRPC) error {
		cursor := ""
		for {
			var page struct {
				Data       []ModelInfo
				NextCursor string
			}
			params := map[string]any{"includeHidden": false}
			if cursor != "" {
				params["cursor"] = cursor
			}
			if err := r.call("model/list", params, &page); err != nil {
				return err
			}
			catalog.Models = append(catalog.Models, page.Data...)
			if page.NextCursor == "" || page.NextCursor == cursor {
				break
			}
			cursor = page.NextCursor
		}
		var modes struct{ Data []ModeInfo }
		if err := r.call("collaborationMode/list", nil, &modes); err == nil {
			catalog.Modes = modes.Data
		}
		return nil
	})
	return catalog, err
}
