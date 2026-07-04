// Package app — clip_ops port adapters (PR 2, June 2026).
//
// Bridges concrete domain/job.Service, *deletion.DeletionService, and
// clips.ClipRepositoryPort into the typed ports consumed by
// application/clips.ClipOpsService.
//
// PR-GODOBJ-4 (Azione 4, July 2026): consolidated from clips_ops_adapters.go
// and clipOpsSourceResolverAdapter from clips_adapters_repo.go.
// 3 adapters: clipsCleanupPortAdapter, clipsJobsPortAdapter, clipOpsSourceResolverAdapter.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── clipsCleanupPortAdapter ─────────────────────────────────────────────

// clipsCleanupPortAdapter wraps *deletion.DeletionService to satisfy
// clips.CleanupServicePort. The 2 methods are exact delegations —
// CleanupOrphanFiles(ctx,path,dryRun) and DeleteClip(ctx,src,id,hard).
type clipsCleanupPortAdapter struct {
	inner *deletion.DeletionService
}

// Compile-time assertion: clipsCleanupPortAdapter satisfies clips.CleanupServicePort.
var _ clips.CleanupServicePort = (*clipsCleanupPortAdapter)(nil)

func newClipsCleanupPortAdapter(svc *deletion.DeletionService) clips.CleanupServicePort {
	if svc == nil {
		return nil
	}
	return &clipsCleanupPortAdapter{inner: svc}
}

func (a *clipsCleanupPortAdapter) CleanupOrphanFiles(ctx context.Context, path string, dryRun bool) (int, error) {
	if a == nil {
		return 0, fmt.Errorf("clipsCleanupPortAdapter: receiver is nil")
	}
	if a.inner == nil {
		return 0, fmt.Errorf("clipsCleanupPortAdapter: inner deletion service is nil")
	}
	return a.inner.CleanupOrphanFiles(ctx, path, dryRun)
}

func (a *clipsCleanupPortAdapter) DeleteClip(ctx context.Context, source, clipID string, hardDelete bool) error {
	if a == nil {
		return fmt.Errorf("clipsCleanupPortAdapter: receiver is nil")
	}
	if a.inner == nil {
		return fmt.Errorf("clipsCleanupPortAdapter: inner deletion service is nil")
	}
	return a.inner.DeleteClip(ctx, source, clipID, hardDelete)
}

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

// clipOpsSourceResolverAdapter wraps a single clips.ClipRepositoryPort.
// Collapse (June 2026): SourceResolver eliminated — all clip-type sources
// share the same concrete repo in production.
type clipOpsSourceResolverAdapter struct {
	clips clips.ClipRepositoryPort
}

var _ clips.SourceResolverPort = (*clipOpsSourceResolverAdapter)(nil)

func (a *clipOpsSourceResolverAdapter) ResolveRepo(source string) clips.ClipRepositoryPort {
	if a == nil {
		return nil
	}
	return a.clips
}
