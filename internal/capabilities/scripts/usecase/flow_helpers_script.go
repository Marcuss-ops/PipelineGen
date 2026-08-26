// Package usecase — flow_helpers_script.go: script-domain stub types.
//
// Extracted from flow_helpers.go (July 2026, PR-FLOW-HELPERS-SPLIT).
// Owns: ScriptDriveFolderSuggestion, EntityScriptExtractor.
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ── Script-domain stub types ────────────────────────────────────────────────

// ScriptDriveFolderSuggestion recommends a Drive folder for a script.
type ScriptDriveFolderSuggestion struct {
	Database string `json:"database"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Link     string `json:"link"`
	FolderID string `json:"folder_id"`
	Score    int    `json:"score"`
	Reason   string `json:"reason"`
}

// EntityScriptExtractor is the interface for extracting entities from script text.
type EntityScriptExtractor interface {
	ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, maxEntities int, model string) (*detail.FullEntityAnalysis, error)
}
