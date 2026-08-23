// Package remote — complete_artifacts.go (P1 wave Azione 6, July 2026).
//
// CompleteWithArtifactsRequest / CompleteWithArtifactsResponse is the typed
// envelope the Sender-side atomic CompleteWithArtifacts service
// receives/consumes. Extends the P0 Commit 7 CompleteJob surface with the
// asset-location write side (godlike/06 SSOT: the completion service owns
// the canonical mapping from artifact_id (content-hash ID) to asset_id
// (catalog ID) + asset_locations row, all in a single SQLite TX).
//
// godlike/07 typed-error contract:
//   - ErrCompleteWithArtifactsNotConfigured (nil-receiver / wiring gap)
//   - ErrCompleteWithArtifactsRequestMissingFields (pre-TX fail-fast)
//   - ErrRemoteArtifactLocationMismatch (in-TX round-trip check; the
//     typed counterpart of C7's ErrRemoteArtifactHashMismatch, surfaces
//     when a prior SUCCEEDED state for the same (jobID, assetID) has a
//     DIFFERENT location fingerprint)
//
// Migration validation order (locked; mirrors the C7 spec):
//  1. Receiver nil-guard → ErrCompleteWithArtifactsNotConfigured
//  2. WorkerID / JobID / Attempt / LeaseID / Result / ResultHash /
//     AssetMappings presence → ErrCompleteWithArtifactsRequestMissingFields
//  3. (in TX) lease + attempt row-level CAS → ErrConcurrentLeaseRefutation
//     (reused from C7 — same gate; no drift surface introduced by the
//     new flow)
//  4. (in TX) result-hash round-trip → ErrCompleteJobIdempotencyConflict
//     (reused from C7 — same gate)
//  5. (in TX) artifact-hash round-trip → ErrRemoteArtifactHashMismatch
//     (reused from C7 — same gate; the typed error chain traverses
//     through the WithArtifacts service without modification)
//  6. (in TX) asset-location round-trip → ErrRemoteArtifactLocationMismatch
//     (NEW; surfaces when a prior SUCCEEDED state has a DIFFERENT
//     asset_locations row for the same (jobID, assetID))
package remote

