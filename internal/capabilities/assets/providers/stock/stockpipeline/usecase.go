package stockpipeline

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/background"
)

// jobsEnqueuer is the narrowed surface of the jobs service that
// StockUseCase needs: just Enqueue. Mirrors the same-named narrowed
// surface in internal/application/clips (Wave 14 PR2 pattern); the
// worker registrant (Service.RegisterHandler) is unaffected.
//
// We use the canonical job.EnqueueRequest / *job.Job
// shape directly so the composition root can hand the concrete
// *jobs.Service straight in without an adapter wrapper. The
// *jobs.Job.ID return field is read without any custom projection.
type jobsEnqueuer interface {
	Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error)
}

// ServiceRunner is the narrowed surface of stockpipeline.Service that
// StockUseCase needs: just Run. Lets tests stub the runner without the
// full ffmpeg/render composition graph.
type ServiceRunner interface {
	Run(ctx context.Context, input *RunInput) (*PipelineResult, error)
}

// StockUseCase centralises stock-pipeline submission. It owns the
// decision of routing to the jobs service (async, the canonical path
// from S1b/S1a) versus running synchronously (test fixtures and partial
// deploys only — production callers MUST wire jobsSvc).
//
// Previously, the api handler branched on `if h.jobsSvc == nil { ... }`
// inline. That duplicated dispatch logic in two endpoints and made it
// impossible to test the dispatch decision in isolation. StockUseCase
// collapses it to a single Submit entry point.
type StockUseCase struct {
	service ServiceRunner
	jobsSvc jobsEnqueuer
	log     *zap.Logger
}

// NewStockUseCase constructs the canonical use case. Pass nil for jobsSvc
// only in test fixtures — production callers MUST wire the concrete
// jobs service so Submit can route through the broker pool.
func NewStockUseCase(service ServiceRunner, jobsSvc jobsEnqueuer, log *zap.Logger) *StockUseCase {
	if log == nil {
		log = zap.NewNop()
	}
	return &StockUseCase{
		service: service,
		jobsSvc: jobsSvc,
		log:     log,
	}
}

// ErrJobsServiceRequired is returned by Submit when async=true and no
// jobs service is wired. Callers convert it to a 503 at the API
// boundary; the canonical message mirrors the S1b wording for the
// clips.Cleanup path (matches: `cleanup requires jobs service (no sync
// pagination fallback — use POST /:source/cleanup)`).
var ErrJobsServiceRequired = errors.New("stock: jobs service required for async submit (S2b removed the in-process sync fallback)")

// Submit dispatches a stock pipeline run.
//
//	async=true  + jobsSvc wired → enqueue a media.stock job via the
//	             broker pool; returns the assigned job ID.
//	async=true  + jobsSvc nil   → ErrJobsServiceRequired (matches the
//	             S1b+S1c pattern: fail loud, no in-process fallback).
//	async=false + jobsSvc nil   → run synchronously via the runner
//	             (test fixture + partial deploy).
//	async=false + jobsSvc wired → run synchronously anyway — the
//	             operator asked for synchronous, the runner is the
//	             canonical answer.
//
// On success, returns (jobID, nil) for async paths and ("", nil) for
// synchronous paths. Run-result inspection is a separate verify-clip
// concern; the use case returns the dispatch receipt only.
func (u *StockUseCase) Submit(ctx context.Context, cmd *StockCommand, async bool) (string, error) {
	if cmd == nil {
		return "", errors.New("stock: Submit: nil *StockCommand")
	}

	if async {
		if u.jobsSvc == nil {
			return "", ErrJobsServiceRequired
		}
		enqueueCtx, cancel := background.DetachWithTimeout(ctx, "stock-submit-enqueue", 30*time.Second)
		defer cancel()

		job, err := u.jobsSvc.Enqueue(enqueueCtx, &job.EnqueueRequest{
			Type:    "media.stock",
			Payload: cmd.ToJobPayload(),
		})
		if err != nil {
			u.log.Error("stock use case: failed to enqueue", zap.Error(err))
			return "", err
		}
		u.log.Info("stock use case: enqueued",
			zap.String("job_id", job.ID),
			zap.Int("search_queries", len(cmd.SearchQueries)),
			zap.Int("direct_urls", len(cmd.DirectURLs)),
			zap.Int("drive_urls", len(cmd.DriveURLs)),
			zap.Int("clips", len(cmd.Clips)),
			zap.Int("total_minutes", cmd.TotalMinutes),
		)
		return job.ID, nil
	}

	if u.service == nil {
		return "", errors.New("stock: Submit: service runner not wired")
	}
	u.log.Info("stock use case: running synchronously",
		zap.Int("search_queries", len(cmd.SearchQueries)),
		zap.Int("direct_urls", len(cmd.DirectURLs)),
		zap.Int("drive_urls", len(cmd.DriveURLs)),
		zap.Int("clips", len(cmd.Clips)),
		zap.Int("total_minutes", cmd.TotalMinutes),
	)
	if _, err := u.service.Run(ctx, cmd.ToRunInput()); err != nil {
		u.log.Error("stock use case: sync run failed", zap.Error(err))
		return "", err
	}
	return "", nil
}

// Compile-time check that the concrete *stockpipeline.Service satisfies
// ServiceRunner. If the runner signature changes, this fails to compile
// and forces the use case + handler to be updated together.
var _ ServiceRunner = (*Service)(nil)
