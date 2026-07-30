// Package jobs — worker_execution_result.go (worker_execution.go
// split, July 2026).
//
// Result finalisation extracted from worker_execution.go. Owns:
//
//  1. func extractStagedArtifacts — handler-result → broker
//     StagedArtifacts JSON conversion with the FASE 1 ordering pin
//     (decode → nil-check → empty-envelope → validate → process).
//     The ordering is regression-locked by 4 tests:
//     TestExtractStagedArtifacts_EmptyArtifactsList,
//     TestExtractStagedArtifacts_DecodeFailure_TypedSentinel,
//     TestExtractStagedArtifacts_ValidateFailure_TypedSentinel,
//     TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing.
//     Any reorder silently lets a malformed manifest reach
//     SUCCEEDED — DO NOT touch the if-cascade byte-for-byte.
//
//  2. func (w *Worker) finalizeJob — the per-job finalisation
//     pipeline that consumes (result, dispatchErr) from the
//     dispatcher and routes through the 4 terminal-state paths:
//     ScheduleRetry (retryable, RetryCount < MaxRetries),
//     Fail + DeadLetter (exhausted retries),
//     CompleteWithArtifacts (artifact-producing, gated by the
//     typed CompletionPort per PR-WORKER-RUNNER-INPROCESS-MIGRATION),
//     Complete (legacy non-artifact jobs). Lease-loss handling
//     and the typed-error contracts godlike/07 fail-closed
//     live here.
//
// CRITICAL: this file's finalizeJob receives the AGENTS.md
// allowlisted `context.WithTimeout(context.Background(),
// 30*time.Second)` ctx from worker_execution.go::runJob's
// envelope. DO NOT replace it with the wrapped jobCtx — that
// would lose outcome persistence when jobCtx is cancelled by
// timeout or worker Stop.
//
// Mechanical split from worker_execution.go. Zero behavior
// change. The pre-split PR7 invariant — finalizationCtx is
// detached from jobCtx so the DB final-state write survives
// jobCtx cancellation — is preserved by the runJob envelope
// passing the already-constructed finalizationCtx into
// finalizeJob as the ctx parameter; finalizeJob does NOT
// reconstruct it.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
	"go.uber.org/zap"
)

