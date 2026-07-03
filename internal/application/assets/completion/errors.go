// Package completion owns the post-Prepare-and-Publish surface for the
// CUTOVER-COMPLETE-WITH-ARTIFACTS wave. The package sits between the
// on-disk integrity gate (internal/application/assets/verification,
// Azione 4) and the JobFinalizer CompleteWithArtifacts single-TX atomic
// write (Azione 6). Its job:
//
//   - Drive the Publish seam (ArtifactPublisherAdapter.Publish) for each
//     VerifiedArtifact → AssetLocation mapping
//   - Surface typed sentinels for the 3 distinct failure modes so the
//     cutover pipe (Azione 7 Tools integration) can route retry vs.
//     fail-closed via errors.Is probing (godlike/07 typed-error contract)
//   - Maintain idempotency-key byte-stability on retries via the
//     canonical ArtifactIdempotencyKey(jobID, artifactID, sha256Hex)
//     helper at internal/domain/remote/idempotency.go (P0.7 convention)
//
// Godlike/07 typed-error contract — all sentinels are `errors.New(...)` and
// reachable via `errors.Is(err, ErrXxx)` after `fmt.Errorf("...: %w", ErrXxx)`
// wrapping in publish_verified.go. NO string-match fallbacks, NO direct
// pointer-equality (the wrap probes must survive across functions).
package completion

import "errors"

// ErrAlreadyPublished is the typed sentinel for the idempotency short-circuit:
// when the (jobID, artifactID, sha256Hex) triple is already recorded as
// published in the IdempotencyBookkeeper, PublishVerifiedArtifacts DOES NOT
// re-run Prepare+Publish and DOES NOT re-upload to Drive. The existing
// PublishedArtifact record is returned (idempotent replay).
//
// The sentinel name is descriptive of the SHORT-CIRCUIT path; the wrapped
// error chain ALSO carries the existing *PublishedArtifact envelope so the
// caller can resolve the prior publication status via errors.As (forward-
// pointer to bookkeeper.LookupPublished for the resolution). The caller
// distinguishes "missing" from "already-published" via errors.Is patterns:
//
//	if errors.Is(err, ErrAlreadyPublished) {
//	    var existing *finalization.PublishedArtifact
//	    if errors.As(err, &existing) {
//	        // Idempotent replay: publisher logs the historical record
//	    }
//	}
var ErrAlreadyPublished = errors.New(
	"completion: artifact already published (idempotency-key collision, no re-upload)",
)

// ErrFinalChecksumMismatch is the post-publish fail-closed invariant: after
// ArtifactPublisherAdapter.Publish has succeeded (Drive file exists), the
// verifier re-reads the on-disk local file and recomputes SHA-256. If the
// recomputed hash differs from the staged claim (finalization.VerifiedArtifact
// .SHA256), this typed sentinel surfaces — the published file is NOT the
// staged content, signalling EITHER (a) Drive-write corruption, (b) wrong
// source file at the staging seam, or (c) race condition with concurrent
// re-staging on the Sender.
//
// Per godlike/07 no-fake-availability: this check fires AFTER Publish returns
// success (so a transient Drive error during upload would have surfaced as a
// different typed error, NOT this). FinalChecksum mismatch is the
// end-of-pipeline invariant and signals "the artifact content on disk is not
// the artifact content the surrounding code assumed" — fail-closed by design.
var ErrFinalChecksumMismatch = errors.New(
	"completion: post-publish final checksum mismatch (staged SHA != on-disk recompute)",
)

// ErrPublishInvalidArtifact is the typed sentinel returned when the input
// artifact slice contains a nil pointer. Per godlike/07: defensive nil-check
// is a typed error, not a panic. The wrap chain preserves the index of the
// nil entry for operator diagnosis.
var ErrPublishInvalidArtifact = errors.New(
	"completion: invalid nil artifact pointer in Published input slice",
)

// ErrPublishEmptySlice is the typed sentinel returned when the input artifact
// slice is empty. No-op publishes are not a valid use-case for this function;
// an empty slice is most likely a wiring bug at the cutover-pipe caller and
// should be caught early via typed-error surfacing rather than silent no-op.
var ErrPublishEmptySlice = errors.New(
	"completion: empty artifact slice (no-op publish is invalid)",
)
