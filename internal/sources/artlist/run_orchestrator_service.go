package artlist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	timeutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"
)

// RunOrchestratorService coordina l'esecuzione dei run Artlist
type RunOrchestratorService struct {
	svc *Service
}

// NewRunOrchestratorService crea un nuovo orchestratore di run
func NewRunOrchestratorService(svc *Service) *RunOrchestratorService {
	return &RunOrchestratorService{svc: svc}
}

// GetRunTag ottiene lo stato di un run esistente
func (o *RunOrchestratorService) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	if runID == "" {
		return nil, fmt.Errorf("runID is required")
	}

	job, err := o.svc.jobAdapter.GetJobByRunID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to get job for run %s: %w", runID, err)
	}

	if job == nil {
		return nil, fmt.Errorf("job not found for run %s", runID)
	}

	return o.svc.jobAdapter.jobToResponse(job), nil
}

// RunTag esegue la pipeline Artlist per un termine di ricerca.
// La pipeline è suddivisa in stage chiaramente separati per testabilità:
//
//	DiscoverClips → ResolveDestination → BuildProcessInputs → ProcessBatch → PersistResults → EnrichAsync → IndexAsync
func (o *RunOrchestratorService) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	resp := &RunTagResponse{
		OK:        true,
		Term:      strings.TrimSpace(req.Term),
		Strategy:  strings.TrimSpace(req.Strategy),
		DryRun:    req.DryRun,
		Requested: req.Limit,
		StartedAt: func() *string { t := timeutil.FormatRFC3339(time.Now()); return &t }(),
	}

	if resp.Term == "" {
		resp.OK = false
		resp.Error = "term is required"
		return resp, fmt.Errorf("term is required")
	}

	// Stage 1: Discover clips via live search
	discoveryResp, err := o.stageDiscoverClips(ctx, req, resp)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}
	// No candidates found — not an error, just empty
	if resp.Found == 0 {
		resp.OK = false
		if resp.Error == "" {
			resp.Error = "no candidates found"
		}
		return resp, nil
	}

	// Stage 2: Resolve destination Drive folder
	_, err = o.stageResolveDestination(ctx, req, resp)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}

	// Stage 3: Build process inputs from discovered clips (reuses discoveryResp from stage 1)

	workItems := o.stageBuildProcessInputs(ctx, req, resp, discoveryResp.Clips)
	if len(workItems) == 0 {
		return resp, nil // all were dry-run or skipped
	}

	// Stage 4: Process clips (parallel with bounded concurrency)
	ps := &pipelineState{
		resp:        resp,
		workItems:   workItems,
		concurrency: concurrencyFromRequest(req),
	}
	if err := o.stageProcessBatch(ctx, ps); err != nil {
		resp.OK = false
		resp.Error = err.Error()
		return resp, err
	}

	// Stages 5-7: Post-processing (persist, enrich, index)
	o.stagePersistResults(ctx, resp)
	o.stageEnrichAsync(ctx, resp)
	o.stageIndexAsync(ctx, resp)

	processedCount := resp.Processed
	o.svc.log.Info("artlist run completed",
		zap.String("term", resp.Term),
		zap.Int("concurrency", ps.concurrency),
		zap.Int("found", resp.Found),
		zap.Int("processed", processedCount),
		zap.Int("failed", resp.Failed),
		zap.Int("skipped", resp.Skipped),
	)

	if processedCount == 0 && resp.Failed > 0 && resp.Skipped == 0 {
		resp.OK = false
		resp.Error = "all artlist items failed"
	}

	resp.CompletedAt = func() *string { t := timeutil.FormatRFC3339(time.Now()); return &t }()
	return resp, nil
}
