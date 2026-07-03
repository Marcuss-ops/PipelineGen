// Package render — concrete VideoCutter port implementation (PR6, June 2026; FASE 2.4, July 2026).
//
// This adapter owns the batch-then-fallback-to-individual FFmpeg logic
// that previously lived in stockpipeline.process.go's processSingleVideo,
// the per-job on-disk verification, and (FASE 2.4) the per-clip ffprobe
// validation step that lifts DurationSec + SizeBytes into the structured
// CutBatchResult surface.
//
// Import-boundary invariant (AGENTS.md Pattern 0 + Pattern 8):
//   - internal/application/** MUST NOT import this package.
//   - This package MAY import the stock port types from the application
//     layer (hexagonal: app owns the port, infra owns the adapter).
//
// Behavioural equivalence with pre-PR6:
//  1. Try CutReencodeBatch (single FFmpeg invocation producing N clips)
//  2. On batch failure, fall back to per-clip CutReencode invocations
//  3. Verify each produced output exists on disk (os.Stat)
//  4. (FASE 2.4) Run ffprobe on every produced clip to populate
//     DurationSec; flip Status to Validated on success.
//
// FASE 2.4 (July 2026): the legacy CutResult.{ProducedPaths-only} contract
// is replaced with CutBatchResult (always non-nil, len(Items)=len(input.Jobs)).
// Per-job failure detail (Err, JobID) is exposed to the application layer
// without forcing the caller to guess which clip failed.
package render

