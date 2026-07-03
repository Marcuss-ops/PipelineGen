// Package verification owns the on-disk integrity gate that runs BETWEEN the
// staged resolver (Azione 3) and the JobFinalizer CompleteWithArtifacts
// single-TX atomic write (Azione 6). The package declares the typed sentinels
// for the 2 drift detections: SHA-256 recompute miss, and os.Stat size miss.
//
// Godlike/07 typed-error contract: both sentinels are `errors.New(...)` only;
// callers MUST reach them via `errors.Is(err, ErrStaged...)` (NOT direct
// pointer-equality or string-match). Implementations in verified.go wrap them
// via `fmt.Errorf("...: %w", sentinel)` chains so that:
//
//   - errors.Is(err, ErrStagedChecksumMismatch) → true (when SHA drift detected)
//   - errors.Is(err, ErrStagedSizeMismatch)      → true (when size drift detected)
//
// The cutover pipe (Azione 7 Tools integration) consumes them via errors.Is
// for retry classification: a checksum/size drift is RETRYABLE (the
// Creator may re-stage the file from source) but DEAD-LETTERABLE after N
// retries (the on-disk filesystem is corrupt or staging writer is broken).
package verification

import "errors"

// ErrStagedChecksumMismatch is returned when the on-disk SHA-256 recompute
// (via internal/infrastructure/files.HashFile path sha256.New) returns a hex
// digest that does NOT match the StagedArtifact.SHA256 claim in the stage
// metadata. Resolution: re-stage the file from source OR fail-closed if the
// Creator-side has no source caching.
var ErrStagedChecksumMismatch = errors.New(
	"verification: staged SHA-256 does not match on-disk recomputed SHA-256",
)

// ErrStagedSizeMismatch is returned when the on-disk os.Stat size differs
// from the StagedArtifact.SizeBytes claim in the stage metadata.
//
// Important: SIZE-check runs BEFORE the hash recompute (cheap-first ordering):
// a stat() syscalls is O(metadata) while SHA-256 recompute is O(bytes_processed).
// Cutover pipe can short-circuit on size mismatch without paying the hash cost.
var ErrStagedSizeMismatch = errors.New(
	"verification: staged SizeBytes does not match os.Stat size",
)
