// Package usecase — flow helper types and stubs.
//
// flow_helpers.go owns the local type stubs that were previously
// defined in packages that have been removed (realtime, association,
// clipresolver). These types are shared across the capability-specific
// files (clip_source.go, document_builder.go, specscene.go,
// translation.go).
//
// Split topology (July 2026, LONG-FILES-SPLIT-2026-07-06):
//   - flow_helpers.go:     THIS FILE — stub types + minInt + Phase 1b stubs
//   - clip_source.go:      SearchScriptAssets, BuildPhraseClipSuggestions, SearchIntroClips, query helpers
//   - document_builder.go: ResolveRecommendedDriveFolder
//   - specscene.go:        EnrichSpecialNamesWithImages, enrichSingleEntity, ExtractScriptEntities
//   - translation.go:      SearchArtlistClips, artlistSearchPhrase, resolveArtlistFolderForPhrase, enqueueArtlistBackgroundJob
package usecase

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── Local type stubs for removed packages ───────────────────────────────────
// realtime.MatchAsset and association.CandidatesRequest were defined in
// packages that no longer exist (removed from remote, June 2026). These
// local types preserve compilation until the real types are restored.

// RealtimeMatchAsset mirrors the removed realtime.MatchAsset.
type RealtimeMatchAsset struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}

// AssociationCandidatesRequest mirrors the removed association.CandidatesRequest.
type AssociationCandidatesRequest struct {
	Topic     string   `json:"topic"`
	Subject   string   `json:"subject"`
	Narrative string   `json:"narrative"`
	Keywords  []string `json:"keywords"`
	Entities  []string `json:"entities"`
	TopK      int      `json:"top_k"`
}

// AssociationCandidate mirrors a single candidate from the removed association package.
type AssociationCandidate struct {
	Database string  `json:"database"`
	Source   string  `json:"source"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Link     string  `json:"link"`
	FolderID string  `json:"folder_id"`
	Score    float64 `json:"score"`
	Reason   string  `json:"reason"`
}

// AssociationCandidatesResponse mirrors the removed association response.
type AssociationCandidatesResponse struct {
	Candidates []AssociationCandidate `json:"candidates"`
}

// ── Stub types for packages removed during consolidation ────────────────────

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

// ScriptArtlistClipSuggestion pairs an artlist phrase with matching clips.
//
// TranslationError (PR 0.6, June 2026) is the explicit error marker
// for the artlist-phrase → English translation step. Non-empty when
// the translator call (artlistSearchPhrase) failed or returned an
// empty string. When populated, Clips is intentionally empty (no
// silent fallback to the original phrase — godlike/07
// no-fake-availability). Phrase stays populated with the
// user-supplied input so the API response remains contract-stable.
type ScriptArtlistClipSuggestion struct {
	Phrase           string                  `json:"phrase"`
	Clips            []ScriptAssetSuggestion `json:"clips"`
	FolderLink       string                  `json:"folder_link"`
	FolderName       string                  `json:"folder_name"`
	FolderID         string                  `json:"folder_id"`
	TranslationError string                  `json:"translation_error,omitempty"`
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
	ExtractEntitiesFromScriptWithModel(ctx context.Context, segments []string, maxEntities int, model string) (*asset.FullEntityAnalysis, error)
}

// ── Shared helpers ──────────────────────────────────────────────────────────

// minInt is a local helper (avoid import cycle from sliceutil).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// NewGenerateEnqueueRequest builds the request for the unified generate endpoint.
// Phase 1b stub.
func NewGenerateEnqueueRequest(payload any) any { return payload }

// EnqueueGenerationJob is a Phase 1b stub.
func EnqueueGenerationJob(ctx context.Context, jobsSvc any, req any, log any) (any, error) {
	return nil, fmt.Errorf("enqueue not implemented (Phase 1b stub)")
}
