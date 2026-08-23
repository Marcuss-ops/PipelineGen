// Package finalizer — request_validator.go (PR-GODOBJ-5-FINALIZER split)
//
// Hosts the pre-transaction fail-fast validation surface for the
// JobFinalizer. Two PURE functions move from the pre-split monolithic
// job_finalizer.go into this file:
//
//   - validateRequest — fail-fast lease/result/artifacts/declarations
//     pre-checks BEFORE opening the SQLite transaction. Catches
//     programming errors early and avoids wasted transaction opens.
//
//   - buildOptionalArtifactReport — cross-references OptionalDeclarations
//     against the request's Artifacts list to produce the per-optional
//     audit sidecar (P1.2, July 2026). Returns a typed
//     ErrOptionalArtifactFinalizedMismatch when a declaration promises
//     Finalized but the ArtifactID is missing from Artifacts — a
//     programmer error worth surfacing loudly.
//
// Both functions are PURE over the request struct: no SQL,
// no side-effects, no logger writes. The orchestrator calls them
// outside (validateRequest) or before (buildOptionalArtifactReport)
// the SQLite transaction — never inside.
//
// godlike/06 SSOT: this file is the canonical owner of "what does the
// request shape look like?" + "what does the optional-audit report
// look like for job X?". Callers MUST route through these methods
// — never recompute the validation or cross-reference inline.
package finalizer

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ── Pre-validation ──────────────────────────────────────────────────

