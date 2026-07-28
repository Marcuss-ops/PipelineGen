// Package stockpipeline owns the stock broker handler and result projection.
package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StockJobResult is the canonical typed result returned by HandleJob.
type StockJobResult struct {
	Manifest                *job.ArtifactManifest `json:"__artifact_manifest"`
	FinalStatus             string                `json:"final_status"`
	TotalClips              int                   `json:"total_clips"`
	TotalChunks             int                   `json:"total_chunks"`
	Chunks                  []ChunkResult         `json:"chunks"`
	MetadataLink            string                `json:"metadata_link"`
	MetadataFileID          string                `json:"metadata_file_id"`
	FinalizationStatus      string                `json:"__finalization_status,omitempty"`
	FinalizationCompletedAt time.Time             `json:"__finalization_completed_at,omitempty"`
	Counts                  RunCounts             `json:"counts"`
}

func (r StockJobResult) ToResultMap() map[string]any {
	result := map[string]any{
		job.ManifestKey:    r.Manifest,
		"final_status":     r.FinalStatus,
		"total_clips":      r.TotalClips,
		"total_chunks":     r.TotalChunks,
		"chunks":           r.Chunks,
		"metadata_link":    r.MetadataLink,
		"metadata_file_id": r.MetadataFileID,
	}
	if r.Counts != (RunCounts{}) {
		result["counts"] = r.Counts
	}
	if r.FinalizationStatus != "" {
		result["__finalization_status"] = r.FinalizationStatus
	}
	if !r.FinalizationCompletedAt.IsZero() {
		result["__finalization_completed_at"] = r.FinalizationCompletedAt
	}
	return result
}

// RegisterHandler binds the stock handler to the canonical jobs service.
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

// HandleJob executes one stock job through the resilient orchestrator.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
func (s *Service) HandleJob(ctx context.Context, queuedJob *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	var payload StockRunPayload
	if len(queuedJob.Payload) > 0 {
		if err := json.Unmarshal(queuedJob.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stock payload: %w", err)
		}
	}

	s.log.Info("stock job payload received",
		zap.String("job_id", queuedJob.ID),
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
		FinalizationLease: extractLease(queuedJob),
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

	summary, err := s.runOrchestratorResilient(ctx, input, queuedJob.ID)
	if err != nil {
		return nil, err
	}
	if tools.Progress != nil {
		tools.Progress(80, "Stock orchestrator complete")
		tools.Progress(100, "Stock pipeline finalised")
	}

	projected := projectManifestToPipelineResult(summary.Manifest)
	return (StockJobResult{
		Manifest:       summary.Manifest,
		FinalStatus:    string(summary.FinalStatus),
		TotalClips:     projected.TotalClips,
		TotalChunks:    projected.TotalChunks,
		Chunks:         projected.Chunks,
		MetadataLink:   projected.MetadataLink,
		MetadataFileID: projected.MetadataFileID,
		Counts:         summary.Counts,
	}).ToResultMap(), nil
}

// extractLease maps the claimed broker job to the finalizer lease contract.
func extractLease(queuedJob *appjobs.Job) finalization.Lease {
	if queuedJob == nil {
		return finalization.Lease{}
	}
	now := time.Now().UTC()
	// Stock extraction/rendering can legitimately outlive the broker's
	// initial five-minute lease (large multi-source batches). The worker
	// renews the broker lease while the handler runs, so extend only a
	// near-term claimed expiry; preserve far-future and expired values so
	// the lease contract and its fail-closed tests remain meaningful.
	expiresAt := now.Add(5 * time.Minute)
	if queuedJob.LeaseExpiry != nil && !queuedJob.LeaseExpiry.IsZero() {
		expiresAt = *queuedJob.LeaseExpiry
		if expiresAt.After(now) && expiresAt.Before(now.Add(10*time.Minute)) {
			expiresAt = now.Add(1 * time.Hour)
		}
	}
	return finalization.Lease{
		LeaseID:   queuedJob.LeaseID,
		JobID:     queuedJob.ID,
		WorkerID:  queuedJob.WorkerID,
		Attempt:   queuedJob.RetryCount + 1,
		ExpiresAt: expiresAt,
	}
}

func manifestBytes(manifest *job.ArtifactManifest) ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: nil manifest")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: marshal: %w", err)
	}
	return raw, nil
}
