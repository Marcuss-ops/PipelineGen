package render

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// Cut is the concrete port implementation. It attempts the batch cut
// first; on failure, it falls back to per-clip sequential cuts and
// returns the union of successfully-produced clip paths.
//
// FASE 2.4 / Fase 1 contract:
//   - Returns CutBatchResult with len(Items) == len(req.Jobs) ALWAYS.
//   - Each Item carries Status / SizeBytes / DurationSec /
//     SHA256Hex / Err according to the per-job outcome.
//   - Clips are written to a transient .part file, validated, and
//     atomically renamed to the final deterministic path.
//   - ffprobe runs on every produced clip (when probeAfterCut=true):
//     a clip that fails validation is marked Failed and removed so
//     it can be retried, never reused as a corrupt artifact.
//   - Top-level err is non-nil when ANY job failed (fail-closed):
//     the whole batch is rejected if even one clip does not match
//     the canonical profile.
//
// Phase 9 split: the source-duration probe prelude is delegated to
// c.probeSourceDuration (cutter_probe.go). Each post-cut validation
// is delegated to c.validateCanonicalClip (cutter_post_process.go).
// Each .part→final atomic rename is delegated to c.commitPartialFile
// (cutter_mux.go). Batch-level + per-item result aggregation lives
// in c.finalBatchResult + c.batchErr (cutter_mux.go).
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

	// Resume interrupted stock jobs without recreating clips that are
	// already present at their deterministic output paths. The original
	// item ordering is preserved; only missing or invalid files reach
	// FFmpeg.
	pendingJobs := make([]stockpipeline.CutJob, 0, len(req.Jobs))
	pendingToOrig := make([]int, 0, len(req.Jobs))
	for origIdx, j := range req.Jobs {
		info, statErr := os.Stat(j.OutputPath)
		if statErr != nil || info.Size() <= 0 {
			pendingJobs = append(pendingJobs, j)
			pendingToOrig = append(pendingToOrig, origIdx)
			continue
		}
		// Only reuse files that live in the persistent stock workspace
		// and follow the expected naming convention.
		if !strings.Contains(j.OutputPath, persistentStockWorkspace) ||
			!strings.Contains(j.OutputPath, "/extracted/stock_cut_") {
			pendingJobs = append(pendingJobs, j)
			pendingToOrig = append(pendingToOrig, origIdx)
			continue
		}

		// Validate the existing file before trusting it: empty or
		// corrupt leftovers from a previous interrupted run must be
		// regenerated.
		durationSec, sha, validateErr := c.validateProducedClip(ctx, j.OutputPath)
		if validateErr != nil {
			c.log.Warn("stock extractor: existing clip invalid, regenerating",
				zap.String("output_path", j.OutputPath),
				zap.Error(validateErr))
			_ = os.Remove(j.OutputPath)
			_ = os.Remove(partPath(j.OutputPath))
			pendingJobs = append(pendingJobs, j)
			pendingToOrig = append(pendingToOrig, origIdx)
			continue
		}

		items[origIdx].OutputPath = j.OutputPath
		items[origIdx].SizeBytes = info.Size()
		items[origIdx].DurationSec = durationSec
		items[origIdx].SHA256Hex = sha
		if c.probeAfterCut {
			items[origIdx].Status = stockpipeline.CutItemStatusValidated
		} else {
			items[origIdx].Status = stockpipeline.CutItemStatusSucceeded
		}
	}

	if len(pendingJobs) == 0 {
		return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, nil
	}

	// Bound FFmpeg filter graphs. Hundreds of trim/map/output branches in
	// one process are disproportionately slow and can exhaust resources.
	if len(pendingJobs) > 15 {
		var lastErr error
		produced := 0
		for start := 0; start < len(pendingJobs); start += 15 {
			end := start + 15
			if end > len(pendingJobs) {
				end = len(pendingJobs)
			}
			subReq := req
			subReq.Jobs = pendingJobs[start:end]
			sub, cutErr := c.Cut(ctx, subReq)
			if cutErr != nil {
				lastErr = cutErr
			}
			for i, subItem := range sub.Items {
				origIdx := pendingToOrig[start+i]
				items[origIdx] = subItem
				if subItem.Status == stockpipeline.CutItemStatusSucceeded ||
					subItem.Status == stockpipeline.CutItemStatusValidated ||
					subItem.Status == stockpipeline.CutItemStatusProbeFailed {
					produced++
				}
			}
		}
		result := stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}
		if produced < len(pendingJobs) {
			if lastErr == nil {
				lastErr = fmt.Errorf("cutter: %d of %d micro-batch clips failed", len(pendingJobs)-produced, len(pendingJobs))
			}
			return result, lastErr
		}
		return result, nil
	}

	logger := c.log
	if req.Logger != nil {
		logger = req.Logger
	}

	// ── Probe source duration for pre-flight validation ──────────────
	// If the source is shorter than the clip timestamps, FFmpeg's
	// trim filter silently produces empty 262-byte MP4 containers.
	// Probe first so we can fail-fast with a clear, actionable error
	// instead of producing 11 identical empty files.
	srcDuration, _ := c.probeSourceDuration(ctx, req.SourcePath, req.SourceDuration, logger)

	// Filter out clips whose timestamps are entirely beyond the
	// source duration. If we have the source duration and a clip's
	// StartSec >= duration, skip it with a clear error. For clips
	// where only EndSec exceeds duration, clamp to source end.
	//
	// validToOrig maps validJobs[i] → original req.Jobs index so
	// we can write results back to the correct items[] slot after
	// the batch/fallback cut. Without this mapping, skipped clips
	// cause the indices to desynchronise (e.g. items[1] gets C's
	// output when B was skipped and A,C,E are the valid jobs).
	validJobs := make([]stockpipeline.CutJob, 0, len(req.Jobs))
	validToOrig := make([]int, 0, len(req.Jobs))
	skipped := 0
	for pendingIdx, j := range pendingJobs {
		origIdx := pendingToOrig[pendingIdx]
		if srcDuration > 0 && j.StartSec >= srcDuration {
			skipped++
			logger.Warn("stock extractor: clip skipped — start timestamp beyond source duration",
				zap.String("output", j.OutputPath),
				zap.Float64("start_sec", j.StartSec),
				zap.Float64("end_sec", j.EndSec),
				zap.Float64("source_duration", srcDuration),
			)
			items[origIdx].Status = stockpipeline.CutItemStatusFailed
			items[origIdx].Err = fmt.Errorf("clip start %.2fs beyond source duration %.2fs", j.StartSec, srcDuration)
			continue
		}
		clamped := j
		if srcDuration > 0 && clamped.EndSec > srcDuration {
			logger.Info("stock extractor: clip end clamped to source duration",
				zap.String("output", j.OutputPath),
				zap.Float64("original_end", j.EndSec),
				zap.Float64("clamped_end", srcDuration),
			)
			clamped.EndSec = srcDuration
		}
		validJobs = append(validJobs, clamped)
		validToOrig = append(validToOrig, origIdx)
	}
	if skipped > 0 {
		logger.Warn("stock extractor: clips skipped due to timestamp overflow",
			zap.Int("skipped", skipped),
			zap.Int("remaining", len(validJobs)),
			zap.Float64("source_duration_sec", srcDuration),
		)
	}
	if len(validJobs) == 0 {
		return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items},
			fmt.Errorf("cutter: all %d clips skipped — timestamps beyond source duration %.2fs", len(req.Jobs), srcDuration)
	}

	internalJobs := toInternalCutJobs(validJobs)
	producedIdx := make([]int, 0, len(internalJobs)) // indices into items where a file is on disk

	// Build .part paths for atomic output. FFmpeg writes to the
	// transient .part file; after validation we rename to the final
	// deterministic path.
	finalOutputs := make([]string, len(internalJobs))
	for i, j := range internalJobs {
		finalOutputs[i] = j.Output
		internalJobs[i].Output = partPath(j.Output)
	}

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
		// Batch succeeded — validate each .part, commit to final,
		// and record produced indices.
		for i, j := range internalJobs {
			origIdx := validToOrig[i]
			expectedDuration := j.EndSec - j.StartSec
			durationSec, sha, validateErr := c.validateCanonicalClip(ctx, j.Output, req.NoAudio, expectedDuration)
			if validateErr != nil {
				logger.Warn("stock extractor: batch clip validation failed",
					zap.Int("source_index", req.SourceIdx),
					zap.Int("clip_index", i),
					zap.String("part_path", j.Output),
					zap.Error(validateErr))
				items[origIdx].Status = stockpipeline.CutItemStatusFailed
				items[origIdx].Err = validateErr
				_ = os.Remove(j.Output)
				continue
			}
			commitItem, commitErr := c.commitPartialFile(j.Output, finalOutputs[i])
			if commitErr != nil {
				items[origIdx].Status = stockpipeline.CutItemStatusFailed
				items[origIdx].Err = commitErr
				continue
			}
			producedIdx = append(producedIdx, origIdx)
			items[origIdx].OutputPath = commitItem.OutputPath
			items[origIdx].SizeBytes = commitItem.SizeBytes
			items[origIdx].DurationSec = durationSec
			items[origIdx].SHA256Hex = sha
			if c.probeAfterCut {
				items[origIdx].Status = stockpipeline.CutItemStatusValidated
			} else {
				items[origIdx].Status = stockpipeline.CutItemStatusSucceeded
			}
		}
		logger.Info("stock extractor: single-pass batch cut completed",
			zap.Int("source_index", req.SourceIdx),
			zap.Int("clips_produced", len(producedIdx)),
		)
		return c.finalBatchResult(req, items, logger)
	}

	// ── Attempt 2: per-clip fallback ─────────────────────────────────
	logger.Warn("stock extractor: batch cut failed, falling back to individual cuts",
		zap.Int("source_index", req.SourceIdx),
		zap.Error(batchErr),
	)

	var lastErr error
	for i, j := range internalJobs {
		origIdx := validToOrig[i]
		finalPath := finalOutputs[i]
		partFile := j.Output // internalJobs were already rewritten to .part

		select {
		case <-ctx.Done():
			// Context cancelled: mark every still-unprocessed item as
			// failed with ctx.Err; iterate only over the un-cut suffix.
			for k := i; k < len(internalJobs); k++ {
				ok := validToOrig[k]
				if items[ok].Status == stockpipeline.CutItemStatusUnknown {
					items[ok].Status = stockpipeline.CutItemStatusFailed
					items[ok].Err = ctx.Err()
				}
			}
			return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, ctx.Err()
		default:
		}

		// Ensure no stale final or .part file from a previous run
		// remains.
		_ = os.Remove(finalPath)
		_ = os.Remove(partFile)

		cutErr := c.proc.CutReencode(
			ctx, req.SourcePath, partFile,
			ffmpeg.FormatSec(j.StartSec), ffmpeg.FormatSec(j.EndSec),
			req.NoAudio, req.Codec, req.Preset, req.CRF,
		)
		if cutErr != nil {
			logger.Warn("stock extractor: fallback cut failed",
				zap.Int("source_index", req.SourceIdx),
				zap.Int("clip_index", i),
				zap.Error(cutErr),
			)
			items[origIdx].Status = stockpipeline.CutItemStatusFailed
			items[origIdx].Err = cutErr
			lastErr = cutErr
			continue
		}

		expectedDuration := j.EndSec - j.StartSec
		durationSec, sha, validateErr := c.validateCanonicalClip(ctx, partFile, req.NoAudio, expectedDuration)
		if validateErr != nil {
			logger.Warn("stock extractor: fallback clip validation failed",
				zap.Int("source_index", req.SourceIdx),
				zap.Int("clip_index", i),
				zap.String("part_path", partFile),
				zap.Error(validateErr))
			items[origIdx].Status = stockpipeline.CutItemStatusFailed
			items[origIdx].Err = validateErr
			lastErr = validateErr
			_ = os.Remove(partFile)
			continue
		}

		commitItem, commitErr := c.commitPartialFile(partFile, finalPath)
		if commitErr != nil {
			items[origIdx].Status = stockpipeline.CutItemStatusFailed
			items[origIdx].Err = commitErr
			lastErr = items[origIdx].Err
			continue
		}

		producedIdx = append(producedIdx, origIdx)
		items[origIdx].OutputPath = commitItem.OutputPath
		items[origIdx].SizeBytes = commitItem.SizeBytes
		items[origIdx].DurationSec = durationSec
		items[origIdx].SHA256Hex = sha
		if c.probeAfterCut {
			items[origIdx].Status = stockpipeline.CutItemStatusValidated
		} else {
			items[origIdx].Status = stockpipeline.CutItemStatusSucceeded
		}
	}

	logger.Info("stock extractor: per-clip fallback completed",
		zap.Int("source_index", req.SourceIdx),
		zap.Int("clips_produced", len(producedIdx)),
	)
	return c.finalBatchResult(req, items, logger, lastErr)
}
