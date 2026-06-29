// Package scripts — metadata.go is the canonical home for the
// post-generation metadata helpers previously co-located in
// api/script/helpers.go (BuildMetadataLanguages, GenerateVideoMetadata).
//
// These functions live in the application layer so PostGenUseCase
// calls them directly instead of receiving them as opaque
// function-port deps from the API layer. The call site in
// app/wire_script.go imports this package and passes the functions
// directly (no import cycle).
//
// PR 8 (June 2026): GenerateVideoMetadata returns
// scriptpkg.VideoMetadata (internal/domain/script) directly —
// the pre-PR-8 in-package VideoMetadata alias is gone.
package dto

import (
	"context"
	"strings"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// MetadataTranslator is the narrow port dto.GenerateVideoMetadata
// consumes. PR-VO-A6-style Pattern 0 (June 2026): *ollama.Generator
// satisfies this interface implicitly so existing production wiring
// compiles unchanged, while tests can inject a deterministic stub.
//
// The two methods are the only ones dto.GenerateVideoMetadata calls on
// the generator — keep this list lean so adapters don't have to stub
// unrelated methods.
type MetadataTranslator interface {
	GenerateVideoMetadataWithModel(ctx context.Context, title, model string) (string, []string, error)
	TranslateTextWithModel(ctx context.Context, text, lang, model string) (string, error)
}

// BuildMetadataLanguages normalizes the requested metadata languages.
// English is always first, then any additional unique languages from the
// payload in the order they were provided.
func BuildMetadataLanguages(languages []string) []string {
	out := []string{"en"}
	// FIXME(Phase 1c): NormalizeLanguages moved to scripts/adapters/.
	// Restore the call after contracts extraction.
	normalized := languages
	for _, lang := range normalized {
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
//
// PR 8 (June 2026): returns scriptpkg.VideoMetadata (the canonical
// structured shape in internal/domain/script).
//
// PR-VO cleanup (June 2026, P0.6 sotto-task 4): removed the silent
// "fallback to original text" behaviour on translator error. Each
// per-language VideoMetadata now carries an explicit
// TranslationStatus: "translated" on success, "untranslated" if the
// translator failed or returned an empty string. When
// TranslationStatus="untranslated" the Title/Description/Tags fields
// are EXPLICITLY cleared (empty string / nil) so callers cannot
// mistakenly surface the original `Title` or `enDesc` text as a fake
// translation. Per godlike/07 no-fake-availability.
func GenerateVideoMetadata(ctx context.Context, generator MetadataTranslator, title string, languages []string, model string) []scriptpkg.VideoMetadata {
	var mu sync.Mutex
	metadata := make([]scriptpkg.VideoMetadata, 0, len(languages))

	if generator == nil {
		// Nil generator short-circuits to empty result; callers must
		// decide whether to surface this as an error. The PR-VO
		// contract is to surface, not to silently substitute the
		// original `title` — we leave the slice empty so the caller
		// can detect the missing dependency.
		return metadata
	}

	var wg sync.WaitGroup

	// Generate English metadata FIRST — single LLM call shared across all languages.
	// On error we leave enDesc/enTags empty; callers downstream see
	// TranslationStatus="untranslated" rather than the (silent-success)
	// pre-PR-VO fallback of "Description = enDesc".
	//
	// enOK is the P0.6 godlike/07 bristle: it must be true ONLY when
	// the LLM returned a non-empty payload. A successful call with
	// both `enDesc==""` AND `len(enTags)==0` is the silent-fake-success
	// variant where the LLM cheerfully reports success but ships no
	// content — classifying that as "translated" would surface empty
	// fields to the caller under a green status. Per godlike/07 we
	// must downgrade to "untranslated" in that case too.
	var (
		enDesc string
		enTags []string
		enOK   bool
	)
	if desc, tags, err := generator.GenerateVideoMetadataWithModel(ctx, title, model); err == nil && (desc != "" || len(tags) > 0) {
		enDesc = desc
		enTags = tags
		enOK = true
	}

	for _, lang := range languages {
		lang := lang // capture
		wg.Add(1)
		concurrent.SafeGoFunc("video-metadata-"+lang, lang, func(lang string) {
			defer wg.Done()

			meta := scriptpkg.VideoMetadata{Language: lang, TranslationStatus: "translated"}

			if lang == "en" {
				// English is the source language — no translation
				// needed; the canonical Title is the input, the
				// canonical Description/Tags come from the
				// single-LLM batch.
				if enOK {
					meta.Title = title
					meta.Description = enDesc
					meta.Tags = enTags
					// TranslationStatus stays "translated" (struct
					// init). When the LLM call reports err OR
					// returns an empty payload, we explicitly
					// downgrade to "untranslated" — godlike/07
					// bans silent fake-success even on the source
					// language.
				} else {
					meta.TranslationStatus = "untranslated"
				}
			} else {
				titleTrans, titleErr := generator.TranslateTextWithModel(ctx, title, lang, model)
				if titleErr != nil || strings.TrimSpace(titleTrans) == "" {
					meta.TranslationStatus = "untranslated"
				} else {
					meta.Title = titleTrans
					if enDesc != "" {
						descTrans, descErr := generator.TranslateTextWithModel(ctx, enDesc, lang, model)
						if descErr != nil || strings.TrimSpace(descTrans) == "" {
							meta.TranslationStatus = "untranslated"
						} else {
							meta.Description = descTrans
							tagsOK := true
							translatedTags := make([]string, 0, len(enTags))
							for _, tag := range enTags {
								t, tagErr := generator.TranslateTextWithModel(ctx, tag, lang, model)
								if tagErr != nil || strings.TrimSpace(t) == "" {
									tagsOK = false
									break
								}
								translatedTags = append(translatedTags, t)
							}
							if !tagsOK {
								meta.TranslationStatus = "untranslated"
							} else {
								meta.Tags = translatedTags
							}
						}
					}
				}
			}

			if meta.TranslationStatus == "untranslated" {
				// Explicit reset: do NOT surface the original
				// `title` / `enDesc` / `enTags` as a "successful
				// translation". Per godlike/07.
				meta.Title = ""
				meta.Description = ""
				meta.Tags = nil
			}

			mu.Lock()
			metadata = append(metadata, meta)
			mu.Unlock()
		})
	}
	wg.Wait()

	return metadata
}
