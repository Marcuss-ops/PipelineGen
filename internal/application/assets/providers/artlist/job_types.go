package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
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
// method on Service preserves the facade consumed by worker and API code.
func (s *Service) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	if s == nil || s.runOrchestrator == nil {
		return nil, fmt.Errorf("artlist.Service.RunTag: run orchestrator is not configured")
	}
	return s.runOrchestrator.RunTag(ctx, req)
}

// HandleJob is the canonical worker-side Artlist path. Besides running the
// orchestrator it owns outcome evaluation, operator events and the mandatory
// artlist_runs aggregate write; returning success without that write is
// forbidden by the Gate 03 persistence contract.
func (a *JobAdapter) HandleJob(
	ctx context.Context,
	j *job.Job,
	tools *job.JobExecutionTools,
) (job.Result, error) {
	if a == nil || a.service == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.HandleJob: service is not configured")
	}
	if j == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.HandleJob: job is nil")
	}

	s := a.service
	eventFn := appjobs.SafeEventFn(tools)
	s.log.Info("handling artlist job",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
	)

	var payloadMap map[string]any
	if err := json.Unmarshal(j.Payload, &payloadMap); err != nil {
		return nil, fmt.Errorf("decode artlist job payload: %w", err)
	}
	codec := &JobCodec{}
	req := codec.RequestFromPayload(payloadMap)
	normalized := NormalizeRunTagRequest(*req, RunDefaults{
		DefaultRootFolderID: ResolveRootFolderID(s.cfg),
		MaxLimit:            500,
	})
	req = &normalized

	if strings.TrimSpace(req.RootFolderID) == "" {
		s.log.Warn("skipping artlist job because no root folder is configured",
			zap.String("job_id", j.ID),
			zap.String("term", req.Term),
		)
		eventFn("warning", "artlist job skipped: no root folder configured", map[string]any{
			"term": req.Term,
		})
		return job.Result{
			"skipped": 1,
			"reason":  "no root folder configured",
		}, nil
	}

	resp, err := s.RunTag(ctx, req)
	if err != nil || (resp != nil && !resp.OK) {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else if resp != nil {
			errMsg = resp.Error
		}
		if errMsg == "" {
			errMsg = "unknown error"
		}
		// PR-ARTLIST-RETRY-WAIT-DIAGNOSTIC (July 2026): attach the same
		// per-item Status histogram that logFailedItemBreakdown emits
		// to stdout so the failure breakdown is durable in
		// job_events.data_json (operators can audit future
		// RETRY_WAIT jobs WITHOUT re-running the search).
		counts, samples := failedItemBreakdown(resp)
		found, processed, skipped, failed := respCounters(resp)
		eventFn("error", "artlist run failed", map[string]any{
			"error":          errMsg,
			"found":          found,
			"processed":      processed,
			"skipped":        skipped,
			"failed":         failed,
			"status_counts":  counts,
			"status_samples": samples,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	if failed, errMsg := EvaluateRunOutcome(resp); failed {
		// Mirror the histogram into the EvaluateRunOutcome error path
		// (Policy B: undercounted / silent-loss signature).
		counts, samples := failedItemBreakdown(resp)
		found, processed, skipped, _ := respCounters(resp)
		eventFn("error", errMsg, map[string]any{
			"failed":         resp.Failed,
			"found":          found,
			"processed":      processed,
			"skipped":        skipped,
			"status_counts":  counts,
			"status_samples": samples,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	if s.runRepo != nil && resp != nil {
		runRecord := buildRunRecordFromResponse(j.ID, resp)
		if err := s.runRepo.Record(ctx, runRecord); err != nil {
			eventFn("error", "artlist_runs aggregate write failed", map[string]any{
				"run_id": j.ID,
				"error":  err.Error(),
			})
			s.log.Error("artlist_runs aggregate write failed",
				zap.String("job_id", j.ID),
				zap.String("term", resp.Term),
				zap.Error(err),
			)
			return nil, fmt.Errorf("artlist_runs.Record(run_id=%q): %w", j.ID, err)
		}
	}

	eventFn("completed", "artlist run completed", map[string]any{
		"found":     resp.Found,
		"processed": resp.Processed,
		"skipped":   resp.Skipped,
		"failed":    resp.Failed,
	})
	return codec.ResultFromResponse(resp), nil
}

// RegisterHandler binds the Artlist consumer using the media domain's
// canonical discriminator and the shared kernel handler contract.
// HandleCacheRefresh executes the durable stale-cache refresh job. It is
// deliberately separate from HandleJob: a refresh fetches only provider
// candidates and updates the Artlist search cache; it must not run the full
// download/upload Artlist pipeline.
func (a *JobAdapter) HandleCacheRefresh(
	ctx context.Context,
	j *job.Job,
	tools *job.JobExecutionTools,
) (job.Result, error) {
	_ = tools
	if a == nil || a.service == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.HandleCacheRefresh: service is not configured")
	}
	if j == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.HandleCacheRefresh: job is nil")
	}
	if a.service.scraperSearcher == nil {
		return nil, ErrUnavailable
	}
	if a.service.liveCache == nil {
		return nil, fmt.Errorf("artlist.JobAdapter.HandleCacheRefresh: cache is not configured")
	}

	payload, err := appjobs.DecodePayload[appjobs.ArtlistCacheRefreshPayload](j)
	if err != nil {
		return nil, fmt.Errorf("decode Artlist cache refresh payload: %w", err)
	}
	term := normalizeSearchTermLower(payload.Term)
	if term == "" {
		return nil, fmt.Errorf("%w: cache refresh term is required", ErrEmpty)
	}
	limit := payload.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	candidates, err := a.service.scraperSearcher.Search(ctx, SearchRequest{
		Term:         term,
		Limit:        limit,
		PreferRemote: payload.PreferRemote,
		ForceRefresh: true,
	})
	if err != nil {
		return nil, fmt.Errorf("Artlist cache refresh search for %q: %w", term, err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: Artlist cache refresh returned no candidates for %q", ErrEmptyResult, term)
	}

	if err := a.service.liveCache.setWithContext(ctx, term, candidates); err != nil {
		return nil, fmt.Errorf("persist Artlist cache refresh for %q: %w", term, err)
	}
	if a.service.log != nil {
		a.service.log.Info("Artlist cache refresh completed",
			zap.String("term", term),
			zap.Int("clips", len(candidates)),
			zap.String("job_id", j.ID),
		)
	}
	return job.Result{
		"term":  term,
		"clips": len(candidates),
	}, nil
}

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
	if err := jobsSvc.RegisterHandler(media.TypeArtlistCacheRefresh, appjobs.HandlerFunc(a.HandleCacheRefresh)); err != nil {
		return fmt.Errorf("artlist.JobAdapter.RegisterHandler: bind %q: %w", media.TypeArtlistCacheRefresh, err)
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
