package artlist

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func (a *JobAdapter) GetJobByRunID(ctx context.Context, runID string) (*job.Job, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, fmt.Errorf("run_id is required")
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
