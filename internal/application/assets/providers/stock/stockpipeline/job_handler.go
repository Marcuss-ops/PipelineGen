// Package stockpipeline — job_handler.go (Stock P0 split, July 2026).
//
// This file owns the job-handler methods previously co-located in
// service.go: HandleJob (the canonical broker entry-point),
// RegisterHandler (composition-root wiring), extractLease (broker→finalizer
// Lease translation), and manifestBytes (manifest marshal helper).
//
// godlike/06 SSOT: one canonical owner for "how does the stock pipeline
// handle a job from the broker?" — RegisterHandler + HandleJob.
// HandleJob delegates to runOrchestratorResilient (run_orchestrator.go)
// for the actual pipeline execution.
package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// RegisterHandler registers the stock pipeline job handler with the jobs
// system.
//
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, appjobs.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs. The error-return signature (refactored in
// Audit P0 #2 cont. — PR-VALIDATOR-LITERAL-REGISTER, July 2026)
// closes the silent-success class of "if jobsSvc != nil { log.Info }"
// that pre-P0 #2 swallowed nil-typed-dispatcher + duplicate-bind
// failures.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("stockpipeline.Service.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeMediaStock, appjobs.HandlerFunc(s.HandleJob)); err != nil {
		return fmt.Errorf("stockpipeline.Service.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeMediaStock, err)
	}
	s.log.Info("registered media.stock job handler", zap.String("type", appjobs.TypeMediaStock))
	return nil
}

