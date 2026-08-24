// Package stock is the parent application-level orchestration layer for
// the stock video pipeline. It hosts the canonical Submit use case
// (SubmitStockPipelineUseCase) and the API→domain converter with
// input validation (FromAPIRequest). The runner / port types live in
// the stockpipeline child package.
//
// S2a (June 2026): re-exposes the canonical StockCommand and
// StockSearchAndRunRequest from stockpipeline through type aliases so
// the api layer binds JSON onto these types without a local mirror.
// The use case here is the rich-return coordinator that the api
// handler delegates to; pre-S2a the api handler drove dispatch
// inline (with a stray ListClipsPaged(10000) sync fallback that
// was removed in S2b).
package assets

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Type aliases (SSOT preservation across the parent/child split) ──
//
// StockCommand is canonical in stockpipeline (the package that owns
// the runner + RunInput + ChunkMetadataInput shape). Re-exposed via
// type alias here so api-layer code can import `stock.StockCommand`
// without duplicating the struct or risking drift on a future
// field add. The two paths resolve to the same underlying type at
// compile time — no wrapping, no projection, no copy.
type (
	StockCommand             = stockpipeline.StockCommand
	StockSearchAndRunRequest = stockpipeline.StockSearchAndRunRequest
	ChunkMetadataInput       = stockpipeline.ChunkMetadataInput
)

// ── SubmitResult ────────────────────────────────────────────────────────
//
// SubmitResult is the rich return shape spec'd in S2a. It carries the
// dispatch receipt (JobID, Status) on async paths and the runtime
// summary (TotalClips, TotalChunks, Chunks) on sync paths. Error is
// populated whenever the submit ends in a non-success terminal state;
// the returned error mirrors Error for errors.Is classification.
type SubmitResult struct {
	JobID       string                      `json:"job_id,omitempty"`
	Status      string                      `json:"status"`
	TotalClips  int                         `json:"total_clips,omitempty"`
	TotalChunks int                         `json:"total_chunks,omitempty"`
	Chunks      []stockpipeline.ChunkResult `json:"chunks,omitempty"`
	Error       string                      `json:"error,omitempty"`
}

// ── FromAPIRequest (api → StockCommand with validation) ──────────────
//
// FromAPIRequest converts the search-and-run request body into a
// StockCommand AND applies the canonical domain validation rules
// that previously lived inline in the api handler:
//
//   - ClipDuration: 0 means "no clip-duration override"; otherwise
//     must be in [3, 30] seconds. The pipeline rejects values outside
//     this range as unrunnable (clips < 3s are too short for
//     transitions / effects; clips > 30s blow the chunk envelope).
//   - TotalMinutes: 0 (or negative) means "use default 5 minutes".
//
// Validation is co-located with the domain constructor so the api
// handler can pass raw user input through and rely on the result.
// Errors are surfaced to the api boundary as 400 Bad Request.
func FromAPIRequest(req *StockSearchAndRunRequest) (*StockCommand, error) {
	if req == nil {
		return nil, errors.New("stock: FromAPIRequest: nil *StockSearchAndRunRequest")
	}
	if req.ClipDuration != 0 && (req.ClipDuration < 3 || req.ClipDuration > 30) {
		return nil, fmt.Errorf("stock: clip_duration must be between 3 and 30 seconds (got %d)", req.ClipDuration)
	}
	if err := stockpipeline.ValidateDurationContract(req.TargetTotalDurationSeconds, req.TargetDurationPerSourceSeconds, req.ClipsPerSource, req.ClipDurationSeconds, req.DownloadMode); err != nil {
		return nil, err
	}
	// Mutate-in-place via a local copy so we don't surprise callers
	// who hold the request pointer post-binding.
	cloned := *req
	if cloned.TotalMinutes <= 0 {
		cloned.TotalMinutes = 5
	}
	cmd, err := stockpipeline.FromSearchAndRunRequest(&cloned)
	if err != nil {
		return nil, fmt.Errorf("stock: FromAPIRequest: %w", err)
	}
	return cmd, nil
}

// ── SubmitStockPipelineUseCase ──────────────────────────────────────────

// SubmitStockPipelineUseCase centralises stock-pipeline submission.
// It owns the dispatch decision between async (broker pool, canonical
// production path) and sync (test fixtures + partial deploys), and
// returns the rich SubmitResult receipt on every code path.
//
// Concrete types (per S2a spec — `*stockpipeline.Service`,
// `job.Service`) match the user spec exactly; tests can stub via
// narrower interfaces if needed in a follow-up (the S2b pattern of
// ServiceRunner / jobsEnqueuer narrowed interfaces is preserved
// internally for composition but not exported here to avoid the
// package needing to duplicate the same interface twice).
type SubmitStockPipelineUseCase struct {
	svc  *stockpipeline.Service
	jobs job.Service
	log  *zap.Logger
}