import (
	"context"
	"errors"
	"fmt"
	"os"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// FFmpegCutter is the canonical concrete implementation of the
// VideoCutter port. It wraps the existing ffmpeg.Processor (kept as
// an internal collaborator so the well-tested cut-arg-assembly is
// preserved) AND lifts the batch/fallback + disk-verification + ffprobe
// logic out of the application layer.
type FFmpegCutter struct {
	proc *ffmpeg.Processor
	log  *zap.Logger
	// probeAfterCut toggles the ffprobe-validation pass; default true.
	// Slice with NewFFmpegCutterOnlyCut when a watcher path needs to
	// skip probe (typically the staging run where ProbeError is
	// irrelevant — the file is re-staged, not validated).
	probeAfterCut bool
}

// NewFFmpegCutter constructs the cutter with the canonical binary
// path + logger + ffprobe-after-cut enabled. The processor is built
// lazily from the supplied ffmpeg path (empty path defaults to
// "ffmpeg" via ffmpeg.NewProcessor).
func NewFFmpegCutter(ffmpegPath string, log *zap.Logger) *FFmpegCutter {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &FFmpegCutter{
		proc:          ffmpeg.NewProcessor(ffmpegPath),
		log:           log,
		probeAfterCut: true,
	}
}

// NewFFmpegCutterOnlyCut constructs the cutter with the canonical
// binary path + logger + ffprobe-after-cut disabled. Used by tests
// that assemble a tiny synthetic clip and want to assert just the
// cut behaviour (and not the ffprobe-validation gate).
func NewFFmpegCutterOnlyCut(ffmpegPath string, log *zap.Logger) *FFmpegCutter {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &FFmpegCutter{
		proc:          ffmpeg.NewProcessor(ffmpegPath),
		log:           log,
		probeAfterCut: false,
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
//
// FASE 2.4 contract:
//   - Returns CutBatchResult with len(Items) == len(req.Jobs) ALWAYS.
//   - Each Item carries Status / SizeBytes / DurationSec / Err
//     according to the per-job outcome.
//   - ffprobe runs on every produced clip (when probeAfterCut=true):
//     Status flips from Succeeded to Validated on success; failures
//     keep Status=Succeeded with Err set (soft-fail — file is on disk
//     and ffprobe is informational, not blocking).
//   - Top-level err is non-nil only when EVERY job failed; partial-
//     success batches return nil error so the caller iterates Items
//     to partition succeeded / failed.
func (c *FFmpegCutter) Cut(ctx context.Context, req stockpipeline.CutRequest) (stockpipeline.CutBatchResult, error) {
	items := make([]stockpipeline.CutItemResult, len(req.Jobs))
	for i, j := range req.Jobs {
		items[i] = stockpipeline.CutItemResult{JobID: j.OutputPath}
	}

	if req.SourcePath == "" {
		// Mark every item failed with a single shared error; the
		// batch-level err echoes the same sentinel wrapped.
		err := errors.New("cutter: empty source path")
		for i := range items {
			items[i].Status = stockpipeline.CutItemStatusFailed
			items[i].Err = err
		}
		return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, err
	}
	if len(req.Jobs) == 0 {
		// No work to do — empty batch is a success (len(items)==0).
		return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
	}

	logger := c.log
	if req.Logger != nil {
		logger = req.Logger
	}

	internalJobs := toInternalCutJobs(req.Jobs)
	producedIdx := make([]int, 0, len(internalJobs)) // indices into items where a file is on disk

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
		// Batch succeeded — verify on disk + record produced indices.
		for i, j := range internalJobs {
			if info, statErr := os.Stat(j.Output); statErr == nil {
				producedIdx = append(producedIdx, i)
				items[i].OutputPath = j.Output
				items[i].SizeBytes = info.Size()
				items[i].Status = stockpipeline.CutItemStatusSucceeded
			} else {
				// ffmpeg exited 0 but the file is missing — this is
				// a partial-success: the batch cmd "succeeded" but
				// the produced file is gone (simulated cleanup race,
				// disk full, antivirus deletion). Mark Failed for
				// honesty; the top-level err stays nil.
				items[i].Status = stockpipeline.CutItemStatusFailed
				items[i].Err = fmt.Errorf("batch reported success but file missing: %w", statErr)
			}
		}
		c.runProbe(ctx, logger, items, producedIdx)
		logger.Info("stock extractor: single-pass batch cut completed",
			zap.Int("source_index", req.SourceIdx),
			zap.Int("clips_produced", len(producedIdx)),
		)
		return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, c.batchErr(logger, items)
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
			// Context cancelled: mark every still-unprocessed item as
			// failed with ctx.Err; iterate only over the un-cut suffix.
			for k := i; k < len(items); k++ {
				if items[k].Status == stockpipeline.CutItemStatusUnknown {
					items[k].Status = stockpipeline.CutItemStatusFailed
					items[k].Err = ctx.Err()
				}
			}
			return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, ctx.Err()
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
			items[i].Status = stockpipeline.CutItemStatusFailed
			items[i].Err = cutErr
			lastErr = cutErr
			continue
		}
		if info, statErr := os.Stat(j.Output); statErr == nil {
			items[i].OutputPath = j.Output
			items[i].SizeBytes = info.Size()
			items[i].Status = stockpipeline.CutItemStatusSucceeded
			producedIdx = append(producedIdx, i)
		} else {
			items[i].Status = stockpipeline.CutItemStatusFailed
			items[i].Err = fmt.Errorf("fallback reported success but file missing: %w", statErr)
			lastErr = statErr
		}
	}
	c.runProbe(ctx, logger, items, producedIdx)

	logger.Info("stock extractor: per-clip fallback completed",
		zap.Int("source_index", req.SourceIdx),
		zap.Int("clips_produced", len(producedIdx)),
	)
	return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, c.batchErr(logger, items, lastErr)
}

// runProbe runs the ffprobe validation pass on every produced clip,
// flipping Status from Succeeded to Validated on success. Probe
// failures flip Status to ProbeFailed (with Err set) so downstream
// SuccessfulItems partitions correctly.
//
// Concurrent: NOT safe — Cut callers do not parallelise per-job
// (the ffmpeg batch-then-fallback ladder is sequential). If a
// future port re-introduces parallel probes, this method needs
// a per-clip goroutine + WaitGroup to avoid blocking on the probe
// timeout. Today's call-surface stays sequential for simplicity.
func (c *FFmpegCutter) runProbe(ctx context.Context, logger *zap.Logger, items []stockpipeline.CutItemResult, producedIdx []int) {
	if !c.probeAfterCut {
		return
	}
	// Honuor the upstream context — after a long batch +
	// per-clip fallback path, the parent ctx may already be
	// cancelled; sequential probes that ignore ctx could block
	// for up to 2*time.Minute per clip before noticing the
	// cancel. Bail fast so the broker's job-cancel path is
	// responsive regardless of probe queue depth.
	select {
	case <-ctx.Done():
		for _, i := range producedIdx {
			it := &items[i]
			if it.Status == stockpipeline.CutItemStatusSucceeded {
				it.Status = stockpipeline.CutItemStatusProbeFailed
				it.Err = fmt.Errorf("ffprobe validation cancelled: %w", ctx.Err())
			}
		}
		return
	default:
	}
	for _, i := range producedIdx {
		it := &items[i]
		info, err := c.proc.Probe(ctx, it.OutputPath)
		if err != nil {
			// Soft-fail: surface on the item but mark ProbeFailed
			// so SuccessfulItems still includes it (the file IS
			// on disk and downstream renderChunk can consume it)
			// while AllSucceeded() reports it as a non-strict
			// success for dashboards that partition
			// "fully validated" from "playable but unvalidated".
			logger.Warn("stock extractor: ffprobe validation failed for clip",
				zap.String("output_path", it.OutputPath),
				zap.String("job_id", it.JobID),
				zap.Error(err),
			)
			it.Status = stockpipeline.CutItemStatusProbeFailed
			it.Err = fmt.Errorf("ffprobe validation failed: %w", err)
			continue
		}
		// Populate DurationSec; flip to Validated when the
		// ffprobe-reported duration is a positive number. Zero
		// or negative durations stay at ProbeFailed (the file
		// is on disk but unvalidated).
		if info != nil {
			it.DurationSec = info.Duration.Seconds()
			if it.DurationSec > 0 {
				it.Status = stockpipeline.CutItemStatusValidated
			} else {
				it.Status = stockpipeline.CutItemStatusProbeFailed
				it.Err = errors.New("ffprobe reported zero/negative duration")
			}
		}
	}
}

// batchErr returns the top-level batch error for the Cut call.
// nil err when at least one Item succeeded; non-nil when ALL items
// failed (with the last captured failure preconditioned as argu).
//
// The legacy partial-success contract — non-nil error with some
// clips produced — is replaced with the CutBatchResult invariant:
// is preserved so the public contract reads identically to the
// pre-FASE-2.4 CutResult.ProducedPaths-keyed signature.
func (c *FFmpegCutter) batchErr(logger *zap.Logger, items []stockpipeline.CutItemResult, lastErrs ...error) error {
	// Aggregate any preserved lastErrs (typically a single ffmpeg
	// exit error) so callers can log a single root cause without
	// iterating Items.
	var lastErr error
	if len(lastErrs) > 0 {
		lastErr = lastErrs[0]
	}
	for _, it := range items {
		if it.Status == stockpipeline.CutItemStatusSucceeded || it.Status == stockpipeline.CutItemStatusValidated {
			return nil
		}
		if it.Err != nil {
			lastErr = it.Err
		}
	}
	if lastErr == nil {
		// Empty Items (or all Items with StatusUnknown — bodies
		// constructed but never assigned): surface a generic
		// "batch produced nothing" sentinel so callers never see
		// a silent nil.
		return errors.New("cutter: all jobs failed (no per-item err captured)")
	}
	logger.Warn("stock extractor: batch-level failure (all items failed)",
		zap.Error(lastErr),
	)
	return fmt.Errorf("cutter: all %d jobs failed; last err: %w", len(items), lastErr)
}