// validateRequest performs fail-fast validation before opening a
// transaction. This catches programming errors early and avoids
// wasted transaction opens.
//
// Validation surface (godlike/07 typed-error contract):
//
//   - Lease: empty JobID, expired lease, empty LeaseID, empty WorkerID
//     → wrapped ErrLeaseExpired (sent back as INVALID_LEASE / LEASE_EXPIRED).
//   - Result: empty JobID, mismatched JobID (request vs lease)
//     → INVALID_RESULT / MISMATCHED_JOB_ID.
//   - Artifacts: empty ArtifactID, Requirement == Invalid, empty
//     IdempotencyKey, duplicate ArtifactID → ErrRequiredArtifactMissing
//     / ErrArtifactRequirementInvalid / ErrInvalidIdempotencyKey /
//     ErrDuplicateArtifact.
//   - OptionalDeclarations: Requirement == Invalid;
//     Requirement != Optional; duplicate ArtifactID →
//     ErrArtifactRequirementInvalid / ErrOptionalDeclarationHasRequiredRequirement
//     / ErrDuplicateArtifact.
//   - Aggregate invariant: when Artifacts is non-empty, at least one
//     artifact MUST be Required (no required-sidecar in a non-empty
//     list) → ErrRequiredArtifactMissing.
func (f *Finalizer) validateRequest(req *finalization.FinalizationRequest) error {
	// Lease validation.
	if req.Lease.JobID == "" {
		return finalization.NewFinalizationError(
			"INVALID_LEASE", "lease has empty JobID",
			"", 0, finalization.ErrLeaseExpired,
		)
	}
	if !req.Lease.Valid() {
		return finalization.NewFinalizationError(
			"LEASE_EXPIRED", "lease has expired",
			req.Lease.JobID, req.Lease.Attempt, finalization.ErrLeaseExpired,
		)
	}
	if req.Lease.LeaseID == "" {
		return finalization.NewFinalizationError(
			"INVALID_LEASE", "lease has empty LeaseID",
			req.Lease.JobID, req.Lease.Attempt, finalization.ErrLeaseExpired,
		)
	}
	if req.Lease.WorkerID == "" {
		return finalization.NewFinalizationError(
			"INVALID_LEASE", "lease has empty WorkerID",
			req.Lease.JobID, req.Lease.Attempt, finalization.ErrLeaseExpired,
		)
	}

	// Result manifest validation.
	if req.Result.JobID == "" {
		return finalization.NewFinalizationError(
			"INVALID_RESULT", "result manifest has empty JobID",
			"", 0, nil,
		)
	}
	if req.Result.JobID != req.Lease.JobID {
		return finalization.NewFinalizationError(
			"MISMATCHED_JOB_ID", "result JobID does not match lease JobID",
			req.Result.JobID, req.Lease.Attempt, nil,
		)
	}

	// Artifact validation.
	seen := make(map[string]bool)
	hasRequired := false
	for i, a := range req.Artifacts {
		if a.ArtifactID == "" {
			return finalization.NewFinalizationError(
				"INVALID_ARTIFACT",
				fmt.Sprintf("artifact[%d] has empty ArtifactID", i),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrRequiredArtifactMissing,
			)
		}
		// P1.2 (July 2026): typed Requirement enum. The zero value
		// (ArtifactRequirementInvalid) is explicitly rejected so a
		// forgotten-to-set field fail-closes loudly — mirrors how
		// PublishAction's empty-string zero value is handled.
		if !a.Requirement.Valid() {
			return finalization.NewFinalizationError(
				"INVALID_REQUIREMENT",
				fmt.Sprintf("artifact[%d] (%s) has Requirement=%s — must be Required or Optional", i, a.ArtifactID, a.Requirement),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrArtifactRequirementInvalid,
			)
		}
		if a.IdempotencyKey == "" {
			return finalization.NewFinalizationError(
				"INVALID_IDEMPOTENCY_KEY",
				fmt.Sprintf("artifact[%d] (%s) has empty IdempotencyKey", i, a.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrInvalidIdempotencyKey,
			)
		}
		if seen[a.ArtifactID] {
			return finalization.NewFinalizationError(
				"DUPLICATE_ARTIFACT",
				fmt.Sprintf("artifact[%d] (%s) is a duplicate", i, a.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrDuplicateArtifact,
			)
		}
		seen[a.ArtifactID] = true
		// P1.2 (July 2026): typed Requirement enum replaces the
		// legacy Required bool. hasRequired still tracks the
		// required-set membership for the "at least one required"
		// invariant below.
		if a.Requirement == finalization.ArtifactRequirementRequired {
			hasRequired = true
		}
	}

	// P1.2 (July 2026): OptionalDeclarations is the OPTIONAL sidecar
	// only. We surface three failure classes with distinct typed
	// sentinels so log scrapers and dashboards can attribute the
	// issue without parsing the error message:
	//
	//   (a) Requirement=Invalid (zero value) — fail-fast: caller
	//       forgot to set the field. Mirrors how an artifact literal
	//       with Invalid Requirement is rejected above.
	//   (b) Requirement=Required — fail-fast: caller put a required
	//       artifact on the optional sidecar. Required artifacts
	//       belong on `Artifacts`, not on the declaration sidecar.
	//   (c) Duplicate ArtifactID within declarations — fail-fast:
	//       caller emitted two records for one optional artifact.
	//       Without this check, the cross-reference would produce
	//       two audit rows and surfaces misleading outcome counts.
	seenDecl := make(map[string]bool, len(req.OptionalDeclarations))
	for i, d := range req.OptionalDeclarations {
		if d.Requirement == finalization.ArtifactRequirementInvalid {
			return finalization.NewFinalizationError(
				"DECLARATION_HAS_INVALID_REQUIREMENT",
				fmt.Sprintf("OptionalDeclarations[%d] (%s) has Requirement=INVALID (zero value) — set explicitly to Required or Optional", i, d.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrArtifactRequirementInvalid,
			)
		}
		if d.Requirement != finalization.ArtifactRequirementOptional {
			return finalization.NewFinalizationError(
				"DECLARATION_HAS_REQUIRED_REQUIREMENT",
				fmt.Sprintf("OptionalDeclarations[%d] (%s) has Requirement=%s — required artifacts belong on Artifacts, declarations are the optional sidecar only", i, d.ArtifactID, d.Requirement),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrOptionalDeclarationHasRequiredRequirement,
			)
		}
		if seenDecl[d.ArtifactID] {
			return finalization.NewFinalizationError(
				"DUPLICATE_OPTIONAL_DECLARATION",
				fmt.Sprintf("OptionalDeclarations[%d] (%s) is a duplicate", i, d.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrDuplicateArtifact,
			)
		}
		seenDecl[d.ArtifactID] = true
	}

	// At least one artifact must be required (otherwise the job has
	// nothing substantive to record).
	if len(req.Artifacts) > 0 && !hasRequired {
		return finalization.NewFinalizationError(
			"NO_REQUIRED_ARTIFACTS",
			"all artifacts are optional — at least one required artifact expected",
			req.Result.JobID, req.Lease.Attempt,
			finalization.ErrRequiredArtifactMissing,
		)
	}

	return nil
}

// ── Optional artifact audit report (P1.2) ──────────────────────────

// buildOptionalArtifactReport cross-references OptionalDeclarations
// against the request's Artifacts list to produce the per-optional-
// artifact audit sidecar that's persisted in the `optional_artifact_report`
// job_events row alongside the existing `job_completed` event.
//
// Phase 1 — explicit declarations are AUTHORITATIVE.
//
//   - OptionalArtifactStatusFinalized: the artifact MUST appear in
//     Artifacts (matched by ArtifactID). When missing, returns
//     ErrOptionalArtifactFinalizedMismatch — the worker promised
//     the artifact but it's absent from the publish-side, which is
//     almost certainly a programmer error (the worker dropped the
//     artifact on the way to BuildFinalizationRequest, or set the
//     wrong ArtifactID). Loud-fail is preferred over emitting a
//     misleading Finalized record.
//
//     When present, the canonical Filename and IdempotencyKey are
//     copied from the matching PublishedArtifact (NOT the
//     declaration's hint) — the PublishedArtifact is the
//     authoritative source per godlike/06 SSOT (one canonical
//     owner per fact).
//
//   - OptionalArtifactStatusFailed: typed-data envelope populated
//     verbatim from the declaration. Err is preserved in-memory
//     for runtime errors.Is / errors.As traversal; ErrorMessage
//     carries the string into job_events data_json via json.Marshal
//     (Err has json:"-" so a separate persistent carrier is required).
//
//   - OptionalArtifactStatusMissing: silent absent — no Err,
//     no ErrorMessage. Validates that the worker was loud-and-clear
//     about NOT producing the artifact.
//
// validateRequest already rejects OptionalDeclarations entries
// with Requirement != Optional (ErrOptionalDeclarationHasRequiredRequirement)
// and artifact.Requirement == Invalid (ErrArtifactRequirementInvalid),
// so this method assumes canonical declarations on input.
//
// Phase 2 — inference fallback iterates Artifacts filtered to
// Requirement==Optional and surfaces Finalized records for any not
// already covered by a Phase 1 declaration. Note that the fallback
// CANNOT surface Missing / Failed artifacts (those are only visible
// when the worker emits explicit declarations) — this is by design:
// silent FAIL-flips are a recurring source of hidden degradation;
// explicit declarations are the operator's signal that they WANT
// visibility onto a particular optional slot. The fallback exists
// for backwards-compat with workers that haven't yet migrated to
// the explicit-declaration path.
//
// godlike/06 SSOT: this method is the single canonical owner of
// "what does the optional-artifact audit report look like for
// job X?" — callers MUST NOT compute their own cross-reference
// outside this method.
func (f *Finalizer) buildOptionalArtifactReport(
	req *finalization.FinalizationRequest,
	now time.Time,
) ([]finalization.OptionalArtifactRecord, error) {
	pubByID := make(map[string]finalization.PublishedArtifact, len(req.Artifacts))
	for _, a := range req.Artifacts {
		pubByID[a.ArtifactID] = a
	}

	report := make([]finalization.OptionalArtifactRecord, 0, len(req.OptionalDeclarations)+len(req.Artifacts))
	seen := make(map[string]bool, len(req.OptionalDeclarations))

	// Phase 1 — process explicit declarations (authoritative).
	for _, d := range req.OptionalDeclarations {
		rec := finalization.OptionalArtifactRecord{
			ArtifactID:     d.ArtifactID,
			Kind:           d.Kind,
			Requirement:    finalization.ArtifactRequirementOptional,
			Status:         d.Status,
			Filename:       d.Filename,
			IdempotencyKey: d.IdempotencyKey,
			RecordedAt:     now,
		}
		switch d.Status {
		case finalization.OptionalArtifactStatusFinalized:
			pa, ok := pubByID[d.ArtifactID]
			if !ok {
				return nil, finalization.NewFinalizationError(
					"OPTIONAL_FINALIZED_MISMATCH",
					fmt.Sprintf("OptionalDeclarations[%s] declared Finalized but is missing from Artifacts", d.ArtifactID),
					req.Result.JobID, req.Lease.Attempt,
					finalization.ErrOptionalArtifactFinalizedMismatch,
				)
			}
			// Overwrite Phase 1 guesses with canonical Truth from
			// the cross-match — the declaration may carry a hint
			// but the PublishedArtifact is the authoritative source.
			rec.Filename = pa.Filename
			rec.IdempotencyKey = pa.IdempotencyKey
			rec.Err = nil
		case finalization.OptionalArtifactStatusFailed:
			// Surface the typed-data envelope. Err is preserved
			// in-memory for errors.Is/As; ErrorMessage is the
			// JSON-persistent string carrier.
			rec.Err = d.Err
			if d.Err != nil {
				rec.ErrorMessage = d.Err.Error()
			}
		case finalization.OptionalArtifactStatusMissing:
			// Silent absent — no Err, no ErrorMessage.
		}
		report = append(report, rec)
		seen[d.ArtifactID] = true
	}

	// Phase 2 — inference fallback: Artifacts filtered by
	// Requirement==Optional, dedup against Phase 1 entries.
	for _, a := range req.Artifacts {
		if seen[a.ArtifactID] {
			continue
		}
		if a.Requirement != finalization.ArtifactRequirementOptional {
			continue
		}
		report = append(report, finalization.OptionalArtifactRecord{
			ArtifactID:     a.ArtifactID,
			Kind:           a.Kind,
			Requirement:    finalization.ArtifactRequirementOptional,
			Status:         finalization.OptionalArtifactStatusFinalized,
			Filename:       a.Filename,
			IdempotencyKey: a.IdempotencyKey,
			RecordedAt:     now,
		})
	}

	return report, nil
}
