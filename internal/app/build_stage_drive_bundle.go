// Package app — build_stage_drive_bundle.go
//
// Canonical typed sentinel for the Stage→Drive forward-pointer components.
//
// The StageDriveBundle struct and BuildStageDriveBundle func were designed
// as a canonical composition surface for the COMPLETION-CUTOVER-P0 wave.
// They were removed (July 2026) per YAGNI: zero external callers existed
// in production code; the forward-pointer components (ArtifactPreparation +
// WithArtifactsService) have independent composition paths via their own
// build_bundles_* files.
//
// The sentinel below is retained for backward compatibility with
// lifecycle_capability_disabled_test.go (sentinel-identity probe).
package app

import "errors"

// ErrStageDriveInsufficientForCompletion is the typed sentinel for the
// forward-pointer components (P0-COMPL-4-PUBLISH-DEDUPE + P0-COMPL-5-
// SINGLE-BACKBONE). The companion StageDriveBundle struct and typed-nil-
// safe accessors were removed per YAGNI (zero production callers).
//
// P0-COMPL-4-PUBLISH-DEDUPE shipped (commit ca73476d, 2026-07-03).
// P0-COMPL-5-SINGLE-BACKBONE deadline: 2026-08-15.
var ErrStageDriveInsufficientForCompletion = errors.New(
	"stage-drive bundle: forward-pointer components (ArtifactPreparation + WithArtifactsService) require P0-COMPL-5-SINGLE-BACKBONE (deadline 2026-08-15) to ship",
)
