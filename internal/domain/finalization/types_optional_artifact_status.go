// types/types_optional_artifact_status.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// OptionalArtifactStatus classifies the outcome of an optional
// artifact's per-request attempt. The JobFinalizer records one
// OptionalArtifactRecord per optional artifact in
// FinalizationResult.OptionalArtifactReport and persists a durable
// copy in the `optional_artifact_report` job_events row.
type OptionalArtifactStatus int

const (
	// OptionalArtifactStatusUnknown is the zero value. The
	// JobFinalizer MUST never produce a record in this state.
	OptionalArtifactStatusUnknown OptionalArtifactStatus = iota

	// OptionalArtifactStatusFinalized means the artifact was
	// successfully published and is present in the request's
	// Artifacts list (matched by ArtifactID).
	OptionalArtifactStatusFinalized

	// OptionalArtifactStatusMissing means the worker declared the
	// artifact (in OptionalDeclarations) but did NOT publish it.
	// No underlying error — the worker intentionally skipped the
	// artifact (e.g. condition that made the artifact irrelevant).
	OptionalArtifactStatusMissing

	// OptionalArtifactStatusFailed means the worker attempted to
	// publish but ArtifactPreparation returned an error. The underlying
	// error is preserved in OptionalArtifactRecord.Err so an operator
	// can investigate WITHOUT needing to correlate against a separate
	// error log.
	OptionalArtifactStatusFailed
)

// Valid returns true if s is one of the three canonical values
// (Finalized, Missing, Failed). The zero value (Unknown) is NOT valid.
func (s OptionalArtifactStatus) Valid() bool {
	switch s {
	case OptionalArtifactStatusFinalized,
		OptionalArtifactStatusMissing,
		OptionalArtifactStatusFailed:
		return true
	}
	return false
}

// String returns the human-readable label for logs / job_events.
func (s OptionalArtifactStatus) String() string {
	switch s {
	case OptionalArtifactStatusFinalized:
		return "FINALIZED"
	case OptionalArtifactStatusMissing:
		return "MISSING"
	case OptionalArtifactStatusFailed:
		return "FAILED"
	}
	return "UNKNOWN"
}
