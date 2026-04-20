package stockdb

import (
	"velox/go-master/internal/slugify"
)

// NormalizeSlug exports the normalizeSlug function for external use
func NormalizeSlug(s string) string {
	return slugify.Marshal(s)
}

func normalizeSlug(s string) string {
	return slugify.Marshal(s)
}
