// types/types_artifact_requirement.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/capabilities/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// ArtifactRequirement classifies whether an artifact blocks job
// completion or is non-blocking (best-effort, recorded but does not
// fail the job). Replaces the legacy `Required bool` field on
// VerifiedArtifact and PublishedArtifact (P1.2 cutover).
//
// godlike/07 typed-error contract: the zero value
// (ArtifactRequirementInvalid) is explicitly rejected at validation
// time so a caller that forgot to set the field fail-closes loudly
// (mirrors PublishAction's empty-string zero-value handling).
//
// Canonical values:
//
//   - ArtifactRequirementRequired — blocks job completion. Missing
//     at completion → ErrRequiredArtifactMissing.
//   - ArtifactRequirementOptional — non-blocking. JobFinalizer records
//     the artifact in OptionalArtifactRecord (per-optional audit
//     sidecar) regardless of outcome.
type ArtifactRequirement int

const (
	// ArtifactRequirementInvalid is the zero value. Exists so the
	// field is explicitly NOT-Required and NOT-Optional until the
	// caller sets it; rejected by validateRequest so a default-zero
	// artifact cannot silently pass the gate as "Optional".
	ArtifactRequirementInvalid ArtifactRequirement = iota

	// ArtifactRequirementRequired marks an artifact whose absence
	// at completion time fails the request with ErrRequiredArtifactMissing.
	ArtifactRequirementRequired

	// ArtifactRequirementOptional marks a non-blocking artifact.
	// JS completion succeeds even when the optional artifact is
	// missing or failed; the per-optional outcome is recorded in
	// FinalizationResult.OptionalArtifactReport for audit.
	ArtifactRequirementOptional
)

// Valid returns true if r is one of the two canonical values
// (Required or Optional). ArtifactRequirementInvalid (zero value)
// is NOT valid; callers MUST set Requirement explicitly.
func (r ArtifactRequirement) Valid() bool {
	switch r {
	case ArtifactRequirementRequired, ArtifactRequirementOptional:
		return true
	}
	return false
}

// String returns the human-readable label ("REQUIRED" or "OPTIONAL")
// used in structured logs and the job_events audit row. The zero
// value renders as "INVALID" so a wrong-default sentinel surfaces
// loudly at log scrape time.
func (r ArtifactRequirement) String() string {
	switch r {
	case ArtifactRequirementRequired:
		return "REQUIRED"
	case ArtifactRequirementOptional:
		return "OPTIONAL"
	}
	return "INVALID"
}
