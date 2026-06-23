// Package scripts — flow types extracted from api/script/flow.go (PR2, June 2026).
//
// Note: ScriptInsights is declared in documents.go (canonical for the
// application layer). The types declared here are the narrow sub-types
// used by flow helpers and the insight builder.
package scripts

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Flow types ───────────────────────────────────────────────────────────────

// AssetSearchTarget represents a search target (source + mediaType).
type AssetSearchTarget struct {
	Source    string
	MediaType string
}

// ScriptAssetSuggestion is a single ranked asset suggestion.
type ScriptAssetSuggestion struct {
	ID        string  `json:"id,omitempty"`
	Name      string  `json:"name,omitempty"`
	Source    string  `json:"source,omitempty"`
	Score     float64 `json:"score,omitempty"`
	DriveLink string  `json:"drive_link,omitempty"`
}

// ScriptPhraseClipSuggestion pairs a phrase with matching clips.
type ScriptPhraseClipSuggestion struct {
	Phrase string                  `json:"phrase,omitempty"`
	Clips  []ScriptAssetSuggestion `json:"clips,omitempty"`
}

// ScriptDriveFolderSuggestion recommends a Drive folder for a script.
type ScriptDriveFolderSuggestion struct {
	Database string `json:"database,omitempty"`
	Source   string `json:"source,omitempty"`
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	Link     string `json:"link,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
	Score    int    `json:"score,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// ScriptArtlistClipSuggestion pairs an artlist phrase with clips and folder.
type ScriptArtlistClipSuggestion struct {
	Phrase     string                  `json:"phrase"`
	Clips      []ScriptAssetSuggestion `json:"clips,omitempty"`
	FolderLink string                  `json:"folder_link,omitempty"`
	FolderName string                  `json:"folder_name,omitempty"`
	FolderID   string                  `json:"folder_id,omitempty"`
}

// ScriptEntityImage represents an enriched image for a named entity.
type ScriptEntityImage struct {
	EntityName  string `json:"entity_name"`
	ImageHash   string `json:"image_hash,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	PathRel     string `json:"path_rel,omitempty"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
	Error       string `json:"error,omitempty"`
}

// EntityScriptExtractor extracts entities from a script.
type EntityScriptExtractor interface {
	ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, entityCount int, model string) (*asset.FullEntityAnalysis, error)
}
