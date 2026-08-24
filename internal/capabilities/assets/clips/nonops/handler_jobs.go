// Package nonops — handler_jobs.go: RegisterJobHandlers +
// HandleBulkUploadYouTubeClipsJob extracted from
// clips/handler_delegators.go (RegisterJobHandlers) and
// clips/clip_ops_handlers.go (HandleBulkUploadYouTubeClipsJob) per
// PR-CLIPS-NONOPS-EXTRACT (July 2026). The 2 methods were split
// across 2 files in the parent package; this commit co-locates
// them in a single capability-specific file inside nonops.
//
// PR-CLIPS-NONOPS-FAIL-CLOSED (July 2026): the canonical 3-method
// registration chain is now enforced — see RegisterJobHandlers
// below. The legacy "Deprecated" back-compat surface is REMOVED;
// this is no longer a back-compat path but the canonical THIRD link
// of the production registration chain.
package assets

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// RegisterJobHandlers is the THIRD (innermost) link in the canonical
// 3-method job-handler registration chain (godlike/06 SSOT — one
// canonical owner per fact):
//
//  1. ClipsDescriptor.RegisterJobHandlers(svc api.JobRegistrar)
//     — composition-root entry point
//     (internal/api/assets/clips/module.go)
//  2. Handler.RegisterJobHandlers()
//     — 1-line delegator on the orchestrator *Handler
//     (internal/api/assets/clips/handler.go)
//  3. NonOpsHandler.RegisterJobHandlers() (this method)
//     — writes h.jobsSvc.RegisterHandler(string(TypeBulkUploadYouTubeClips), ...)
//
// The chain terminates at jobs.Service.RegisterHandler where the
// dispatcher's handler map gains the entry that lets the broker
// dispatch a bulk_upload_youtube_clips job to the orchestrator's
// HandleBulkUploadYouTubeClipsJob method (which 1-line-delegates
// back to nonops.HandleBulkUploadYouTubeClipsJob here).
//
// godlike/07 fail-closed: when JobsSvc is nil at this step the
// composition root either (a) skipped NewNonOpsHandlerStrict (used
// the legacy nil-tolerant NewNonOpsHandler path) — composition-time
// bug — or (b) reached this method from a non-canonical entry
// point. Either way the typed sentinel surfaces the dep here rather
// than letting the handler stay silently unregistered (which would
// fail at first enqueue with the generic
// `appjobs: no handler registered for the requested job type:
// bulk_upload_youtube_clips` message, with NO diagnostic pointing
// at the missing dep).
func (h *NonOpsHandler) RegisterJobHandlers() error {
	if h.jobsSvc == nil {
		return fmt.Errorf("%w: nonops.RegisterJobHandlers called with jobsSvc=nil (composition bug; NewNonOpsHandlerStrict must reject nil JobsSvc at construction; the legacy nil-tolerant NewNonOpsHandler is for test back-compat only)",
			jobs.ErrJobsSvcRequiredAtRegistration)
	}
	return h.jobsSvc.RegisterHandler(string(media.TypeBulkUploadYouTubeClips), jobs.HandlerFunc(h.HandleBulkUploadYouTubeClipsJob))
}

// HandleBulkUploadYouTubeClipsJob is the bulk_upload_youtube_clips
// job dispatcher. Wired into the jobs system via
// (*NonOpsHandler).RegisterJobHandlers. The substantive work lives
// in appclips.BulkUploadWorker. The orchestrator *Handler exposes
// a 1-line delegator (clips.Handler.HandleBulkUploadYouTubeClipsJob)
// for module.go::ClipsDescriptor.RegisterJobHandlers to consume
// without a direct nonops.Handler import.
func (h *NonOpsHandler) HandleBulkUploadYouTubeClipsJob(ctx context.Context, j *kerneljob.Job, tools *jobs.JobTools) (map[string]any, error) {
	if h.bulkUploadWorker == nil {
		return nil, fmt.Errorf("bulk upload worker not configured")
	}
	return h.bulkUploadWorker.HandleJob(ctx, j, tools)
}
