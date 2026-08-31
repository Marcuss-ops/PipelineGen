// types/types_optional_artifact_record.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/capabilities/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

import "time"

// OptionalArtifactRecord is the per-optional-artifact audit sidecar
// entry on FinalizationResult. JobFinalizer.CompleteWithArtifacts
// populates one record per optional artifact (regardless of outcome)
// and persists a JSON-encoded copy of the entire report inside the
// same SQLite transaction under the `optional_artifact_report` job_events
// row (separate from the `job_completed` event).
//
// Why a sidecar?
//
//	job_completed alone tells operators "this job succeeded" but
//	says nothing about why some optional artifacts are missing or
//	failed (success today can paper over hidden degradation: an
//	AI image that never generated, a metadata.json that never
//	uploaded, etc.). The sidecar carries the EXACT per-optional
//	outcome so dashboards can surface the degradation to operators
//	without parsing text logs.
type OptionalArtifactRecord struct {
	// ArtifactID is the canonical identifier of the optional artifact.
	ArtifactID string `json:"artifact_id"`

	// Kind is the artifact category (preserved from the declaration /
	// artifact for dashboards that aggregate by Kind).
	Kind ArtifactKind `json:"kind"`

	// Requirement is ALWAYS ArtifactRequirementOptional for the
	// records on this struct; included for JSON schema symmetry with
	// ArtifactDeclaration so the audit row reads without case-split.
	Requirement ArtifactRequirement `json:"requirement"`

	// Status is the per-artifact outcome (Finalized / Missing / Failed).
	Status OptionalArtifactStatus `json:"status"`

	// Err is the underlying failure carrier when Status == Failed.
	// In-memory-runtime only (json:"-") — used by callers during the
	// same process for errors.Is / errors.As traversal. The
	// JSON-persistent carrier is ErrorMessage below.
	Err error `json:"-"`

	// ErrorMessage is the JSON-persistent string carrier for the
	// underlying failure when Status == Failed. Populated from
	// Err.Error() at JobFinalizer.CompleteWithArtifacts time so the
	// `optional_artifact_report` job_events row can carry the
	// failure reason verbatim through standard JSON marshaling.
	// (Err has json:"-" so it is otherwise stripped during the
	// job_events data_json marshal.)
	ErrorMessage string `json:"error_message,omitempty"`

	// Filename is the canonical publication filename when known —
	// overwritten with the resolved value from the matched
	// PublishedArtifact when Status == Finalized, falling back to
	// the declaration's intended filename otherwise.
	Filename string `json:"filename,omitempty"`

	// IdempotencyKey is the deterministic key when known —
	// overwritten with the resolved value from the matched
	// PublishedArtifact when Status == Finalized, falling back to
	// the declaration's intended key otherwise.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// RecordedAt is the UTC timestamp the record was assembled by
	// the JobFinalizer. Useful for sequencing optional outcomes in
	// dashboards when the worker took Time-on-step.
	RecordedAt time.Time `json:"recorded_at"`
}
