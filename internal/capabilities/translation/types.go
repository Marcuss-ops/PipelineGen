// Package translation provides the canonical translation port for the
// application layer. Voiceover.Promo moves to workflow/promo and imports
// from here instead of voiceover/types.go.
package translation

import "context"

// TranslatorFunc translates text to a target language. Implementations
// are wired at composition time (build_bundles_voiceover.go wraps
// ollama.Generator.TranslateText).
type TranslatorFunc func(ctx context.Context, text, targetLanguage string) (string, error)
