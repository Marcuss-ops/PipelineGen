// types/types_finalization_result.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

import "time"

// FinalizationResult is returned by JobFinalizer.CompleteWithArtifacts
// on success. It carries the artifact references for downstream
// consumers (workflow coordinator, dashboards) AND, in P1.2, the
// optional-artifact audit sidecar.
type FinalizationResult struct {
	// JobID is the canonical job identifier.
	JobID string `json:"job_id"`

	// Status is the terminal job status (SUCCEEDED).
	Status string `json:"status"`

	// CompletedAt is the UTC timestamp of transaction commit.
	CompletedAt time.Time `json:"completed_at"`

	// ArtifactRefs is the list of finalised artifact references.
	ArtifactRefs []ArtifactRef `json:"artifact_refs"`

	// OptionalArtifactReport (P1.2) is the audit sidecar for every
	// optional artifact the job declared (or every artifact with
	// Requirement == Optional from the cross-reference fallback).
	// One record per optional artifact, regardless of outcome.
	// The JobFinalizer also persists a JSON-encoded copy of this
	// slice into the `optional_artifact_report` job_events row
	// inside the SAME transaction (next to job_completed).
	OptionalArtifactReport []OptionalArtifactRecord `json:"optional_artifact_report,omitempty"`
}
