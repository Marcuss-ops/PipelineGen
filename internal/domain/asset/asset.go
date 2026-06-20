// Package asset is the canonical domain home for the Asset model that
// sits at the application-provider boundary.
//
// Wave 12 follow-up scope: this file is a thin alias layer over the
// legacy internal/assets package. Type aliases give us the formal
// "asset = canonical domain entity" position with zero consumer
// churn — every existing internal/assets importer still compiles
// because underlying types are identical.
//
// Migration progress against the 73-importer Verdetto:
//
//   - Phase 1 (Wave 12 turn 2): foundation. Aliases = {Asset, MediaType}.
//     providers contract (internal/application/assets/providers) is
//     already on this package.
//   - Phase 2 PR-1: internal/api/sources/ migrated (16 files).
//     Aliases extended YAGNI = {Source, Repository, Filter}.
//   - Phase 2 PR-2 (this PR): internal/application/ migrated (10 files).
//     Aliases extended YAGNI: now 5 entries — {Details, Service,
//     LocationKind, LocationKindDrive, LocationKindLocal}. Two structs
//     (Details, Service), one type alias (LocationKind), and two
//     typed consts (LocationKindDrive/Local). Every other symbol
//     application/ uses was already covered by PR-1's alias set.
//     Parity covered by asset_test.go (TestLocationKind*).
//   - Phase 2 PR-3-4: internal/media/, internal/infrastructure/
//     — bounded-context batched, one PR each.
//   - Phase 3: convergence with internal/domain/media (MediaType
//     constants, LifecycleState promotion, etc.) — DEFERRED.
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
	//
	// Wave 12 follow-up (Sonnet): Asset is the foundation alias
	// from Phase 1 (Wave 12 turn 2).
	Asset = assets.Asset

	// MediaType classifies the content kind of a media asset (see
	// the package doc above for the cross-package conflict note).
	MediaType = assets.MediaType

	// Source is the per-asset origin tag (e.g. "youtube", "artlist",
	// "stock"). Hard alias; YAGNI added in Wave 12 follow-up Phase 2
	// PR-1 (internal/api/sources/ migration) since every clip-flow
	// handler instantiates it via assets.Source("<label>"). Will be
	// promoted to a typed enum with constants in phase 3 alongside
	// MediaType convergence.
	Source = assets.Source

	// Repository is the canonical asset persistence contract.
	// YAGNI added in Wave 12 follow-up Phase 2 PR-1.
	// `*assets.Repository` implementations (sqlite-backed ClipsRepository)
	// remain in internal/assets; this alias is the type-level
	// anchor for the new domain package.
	Repository = assets.Repository

	// Filter is the cross-source query predicate honored by
	// internal/assets.Repository.List / Count. YAGNI added in Phase 2
	// PR-1 (used by clips/clip_read.go). The struct's field set is
	// owned by internal/assets until phase 3 absorbs it.
	Filter = assets.Filter

	// Details is the per-asset diagnostic payload: license info,
	// provenance chain, fetch timestamps. Hard alias; YAGNI added in
	// Wave 12 follow-up Phase 2 PR-2 (internal/application/ migration)
	// because internal/application/jobs/assets/service.go stores
	// pipeline outcomes in Asset.Details during job execution.
	Details = assets.Details

	// Service is the asset-management façade — a struct that bundles
	// repositories (clips / locations / processing / versions / store /
	// artifact / delivery) for transactional composition. Hard alias;
	// YAGNI added in Wave 12 follow-up Phase 2 PR-2 because the same
	// file (internal/application/jobs/assets/service.go) constructs
	// it. Will be promoted to an interface in phase 3 when the job
	// runner stops reaching into a concrete façade.
	Service = assets.Service

	// LocationKind is the typed enum behind
	// internal/assets.Locations[].LocationKind (see
	// internal/assets/details.go). Hard alias; YAGNI added in Wave 12
	// follow-up Phase 2 PR-2 because the migrated job-runner path in
	// internal/application/jobs/assets/service.go::convertMediaAsset
	// reads `loc.LocationKind == asset.LocationKindLocal` / `Drive`,
	// where `loc.LocationKind` is a field of this typed enum.
	LocationKind = assets.LocationKind
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

// ── LocationKind constants ───────────────────────────────────────────
//
// Re-declared from internal/assets/location.go because Go const-group
// re-exports aren't possible through type aliases. Values match the
// legacy package; parity is asserted indirectly via go build (any drift
// surfaces as an unresolved-reference error in the migrated callers).
// YAGNI added in Wave 12 follow-up Phase 2 PR-2.

const (
	LocationKindDrive = assets.LocationKindDrive
	LocationKindLocal = assets.LocationKindLocal
)
