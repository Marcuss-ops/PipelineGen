// Package usecase — flow_helpers_voiceover.go: voiceover-domain stub types.
//
// Extracted from flow_helpers.go (July 2026, PR-FLOW-HELPERS-SPLIT).
// Owns: ScriptArtlistClipSuggestion.
package usecase

// ── Voiceover-domain stub types ─────────────────────────────────────────────

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