// NewSubmitStockPipelineUseCase constructs the canonical use case.
// Pass nil for jobs only in test fixtures — production callers MUST
// wire the concrete jobs service so Submit can route through the
// broker pool. The log is never nil (falls back to zap.NewNop()).
func NewSubmitStockPipelineUseCase(svc *stockpipeline.Service, jobs job.Service, log *zap.Logger) *SubmitStockPipelineUseCase {
	if log == nil {
		log = zap.NewNop()
	}
	return &SubmitStockPipelineUseCase{
		svc:  svc,
		jobs: jobs,
		log:  log,
	}
}

// ErrJobsServiceRequired is returned by Submit when async=true and no
// jobs service is wired. Matches the S1b+S1c+S2b wording pattern
// (fail loud, no in-process fallback). Callers convert it to a 503
// at the api boundary.
var ErrJobsServiceRequired = errors.New("stock: jobs service required for async submit (S2a/S2b removed the in-process sync fallback)")

// Submit dispatches a stock pipeline run.
//
//	async=true  + jobs wired → enqueue a media.stock job; return
//	             SubmitResult{JobID, Status: "enqueued"}.
//	async=true  + jobs nil   → SubmitResult{Status: "rejected",
//	             Error: "...jobs required..."}, ErrJobsServiceRequired.
//	async=true  + enqueue FAILED → SubmitResult{Status: "error",
//	             Error: err.Error()}, err.
//	async=false             → run synchronously via svc.Run;
//	             return SubmitResult projected from *PipelineResult,
//	             or SubmitResult{Status: "error", Error: err.Error()}
//	             on failure.
//
// Spec'd signature: `Submit(ctx, cmd, async) (*SubmitResult, error)`.
// The returned error is non-nil on any non-success terminal state;
// SubmitResult.Error is the human-readable mirror for cases where
// the api layer prefers a soft-OK + inline error body.
func (u *SubmitStockPipelineUseCase) Submit(ctx context.Context, cmd *StockCommand, async bool) (*SubmitResult, error) {
	if cmd == nil {
		return &SubmitResult{Status: "error", Error: "stock: Submit: nil *StockCommand"},
			errors.New("stock: Submit: nil *StockCommand")
	}

	if async {
		if u.jobs == nil {
			err := ErrJobsServiceRequired
			return &SubmitResult{
				Status: "rejected",
				Error:  err.Error(),
			}, err
		}
		jobRef, err := u.jobs.Enqueue(ctx, &job.EnqueueRequest{
			Type:    "media.stock",
			Payload: cmd.ToJobPayload(),
		})
		if err != nil {
			u.log.Error("submit stock pipeline: enqueue failed", zap.Error(err))
			return &SubmitResult{
				Status: "error",
				Error:  err.Error(),
			}, err
		}
		u.log.Info("submit stock pipeline: enqueued",
			zap.String("job_id", jobRef.ID),
			zap.Int("search_queries", len(cmd.SearchQueries)),
			zap.Int("direct_urls", len(cmd.DirectURLs)),
			zap.Int("total_minutes", cmd.TotalMinutes),
		)
		return &SubmitResult{
			JobID:  jobRef.ID,
			Status: "enqueued",
		}, nil
	}

	if u.svc == nil {
		err := errors.New("stock: Submit: stockpipeline service not wired")
		return &SubmitResult{Status: "error", Error: err.Error()}, err
	}
	u.log.Info("submit stock pipeline: running synchronously",
		zap.Int("search_queries", len(cmd.SearchQueries)),
		zap.Int("direct_urls", len(cmd.DirectURLs)),
		zap.Int("total_minutes", cmd.TotalMinutes),
	)
	runInput := cmd.ToRunInput()
	if runInput == nil {
		err := errors.New("stock: Submit: nil RunInput from cmd.ToRunInput()")
		return &SubmitResult{Status: "error", Error: err.Error()}, err
	}
	pr, err := u.svc.Run(ctx, runInput)
	if err != nil {
		u.log.Error("submit stock pipeline: sync run failed", zap.Error(err))
		return &SubmitResult{
			Status: "error",
			Error:  err.Error(),
		}, err
	}
	return &SubmitResult{
		Status:      "completed",
		TotalClips:  pr.TotalClips,
		TotalChunks: pr.TotalChunks,
		Chunks:      pr.Chunks,
	}, nil
}
