// Package scripts — metadata.go is the canonical home for the
// post-generation metadata helpers previously co-located in
// api/script/helpers.go (BuildMetadataLanguages, GenerateVideoMetadata).
//
// These functions live in the application layer so PostGenUseCase
// calls them directly instead of receiving them as opaque
// function-port deps from the API layer. The call site in
// app/wire_script.go imports this package and passes the functions
// directly (no import cycle).
package scripts

import (
	"context"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildMetadataLanguages normalizes the requested metadata languages.
// English is always first, then any additional unique languages from the
// payload in the order they were provided.
func BuildMetadataLanguages(languages []string) []string {
	out := []string{"en"}
	for _, lang := range NormalizeLanguages(languages) {
		if lang == "en" {
			continue
		}
		out = append(out, lang)
	}

	return out
}

// GenerateVideoMetadata generates YouTube metadata (title, description, tags)
// for multiple languages in parallel. Optimized: generates English metadata
// ONCE via LLM, then translates for all other languages.
// If model is non-empty, it's passed to the Generator for the metadata +
// translation calls.
func GenerateVideoMetadata(ctx context.Context, generator *ollama.Generator, title string, languages []string, model string) []VideoMetadata {
	var mu sync.Mutex
	metadata := make([]VideoMetadata, 0, len(languages))
	var wg sync.WaitGroup

	// Generate English metadata FIRST — single LLM call shared across all languages
	var enDesc string
	var enTags []string
	if desc, tags, err := generator.GenerateVideoMetadataWithModel(ctx, title, model); err == nil {
		enDesc = desc
		enTags = tags
	}

	for _, lang := range languages {
		lang := lang // capture
		wg.Add(1)
		concurrent.SafeGoFunc("video-metadata-"+lang, lang, func(lang string) {
			defer wg.Done()

			meta := VideoMetadata{Language: lang}

			// Translate title to target language
			titleTranslated, _ := generator.TranslateTextWithModel(ctx, title, lang, model)
			if titleTranslated != "" {
				meta.Title = titleTranslated
			} else {
				meta.Title = title // fallback to original
			}

			if lang == "en" {
				// Use directly generated English metadata (no translation needed)
				meta.Description = enDesc
				meta.Tags = enTags
			} else if enDesc != "" {
				// Translate the pre-generated English metadata to target language
				descTranslated, _ := generator.TranslateTextWithModel(ctx, enDesc, lang, model)
				if descTranslated != "" {
					meta.Description = descTranslated
				} else {
					meta.Description = enDesc
				}

				// Translate tags
				var translatedTags []string
				for _, tag := range enTags {
					if t, err := generator.TranslateTextWithModel(ctx, tag, lang, model); err == nil && t != "" {
						translatedTags = append(translatedTags, t)
					} else {
						translatedTags = append(translatedTags, tag) // fallback to original tag
					}
				}
				meta.Tags = translatedTags
			}

			mu.Lock()
			metadata = append(metadata, meta)
			mu.Unlock()
		})
	}
	wg.Wait()

	return metadata
}
