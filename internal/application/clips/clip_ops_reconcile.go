package clips

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// Reconcile reconciles database with Drive files via a real
// catalog.sync job. PR-3 (June 2026): the previous log-only stub
// has been removed — every Reconcile call now enqueues a durable
// catalog.sync job that a worker consumes from the broker pool.
// Fail-closed: every non-success path returns the typed sentinel
// ErrReconcileQueueUnavailable so the HTTP handler can map it to
// 503 + RECONCILE_QUEUE_UNAVAILABLE instead of presenting a fake
// "ok" response.
func (s *ClipOpsService) Reconcile(ctx context.Context, source, folderID string) (*ReconcileResult, error) {
	if s.jobs == nil {
		return nil, ErrReconcileQueueUnavailable
	}
	resp, err := s.jobs.Enqueue(ctx, JobsEnqueueRequest{
		Type: job.TypeCatalogSync,
		Payload: map[string]any{
			"source":     source,
			"folder_id":  folderID,
			"force_full": true,
		},
		Priority: 5,
	})
	if err != nil {
		if s.log != nil {
			s.log.Error("reconcile: enqueue catalog.sync failed (broker unreachable / rejected)",
				zap.String("source", source),
				zap.String("folder_id", folderID),
				zap.Error(err))
		}
		return nil, fmt.Errorf("%w: %v", ErrReconcileQueueUnavailable, err)
	}
	if s.log != nil {
		s.log.Info("reconcile: catalog.sync job enqueued",
			zap.String("source", source),
			zap.String("folder_id", folderID),
			zap.String("job_id", resp.ID))
	}
	return &ReconcileResult{JobID: resp.ID}, nil
}

// isKnownCleanupSource returns true when src (already
// lowercase-normalized by the caller) matches one of the
// canonical static global cleanup scopes or resolves via
// s.sourceResolver to a registered clip repo.
func (s *ClipOpsService) isKnownCleanupSource(src string) bool {
	switch src {
	case "all", "voiceover", "images":
		return true
	}
	return s.sourceResolver != nil && s.sourceResolver.ResolveRepo(src) != nil
}
