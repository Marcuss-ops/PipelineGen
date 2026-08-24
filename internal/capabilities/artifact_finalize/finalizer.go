// Package artifact_finalize — finalizer.go (FASE 3 / Push 3.1d, July 2026).
//
// Application-layer Finalizer for the FASE 3 Spina Dorsale saga's
// Finalize step. Closes the publication saga after the publisher
// worker pool has drained the outbox event for a job: the Finalizer
// scans the canonical artifact_stages rows for a given job_id (via
// Repository.ListByJob) and flips every PUBLISHED row to SUCCEEDED
// via the fenced Repository.MarkSucceeded primitive — gated on
// "all REQUIRED artifacts for the job are PUBLISHED" (failing
// closed with the canonical artifact.ErrArtifactRequiredMissing
// sentinel otherwise, FASE 3 (b) "richiesto mancante ⇒ errore,
// mai warning").
//
// godlike/06 SSOT: this port + the FinalizeResult type are the SOLE
// canonical application-layer surface for FASE 3 finalization.
// Compilation anchors:
//   - `var _ Finalizer = (*finalizerService)(nil)` is in service.go
//     of this package (concrete anchor).
//   - `var _ artifact.Repository = (*Repository)(nil)` is in
//     internal/platform/sqlite/artifact_stages/repository.go
//     (Repository concrete anchor).
//
// godlike/07 fail-closed:
//   - Job-level readiness: every REQUIRED artifact must be in
//     PUBLISHED state. A REQUIRED artifact in STAGED (publisher
//     worker hasn't uploaded yet) or FAILED_PERMANENT (publisher
//     worker exhausted retries) trips ErrArtifactRequiredMissing
//     (the canonical sentinel — wrapped with job_id + the FIRST
//     missing artifact id for operator-audit logs; the rest of the
//     missing ids join the wrap as a delimited list).
//   - Per-stage fenced MarkSucceeded: a row already in a terminal
//     state (SUCCEEDED or FAILED_PERMANENT) is rejected by the
//     underlying Repository.ErrTerminalStateRejection. The
//     finalizer swallows this SPECIFIC sentinel (idempotent case
//     for a duplicate finalize call) but propagates any other
//     infrastructural error (DB timeout, ctx cancellation, etc.).
//   - Empty job: a job_id with zero artifact_stages is a no-op
//     (zero counts, no error). The finalizer does NOT surface an
//     error because "no rows" is a non-actionable condition for
//     the constructor-only pipeline. A debug log line makes the
//     trace visible without consuming a typed sentinel.
//
// Pattern 0 (AGENTS.md): the port is application-layer so the
// publisher worker pool (forward-pointer, Push 3.1c) and admin
// tools can declare compile-time dependencies without importing
// infrastructure.
package artifact_finalize

import (
	"context"
)

// Finalizer is the FASE 3 application-layer finalizer port.
// Concrete implementations (Push 3.1d: finalizerService) satisfy
// this interface (compile-time assertion `var _ Finalizer =
// (*finalizerService)(nil)` in service.go of this package).
//
// godlike/06 SSOT: this interface is the SOLE canonical surface
// for FASE 3 finalization. The concrete is built at the
// composition root (internal/app/build_bundles_artifact_finalize.go).
type Finalizer interface {
	// Finalize performs the FASE 3 (d) "verify all PUBLISHED →
	// SUCCEEDED" check for the given job_id.
	//
	// Behaviour:
	//   - Job-level readiness: every REQUIRED artifact for the
	//     job MUST be in PUBLISHED state. Any required artifact
	//     that is missing (no row) or in STAGED / FAILED_PERMANENT
	//     state trips ErrArtifactRequiredMissing (the wrap carries
	//     jobID + the FIRST offending artifact id; the rest of
	//     the missing ids are appended as a comma-delimited
	//     list so operators can grep logs by the full set).
	//   - Scope of MarkSucceeded: every PUBLISHED row for the
	//     job (REQUIRED + OPTIONAL) is flipped to SUCCEEDED.
	//     Optional artifacts that are still in STAGED (publisher
	//     worker has not finished) are left alone; the finalizer
	//     does NOT block on them (forward-compat with jobs that
	//     have optional enrichment artifacts).
	//   - Fenced concurrency: if the same Finalize(jobID) is
	//     invoked concurrently (e.g. the publisher worker pool
	//     triggers an invocation after each per-artifact
	//     MarkPublished + an admin tool triggers a manual
	//     invocation), the underlying MarkSucceeded's fenced
	//     CAS rejects already-terminal rows with
	//     ErrTerminalStateRejection. The finalizer swallows this
	//     SPECIFIC sentinel (idempotent re-flip) and continues
	//     with the remaining rows. Other infrastructural errors
	//     (DB timeout, ctx cancelled, etc.) abort and are
	//     returned to the caller.
	//   - Empty job: a job_id with zero artifact_stages is a
	//     no-op (zero counts, no error). The FinalizeResult
	//     returned has Scanned=0 and every counter=0.
	//
	// Returns FinalizeResult with telemetry counters + nil on
	// success. Returns a typed error wrapping
	// ErrArtifactRequiredMissing on readiness failure.
	Finalize(ctx context.Context, jobID string) (*FinalizeResult, error)
}

// FinalizeResult is the canonical post-Finalize telemetry
// envelope. All counters are non-negative; the sum of
// (RequiredTotal + optional telemetry rows) == Scanned.
//
// Field semantics:
//   - JobID: the input to Finalize (echoed back for caller
//     correlation in logs / dashboards).
//   - Scanned: total artifact_stages rows for the job, across
//     all requirements and states.
//   - RequiredTotal: count of rows with Requirement=REQUIRED.
//   - FlippedToSucceeded: count of MarkSucceeded calls that
//     transitioned a row from PUBLISHED → SUCCEEDED in THIS
//     invocation. Excludes rows that were already in SUCCEEDED
//     on entry (idempotent re-finalize case).
//   - OptionalFailed: count of OPTIONAL rows in FAILED_PERMANENT
//     (informational; not blocking).
//   - OptionalStillStaged: count of OPTIONAL rows still in
//     STAGED (publisher worker has not finished them yet;
//     informational; not blocking).
type FinalizeResult struct {
	JobID               string `json:"job_id"`
	Scanned             int    `json:"scanned"`
	RequiredTotal       int    `json:"required_total"`
	FlippedToSucceeded  int    `json:"flipped_to_succeeded"`
	OptionalFailed      int    `json:"optional_failed"`
	OptionalStillStaged int    `json:"optional_still_staged"`
}

// godlike/06 SSOT: this file owns only the Finalizer interface
// and the FinalizeResult struct. The artifact.* types (State,
// Requirement, etc.) are referenced by the finalizerService
// concrete in service.go of this package — the port surface
// here is intentionally decoupled from the domain types so the
// FinalizeResult shape stays a pure telemetry envelope (no
// leak of the domain types into the public, pre-domain
// boundary of the application package).
