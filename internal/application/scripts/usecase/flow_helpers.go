// Package usecase — flow_helpers.go: shared stub types and helpers.
//
// PR-FLOW-HELPERS-SPLIT (July 2026): decomposed the original
// flow_helpers.go into 4 domain-specific files per AGENTS.md Pattern 5:
//
//   - flow_helpers.go           — shared: RealtimeMatchAsset,
//     AssociationCandidatesRequest/Response,
//     minInt
//   - flow_helpers_clips.go     — clips: AssetSearchTarget,
//     ScriptAssetSuggestion,
//     ScriptPhraseClipSuggestion,
//     ScriptEntityImage
//   - flow_helpers_script.go    — script: ScriptDriveFolderSuggestion,
//     EntityScriptExtractor
//   - flow_helpers_voiceover.go — voiceover: ScriptArtlistClipSuggestion
package usecase

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

// ── Shared helpers ──────────────────────────────────────────────────────────

// minInt is a local helper (avoid import cycle from sliceutil).
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
