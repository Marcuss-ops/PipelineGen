// types/types_finalization_request.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/capabilities/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// FinalizationRequest is the input to JobFinalizer.CompleteWithArtifacts.
// It carries the lease (for ownership verification), the result
// manifest, all published artifacts, optional artifact declarations,
// and any outbox events to emit atomically.
//
// P1.2 (July 2026): the request carries two artefact-side surfaces:
//
//   - `Artifacts` — the actually-published artifacts (locations
//     returned by ArtifactPreparation). Required artifacts MUST
//     appear here, or CompleteWithArtifacts returns
//     ErrRequiredArtifactMissing.
//   - `OptionalDeclarations` — the worker's per-optional-artifact
//     intent (Finalized/Missing/Failed). Optional: the cross-ref
//     can infer optional outcomes by filtering `Artifacts` against
//     `Requirement == ArtifactRequirementOptional`, but explicit
//     declarations let the worker pre-signal Missing/Failed without
//     running an unsuccessful publish.
type FinalizationRequest struct {
	// Lease is the job lease held by the calling worker. The finalizer
	// validates that the lease is still valid and belongs to the
	// calling worker before committing.
	Lease Lease `json:"lease"`

	// Result is the job's result manifest.
	Result ResultManifest `json:"result"`

	// Artifacts is the list of published artifacts to register.
	Artifacts []PublishedArtifact `json:"artifacts"`

	// OptionalDeclarations (P1.2) is the worker's per-optional-artifact
	// intent. Each entry classifies a declared optional artifact as
	// Finalized, Missing, or Failed. The JobFinalizer cross-references
	// against Artifacts and persists the resolved audit report both
	// on FinalizationResult.OptionalArtifactReport (in-memory) and
	// inside the `optional_artifact_report` job_events row (durable).
	//
	// May be empty — the JobFinalizer's fallback path infers optional
	// outcomes from Artifacts (filter Requirement == Optional →
	// Finalized record). Explicit declarations take precedence when
	// present.
	OptionalDeclarations []ArtifactDeclaration `json:"optional_declarations,omitempty"`

	// Events is the list of outbox events to emit atomically with the
	// job completion.
	Events []OutboxEvent `json:"events,omitempty"`
}
