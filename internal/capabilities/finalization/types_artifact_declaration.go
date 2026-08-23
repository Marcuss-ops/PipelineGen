// types/types_artifact_declaration.go — one canonical type per godlike/06 SSOT.
// Code-motion split from internal/domain/finalization/types.go (674 LOC, LONG-FILES-DECOMPOSITION-2026-07-06 P0 critical band slice, 2026-07-06).
package finalization

// ArtifactDeclaration is the worker's registry of an artifact it
// INTENDED to handle during the job, marked with its Requirement.
// The JobFinalizer cross-references OptionalDeclarations against
// the request's actually-published `Artifacts` list to build
// FinalizationResult.OptionalArtifactReport.
//
// For required artifacts, the worker publishes the artifact directly
// into `request.Artifacts` — a declaration is OPTIONAL (the cross-ref
// path is a fallback). For optional artifacts, an explicit declaration
// lets the worker pre-signal the outcome (Finalized / Missing / Failed)
// without depending on cross-referencing inference.
//
// Either way, every optional artifact's outcome is recorded in the
// audit sidecar so operators have an at-a-glance count of
// success/missing/failed without correlating against separate error
// logs.
type ArtifactDeclaration struct {
	// ArtifactID is the canonical artifact identifier the worker
	// intends to handle. Matches by ArtifactID against the request's
	// Artifacts list.
	ArtifactID string `json:"artifact_id"`

	// Kind is the high-level category of the artifact.
	Kind ArtifactKind `json:"kind"`

	// Filename is the optional canonical publication filename.
	Filename string `json:"filename,omitempty"`

	// MIMEType is the optional IANA media type.
	MIMEType string `json:"mime_type,omitempty"`

	// IdempotencyKey is the deterministic key the ArtifactPreparation
	// service would use for publication. Carried for audit; the
	// JobFinalizer does not enforce uniqueness across declarations.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// Requirement classifies the declaration. MUST equal
	// ArtifactRequirementOptional — declaring a required artifact
	// in OptionalDeclarations is a programming error
	// (ErrOptionalDeclarationHasRequiredRequirement).
	Requirement ArtifactRequirement `json:"requirement"`

	// Status is the worker's pre-publish intent for this artifact.
	//
	//   - OptionalArtifactStatusFinalized: the worker produced the
	//     artifact and includes it in `request.Artifacts`. The
	//     JobFinalizer MUST verify the ArtifactID appears in
	//     `request.Artifacts` — when missing, returns
	//     ErrOptionalArtifactFinalizedMismatch.
	//
	//   - OptionalArtifactStatusMissing: the worker did NOT produce
	//     the artifact (silent — no error). Recorded as Missing.
	//
	//   - OptionalArtifactStatusFailed: the worker attempted to
	//     produce but ArtifactPreparation returned an error. Recorded
	//     as Failed with Err carrying the underlying typed sentinel
	//     (preserves errors.Is/As traversal).
	Status OptionalArtifactStatus `json:"status"`

	// Err is the underlying failure carrier when Status ==
	// OptionalArtifactStatusFailed. Nil otherwise.
	Err error `json:"-"`
}