// HandleJob handles a stock pipeline job from the job queue.
//
// Stock Cutover Commit 2 (July 2026): the handler no longer calls
// s.Run (the legacy ~280-line body that called resolveQuery /
// processSingleVideo / renderChunk / InterleaveClips / uploadAndIndexChunk).
// Instead it calls s.runOrchestrator directly so it has access to
// the typed *job.ArtifactManifest, which is the canonical wire
// artefact for the broker's downstream runner (the worker runner
// at internal/application/jobs/worker/runner.go::uploadManifest
// reads the result map's "__artifact_manifest" key per
// domain/job.ManifestKey).
//
// Result-map shape (Stock Cutover Commit 2):
//
//	"__artifact_manifest" -> *job.ArtifactManifest (canonical wire artefact)
//	"total_clips"          -> int                      (legacy field, projected from manifest; zero in Commit 2, hydrated in Commit 4-7)
//	"total_chunks"         -> int                      (legacy field, projected from manifest; zero in Commit 2)
//	"chunks"               -> []ChunkResult            (legacy field, projected from manifest; nil in Commit 2)
//	"metadata_link"        -> string                   (legacy field, projected from manifest; empty in Commit 2)
//	"metadata_file_id"     -> string                   (legacy field, projected from manifest; empty in Commit 2)
//
// Legacy fields are kept so dashboards reading the JobStatusResponse
// continue to render without a schema break; the canonical manifest
// is the new source of truth. Commit 4-7 hydrates the legacy fields
// from the committed RunOutput metadata once the chunk ladder ships.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	var payload StockRunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stock payload: %w", err)
		}
	}

	s.log.Info("stock job payload received",
		zap.String("job_id", job.ID),
		zap.Int("search_queries", len(payload.SearchQueries)),
		zap.Int("direct_urls", len(payload.DirectURLs)),
		zap.Int("total_minutes", payload.TotalMinutes),
		zap.Int("chunk_duration", payload.ChunkDuration),
		zap.String("subfolder", payload.Subfolder),
		zap.String("folder_name", payload.FolderName),
		zap.String("folder_id", payload.FolderID),
	)

	input := &RunInput{
		SearchQueries: payload.SearchQueries,
		DirectURLs:    payload.DirectURLs,
		TotalMinutes:  payload.TotalMinutes,
		ChunkDuration: payload.ChunkDuration,
		ClipDuration:  payload.ClipDuration,
		NoAudio:       payload.NoAudio,
		NoEffects:     payload.NoEffects,
		NoTransitions: payload.NoTransitions,
		MaxVideos:     payload.MaxVideos,
		Subfolder:     payload.Subfolder,
		FolderName:    payload.FolderName,
		FolderID:      payload.FolderID,
	}
	if payload.Metadata != nil {
		input.Metadata = &ChunkMetadataInput{
			Title:       payload.Metadata.Title,
			Description: payload.Metadata.Description,
			Tags:        payload.Metadata.Tags,
			Category:    payload.Metadata.Category,
			Author:      payload.Metadata.Author,
			Extra:       payload.Metadata.Extra,
		}
	}

	if tools.Progress != nil {
		input.Progress = tools.Progress
		tools.Progress(5, "Starting stock orchestrator")
	}

	// Stock Cutover Commit 4-expanded: route through
	// runOrchestratorResilient so the typed *RunSummary carries the
	// per-run FinalStatus that the broker JobFinalizer stamps on the
	// job row (resilience contract: artifacts emitted + Qdrant
	// projection deferred ⇒ INDEX_PENDING; artifacts emitted + Qdrant
	// OK ⇒ SUCCEEDED; manifest-gate/atomic-dispatch failure ⇒ typed
	// sentinel ⇒ JobFailed).
	summary, err := s.runOrchestratorResilient(ctx, input, job.ID)
	if err != nil {
		return nil, err
	}
	manifest := summary.Manifest

	if tools.Progress != nil {
		tools.Progress(80, "Stock orchestrator complete")
	}

	// Stock Cutover §12-1 §F (July 2026): thread HandleJob through
	// the canonical Spina Dorsale. Build the OrchestrationResult
	// envelope (already defined in finalizer_gates.go) carrying
	// the typed manifest + the per-chunk + per-metadata gate
	// inputs. Today (Commit 4-7 not landed) Chunks and Metadata
	// are EMPTY so BuildFinalizationRequest's VerifyChunks raises
	// ErrStockNoChunksFinalized — the gate fires before any
	// finalizer call, propagating the typed error to the broker
	// which marks the job FAILED (closing the silent-success
	// class per user spec P0 2.1).
	orchestration := &OrchestrationResult{
		Manifest: manifest,
		Chunks:   []ChunkState{},  // pre-Commit-4-7: empty
		Metadata: MetadataState{}, // pre-Commit-4-7: empty
	}

	// §F.1 (this commit): the Spine is OPTIONAL. When wired
	// (production case in §F.2 follow-up) the atomic
	// single-TX SUCCEEDED write happens; when unwired (today's
	// composition root, which doesn't yet thread a finalizer)
	// the gate-fail-fast path still propagates and the legacy
	// return-map shape is preserved so dashboards keep rendering.
	manifestData, marshalErr := manifestBytes(manifest)
	if marshalErr != nil {
		return nil, fmt.Errorf("stockpipeline.Service.HandleJob: marshal manifest: %w", marshalErr)
	}
	lease := extractLease(job)
	finReq, buildErr := BuildFinalizationRequest(
		job.ID,
		lease,
		manifestData,
		orchestration.Chunks,
		orchestration.Metadata,
	)
	if buildErr != nil {
		// Gate failed — propagate the typed sentinel so the
		// broker's downstream runner marks the job FAILED.
		// Today this returns ErrStockNoChunksFinalized on EVERY
		// stock run (pre-Commit-4-7). §F.2 does NOT change this
		// fail-closed behavior — it just enables the
		// post-gate-pass path through finalizer.CompleteWithArtifacts.
		return nil, fmt.Errorf("stockpipeline.Service.HandleJob: gates failed (job cannot SUCCEED): %w", buildErr)
	}

	var finResult *finalization.FinalizationResult
	if s.finalizer != nil {
		var finalErr error
		finResult, finalErr = s.finalizer.CompleteWithArtifacts(ctx, *finReq)
		if finalErr != nil {
			return nil, fmt.Errorf("stockpipeline.Service.HandleJob: finalizer spine write: %w", finalErr)
		}
		s.log.Info("stock finaliser spine SUCCEEDED",
			zap.String("job_id", job.ID),
			zap.Int("attempt", lease.Attempt),
			zap.Int("artifact_count", len(finResult.ArtifactRefs)),
		)
	} else {
		// §F.1 fallback: composition root hasn't wired the
		// production finalizer yet. The legacy return-map shape
		// preserves JobStatusResponse rendering and the broker's
		// downstream runner still sees the manifest. The job is
		// NOT marked SUCCEEDED in this branch (finalizer is the
		// single writer per godlike/06 SSOT).
		s.log.Warn("stock Service.HandleJob finalizer NOT wired (§12-1 §F.1 OPTIONAL gate) — gates passed but no spine write occurred; legacy return-map path is active",
			zap.String("job_id", job.ID),
			zap.Int("attempt", lease.Attempt),
		)
	}

	if tools.Progress != nil {
		tools.Progress(100, "Stock pipeline finalised")
	}

	// Project the typed manifest into the legacy shape for
	// dashboards that read the top-level fields (zero in Commit 2;
	// post-cutover chunks populate them in Commit 4-7).
	projected := projectManifestToPipelineResult(manifest)

	// Note on `jobdomain` (alias vs HandleJob's `job` parameter): the
	// HandleJob parameter is named `job *appjobs.Job` so the bare
	// identifier `job` resolves to the broker job, NOT to a package
	// alias. We therefore import domain/job as `jobdomain` so the
	// artifact-manifest constants (jobdomain.ManifestKey,
	// jobdomain.SchemaVersionArtifactManifestV1) are unambiguous.
	result := map[string]any{
		jobdomain.ManifestKey: manifest,                    // "__artifact_manifest" — canonical wire artefact
		"final_status":        string(summary.FinalStatus), // "SUCCEEDED" | "INDEX_PENDING" | "FAILED" | ...
		"total_clips":         projected.TotalClips,
		"total_chunks":        projected.TotalChunks,
		"chunks":              projected.Chunks,
		"metadata_link":       projected.MetadataLink,
		"metadata_file_id":    projected.MetadataFileID,
	}
	if finResult != nil {
		result["__finalization_status"] = finResult.Status
		result["__finalization_completed_at"] = finResult.CompletedAt
	}
	return result, nil
}

