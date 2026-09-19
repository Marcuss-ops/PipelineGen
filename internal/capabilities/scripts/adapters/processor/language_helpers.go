// Package scripts — language helpers + constants extracted from types.go
// (PG-029, June 2026).
//
// Phase 1c Commit 2/4 (June 2026): NormalizeLanguages was removed from this
// package. The helper later moved down into the dto package for the canonical
// BuildMetadataLanguages; both were removed in the post-cutover NLP cleanup
// (the deterministic VisualNER + phrases.Select chain replaced the LLM
// post-generation metadata phase). SupportedScriptLanguages + the 6
// prompt-version consts RETAINED here because adapters is their call-site
// container (the prompt version consts flow through adapters.NormalizationConfig
// today).
package processor

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
