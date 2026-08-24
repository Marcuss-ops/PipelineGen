// Package scripts — language helpers + constants extracted from types.go
// (PG-029, June 2026).
//
// Phase 1c Commit 2/4 (June 2026): NormalizeLanguages removed from this
// package. The helper was relocated DOWN into
// internal/capabilities/scripts/dto/language_helpers.go so the canonical
// BuildMetadataLanguages (in dto/metadata.go) could reach it without a
// dto→adapters import cycle (the canonical dto imports `kernel/script`
// + `pkg/concurrent` only — adding adapters would have created a future
// cycle when adapters later reached back to dto). The dto-side helper
// extends the pre-commit semantics with a lowercase fold (per the
// user's spec — the pre-commit implementation did trim + dedupe only).
// SupportedScriptLanguages + the 6 prompt-version consts RETAINED here
// because adapters is their existing call-site container (the prompt
// version consts flow through adapters.NormalizationConfig today and
// would have required a future cycle path if moved alongside
// NormalizeLanguages).
package adapters

// ── Default prompt version constants ────────────────────────────────────────

const (
	DefaultBookPromptVersion       = "v1"
	DefaultBookEditorPromptVersion = "v1"
	DefaultBookQAPromptVersion     = "v1"
	DefaultTextPromptVersion       = "v1"
	DefaultTextEditorPromptVersion = "v1"
	DefaultTextQAPromptVersion     = "v1"
)

// ── Language helpers ────────────────────────────────────────────────────────

// SupportedScriptLanguages returns the list of supported script languages.
func SupportedScriptLanguages(translateLanguages []string, sourceLang string) []string {
	langs := []string{}
	if sourceLang != "" {
		langs = append(langs, sourceLang)
	}
	for _, l := range translateLanguages {
		found := false
		for _, existing := range langs {
			if existing == l {
				found = true
				break
			}
		}
		if !found {
			langs = append(langs, l)
		}
	}
	if len(langs) == 0 {
		langs = []string{"en", "it"}
	}
	return langs
}
