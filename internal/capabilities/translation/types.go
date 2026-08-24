// Package translation provides the canonical translation port for the
// application layer. Voiceover.Promo moves to workflow/promo and imports
// from here instead of voiceover/types.go.
package translation

import "context"

// TranslatorFunc translates text to a target language. Implementations
// are wired at composition time (build_bundles_voiceover.go wraps
// ollama.Generator.TranslateText).
type TranslatorFunc func(ctx context.Context, text, targetLanguage string) (string, error)

// LanguageTarget pairs a BCP-47 language code with a human-readable name.
type LanguageTarget struct {
	Code string
	Name string
}

// DefaultPromoLanguages returns the 13 promo voiceover languages.
func DefaultPromoLanguages() []LanguageTarget {
	return []LanguageTarget{
		{"en-US", "English"}, {"es-ES", "Spanish"}, {"fr-FR", "French"},
		{"de-DE", "German"}, {"it-IT", "Italian"}, {"pt-BR", "Portuguese"},
		{"pl-PL", "Polish"}, {"nl-NL", "Dutch"}, {"ja-JP", "Japanese"},
		{"ko-KR", "Korean"}, {"ru-RU", "Russian"}, {"tr-TR", "Turkish"},
		{"id-ID", "Indonesian"},
	}
}
