// Package app — clip_ops port adapters (PR 2, June 2026).
//
// Bridges concrete domain/job.Service and clips.ClipRepositoryPort
// into the typed ports consumed by application/clips.ClipOpsService.
// CleanupServicePort + clipsCleanupPortAdapter removed July 2026
// (dead code — field assigned but never read by any ClipOpsService method).
//
// PR-GODOBJ-4 (Azione 4, July 2026): consolidated from clips_ops_adapters.go
// and clipOpsSourceResolverAdapter from clips_adapters_repo.go.
// 2 adapters: clipsJobsPortAdapter, clipOpsSourceResolverAdapter.
package capabilities

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── clipsJobsPortAdapter ────────────────────────────────────────────────

// clipsJobsPortAdapter wraps the canonical domain/job.Service to
// satisfy clips.JobsServicePort. The narrowed DTO clips.JobsEnqueueRequest
// carries only the 4 fields the ClipOps deep-mode → enqueue path
// actually reads (Type / Payload / Priority / ActiveKey); all other
// EnqueueRequest fields (CorrelationID, MaxRetries, Project, VideoName)
// are intentionally left zero on the canonical request and inherit
// the domain-layer defaults.
type clipsJobsPortAdapter struct {
	inner job.Service
}

// Compile-time assertion: clipsJobsPortAdapter satisfies clips.JobsServicePort.
var _ clips.JobsServicePort = (*clipsJobsPortAdapter)(nil)

func newClipsJobsPortAdapter(svc job.Service) clips.JobsServicePort {
	if svc == nil {
		return nil
	}
	return &clipsJobsPortAdapter{inner: svc}
}

func (a *clipsJobsPortAdapter) Enqueue(ctx context.Context, req clips.JobsEnqueueRequest) (*clips.JobsEnqueueResponse, error) {
	if a == nil {
		return nil, fmt.Errorf("clipsJobsPortAdapter: receiver is nil")
	}
	if a.inner == nil {
		return nil, fmt.Errorf("clipsJobsPortAdapter: inner job service is nil")
	}
	if req.Type == "" {
		return nil, fmt.Errorf("clipsJobsPortAdapter: empty job type on enqueue")
	}
	canonical := &job.EnqueueRequest{
		Type:      req.Type,
		Payload:   req.Payload,
		Priority:  req.Priority,
		ActiveKey: req.ActiveKey,
	}
	j, err := a.inner.Enqueue(ctx, canonical)
	if err != nil {
		return nil, err
	}
	if j == nil {
		return &clips.JobsEnqueueResponse{}, nil
	}
	return &clips.JobsEnqueueResponse{ID: j.ID}, nil
}

// ── clipOpsSourceResolverAdapter ────────────────────────────────────────
//
// PR-CLIPS-DAPTER-RESOLVER-RETIRE (July 2026): the clipOpsSourceResolverAdapter
// is REMOVED. The composition root at wire_assets_clips.go::buildClipsBundle
// now passes the canonical clipsAdapterBundle.ClipsRepo directly to
// NewClipOpsService as its `clipRepo` argument. The 2 production adapters
// (sourceResolverAdapter in clips_adapters_index.go + clipOpsSourceResolverAdapter
// in this file) and the SourceResolverPort interface are all retired in
// this wave — see the comment block above the clipsAdapterBundle struct
// declaration in clips_adapters_index.go for the full rationale.
