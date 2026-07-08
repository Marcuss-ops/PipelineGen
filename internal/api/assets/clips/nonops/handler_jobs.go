// Package nonops — handler_jobs.go: RegisterJobHandlers +
// HandleBulkUploadYouTubeClipsJob extracted from
// clips/handler_delegators.go (RegisterJobHandlers) and
// clips/clip_ops_handlers.go (HandleBulkUploadYouTubeClipsJob) per
// PR-CLIPS-NONOPS-EXTRACT (July 2026). The 2 methods were split
// across 2 files in the parent package; this commit co-locates
// them in a single capability-specific file inside nonops.
package nonops

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// RegisterJobHandlers wires up the bulk-upload worker.
// Deprecated: ClipsDescriptor.RegisterJobHandlers (clips/module.go)
// is the canonical DescriptorJobs path that the production
// composition root invokes. This method survives for
// test-backward-compat only.
func (h *NonOpsHandler) RegisterJobHandlers() error {
	if h.jobsSvc == nil {
		return nil
	}
	return h.jobsSvc.RegisterHandler(string(jobservice.TypeBulkUploadYouTubeClips), jobs.HandlerFunc(h.HandleBulkUploadYouTubeClipsJob))
}

// HandleBulkUploadYouTubeClipsJob is the bulk_upload_youtube_clips
// job dispatcher. Wired into the jobs system via
// (*NonOpsHandler).RegisterJobHandlers. The substantive work lives
// in appclips.BulkUploadWorker. The orchestrator *Handler exposes
// a 1-line delegator (clips.Handler.HandleBulkUploadYouTubeClipsJob)
// for module.go::ClipsDescriptor.RegisterJobHandlers to consume
// without a direct nonops.Handler import.
func (h *NonOpsHandler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *jobservice.Job, tools *jobs.JobTools) (map[string]any, error) {
	if h.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return h.bulkUploadWorker.HandleJob(ctx, j, tools)
}