// extractLease projects the legacy *appjobs.Job (broker-routed,
// already-claimed) into the canonical finalization.Lease struct
// the JobFinalizer validates inside its single-TX commit.
//
// Mapping rules (Stock Cutover §12-1 §F, July 2026):
//
//	LeaseID    ← job.LeaseID         (broker-assigned at Claim time)
//	JobID      ← job.ID              (canonical broker job identifier)
//	WorkerID   ← job.WorkerID        (broker-assigned worker id)
//	Attempt    ← job.RetryCount + 1  (the canonical "next attempt" formula)
//	ExpiresAt  ← job.LeaseExpiry     (broker-issued lease TTL)
//
// TOCTOU note: the broker's lease could expire between this
// pre-tx read and the finalizer's in-tx lease fence. The
// finalizer's own selectJobForFinalization runs the canonical
// re-validation against the DB row (worker_id + lease_id +
// lease_expiry + retry_count+1 == attempt); lease drift here
// surfaces as ErrLeaseExpired / ErrStaleAttempt inside the tx,
// which is the typed-error contract callers can errors.Is()
// against.
//
// Defensive fallback: if the broker hasn't populated LeaseExpiry
// (rare — usually happens only on synthetic test fixtures),
// extractLease returns a 5-minute TTL so validateRequest
// (`Lease.Valid()`) doesn't raise an empty-time false-positive.
// Production broker traffic always carries a non-nil LeaseExpiry.
func extractLease(job *appjobs.Job) finalization.Lease {
	if job == nil {
		return finalization.Lease{}
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	if job.LeaseExpiry != nil && !job.LeaseExpiry.IsZero() {
		expiresAt = *job.LeaseExpiry
	}
	return finalization.Lease{
		LeaseID:   job.LeaseID,
		JobID:     job.ID,
		WorkerID:  job.WorkerID,
		Attempt:   job.RetryCount + 1,
		ExpiresAt: expiresAt,
	}
}

// manifestBytes marshals the canonical *job.ArtifactManifest
// (C12 envelope) into finalization.ResultManifest.Data bytes.
// Errors here are typed-error contract violations (manifest
// schema drift) — a typed-error wrap is the right escalation
// since callers can't recover from a marshaller bug mid-job.
func manifestBytes(manifest *jobdomain.ArtifactManifest) ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: nil manifest")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: marshal: %w", err)
	}
	return raw, nil
}
