package artlist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
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
//	DiscoverClips → ResolveDestination → BuildProcessInputs → ProcessBatch → PersistResults → IndexAsync
//
// P0.6 (June 2026): the previous EnrichAsync stage was deleted from the
// sequence. Background fire-and-forget enrichment violated the
// no-fake-availability rule (godlike/07) because failures could not be
// surfaced to the pipeline caller. P0.18 will reintroduce structured
// outbox-driven enrichment in a successive wave (see
// architecture/current.yaml#P0.18 for the ticket index).
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

	// Stage 5: Post-processing (persist). Stage 6 is a documented no-op
	// kept for sequence continuity — the canonical dispatcher took over
	// indexing inside stagePersistResults (see stageIndexAsync for the
	// no-op contract).
	o.stagePersistResults(ctx, resp)
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

// RunDefaults holds default values for request normalization.
type RunDefaults struct {
	DefaultRootFolderID string
	DefaultLimit        int
	MaxLimit            int
}

// maxSearchWords is the maximum number of words kept by normalizeSearchTerm.
const maxSearchWords = 6

// normalizeSearchTerm trims the term and keeps at most the first [maxSearchWords] words.
func normalizeSearchTerm(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return ""
	}

	parts := strings.Fields(term)
	if len(parts) > maxSearchWords {
		parts = parts[:maxSearchWords]
	}
	return strings.Join(parts, " ")
}

// normalizeSearchTermLower is like normalizeSearchTerm but also lowercases the result.
// Use this for cache keys and index lookups to guarantee case-insensitive matching.
func normalizeSearchTermLower(term string) string {
	return strings.ToLower(normalizeSearchTerm(term))
}

// NormalizeRunTagRequest normalizes a RunTagRequest using the provided defaults.
// This is the SINGLE normalization function that should be used everywhere:
// - Before dedup key generation
// - Before job enqueue
// - Before job execution
// - At the start of pipeline RunTag
func NormalizeRunTagRequest(req RunTagRequest, defaults RunDefaults) RunTagRequest {
	// Normalize term
	req.Term = normalizeSearchTerm(req.Term)

	// Normalize limit
	if req.Limit <= 0 {
		if defaults.DefaultLimit > 0 {
			req.Limit = defaults.DefaultLimit
		} else {
			req.Limit = 1
		}
	}
	if defaults.MaxLimit > 0 && req.Limit > defaults.MaxLimit {
		req.Limit = defaults.MaxLimit
	}

	// Normalize root folder ID
	req.RootFolderID = strings.TrimSpace(req.RootFolderID)
	if req.RootFolderID == "" && defaults.DefaultRootFolderID != "" {
		req.RootFolderID = defaults.DefaultRootFolderID
	}

	// Normalize strategy
	req.Strategy = string(asset.NormalizeStrategy(req.Strategy, false))

	// Normalize concurrency
	if req.Concurrency <= 0 {
		req.Concurrency = 3
	} else if req.Concurrency > 10 {
		req.Concurrency = 10
	}

	return req
}

// runDedupKey REMOVED in Commit B (FASE 5 follow-up, July 2026).
// The function delegates to pkg/idempotency.BuildKey("artlist-run", canonical)
// via the canonical RunDedupKey wrapper in types.go. The legacy private helper
// was byte-equivalent to the new idempotency.BuildKey pipeline
// (json.Marshal(canonical) → sha256.Sum256 → hex string) so the migration
// preserves in-flight queued jobs across the deploy.
//
// godlike/06 SSOT: per-provider RunDedupKey wrappers MUST delegate to
// idempotency.BuildKey. Ad-hoc concatenation in this package would defeat
// cross-entry-point dedup unification at the kernel job broker's UNIQUE
// index on `jobs.active_key`.

// ResolveRootFolderID determines the canonical root folder for Artlist jobs.
// Delegates to cfg.Drive.ArtlistFolder() which resolves MediaRootFolder > ArtlistRootFolder > "".
func ResolveRootFolderID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Drive.ArtlistFolder()
}
