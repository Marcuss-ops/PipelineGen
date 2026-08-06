// Package images — generated_image_naming.go (commit 8, 2026-07):
// pure naming helpers for ingestGeneratedImage.
//
// PR-GODOBJ-3 KILL LIST b (commit 8 split): the previous
// sync_generation.go combined the IngestImage dispatch with
// inline slug/filename/description/source canonicalisation. The
// 4 canonicalisation expressions are pure (no state, no I/O, no
// logging) so they belong as free functions in a dedicated file
// rather than alongside the dispatch.
//
// godlike/06 SSOT: NO pkg/imgsyncutil — these helpers are
// package-private to images and used by exactly one consumer
// (ingestGeneratedImage). Per the wave rule "create pkg/ ONLY
// when ≥2 capability reali need it", no separate package is
// warranted. Adding a pkg/ here would force a public API
// surface for helpers that are intentionally package-private.
//
// godlike/07 wrap-edges: each helper preserves the pre-split
// production-code byte-byte. The slug cap (50), filename cap (80),
// description templating, and source fallback (empty → "google-slides")
// are the same constants the pre-fix inline block produced.
package images

import (
	"fmt"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// buildGeneratedImageSlug produces the canonical slug for ingestion.
// Caps at 50 chars per pre-split byte-byte; relies on
// textutil.Slugify for whitespace/diacritic normalisation.
func buildGeneratedImageSlug(promptUsed string) string {
	slug := textutil.Slugify(promptUsed)
	if len(slug) > 50 {
		slug = slug[:50]
	}
	return slug
}

// buildGeneratedImageFilename produces the canonical on-disk filename.
// Caps the raw prompt at 80 chars, slugifies, then appends the
// format extension (png / jpeg / webp). Result is the canonical
// media_assets filename body ingested under.
func buildGeneratedImageFilename(promptUsed, format string) string {
	filename := promptUsed
	if len(filename) > 80 {
		filename = filename[:80]
	}
	return textutil.Slugify(filename) + "." + format
}

// buildGeneratedImageDescription produces the canonical agent-ops
// description. The string is human-readable on operator dashboards
// and is what the media_assets row stores for full-text search.
func buildGeneratedImageDescription(promptUsed string) string {
	return fmt.Sprintf("AI generated image via Chrome/Playwright for prompt: %s", promptUsed)
}

// resolveGeneratedImageSource picks the canonical source string.
// The Chrome infrastructure adapter emits "google-slides" by default; an empty
// provider field is interpreted as "not yet wired" — the canonical
// fallback preserves prior-version behavior byte-byte.
func resolveGeneratedImageSource(provider string) string {
	if provider == "" {
		return "google-slides"
	}
	return provider
}
