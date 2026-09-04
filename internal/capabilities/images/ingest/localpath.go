package ingest

import "path/filepath"

// LocalPathFor returns the canonical local and relative paths for image ingest.
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
