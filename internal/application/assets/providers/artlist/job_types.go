package artlist

import (
	"context"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func (a *JobAdapter) GetJobByRunID(ctx context.Context, runID string) (*job.Job, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
	}
	if a == nil || a.service == nil || a.service.jobsSvc == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.GetJobByRunID: jobs service is not configured")
	}

	res, err := a.service.jobsSvc.Get(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("lookup job %s in jobs table: %w", runID, err)
	}
	return res, nil
}

// JobAdapter gestisce l'integrazione tra il servizio Artlist e il sistema di job.
type JobAdapter struct {
	service *Service
}

// NewJobAdapter crea una nuova istanza di JobAdapter.
func NewJobAdapter(s *Service) *JobAdapter {
	return &JobAdapter{service: s}
}

// RunTag delegates to the canonical Artlist run orchestrator. Keeping this
// method on Service preserves the facade consumed by JobHandler and API code.
func (s *Service) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	if s == nil || s.runOrchestrator == nil {
		return nil, fmt.Errorf("artlist.Service.RunTag: run orchestrator is not configured")
	}
	return s.runOrchestrator.RunTag(ctx, req)
}

// HandleJob adapts the canonical kernel handler signature to the existing
// Service.JobHandler implementation without duplicating pipeline logic.
func (a *JobAdapter) HandleJob(
	ctx context.Context,
	j *job.Job,
	tools *job.JobExecutionTools,
) (job.Result, error) {
	if a == nil || a.service == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.HandleJob: service is not configured")
	}
	return a.service.JobHandler(ctx, j, tools)
}

// RegisterHandler binds the Artlist consumer using the media domain's
// canonical discriminator and the shared kernel handler contract.
func (a *JobAdapter) RegisterHandler(jobsSvc *appjobs.Service) error {
	if a == nil || a.service == nil {
		return fmt.Errorf("artlist.JobAdapter.RegisterHandler: service is not configured")
	}
	if jobsSvc == nil {
		return fmt.Errorf("artlist.JobAdapter.RegisterHandler: jobs service is nil")
	}
	if err := jobsSvc.RegisterHandler(media.TypeArtlistRun, appjobs.HandlerFunc(a.HandleJob)); err != nil {
		return fmt.Errorf("artlist.JobAdapter.RegisterHandler: bind %q: %w", media.TypeArtlistRun, err)
	}
	return nil
}

// jobToResponse converts a job.Job to RunTagResponse using the codec.
func (a *JobAdapter) jobToResponse(j *job.Job) *RunTagResponse {
	if j == nil {
		return &RunTagResponse{OK: false, Status: "not_found", Error: "job not found"}
	}
	return (&JobCodec{}).ResponseFromJob(j)
}

// JobToRunTagResponse converts a job.Job to RunTagResponse using the codec.
func JobToRunTagResponse(j *job.Job) *RunTagResponse {
	return (&JobCodec{}).ResponseFromJob(j)
}

// toDomain is now a passthrough — the legacy models.MediaAsset has been deleted.
// Callers already pass *asset.Asset; this function exists for compatibility
// with existing call sites and will be removed in a follow-up cleanup.
func toDomain(m *asset.Asset) *asset.Asset {
	return m
}

// toDomainSlice converts a slice of asset.Asset to asset.Asset (passthrough).
func toDomainSlice(items []asset.Asset) []asset.Asset {
	out := make([]asset.Asset, len(items))
	copy(out, items)
	return out
}

// toDomainPtrSlice converts a slice of *asset.Asset to *asset.Asset (passthrough).
func toDomainPtrSlice(items []*asset.Asset) []*asset.Asset {
	out := make([]*asset.Asset, len(items))
	copy(out, items)
	return out
}
