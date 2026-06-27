// Package app — clip_ops port adapters (PR 2, June 2026).
//
// Bridge the concrete domain/job.Service and *deletion.DeletionService
// into the typed ports consumed by application/clips.ClipOpsService.
// Pattern parity with the existing clips_adapters_*.go files
// (PG-005, June 2026): the API layer depends on port interfaces, the
// composition-root adapter wraps the concrete infra, and the
// compile-time `var _ <Port> = (*<Adapter>)(nil)` assertion catches
// signature drift at build time.
//
// The cutters are minimal:
//   - CleanupServicePort methods signature-match the canonical
//     *deletion.DeletionService methods one-for-one. The adapter is
//     a typed pass-through.
//   - JobsServicePort.MinimalEnqueue maps the narrowed clips
//     JobsEnqueueRequest to the canonical EnqueueRequest, drops the
//     extended fields (CorrelationID / MaxRetries / Project /
//     VideoName) the ClipOps path doesn't read, and returns the
//     narrowed {ID} response.
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// clipsCleanupPortAdapter wraps *deletion.DeletionService to satisfy
// clips.CleanupServicePort. The 2 methods are exact delegations —
// CleanupOrphanFiles(ctx,path,dryRun) and DeleteClip(ctx,src,id,hard).
type clipsCleanupPortAdapter struct {
	inner *deletion.DeletionService
}

// Compile-time assertion: clipsCleanupPortAdapter satisfies clips.CleanupServicePort.
// Latent signature drift in either direction fails `go build` rather
// than waiting for the first runtime panic.
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

// clipsJobsPortAdapter wraps the canonical domain/job.Service to
// satisfy clips.JobsServicePort. The narrowed DTO clips.JobsEnqueueRequest
// carries only the 4 fields the ClipOps deep-mode → enqueue path
// actually reads (Type / Payload / Priority / ActiveKey); all other
// EnqueueRequest fields (CorrelationID, MaxRetries, Project, VideoName)
// are intentionally left zero on the canonical request and inherit
// the domain-layer defaults. This is the minimal projection at the
// composition seam — does NOT round-trip through JSON.
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
