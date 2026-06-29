// Package clips — clip_ops_handlers.go:
// Legacy home of the bulk_upload_youtube_clips job dispatcher.
//
// Split 4 (June 2026, override ADR 0009): HandleFixHash and
// updateCumulativeMetadataJSON moved into ops.go (Ops cluster).
// updateCumulativeMetadataJSON is also defined on *IngestHandler
// (ingest.go Split 2) so the original copy here is strictly
// orphaned code and was deleted.
//
// Split 5 (June 2026, BulkUpload cluster, not yet landed) will
// move HandleBulkUploadYouTubeClipsJob into a dedicated
// bulk_upload_transport.go receiver alongside the
// BulkUploadYouTubeClips HTTP entrypoint and the
// appclips.BulkUploadWorker. SourcesHandler.RegisterJobHandlers
// still wires h.jobsSvc.RegisterHandler("bulk_upload_youtube_clips",
// h.HandleBulkUploadYouTubeClipsJob) directly against the
// orchestrator *Handler, so the orchestrator surface stays stable
// for Split 5 to assume.
package clips

import (
	"context"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	domainjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// HandleBulkUploadYouTubeClipsJob is the bulk_upload_youtube_clips
// job dispatcher. Wired into the jobs system via
// (*Handler).RegisterJobHandlers. The substantive work lives in
// appclips.BulkUploadWorker (Split 5 territory).
func (h *Handler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *domainjob.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return h.bulkUploadWorker.HandleJob(ctx, j, tools)
}
