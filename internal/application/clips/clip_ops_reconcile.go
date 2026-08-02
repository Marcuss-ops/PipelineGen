package clips

import (
	"context"
	"fmt"

	jobcatalog "github.com/Marcuss-ops/PipelineGen/internal/domain/catalog"
	"go.uber.org/zap"
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
		Type: jobcatalog.TypeSync,
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

// knownCleanupSources is the canonical set of source names that
// the cleanup job accepts. Map lookup keeps the C2-C AST check deterministic.
// switch-case detection (godlike/06 SSOT co-located structural
// validation: the canonical source surface is artifacts.SourceCatalog;
// this map captures the cleanup-specific subset for jobs lifecycle).
var knownCleanupSources = map[string]struct{}{
	"all":       {},
	"voiceover": {},
	"images":    {},
	"youtube":   {},
	"artlist":   {},
	"clips":     {},
	"stock":     {},
}

// isKnownCleanupSource returns true when src (already
// lowercase-normalized by the caller) is one of the canonical
// clip-type source names. PR-CLIPS-DAPTER-RESOLVER-RETIRE (July
// 2026): the resolver check is removed — all clip-type sources
// route to the canonical clipRepo via the per-call source filter;
// the static map is now the SOLE canonical discriminator.
func (s *ClipOpsService) isKnownCleanupSource(src string) bool {
	_, ok := knownCleanupSources[src]
	return ok
}
