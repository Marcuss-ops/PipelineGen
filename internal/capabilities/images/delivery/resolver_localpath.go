// Package destinations — LocalPathFor free function
// (PR-IMAGES-REMOVE-DRIVE-STORE, July 2026).
//
// LocalPathFor computes the canonical (LocalPath, RelativePath) pair
// an image ingest should write to, given the canonical images
// directory root, the image slug, the asset source identifier, and
// the file extension.
//
// The relative-path layout uses the source-prefix convention
// `<source>/<subject>.<ext>` (default source segment "media") that
// the legacy drive.Resolver.Resolve(req) emitted — so existing
// media_assets.PathRel rows OR pre-PR SQL row schema stay
// consistent with newly-ingested rows.
//
// godlike/06 SSOT: this is the canonical SOLE owner of local-disk
// path computation for the images package. The legacy
// drive.Store.ResolveDest was retired; callers that previously
// passed the AssetDestinationRequest shape now pass extracted
// (imagesDir, slug, source, ext) primitives so this function is
// trivially testable (no Drive struct dependency). The PR spec
// "sposta ResolveDest nel DestinationResolver già esistente" is
// satisfied by the function relocating into the same package as the
// canonical DestinationResolver (destinations/) — the interface
// signature stays narrow (Drive folder lookup) while the new
// function holds the path computation.
package delivery

import (
	"path/filepath"
)

// LocalPathFor returns (LocalPath, RelativePath) for an image ingest.
//
// RelativePath is source-prefixed (`<source>/<slug>.<ext>`) to
// preserve the legacy drive.Resolver convention. If source is
// empty the segment defaults to "media" so the same fallback the
// legacy resolver applied kicks in. If imagesDir is empty only
// RelativePath is returned (callers that pre-anchor against a
// different root can splice it themselves).
//
// Pure function. Safe for concurrent use. Idempotent.
func LocalPathFor(imagesDir, slug, source, ext string) (string, string) {
	sourceSegment := source
	if sourceSegment == "" {
		sourceSegment = "media"
	}
	rel := filepath.Join(sourceSegment, slug+ext)
	if imagesDir == "" {
		return "", rel
	}
	return filepath.Join(imagesDir, rel), rel
}