import (
	"errors"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Sentinel errors (godlike/07 typed-error contract) ─────────────

// ErrCompleteWithArtifactsNotConfigured is the typed sentinel for
// nil-receiver / composition-wiring-gap failures on the
// CompleteWithArtifacts path. Mirrors the C7
// ErrCompleteJobNotConfigured but on the artifact-aware variant.
// Callers errors.Is against this sentinel to distinguish a wiring
// bug from a wire-shape bug.
var ErrCompleteWithArtifactsNotConfigured = errors.New("complete with artifacts: not configured")

// ErrCompleteWithArtifactsRequestMissingFields is the pre-TX
// fail-fast gate (mirrors C7's ErrCompleteJobRequestMissingFields
// on the artifact-aware variant). Surfaced in the pre-TX
// Validated() call when one of WorkerID / JobID / Attempt /
// LeaseID / Result / ResultHash / AssetMappings is missing. The
// error message aggregates ALL missing fields in a single
// diagnostic (godlike/07 no-fake-availability).
var ErrCompleteWithArtifactsRequestMissingFields = errors.New("complete with artifacts: required fields missing")

// ErrRemoteArtifactLocationMismatch is the in-TX asset-location
// round-trip check failure (analog of C7's
// ErrRemoteArtifactHashMismatch but on the new location surface).
// Surfaces when a prior SUCCEEDED state for the same
// (jobID, assetID) has a DIFFERENT (location_kind, external_id,
// access_url, download_url, file_hash) tuple — i.e. a downstream
// caller tried to re-complete with the same idempotency-key
// triple but a different remote location, which godlike/07
// no-fake-availability forbids (different location = different
// remote-side state; silent overwrite would corrupt the asset
// catalog).
var ErrRemoteArtifactLocationMismatch = errors.New("complete with artifacts: remote asset location mismatch with previous SUCCEEDED state")

// ── CompleteWithArtifactsRequest (typed envelope) ──────────────

// CompleteWithArtifactsRequest is the canonical Sender-side
// atomic-complete-with-artifacts envelope. Mirrors the C7
// CompleteJobRequest field set (typed field set; no map[string]any;
// explicit godlike/06 one-owner-per-fact contract) PLUS the
// AssetMappings sidecar that maps each published artifact to its
// catalog asset_id.
//
// Design note: the published artifacts themselves are passed as
// the second positional argument to
// Service.CompleteWithArtifacts(ctx, *CompleteWithArtifactsRequest,
// []*PublishedArtifact) — they are NOT inlined into the request
// envelope, mirroring Go's option-pattern ergonomics where
// large/optional data is positional and small/binding data is
// struct-packaged.
type CompleteWithArtifactsRequest struct {
	// WorkerID is the canonical worker_id that owns the lease.
	WorkerID string

	// JobID matches jobs.id (the canonical identifier minted at
	// Enqueue time).
	JobID string

	// Attempt matches jobs.retry_count at completion time.
	Attempt int

	// LeaseID matches the canonical lease_id threaded from
	// ClaimNext -> Start (the lease fencing tuple the worker holds
	// until Complete). The atomic TX gate is `WHERE lease_id = ?`
	// — wrong lease = ErrConcurrentLeaseRefutation.
	LeaseID string

	// Result is the encoded json.RawMessage per the C1/C2 codec
	// contract. Carries the worker's terminal outcome payload
	// (success description + actor IDs + capability-specific
	// metadata).
	Result []byte

	// ResultHash is the canonical SHA-256 hex of Result (computed
	// by the client before submit). Used as the idempotency-key
	// triple with (jobID, attempt) per the UNIQUE INDEX on
	// job_results(job_id, attempt, result_hash).
	ResultHash string

	// AssetMappings maps each published artifact's content-hash
	// ID (ArtifactID) to its catalog asset_id (AssetID). Mandatory
	// non-empty for Azione 6 — every published artifact in the
	// positional `artifacts` argument MUST have a matching
	// AssetMappings entry, OR the pre-TX Validated() gate fails
	// closed. The mapping is used by the Service to derive
	// AssetLocationEntry.AssetID for the in-TX INSERT into
	// asset_locations.
	//
	// godlike/06 SSOT rationale: the artifact_id vs asset_id
	// distinction is non-trivial: ArtifactID is the
	// content-hash-derived identifier stabled by the publisher;
	// AssetID is the catalog-side identifier stabled by the
	// upstream capability (e.g. YouTube channel + segment).
	// PublishedArtifact carries ArtifactID only; the mapping
	// resolution is the caller's contract because the catalog
	// registry lives outside the finalization domain.
	AssetMappings map[string]string
}

// Validated is the pre-TX fail-fast gate (godlike/07
// no-fake-availability). Returns nil if the request is well-formed;
// otherwise returns the aggregated missing-fields diagnostic so
// the operator sees the full picture in one error message.
func (r *CompleteWithArtifactsRequest) Validated() error {
	if r == nil {
		return fmt.Errorf("%w: nil receiver", ErrCompleteWithArtifactsRequestMissingFields)
	}
	var missing []string
	if strings.TrimSpace(r.WorkerID) == "" {
		missing = append(missing, "workerID")
	}
	if strings.TrimSpace(r.JobID) == "" {
		missing = append(missing, "jobID")
	}
	if r.Attempt < 0 {
		missing = append(missing, "attempt (negative)")
	}
	if strings.TrimSpace(r.LeaseID) == "" {
		missing = append(missing, "leaseID")
	}
	if len(r.Result) == 0 || string(r.Result) == "null" {
		missing = append(missing, "result (empty)")
	}
	if strings.TrimSpace(r.ResultHash) == "" {
		missing = append(missing, "resultHash")
	}
	if len(r.AssetMappings) == 0 {
		missing = append(missing, "assetMappings (empty)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w (godlike/07 no-fake-availability): %s",
			ErrCompleteWithArtifactsRequestMissingFields, strings.Join(missing, ", "))
	}
	return nil
}

// ValidateArtifactMappings enforces the invariant that every
// published artifact (caller-side second positional argument) has a
// matching AssetMappings entry. Returns the typed
// ErrRemoteArtifactLocationMismatch for the FIRST missing
// mapping so the operator can correlate the missing entry to the
// artifact list verbatim.
//
// Pre-TX fail-fast gate — godlike/07 no-fake-availability: a
// caller that forgets to wire AssetMappings for an artifact MUST
// NOT silently fall through to the TX path (the in-TX fail would
// be too late to recover from a half-published TX rollback).
func (r *CompleteWithArtifactsRequest) ValidateArtifactMappings(artifactIDs []string) error {
	if r == nil {
		return fmt.Errorf("%w: nil receiver", ErrCompleteWithArtifactsRequestMissingFields)
	}
	for _, id := range artifactIDs {
		if mapped, ok := r.AssetMappings[id]; !ok || strings.TrimSpace(mapped) == "" {
			return fmt.Errorf("%w: artifact %q has no entry in AssetMappings",
				ErrCompleteWithArtifactsRequestMissingFields, id)
		}
	}
	return nil
}

// ── CompleteWithArtifactsResponse (typed envelope) ──────────────

// CompleteWithArtifactsResponse is the canonical
// atomic-complete-with-artifacts response. Mirrors the C7
// CompleteJobResponse field set PLUS the per-artifact
// resolved-location echo (the AssetID that the TX wrote to
// asset_locations, surfaced so the Creator-side caller can
// correlate the response without re-supplying the request
// envelope).
type CompleteWithArtifactsResponse struct {
	// Status is the canonical terminal status of the completed
	// job. Always set to job.StatusSucceeded today; the field
	// reserved for future failure-on-Sender paths (when the
	// asset_locations write succeeds but a downstream projection
	// must retry — defer/forward-pointer).
	Status job.Status

	// JobArtifactIDs is the ordered list of artifact IDs as
	// persisted to job_artifacts(job_id, ...). Mirrors the
	// request's positional `artifacts` slice order so the
	// Creator-side can rely on positional index alignment.
	// Idempotent-on-retry: returns the SAME slice across N
	// calls with the same (jobID, attempt, resultHash) thanks
	// to the C7 ON CONFLICT DO NOTHING preservation.
	JobArtifactIDs []string

	// JobAssetIDs is the parallel ordered list of catalog
	// asset_ids resolved from AssetMappings (request-side).
	// Length MUST equal len(JobArtifactIDs); positional
	// index alignment lets Creator-side correlate jobs and
	// assets at the API surface without re-deriving the
	// mapping.
	JobAssetIDs []string

	// JobID echoes the request's JobID so callers can correlate
	// the response without re-supplying the request envelope.
	JobID string

	// Attempt echoes the request's Attempt for the same reason.
	Attempt int

	// ResultHash echoes the request's ResultHash — surfaced so
	// the Creator-side can log the idempotency-key triple
	// alongside the response for forensics.
	ResultHash string
}
