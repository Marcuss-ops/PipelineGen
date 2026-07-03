// Package staged — errors.go (Azione 1, July 2026,
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave).
//
// godlike/07 typed-error contract: every failure path in the staged
// resolver is surfaced via one of the typed sentinels below. Callers
// reach them via errors.Is; chains are preserved through fmt.Errorf %w
// (1x depth; deeper wraps require errors.As typed envelopes per
// godlike/07 §"Migration sequence"). Each sentinel names the SPECIFIC
// failure modality so a Retry-via-admin or operator dashboard can
// disambiguate routes without substring matching (matches the
// canonical pattern documented in AGENTS.md).
package staged

import "errors"

// ErrStagedArtifactMissing is returned when the DB asset_index has no
// row for the requested artifactID, OR when the row's local_path is
// empty (corrupted row). This is the canonical "no such record" path;
// per godlike/07 §"No fake availability" the resolver MUST throw the
// typed sentinel rather than return a zero-value envelope.
//
// Diagnostic hint: callers seeing this error should re-stage via the
// canonical staging pipeline (SourceStager.StageSource — Step 9/12
// wave), not "retry Resolve" the same artifactID.
var ErrStagedArtifactMissing = errors.New("staged: artifact missing from asset_index lookup")

// ErrStagedArtifactNotOnDisk is returned when the DB row exists (with
// a non-empty local_path column) but `os.Stat` reports the file as
// absent. This is a godlike/07 tripwire: the row says the file should
// exist, but the disk says it does not — never substitute a
// zero-value Bytes envelope to "satisfy" the call.
//
// Diagnostic hint: a disk-side cleanup race or a Drive-side migration
// that left a stale asset_index row. The canonical recovery is to
// delete the row + re-stage the asset via SourceStager.
var ErrStagedArtifactNotOnDisk = errors.New("staged: artifact row exists but local file is gone")

// ErrStagedArtifactNotConfigured is returned by NewResolver when the
// lookupFn dependency is nil, AND by *Resolver.Resolve when called on
// a nil receiver. Composition-time fail-closed posture mirrors the
// P0-Commit 7 NewService constructor in the completion package:
// half-wired surfaces surface the failure at startup, not at the first
// Resolve call.
var ErrStagedArtifactNotConfigured = errors.New("staged: resolver not configured (nil lookupFn)")
