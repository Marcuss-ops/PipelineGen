package render

import (
	"errors"
	"fmt"
	"os"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

// commitPartialFile atomically renames a validated transient .part
// file to its deterministic final path and records the resulting
// size for the result item. On rename or stat failure the .part is
// removed so a retry produces a clean transient. The returned
// CutItemResult carries OutputPath + SizeBytes; the caller is
// responsible for stamping DurationSec + SHA256Hex + Status from
// the validate result.
//
// Phase 9 split: the original Cut body duplicated this block in two
// places (single-pass batch path + per-clip fallback path). Both
// now delegate here so the rename-and-stat sequence is the single
// source of truth.
func (c *FFmpegCutter) commitPartialFile(partFile, finalPath string) (stockpipeline.CutItemResult, error) {
	if renameErr := os.Rename(partFile, finalPath); renameErr != nil {
		_ = os.Remove(partFile)
		return stockpipeline.CutItemResult{OutputPath: finalPath}, fmt.Errorf("rename %s -> %s: %w", partFile, finalPath, renameErr)
	}
	info, statErr := os.Stat(finalPath)
	if statErr != nil {
		return stockpipeline.CutItemResult{OutputPath: finalPath}, fmt.Errorf("stat final clip: %w", statErr)
	}
	return stockpipeline.CutItemResult{OutputPath: finalPath, SizeBytes: info.Size()}, nil
}

// finalBatchResult returns the batch result. If any item failed it
// returns an error so the whole batch is rejected (fail-closed).
func (c *FFmpegCutter) finalBatchResult(req stockpipeline.CutRequest, items []stockpipeline.CutItemResult, logger *zap.Logger, lastErrs ...error) (stockpipeline.CutBatchResult, error) {
	for _, it := range items {
		if it.Status == stockpipeline.CutItemStatusFailed {
			if it.Err != nil {
				return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items},
					fmt.Errorf("cutter: batch fail-closed — clip %s failed canonical validation: %w", it.JobID, it.Err)
			}
			return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items},
				fmt.Errorf("cutter: batch fail-closed — clip %s failed", it.JobID)
		}
	}
	return stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: items}, c.batchErr(logger, items, lastErrs...)
}

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
