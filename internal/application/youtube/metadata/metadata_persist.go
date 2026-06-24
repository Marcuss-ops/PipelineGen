// Package metadata — Persistence side of the YouTube metadata capability.
//
// Per Phase 1B of CPR-CC-6 (June 2026): extracted from the root youtube
// package. The ym* helpers (description, tags, categories, view-count,
// upload-date, thumbnail URL) and the metadata* typed extractors
// (string-slice / float64 / bool / int) are owned canonically by
// helpers.go. metadata_persist.go carries the package boundary so the
// persistence-side import path stays stable while helpers.go remains
// the single source of truth.
//
// AGENT-2 build-fix (June 2026):
// Removed 10 redundant declaration copies (ymDescription / ymTags /
// ymCategories / ymViewCount / ymUploadDate / ymThumbnailURL /
// metadataStringSlice / metadataFloat64 / metadataBool / metadataInt)
// that previously redeclared symbols already declared in helpers.go.
// The two files share `package metadata`, so the duplicate declarations
// raised "redeclared in this block" errors at `go build ./...`. No
// behavioural change: helpers.go remains the sole owner and every
// caller in the package resolves to the canonical implementation
// through Go's package-level scoping.
package metadata
