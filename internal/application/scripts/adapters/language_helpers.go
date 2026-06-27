// Package scripts — language helpers + constants extracted from types.go (PG-029, June 2026).
package adapters

import "strings"

// ── Default prompt version constants ─────────────────────────────────────

const (
	DefaultBookPromptVersion       = "v1"
	DefaultBookEditorPromptVersion = "v1"
	DefaultBookQAPromptVersion     = "v1"
	DefaultTextPromptVersion       = "v1"
	DefaultTextEditorPromptVersion = "v1"
	DefaultTextQAPromptVersion     = "v1"
)

// ── Language helpers ─────────────────────────────────────────────────────

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

// NormalizeLanguages trims, deduplicates, and preserves order for a language list.
func NormalizeLanguages(languages []string) []string {
	out := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, lang := range languages {
		lang = strings.TrimSpace(lang)
		if lang == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		out = append(out, lang)
	}
	return out
}