// extractStagedArtifacts reads the __artifact_manifest from the handler
// result map and converts it to the JSON wire format consumed by
// CompleteWithArtifactsCommand.StagedArtifacts.
//
// FASE 1 (close-out, July 2026) — typed-error contract (audit 2026-07-03
// P0 #4 closure + FASE 1 spec closure):
//
// The function returns (json.RawMessage, error) so every manifest-shape
// failure surfaces through a typed sentinel — NO silent `[]` fallback.
// The caller (finalizeJob) fails the job on any typed error so an
// artifact-producing job whose manifest cannot be honoured can NEVER
// reach SUCCEEDED. Three typed-sentinel modes (godlike/06 SSOT):
//
//  1. job.ErrArtifactManifestMissing — manifest key absent
//     (no __artifact_manifest in handler result). Per FASE 1 spec
//     "il manifest è assente, ... bloccare ... la transizione a
//     SUCCEEDED dei job type ProducesArtifacts=true".
//
//  2. job.ErrArtifactManifestInvalid — manifest cannot be decoded
//     (JSON error, unexpected type) OR fails shape validation
//     (empty schema_version, zero artifacts, empty id/kind,
//     path-set-but-filename-empty, etc). The wrapped error chain
//     carries the sub-mode in the message.
//
//  3. finalization.ErrRequiredArtifactMissing — required artifact has
//     empty path (via the Validate trivial-bit chain wrapping). Callers
//     probe errors.Is against either typed sentinel.
//
// Empty-but-valid manifests (manifest.Artifacts == 0 against a nil-error
// Validate) still return json.RawMessage("[]") — a handler that
// legitimately produces zero files with a well-formed empty envelope
// is allowed per the audit's existing audit-trail path; the empty
// envelope is a VALID artifact manifest per schema, not a missing one.
//
// The mapping from job.Artifact → finalization.PublishedArtifact:
//
//	id          → artifact_id
//	kind        → kind
//	filename    → filename
//	mime_type   → mime_type
//	size_bytes  → size_bytes
//	sha256      → sha256
//	local_path  → (discarded — the Sender never sees local paths)
//	required    → requirement (bool → ArtifactRequirement enum)
//	source      → source (PR-SOURCE-FIX: derived from jobType prefix)
//
// ── CANONICAL ORDERING PIN (FASE 1 close-out, byte-for-byte) ───────────
//
// The if-cascade below MUST stay in this exact order. A future
// contributor reflexively reordering the branches can silently break
// the FASE 1 spec invariants:
//
//  1. Decode   — typed ErrArtifactManifestInvalid on JSON parse /
//     unexpected-type failure (dual-%w wrap chains the
//     inner json error alongside the typed sentinel).
//  2. nil-check — typed ErrArtifactManifestMissing on absent
//     __artifact_manifest key (manifest present but
//     empty is NOT "missing" — empty-but-valid is a
//     distinct path; see step 3).
//  3. empty-envelope — return json.RawMessage("[]") on
//     len(Artifacts) == 0 with a non-nil manifest.
//     MUST precede step 4 because ArtifactManifest.Validate()
//     rejects `len(Artifacts) == 0` with
//     ErrArtifactManifestInvalid. If step 4 ran first,
//     the legitimate "handler legitimately produced
//     zero files" path would incorrectly fail.
//  4. Validate — typed ErrArtifactManifestInvalid on shape
//     violations (empty schema_version, empty id/kind,
//     required-but-empty-path, etc). The dual-%w
//     form wraps BOTH the typed sentinel AND the inner
//     Validate error, so errors.Is can probe either
//     job.ErrArtifactManifestInvalid (general) OR
//     job.ErrRequiredArtifactMissing (specific) via
//     chain traversal.
//  5. process   — publish the typed PublishedArtifact slice via
//     json.Marshal; the Marshal failure path wraps
//     ErrArtifactManifestInvalid (dual-%w form).
//
// Regression lock: the inner ordering is enforced by
// TestExtractStagedArtifacts_EmptyArtifactsList (which exercises
// the legitimate empty-envelope path returning `[]`) +
// TestExtractStagedArtifacts_DecodeFailure_TypedSentinel +
// TestExtractStagedArtifacts_ValidateFailure_TypedSentinel +
// TestExtractStagedArtifacts_RequiredMissingPath_ErrRequiredArtifactMissing
// — all four MUST keep passing or the FASE 1 contract is broken.
func extractStagedArtifacts(result map[string]any, jobType string) (json.RawMessage, error) {
	manifest, err := job.Decode(result)
	if err != nil {
		// Malformed manifest — typed error per FASE 1. Caller fails
		// the job; the broker MUST NOT mark SUCCEEDED for a job
		// whose artifact manifest cannot be decoded. Dual-%w form
		// (Go 1.20+) wraps BOTH the typed sentinel and the inner
		// json/wire error so errors.Is probes for ErrArtifactManifestInvalid
		// OR any sub-mode sentinel ErrRequiredArtifactMissing
		// propagate through the chain.
		return nil, fmt.Errorf("artifact-producing job %q: decode failure: %w: %w", jobType, job.ErrArtifactManifestInvalid, err)
	}
	if manifest == nil {
		// Manifest key absent per FASE 1 spec "manifest è assente".
		// Typed sentinel — caller fails the job; the broker MUST
		// NOT mark SUCCEEDED. This is the close-out fix for the
		// pre-FASE-1 silent-drop `[]` anti-pattern.
		return nil, fmt.Errorf("artifact-producing job %q: %w", jobType, job.ErrArtifactManifestMissing)
	}
	if len(manifest.Artifacts) == 0 {
		// Valid empty envelope (handler legitimately produced zero
		// files). Audit-trail "empty-envelope" path per P0 #4.
		// MUST run BEFORE manifest.Validate() because Validate
		// rejects `len(Artifacts) == 0` with ErrArtifactManifestInvalid
		// — running Validate first would incorrectly fail the
		// legitimate empty-envelope path.
		return json.RawMessage("[]"), nil
	}
	if err := manifest.Validate(); err != nil {
		// Manifest IS present and non-empty but fails shape
		// validation (empty schema_version / empty id / empty
		// kind / required-but-empty-path / etc). Typed sentinel —
		// dual-%w form wraps BOTH the typed sentinel AND the
		// inner Validate error so callers can errors.Is against
		// ErrArtifactManifestInvalid (general) OR
		// ErrRequiredArtifactMissing (specific) by traversing
		// the wrap chain.
		return nil, fmt.Errorf("artifact-producing job %q: validate failure: %w: %w", jobType, job.ErrArtifactManifestInvalid, err)
	}

	staged := make(domainremote.StagedArtifacts, 0, len(manifest.Artifacts))
	// PR-SOURCE-FIX: derive source from job type prefix
	// ("script.generate" → "script", "image.generate.google" → "image", etc.).
	// Hoisted outside the loop — all artifacts in one manifest share the
	// same job type.
	src := ""
	if idx := strings.Index(jobType, "."); idx > 0 {
		src = jobType[:idx]
	}
	for _, a := range manifest.Artifacts {
		staged = append(staged, &domainremote.StagedArtifactReference{
			ArtifactID:  a.ID,
			Destination: destinationForArtifactKind(a.Kind, src),
			SHA256:      a.SHA256,
			Path:        a.Path,
			Filename:    a.Filename,
			MIMEType:    a.MIMEType,
			SizeBytes:   a.SizeBytes,
			Required:    a.Required,
		})
	}

	raw, marshalErr := json.Marshal(staged)
	if marshalErr != nil {
		// Marshal failure on the PublishedArtifact conversion shape —
		// typed error per FASE 1 (c). Caller fails the job. Dual-%w
		// form preserves both the typed sentinel and the underlying
		// json.Marshal error for caller-side errors.Is probing.
		return nil, fmt.Errorf("artifact-producing job %q: PublishedArtifact marshal: %w: %w", jobType, job.ErrArtifactManifestInvalid, marshalErr)
	}
	return json.RawMessage(raw), nil
}

