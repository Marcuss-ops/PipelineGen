// Package usecase — flow_helpers_clips.go: clip-domain stub types.
//
// Extracted from flow_helpers.go (July 2026, PR-FLOW-HELPERS-SPLIT).
// Owns: AssetSearchTarget, ScriptAssetSuggestion,
// ScriptPhraseClipSuggestion, ScriptEntityImage.
package usecase

// ── Clip-domain stub types ──────────────────────────────────────────────────

// AssetSearchTarget narrows a search query to a specific source and media type.
type AssetSearchTarget struct {
	Source    string
	MediaType string
}

// ScriptAssetSuggestion represents a single clip recommendation for a script.
type ScriptAssetSuggestion struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}

// ScriptPhraseClipSuggestion pairs a key phrase with matching clips.
type ScriptPhraseClipSuggestion struct {
	Phrase string                  `json:"phrase"`
	Clips  []ScriptAssetSuggestion `json:"clips"`
}

// ScriptEntityImage represents an image found or generated for a named entity.
type ScriptEntityImage struct {
	EntityName  string `json:"entity_name"`
	ImageHash   string `json:"image_hash"`
	ImageURL    string `json:"image_url"`
	PathRel     string `json:"path_rel"`
	Description string `json:"description"`
	Source      string `json:"source"`
	DriveLink   string `json:"drive_link"`
	Error       string `json:"error,omitempty"`
}
