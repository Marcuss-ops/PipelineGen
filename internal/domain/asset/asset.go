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
//   - Phase 2 PR-3 (this PR): internal/media/ migrated (17 files).
//     Aliases extended YAGNI: +9 entries — {Location,
//     LocationRepository, ProcessingRepository, StageUpload,
//     StageDownload, StatusRunning, StatusCompleted, StatusFailed,
//     ErrNotFound}. Largest importer subset of the 73-importer
//     Verdetto. Recon lifted the user's flagged Frammento-(c) deferral
//     for deletion.go + ingest/adapter_clip.go — neither uses
//     assets.Artifact* (the actual Frammento (c) targets), so alias
//     migration is lossless.
//   - Phase 2 PR-4: internal/infrastructure/ — final PR of phase 2.
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

	// Location is the per-asset filesystem/GDrive fingerprint: where
	// the asset lives on LocalPath or DriveLink, with optional hashes.
	// Hard alias; YAGNI added in Wave 12 follow-up Phase 2 PR-3
	// because internal/media/ingest/adapter_clip.go writes location
	// rows during the ingest path's Upsert.
	Location = assets.Location

	// LocationRepository is the persistence contract for asset
	// location rows. Hard alias; same call-site as Location.
	LocationRepository = assets.LocationRepository

	// ProcessingRepository tracks per-asset processing steps (upload,
	// transcode, etc.). Hard alias; YAGNI added in PR-3 because
	// adapter_clip.go writes Start/Complete/Fail events through this
	// interface as part of the ingest path's status ledger.
	ProcessingRepository = assets.ProcessingRepository
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

// ── Processing constants ──────────────────────────────────────────────
//
// Re-declared from internal/assets/processing_types.go because Go
// const-group re-exports aren't possible through type aliases. Values
// track the legacy package; drift will surface as undefined references
// during build. YAGNI added in Wave 12 follow-up Phase 2 PR-3 because
// internal/media/ingest/adapter_clip.go records upload/transcode events
// via Stage* and Status* when ingesting MediaRecord rows.

const (
	StageUpload   = assets.StageUpload
	StageDownload = assets.StageDownload

	StatusRunning   = assets.StatusRunning
	StatusCompleted = assets.StatusCompleted
	StatusFailed    = assets.StatusFailed
)

// ErrNotFound is the canonical "not found" error returned by
// *assets.Repository.Get(...). Sentinel-error pattern preserved by
// sharing the underlying *errors.errorString pointer with the assets
// package, so callers may compare via == directly. YAGNI added in
// Wave 12 follow-up Phase 2 PR-3 because adapter_clip.go's Get
// path matches `err == assets.ErrNotFound` to surface a nil record
// rather than propagating the error.
var ErrNotFound = assets.ErrNotFound
