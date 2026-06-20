// Package asset is the canonical domain home for the Asset model that
// sits at the application-provider boundary.
//
// Wave 12 follow-up scope (phase 1 of 3): this file is a thin alias
// layer over the legacy internal/assets package. Type aliases give us
// the formal "asset = canonical domain entity" position with zero
// consumer churn — every existing internal/assets importer still
// compiles because underlying types are identical. Future waves
// (phase 2: incremental migration of the 73 importers; phase 3:
// convergence with internal/domain/media) will absorb the actual
// types and their helper methods into this package.
//
// IMPORTANT — MediaType semantic conflict
//
// This package re-exports `asset.MediaType` as a HARD alias of
// `internal/assets.MediaType` (a plain string type used as
// `assets.MediaType("video")` etc. by 73+ existing importers).
//
// A DIFFERENT `media.MediaType` (with typed constants
// MediaTypeStock, MediaTypeClip, etc.) lives in
// internal/domain/media. The two are intentionally DISTINCT types
// and mixing them is a COMPILE error.
//
// Convention until phase 3 unification:
//   - Use `asset.MediaType` everywhere a value crosses the
//     internal/assets or providers boundary.
//   - Use `media.MediaType` only for new code that does NOT touch
//     `internal/assets` or `providers`.
//
// Rule of thumb: today the rule is "follow the package you import".
// After phase 3, the canonical will be `media.MediaType` and this
// package's alias will be replaced with a real type carrying the
// same constants.
//
// Note on State* constants: Go does not allow re-exporting const
// groups via type aliases, so each constant is re-declared by value.
// If you change one here you MUST change the same one in
// internal/assets until phase 3 removes the legacy package. See
// asset_test.go::TestStateConstantsMatchAssets for a parity check
// that runs in CI.
package asset

import (
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

// ── Type aliases ────────────────────────────────────────────────────
//
// Only the symbols the providers contract actually consumes today
// are aliased. Add new aliases one at a time as call sites migrate
// (phase 2); introducing unused aliases is YAGNI and creates
// drift surface.

type (
	// Asset is the canonical domain model for a media asset. See
	// internal/assets for the field-level definition and accessor
	// methods. Hard alias — callers may pass *assets.Asset
	// interchangeably with *asset.Asset at zero cost.
	Asset = assets.Asset

	// MediaType classifies the content kind of a media asset (see
	// the package doc above for the cross-package conflict note).
	MediaType = assets.MediaType
)

// ── LifecycleState constants ────────────────────────────────────────
//
// Re-declared from internal/assets because Go type aliases cannot
// re-export const groups. Values are tracked in the legacy home.
// asset_test.go::TestStateConstantsMatchAssets asserts the two stay
// in sync; the test runs in CI.

const (
	StateStaging    = assets.StateStaging
	StateProcessing = assets.StateProcessing
	StateActive     = assets.StateActive
	StateDeleted    = assets.StateDeleted
	StateReady      = assets.StateReady
	StatePending    = assets.StatePending
)
