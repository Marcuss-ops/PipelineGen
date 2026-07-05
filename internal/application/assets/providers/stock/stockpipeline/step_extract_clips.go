// Package stockpipeline — step_extract_clips.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockExtractClipsStep — the canonical
// implementation of the stock.extract_clips step (Step 3 of the
// 6-step pipeline) per godlike/06 SSOT. Phase 1 (July 2026):
// rewired to use the real VideoCutter.Cut port instead of
// emitting logical IDs.
//
// The step:
//  1. Builds a sourceID → localPath map from StagedAssets.
//  2. Groups ClipPlan entries by SourceID.
//  3. For each group, constructs CutRequest with the real
//     SourcePath and calls runner.Cutter().Cut(ctx, req).
//  4. Collects OutputPath values from SuccessfulItems() →
//     CutPaths.
//  5. Writes asset/outbox via Writer.WriteAndEnqueue for each
//     successfully cut clip.
//
// godlike/07 fail-closed contracts:
//   - Cutter nil → test-fixture path (CutPaths = nil, no error).
//   - plans empty → Debug + return nil (no work to do).
//   - Source not staged → Warn + skip (graceful degradation; other
//     sources may still have staged files).
//   - All cuts fail for a source → error (terminal, typed wrap
//     preserving cutter typed sentinel via %w).
//   - Zero cut files across all sources → terminal error
//     (production gate, closes false-success class).
package stockpipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips. Phase 1 (July 2026): rewired to use the
// real VideoCutter.Cut port instead of emitting logical IDs.
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

func (StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	cutter := runner.Cutter()
	plans := runner.State().Plan

	// Test-fixture path: no cutter wired → skip (downstream
	// compose_chunks handles empty CutPaths gracefully).
	if cutter == nil {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: VideoCutter nil — skipping cut (test-fixture path)")
		}
		runner.State().CutPaths = nil
		return nil
	}

	if len(plans) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: empty plan — nothing to extract")
		}
		runner.State().CutPaths = nil
		return nil
	}

	// Build sourceID → localPath map from StagedAssets.
	stagedBySource := make(map[string]string)
	for _, sa := range runner.State().StagedAssets {
		if sa.SourceID != "" && sa.LocalPath != "" {
			stagedBySource[sa.SourceID] = sa.LocalPath
		}
	}

	// Group ClipPlan by SourceID.
	grouped := make(map[string][]ClipPlan)
	for _, plan := range plans {
		grouped[plan.SourceID] = append(grouped[plan.SourceID], plan)
	}

	in := runner.RunInput()
	noAudio := in != nil && in.NoAudio
	writer := runner.Writer()

	var cutPaths []string
	sourceIdx := 0

	for sourceID, groupPlans := range grouped {
		sourcePath := stagedBySource[sourceID]
		if sourcePath == "" {
			// Source not staged — skip gracefully. The upstream
			// stock.stage_sources step logs Warn on stage failure;
			// here we surface the downstream impact without aborting
			// (other sources may still have staged files).
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: source not staged — skipping cuts",
					zap.String("source_id", sourceID),
					zap.Int("clip_count", len(groupPlans)))
			}
			sourceIdx++
			continue
		}

		// Build CutJobs from ClipPlan entries. Each job gets a
		// unique output path via the per-clip index, avoiding the
		// collision where all clips in a source group shared the
		// same OutputLogicalID prefix.
		jobs := make([]CutJob, 0, len(groupPlans))
		for clipIdx, plan := range groupPlans {
			outputPath := filepath.Join(os.TempDir(),
				fmt.Sprintf("stock_cut_%s_%d_%d.mp4", runner.JobID(), sourceIdx, clipIdx))
			jobs = append(jobs, CutJob{
				StartSec:   plan.StartSec,
				EndSec:     plan.EndSec,
				OutputPath: outputPath,
			})
		}

		req := CutRequest{
			SourcePath: sourcePath,
			Jobs:       jobs,
			Codec:      "libx264",
			Preset:     "medium",
			CRF:        23,
			NoAudio:    noAudio,
			Logger:     runner.Log(),
			SourceIdx:  sourceIdx,
		}

		result, cutErr := cutter.Cut(ctx, req)

		// Process successful items. The port contract guarantees
		// len(Items) == len(req.Jobs) (mai-nil-with-zero-output
		// invariant); SuccessfulItems() filters to file-on-disk-
		// playable outcomes (Succeeded | Validated | ProbeFailed).
		for clipIdx, item := range result.SuccessfulItems() {
			// CutPaths carries REAL file paths (for compose_chunks).
			cutPaths = append(cutPaths, item.OutputPath)

			// Write asset/outbox for this successfully cut clip.
			// Asset ID uses the planner's OutputLogicalID (stable
			// across retries) so retry dedupe works; the real file
			// path is in CutPaths for downstream consumption.
			if writer != nil && clipIdx < len(groupPlans) {
				plan := groupPlans[clipIdx]
				clip := &asset.Asset{
					ID:        plan.OutputLogicalID,
					Name:      fmt.Sprintf("chunk_%d_%d", sourceIdx, clipIdx),
					Source:    asset.Source("stock"),
					MediaType: asset.MediaType("video"),
				}
				if err := writer.WriteAndEnqueue(ctx, clip, ""); err != nil {
					// PR-STOCK-ATLASTORCH-DISPATCH (commit-1 stock fix).
					// Pre-fix: the loop logged + continued on writer
					// error, leaving physical clip on disk but no
					// DB/outbox row — silent-success false-positive.
					// Post-fix: abort the run with the canonical
					// ErrAtomicDispatchFailed sentinel. Surfaced as
					// JobFailed by the broker; ledger writes roll back
					// via the single-TX outbox + SQLite Begin-Immediate
					// path.
					//
					// dual-%w (NEVER %v) preserves the underlying writer
					// error in the typed-error chain so callers can
					// errors.As-recover production causes (e.g.
					// outbox.ErrDispatcherClosed, sqlite3.ErrLocked).
					// Per AGENTS.md godlike/07 typed-error contract +
					// compat_adapters.go precedent — %v would silently
					// drop the chain.
					if runner.Log() != nil {
						runner.Log().Warn("orchestrator: stock.extract_clips: WriteAndEnqueue failed — aborting atomic dispatch",
							zap.String("logical_id", plan.OutputLogicalID),
							zap.String("output_path", item.OutputPath),
							zap.String("source_id", sourceID),
							zap.Int("clip_index", clipIdx),
							zap.Error(err))
					}
					return fmt.Errorf("orchestrator: stock.extract_clips: %w: %w", ErrAtomicDispatchFailed, err)
				}
			}
		}

		if cutErr != nil && len(result.SuccessfulItems()) == 0 {
			return fmt.Errorf("orchestrator: stock.extract_clips: VideoCutter.Cut failed for source %s with zero clips produced: %w",
				sourceID, cutErr)
		}

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.extract_clips: cut batch complete",
				zap.String("source_id", sourceID),
				zap.Int("planned", len(jobs)),
				zap.Int("produced", len(result.SuccessfulItems())))
		}

		sourceIdx++
	}

	// Production gate: cutter wired (non-nil) + zero cut files
	// across all sources → terminal error. This closes the
	// false-success class where extract_clips "succeeds" without
	// producing any real files on disk.
	if len(cutPaths) == 0 {
		return fmt.Errorf("orchestrator: stock.extract_clips: zero cut files produced across %d sources — all sources either unstaged or all cuts failed", len(grouped))
	}

	runner.State().CutPaths = cutPaths
	return nil
}
