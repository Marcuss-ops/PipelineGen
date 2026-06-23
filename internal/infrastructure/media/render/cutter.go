// Package render — concrete VideoCutter port implementation (PR6, June 2026).
//
// This adapter owns the batch-then-fallback-to-individual FFmpeg logic
// that previously lived in stockpipeline.process.go's processSingleVideo.
// It also lifts disk verification (os.Stat) into the returned
// CutResult.ProducedPaths so the application layer no longer imports
// `os` for verification.
//
// Import-boundary invariant (AGENTS.md Pattern 0 + Pattern 8):
//   * internal/application/** MUST NOT import this package.
//   * This package MAY import the stock port types from the application
//     layer (hexagonal: app owns the port, infra owns the adapter).
//
// Behavioural equivalence with pre-PR6:
//   1. Try CutReencodeBatch (single FFmpeg invocation producing N clips)
//   2. On batch failure, fall back to per-clip CutReencode invocations
//   3. Verify each produced output exists on disk (os.Stat) and only
//      include it in the result when present.
package render

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// FFmpegCutter is the canonical concrete implementation of the
// VideoCutter port. It wraps the existing ffmpeg.Processor (kept as
// an internal collaborator so the well-tested cut-arg-assembly is
// preserved) AND lifts the batch/fallback + disk-verification logic
// out of the application layer.
type FFmpegCutter struct {
	proc *ffmpeg.Processor
	log  *zap.Logger
}

// NewFFmpegCutter constructs the cutter with the canonical binary
// path + logger. The processor is built lazily from the supplied
// ffmpeg path (empty path defaults to "ffmpeg" via ffmpeg.NewProcessor).
func NewFFmpegCutter(ffmpegPath string, log *zap.Logger) *FFmpegCutter {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &FFmpegCutter{
		proc: ffmpeg.NewProcessor(ffmpegPath),
		log:  log,
	}
}

// Compile-time check that FFmpegCutter satisfies VideoCutter.
var _ stockpipeline.VideoCutter = (*FFmpegCutter)(nil)

// toInternalCutJobs adapts the neutral stockpipeline.CutJob list to the
// ffmpeg package's CutJob struct (which the existing Processor expects).
func toInternalCutJobs(jobs []stockpipeline.CutJob) []ffmpeg.CutJob {
	out := make([]ffmpeg.CutJob, len(jobs))
	for i, j := range jobs {
		out[i] = ffmpeg.CutJob{
			StartSec: j.StartSec,
			EndSec:   j.EndSec,
			Output:   j.OutputPath,
		}
	}
	return out
}

// Cut is the concrete port implementation. It attempts the batch cut
// first; on failure, it falls back to per-clip sequential cuts and
// returns the union of successfully-produced clip paths.
func (c *FFmpegCutter) Cut(ctx context.Context, req stockpipeline.CutRequest) (stockpipeline.CutResult, error) {
	if req.SourcePath == "" {
		return stockpipeline.CutResult{}, fmt.Errorf("cutter: empty source path")
	}
	if len(req.Jobs) == 0 {
		// No work to do — no-op success.
		return stockpipeline.CutResult{}, nil
	}

	logger := c.log
	if req.Logger != nil {
		logger = req.Logger
	}

	internalJobs := toInternalCutJobs(req.Jobs)
	produced := make([]string, 0, len(internalJobs))

	// ── Attempt 1: single-pass batch cut ──────────────────────────────
	logger.Info("stock extractor: single-pass batch cut starting",
		zap.Int("source_index", req.SourceIdx),
		zap.Int("clip_count", len(internalJobs)),
		zap.Bool("no_audio", req.NoAudio),
		zap.String("codec", req.Codec),
	)

	batchErr := c.proc.CutReencodeBatch(
		ctx, req.SourcePath, internalJobs, req.NoAudio,
		req.Codec, req.Preset, req.CRF,
	)

	if batchErr == nil {
		// Batch succeeded: verify + collect produced paths.
		for _, j := range internalJobs {
			if _, err := os.Stat(j.Output); err == nil {
				produced = append(produced, j.Output)
			}
		}
		logger.Info("stock extractor: single-pass batch cut succeeded",
			zap.Int("source_index", req.SourceIdx),
			zap.Int("clips_produced", len(produced)),
		)
		return stockpipeline.CutResult{ProducedPaths: produced}, nil
	}

	// ── Attempt 2: per-clip fallback ─────────────────────────────────
	logger.Warn("stock extractor: batch cut failed, falling back to individual cuts",
		zap.Int("source_index", req.SourceIdx),
		zap.Error(batchErr),
	)

	var lastErr error
	for i, j := range internalJobs {
		select {
		case <-ctx.Done():
			return stockpipeline.CutResult{ProducedPaths: produced}, ctx.Err()
		default:
		}
		cutErr := c.proc.CutReencode(
			ctx, req.SourcePath, j.Output,
			ffmpeg.FormatSec(j.StartSec), ffmpeg.FormatSec(j.EndSec),
			req.NoAudio, req.Codec, req.Preset, req.CRF,
		)
		if cutErr != nil {
			logger.Warn("stock extractor: fallback cut failed",
				zap.Int("source_index", req.SourceIdx),
				zap.Int("clip_index", i),
				zap.Error(cutErr),
			)
			lastErr = cutErr
			continue
		}
		if _, err := os.Stat(j.Output); err == nil {
			produced = append(produced, j.Output)
		}
	}

	logger.Info("stock extractor: per-clip fallback completed",
		zap.Int("source_index", req.SourceIdx),
		zap.Int("clips_produced", len(produced)),
	)

	if len(produced) == 0 && lastErr != nil {
		return stockpipeline.CutResult{}, fmt.Errorf("cutter: all jobs failed; last err: %w", lastErr)
	}
	return stockpipeline.CutResult{ProducedPaths: produced}, nil
}

