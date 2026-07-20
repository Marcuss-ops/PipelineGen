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
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
)

// persistentStockWorkspace is the on-disk root used by the stock
// pipeline for durable per-job clip workspaces. Files under this
// prefix are trusted to be resumable and are validated with ffprobe
// before reuse.
const persistentStockWorkspace = "/data/stock/workspaces/"

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

// WithRunner replaces the underlying ffmpeg.Processor's subprocess runner.
// Returns the receiver for fluent chaining. Used by composition-root tests
// to inject a capture runner through the full injection chain:
// FFmpegCutter → ffmpeg.Processor → ProcessRunner.
func (c *FFmpegCutter) WithRunner(r ffmpeg.ProcessRunner) *FFmpegCutter {
	c.proc.WithRunner(r)
	return c
}

// partPath returns the transient .part path for a final output path.
// FFmpeg writes here first; after validation the file is atomically
// renamed to the final path. The .part marker is inserted before the
// file extension so ffmpeg can still infer the container format from
// the original extension (e.g. clip.mp4 -> clip.part.mp4).
func partPath(final string) string {
	// Split the path so the .part marker is inserted before the file
	// extension, not somewhere in a parent directory name.
	base := filepath.Base(final)
	dir := filepath.Dir(final)
	if i := strings.LastIndex(base, "."); i > 0 {
		return filepath.Join(dir, base[:i]+".part"+base[i:])
	}
	return final + ".part"
}

// sha256File returns the hex-encoded SHA-256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("sha256 open: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("sha256 hash: %w", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// validateProducedClip validates a produced clip file, returns its
// ffprobe-reported duration (seconds), its SHA-256 hex digest, and an
// error if the file is not a valid video clip.
//
// Fase 1 contract: a clip is considered valid only when:
//   - the file exists and is non-empty;
//   - ffprobe can parse it;
//   - it contains at least one video stream;
//   - its duration is positive.
//
// When probeAfterCut is disabled, ffprobe is skipped and only the
// existence/size/hash checks run. This supports test fixtures and
// watcher paths that intentionally do not validate.
func (c *FFmpegCutter) validateProducedClip(ctx context.Context, path string) (durationSec float64, sha string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return 0, "", fmt.Errorf("clip stat failed: %w", statErr)
	}
	if info.Size() <= 0 {
		return 0, "", errors.New("clip is empty")
	}

	if c.probeAfterCut {
		probeInfo, probeErr := c.proc.Probe(ctx, path)
		if probeErr != nil {
			return 0, "", fmt.Errorf("clip ffprobe validation failed: %w", probeErr)
		}
		if probeInfo == nil {
			return 0, "", errors.New("clip ffprobe returned nil info")
		}
		if !probeInfo.HasVideo {
			return 0, "", errors.New("clip has no video stream")
		}
		if probeInfo.Duration <= 0 {
			return 0, "", errors.New("clip has non-positive duration")
		}
		durationSec = probeInfo.Duration.Seconds()
	}

	sha, hashErr := sha256File(path)
	if hashErr != nil {
		return 0, "", fmt.Errorf("clip sha256 failed: %w", hashErr)
	}

	return durationSec, sha, nil
}

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
// FASE 2.4 / Fase 1 contract:
//   - Returns CutBatchResult with len(Items) == len(req.Jobs) ALWAYS.
//   - Each Item carries Status / SizeBytes / DurationSec /
//     SHA256Hex / Err according to the per-job outcome.
//   - Clips are written to a transient .part file, validated, and
//     atomically renamed to the final deterministic path.
//   - ffprobe runs on every produced clip (when probeAfterCut=true):
//     a clip that fails validation is marked Failed and removed so
//     it can be retried, never reused as a corrupt artifact.
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
	var srcDuration float64
	if req.SourceDuration > 0 {
		srcDuration = req.SourceDuration
		logger.Info("stock extractor: skipping probe, using pre-flight duration",
			zap.String("source", req.SourcePath),
			zap.Float64("duration_sec", srcDuration),
		)
	} else if info, probeErr := c.proc.Probe(ctx, req.SourcePath); probeErr == nil && info != nil {
		srcDuration = info.Duration.Seconds()
		logger.Info("stock extractor: source duration probed",
			zap.String("source", req.SourcePath),
			zap.Float64("duration_sec", srcDuration),
		)
	} else if probeErr != nil {
		logger.Warn("stock extractor: source duration probe failed — proceeding without validation",
			zap.String("source", req.SourcePath),
			zap.Error(probeErr),
		)
	}

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
		// Batch succeeded — validate each .part, rename to final,
		// and record produced indices.
		for i, j := range internalJobs {
			origIdx := validToOrig[i]
			durationSec, sha, validateErr := c.validateProducedClip(ctx, j.Output)
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
			finalPath := finalOutputs[i]
			if err := os.Rename(j.Output, finalPath); err != nil {
				items[origIdx].Status = stockpipeline.CutItemStatusFailed
				items[origIdx].Err = fmt.Errorf("rename %s -> %s: %w", j.Output, finalPath, err)
				_ = os.Remove(j.Output)
				continue
			}
			info, statErr := os.Stat(finalPath)
			if statErr != nil {
				items[origIdx].Status = stockpipeline.CutItemStatusFailed
				items[origIdx].Err = fmt.Errorf("stat final clip: %w", statErr)
				continue
			}
			producedIdx = append(producedIdx, origIdx)
			items[origIdx].OutputPath = finalPath
			items[origIdx].SizeBytes = info.Size()
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
		return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, c.batchErr(logger, items)
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

		durationSec, sha, validateErr := c.validateProducedClip(ctx, partFile)
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

		if err := os.Rename(partFile, finalPath); err != nil {
			items[origIdx].Status = stockpipeline.CutItemStatusFailed
			items[origIdx].Err = fmt.Errorf("rename %s -> %s: %w", partFile, finalPath, err)
			lastErr = items[origIdx].Err
			_ = os.Remove(partFile)
			continue
		}

		info, statErr := os.Stat(finalPath)
		if statErr != nil {
			items[origIdx].Status = stockpipeline.CutItemStatusFailed
			items[origIdx].Err = fmt.Errorf("stat final clip: %w", statErr)
			lastErr = items[origIdx].Err
			continue
		}

		producedIdx = append(producedIdx, origIdx)
		items[origIdx].OutputPath = finalPath
		items[origIdx].SizeBytes = info.Size()
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
	return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, c.batchErr(logger, items, lastErr)
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
