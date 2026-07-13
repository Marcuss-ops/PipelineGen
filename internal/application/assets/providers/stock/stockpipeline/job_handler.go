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
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StockJobResult is the typed result envelope returned by HandleJob.
// Replaces the opaque map[string]any literal so the wire shape is
// declared in a single struct (godlike/06 one-owner-per-fact).
//
// P5 (July 2026): added as part of the stock action-plan wave.
// The return surface stays map[string]any (kerneljob.Result) for
// broker compatibility; ToResultMap() bridges the typed struct to
// the canonical wire representation.
type StockJobResult struct {
	Manifest                *kerneljob.ArtifactManifest `json:"__artifact_manifest"`
	FinalStatus             string                      `json:"final_status"`
	TotalClips              int                         `json:"total_clips"`
	TotalChunks             int                         `json:"total_chunks"`
	Chunks                  []ChunkResult               `json:"chunks"`
	MetadataLink            string                      `json:"metadata_link"`
	MetadataFileID          string                      `json:"metadata_file_id"`
	FinalizationStatus      string                      `json:"__finalization_status,omitempty"`
	FinalizationCompletedAt time.Time                   `json:"__finalization_completed_at,omitempty"`
}

// ToResultMap converts the typed StockJobResult to the canonical
// map[string]any wire representation consumed by the broker's
// downstream runner and dashboard projections.
//
// omitempty fields are only populated when their zero-value guard
// passes (non-empty string / non-zero time).
func (r StockJobResult) ToResultMap() map[string]any {
	m := map[string]any{
		kerneljob.ManifestKey: r.Manifest,
		"final_status":        r.FinalStatus,
		"total_clips":         r.TotalClips,
		"total_chunks":        r.TotalChunks,
		"chunks":              r.Chunks,
		"metadata_link":       r.MetadataLink,
		"metadata_file_id":    r.MetadataFileID,
	}
	if r.FinalizationStatus != "" {
		m["__finalization_status"] = r.FinalizationStatus
	}
	if !r.FinalizationCompletedAt.IsZero() {
		m["__finalization_completed_at"] = r.FinalizationCompletedAt
	}
	return m
}

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
		zap.Int("drive_urls", len(payload.DriveURLs)),
		zap.Int("clips", len(payload.Clips)),
		zap.Int("total_minutes", payload.TotalMinutes),
		zap.Int("chunk_duration", payload.ChunkDuration),
		zap.Int("seconds_per_segment", payload.SecondsPerSegment),
		zap.String("subfolder", payload.Subfolder),
		zap.String("folder_name", payload.FolderName),
		zap.String("drive_folder_id", payload.DriveFolderID),
		zap.String("folder_id", payload.FolderID),
	)

	input := &RunInput{
		SearchQueries:     payload.SearchQueries,
		DirectURLs:        payload.DirectURLs,
		DriveURLs:         payload.DriveURLs,
		Clips:             append([]ClipSpec(nil), payload.Clips...),
		TotalMinutes:      payload.TotalMinutes,
		ChunkDuration:     payload.ChunkDuration,
		ClipDuration:      payload.ClipDuration,
		SecondsPerSegment: payload.SecondsPerSegment,
		NoAudio:           payload.NoAudio,
		NoEffects:         payload.NoEffects,
		NoTransitions:     payload.NoTransitions,
		MaxVideos:         payload.MaxVideos,
		Subfolder:         payload.Subfolder,
		FolderName:        payload.FolderName,
		DriveFolderID:     payload.DriveFolderID,
		FolderID:          payload.FolderID,
		FinalizationLease: extractLease(job),
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

	// Stock Cutover Commit 4-expanded (July 2026): the orchestrator's
	// step 6 (StockFinalizeStep) owns the canonical single-TX spine
	// write via JobFinalizer.CompleteWithArtifacts. HandleJob delegates
	// entirely to runOrchestratorResilient; it does NOT duplicate the
	// finalization gate — any duplication would always fail with
	// ErrStockNoChunksFinalized because the orchestrator already
	// consumed the Published/MetadataPublished runState.

	if tools.Progress != nil {
		tools.Progress(100, "Stock pipeline finalised")
	}

	// Project the typed manifest into the legacy shape for
	// dashboards that read the top-level fields (zero in Commit 2;
	// post-cutover chunks populate them in Commit 4-7).
	projected := projectManifestToPipelineResult(manifest)

	out := StockJobResult{
		Manifest:       manifest,
		FinalStatus:    string(summary.FinalStatus),
		TotalClips:     projected.TotalClips,
		TotalChunks:    projected.TotalChunks,
		Chunks:         projected.Chunks,
		MetadataLink:   projected.MetadataLink,
		MetadataFileID: projected.MetadataFileID,
	}
	return out.ToResultMap(), nil
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
func manifestBytes(manifest *kerneljob.ArtifactManifest) ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: nil manifest")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: marshal: %w", err)
	}
	return raw, nil
}
