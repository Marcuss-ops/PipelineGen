// Package usecase — flow_helpers_clips.go: clip-domain stub types.
//
// Extracted from flow_helpers.go (July 2026, PR-FLOW-HELPERS-SPLIT).
// Owns: ScriptAssetSuggestion — the wire shape shared by the live Artlist
// phrase-search path (flow_helpers_artlist.go::SearchArtlistClips →
// ScriptArtlistClipSuggestion).
//
// The post-cutover NLP cleanup removed AssetSearchTarget,
// ScriptPhraseClipSuggestion and ScriptEntityImage: the deterministic
// VisualNER + phrases.Select chain replaced the LLM clip-suggestion and
// entity-image enrichment phase, leaving those shapes with zero production
// readers.
package usecase

// ── Clip-domain stub types ──────────────────────────────────────────────────

// ScriptAssetSuggestion represents a single clip recommendation for a script.
type ScriptAssetSuggestion struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	DriveLink string  `json:"drive_link"`
}