func destinationForArtifactKind(kind, source string) string {
	switch kind {
	case job.ArtifactKindScriptJSON, job.ArtifactKindScriptText, job.ArtifactKindScenes,
		job.ArtifactKindMetadata, job.ArtifactKindEntities, job.ArtifactKindClipBindings:
		return "script"
	case job.ArtifactKindVoiceover:
		return "voiceover"
	case job.ArtifactKindImage:
		return "image"
	case job.ArtifactKindPDF, job.ArtifactKindMarkdown:
		return "document"
	default:
		if source == "youtube" {
			return "youtube_clip"
		}
		return "document"
	}
}

// finalizeJob consolidates the 4 finalisation paths previously inlined
// at the bottom of worker_execution.go::runJob. It receives the
// AGENTS.md allowlisted finalizationCtx (the caller's
// `context.WithTimeout(context.Background(), 30*time.Second)`) so the
// DB writes that flip the job row to failed / completed / dead-lettered
// state can complete even when the worker jobCtx has been cancelled by
// timeout or worker Stop.
//
// Transition table:
//
//	dispatchErr != nil
//	  └─ j.RetryCount < j.MaxRetries  → ScheduleRetry (server-side backoff
//	                                    via available_at; no intermediate
//	                                    "failed" state to avoid false
//	                                    alerting)
//	  └─ j.RetryCount >= j.MaxRetries → Fail + DeadLetter (terminal)
//
//	dispatchErr == nil
//	  └─ ProducesArtifacts (reg.ProducesArtifacts) true
//	       └─ w.broker == nil             → Fail-closed (godlike/07 — flag
//	                                         the composition miss; NO silent
//	                                         fallback to legacy path)
//	       └─ extractStagedArtifacts err  → Fail + DeadLetter (FASE 1 c
//	                                         typed-error contract: malformed
//	                                         manifest MUST NOT silently reach
//	                                         SUCCEEDED)
//	       └─ broker.CompleteWithArtifacts err → Fail + DeadLetter
//	                                            (PR-COMPLETE-WORKER-BROAD-FIX
//	                                             closure: pre-PR code silently
//	                                             logged + returned, leaving
//	                                             the job RUNNING forever)
//	       └─ success                     → log "job completed with artifacts"
//	  └─ ProducesArtifacts false
//	       └─ w.repo.Complete err
//	            └─ job.ErrLeaseLost                 → log warn
//	            └─ domainremote.ErrCompleteJobPathViolation → Fail (FASE 0.1:
//	                                                          legacy Worker path
//	                                                          cannot complete
//	                                                          artifact production)
//	            └─ other                           → log error
//	       └─ success                             → log "job completed"
//
// All `errors.Is(err, job.ErrLeaseLost)` branches log warn rather than
// re-raise; a lease-loss during a finalisation SQL UPDATE means another
// worker has already CAS-won the row, so the next worker's transition
// is the authoritative one.
func (w *Worker) finalizeJob(ctx context.Context, j *job.Job, result map[string]any, dispatchErr error) {
	// Refresh revision from the DB so the final CAS write carries the
	// latest expected revision (a concurrent Update during execution
	// would invalidate the snapshot copied at ClaimNext).
	finalRevision := j.Revision
	if jFresh, err := w.repo.Get(ctx, j.ID); err == nil && jFresh != nil && jFresh.Revision > 0 {
		finalRevision = jFresh.Revision
	}

	workerID := w.id
	leaseID := j.LeaseID

	if dispatchErr != nil {
		w.log.Error("job failed",
			zap.String("job_id", j.ID),
			zap.Error(dispatchErr))

		if j.RetryCount < j.MaxRetries {
			// Backoff math now routes through pkg/retry.BackoffFor
			// (the canonical owner of "compute exponential backoff" —
			// godlike/06 SSOT, see pkg/retry/options.go godlike/06
			// block). 2s × 2^RetryCount capped at 30s — byte-equivalent
			// with the pre-migration bitwise math, but the cap now
			// lives at `MaxBackoff` in the canonical Options literal
			// instead of an inlined `if backoff > 30*time.Second`
			// post-clamp. JitterFraction defaults to 0 in a struct
			// literal so determinism for the server-side `available_at`
			// schedule is preserved (the SQL stored timestamp is the
			// persisted retry target, NEVER a random pre-sleep).
			backoff := retry.BackoffFor(j.RetryCount, retry.Options{
				InitialBackoff: 2 * time.Second,
				BackoffFactor:  2.0,
				MaxBackoff:     30 * time.Second,
			})
			w.log.Info("scheduling job for retry",
				zap.String("job_id", j.ID),
				zap.Duration("backoff", backoff))

			// ScheduleRetry does running→queued atomically with
			// server-side backoff via available_at. No intermediate
			// "failed" state — avoids false alerting.
			if retryErr := w.repo.ScheduleRetry(ctx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error(), backoff); retryErr != nil {
				if errors.Is(retryErr, job.ErrLeaseLost) {
					w.log.Warn("lease lost during ScheduleRetry — another worker claimed this job",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to schedule retry",
						zap.String("job_id", j.ID),
						zap.Error(retryErr))
				}
			}
			return
		}

		if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, dispatchErr.Error()); failErr != nil {
			if errors.Is(failErr, job.ErrLeaseLost) {
				w.log.Warn("lease lost during fail (exhausted retries)",
					zap.String("job_id", j.ID))
			} else {
				w.log.Error("failed to mark job as failed",
					zap.String("job_id", j.ID),
					zap.Error(failErr))
			}
		}
		if dlqErr := w.repo.DeadLetter(ctx, j.ID, dispatchErr.Error()); dlqErr != nil {
			w.log.Warn("failed to dead-letter job", zap.String("job_id", j.ID), zap.Error(dlqErr))
		} else {
			w.log.Warn("job moved to dead letter queue",
				zap.String("job_id", j.ID),
				zap.Int("retry_count", j.RetryCount),
				zap.Error(dispatchErr))
		}
		return
	}

	// PR-WORKER-RUNNER-INPROCESS-MIGRATION (July 2026): artifact-
	// producing jobs MUST be completed via the typed CompletionPort
	// (broker.CompleteWithArtifacts) — NOT the legacy w.repo.Complete
	// path. The SQL-layer gate at
	// internal/infrastructure/database/sqlite/jobs/repository_lifecycle.go:115
	// returns the typed sentinel domainremote.ErrCompleteJobPathViolation
	// for artifact-producing jobs that attempt the legacy path, which
	// failed FASE B Smoke Test 9 (duplicate payload → same drive_file_id
	// across 2 runs; the SUCCEEDED transition never fired because the
	// asset's drive_file_id write requires a clean broker closure).
	// This branch routes those jobs through the typed narrow port,
	// unblocking the canonical mark-SUCCEEDED.
	//
	// godlike/06 SSOT: ProducesArtifacts lookup lives ONLY on the typed
	// JobTypeRegistry (reg.ProducesArtifacts) at
	// internal/application/jobs/registry.go; nil reg = legacy behaviour,
	// preserving existing test fixtures that don't build a registry.
	//
	// godlike/07 typed-error contract: nil broker + ProducesArtifacts=true
	// fails closed via w.repo.Fail with a diagnostic naming the
	// (.WithBroker) composition miss. NO silent fallback to legacy path;
	// the SQL-layer ErrCompleteJobPathViolation rejection is replaced
	// with a clean Fail row that surfaces the wiring gap in the audit
	// timeline.
	producesArtifacts := w.reg != nil && w.reg.ProducesArtifacts(j.Type)
	if producesArtifacts {
		if w.broker == nil {
			w.log.Error("artifact-producing job encountered without CompletionPort wired — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(fmt.Errorf("worker.CompletionPort unset (call WithBroker(cp) at composition time)")))
			if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
				fmt.Sprintf("worker.CompletionPort not wired for artifact-producing job %q; call WithBroker(cp) on the Worker constructor", j.Type)); failErr != nil {
				if errors.Is(failErr, job.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-missing-broker",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed (after missing-broker gate)",
						zap.String("job_id", j.ID),
						zap.Error(failErr))
				}
			}
			return
		}

		// Extract the artifact manifest from the handler result. Handlers
		// that produce files (script.generate, image.generate.google,
		// etc.) inject a __artifact_manifest key into the result map.
		// The worker extracts it here and passes it as StagedArtifacts
		// so the broker's CompleteWithArtifacts can persist the artifact
		// metadata atomically with the job SUCCEEDED transition.
		//
		// FASE 1 (c) — typed-error contract: a manifest decode/marshal
		// failure surfaces a typed job.ErrArtifactManifestInvalid. The
		// decode-error / marshal-error path FAILS the job (audit 2026-07-03
		// P0 #4 criterion "il manifest non è decodificabile") — a
		// malformed manifest MUST NOT silently reach SUCCEEDED.
		//
		// Empty-but-valid manifests (returned as json.RawMessage("[]"))
		// still ride the normal CompleteWithArtifacts path.
		stagedArtifacts, extractErr := extractStagedArtifacts(result, j.Type)
		if extractErr != nil {
			// FASE 1 (c): the typed manifest error is a hard handler
			// fault (decode/marshal failure). Mirror the CompletionPort
			// error branch: fail the job + dead-letter, so a malformed
			// manifest is observable in the audit timeline and the broker
			// never marks SUCCEEDED.
			manifestErr := fmt.Sprintf("artifact manifest extract failed for artifact-producing job %q: %v", j.Type, extractErr)
			w.log.Error("worker: artifact manifest extract failed — failing job (FASE 1 c typed-error contract)",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(extractErr))
			if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision, manifestErr); failErr != nil {
				if errors.Is(failErr, job.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-manifest-extract-error",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed (after manifest extract error)",
						zap.String("job_id", j.ID),
						zap.Error(failErr))
				}
			}
			if dlqErr := w.repo.DeadLetter(ctx, j.ID, manifestErr); dlqErr != nil {
				w.log.Warn("failed to dead-letter job after manifest extract error",
					zap.String("job_id", j.ID), zap.Error(dlqErr))
			}
			return
		}

		cmd := CompleteWithArtifactsCommand{
			WorkerID:         w.id,
			WorkerSessionID:  "",
			JobID:            j.ID,
			LeaseID:          leaseID,
			ExpectedRevision: finalRevision,
			CorrelationID:    j.CorrelationID,
			ResultData:       mapToRawMessage(result),
			StagedArtifacts:  stagedArtifacts,
			OutboxEvents:     nil,
		}

		if _, err := w.broker.CompleteWithArtifacts(ctx, cmd); err != nil {
			completionErr := fmt.Sprintf("CompletionPort.CompleteWithArtifacts failed for artifact-producing job %q: %v", j.Type, err)
			w.log.Error("failed to mark artifact-producing job as completed via CompletionPort — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(err))
			// PR-COMPLETE-WORKER-BROAD-FIX (July 2026): the pre-PR code
			// silently logged the error and returned, leaving the job
			// RUNNING forever. The canonical fix is to fail the job
			// with a diagnostic naming the CompletionPort failure so
			// the operator can see WHY the job never reached SUCCEEDED.
			if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
				completionErr); failErr != nil {
				if errors.Is(failErr, job.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-completion-error",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed (after CompletionPort error)",
						zap.String("job_id", j.ID),
						zap.Error(failErr))
				}
			}
			// DeadLetter for audit-trail completeness — mirrors the
			// dispatchErr exhausted-retries path (code-review, July 2026).
			if dlqErr := w.repo.DeadLetter(ctx, j.ID, completionErr); dlqErr != nil {
				w.log.Warn("failed to dead-letter job after CompletionPort error",
					zap.String("job_id", j.ID), zap.Error(dlqErr))
			}
		} else {
			w.log.Info("job completed with artifacts",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type))
		}
		return
	}

	if completeErr := w.repo.Complete(ctx, j.ID, workerID, leaseID, finalRevision, mapToRawMessage(result)); completeErr != nil {
		if errors.Is(completeErr, job.ErrLeaseLost) {
			w.log.Warn("lease lost during complete — another worker claimed this job",
				zap.String("job_id", j.ID))
		} else if errors.Is(completeErr, domainremote.ErrCompleteJobPathViolation) {
			// FASE 0.1 (July 4 2026): the legacy Worker path cannot
			// call CompleteWithArtifacts — the job.Store interface has
			// no such method. The canonical typed sentinel
			// domainremote.ErrCompleteJobPathViolation (per godlike/06
			// SSOT at internal/domain/remote/complete_job.go) gates the
			// typoevolee, so this branch fails the job toward a
			// terminal state instead of staying RUNNING forever
			// (godlike/07 no-fake-availability).
			w.log.Error("artifact-producing job cannot complete via legacy Worker path — failing job",
				zap.String("job_id", j.ID),
				zap.String("job_type", j.Type),
				zap.Error(completeErr))
			if failErr := w.repo.Fail(ctx, j.ID, workerID, leaseID, finalRevision,
				fmt.Sprintf("legacy Worker cannot complete artifact-producing job %q: %v", j.Type, completeErr)); failErr != nil {
				if errors.Is(failErr, job.ErrLeaseLost) {
					w.log.Warn("lease lost during fail-after-artifact-gate",
						zap.String("job_id", j.ID))
				} else {
					w.log.Error("failed to mark artifact-producing job as failed",
						zap.String("job_id", j.ID),
						zap.Error(failErr))
				}
			}
		} else {
			w.log.Error("failed to mark job as completed",
				zap.String("job_id", j.ID),
				zap.Error(completeErr))
		}
	} else {
		w.log.Info("job completed", zap.String("job_id", j.ID))
	}
}
