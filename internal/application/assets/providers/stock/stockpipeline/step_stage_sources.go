// Package stockpipeline — step_stage_sources.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SOLE owner of StockStageSourcesStep — the canonical
// implementation of the stock.stage_sources step (Step 2 of the
// 6-step pipeline) per godlike/06 SSOT. P6 (July 2026): wired
// with real assets.SourceStager.StageSource per unique source URL.
// Deduplicates by SourceID so multiple ClipPlan entries sharing
// the same source download once.
//
// godlike/07 fail-closed contracts:
//   - stager wired + plans empty → Debug + return nil (no work to do).
//   - stager wired + plans non-empty + all sources fail (zero
//     *assets.StagedAsset appended) → ErrStockStageSourcesAllFailed
//     (PR-STOCK-FAKE-AVAILABILITY-REMOVAL, 2026-07-04).
//   - stager.StageSource returns err/nil-asset → graceful
//     degradation (Warn + continue; partial successes still produce
//     partial artifacts).
//
// godlike/07 lifecycle: the staged source MUST survive for the
// entire orchestrator run — extract_clips and compose_chunks read
// the real files on disk. Cleanup lives at the orchestrator level
// (orchestrator.go::RunResilient), fired after ALL steps complete
// via context.WithoutCancel so cleanup survives ctx cancellation.
package stockpipeline

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// StockStageSourcesStep is the canonical implementation of
// stock.stage_sources. P6 (July 2026): wired with real
// assets.SourceStager.StageSource per unique source URL.
// Deduplicates by SourceID so multiple ClipPlan entries
// sharing the same source download once.
type StockStageSourcesStep struct{}

func (StockStageSourcesStep) Name() string { return StepKeyStockStageSources }

func (StockStageSourcesStep) Run(ctx context.Context, runner StepRunner) (err error) {
	phaseMetric := startStockPhase(ctx, runner, "stock.stage_sources")
	defer func() {
		plans := runner.State().Plan
		staged := runner.State().StagedAssets
		uniqueSourceCount := countUniquePlanSources(plans)
		var bytes int64
		for _, asset := range staged {
			if asset != nil {
				bytes += asset.Bytes
			}
		}
		if phaseMetric != nil {
			phaseMetric.SetItems(int64(len(plans)), int64(len(staged)))
			phaseMetric.SetBytes(0, bytes)
			phaseMetric.SetDetails(map[string]any{
				"videos_found":      uniqueSourceCount,
				"videos_downloaded": len(staged),
				"download_bytes":    bytes,
			})
		}
		finishStockPhase(runner, phaseMetric, "stock.stage_sources", err)
	}()
	// godlike/07 composition-time guarantee (PR-STOCK-PRODUCTION-DEPS,
	// July 2026): runner.SourceStager() is non-nil. The canonical
	// composition root (stockpipeline.NewService + orchestrator.RunResilient)
	// rejects nil stager with ErrStockPipelineNilSourceStager /
	// ErrOrchestratorNilDeps BEFORE the step body runs. The previous
	// runtime nil-check (test-fixture path) is RETIRED per godlike/07
	// no-fake-availability: a production run cannot reach here with a
	// nil stager, and a test fixture that passes nil must update to
	// wire a non-nil stub (mapStager / recordingStager).
	stager := runner.SourceStager()

	plans := runner.State().Plan

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.stage_sources: starting",
			zap.Int("plan_count", len(plans)))
	}

	if len(plans) == 0 {
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.stage_sources: empty plan — nothing to stage")
		}
		return nil
	}

	seen := make(map[string]bool)
	var staged []*assets.StagedAsset

	// Phase 1 (July 2026): REMOVED defer Cleanup from this step.
	// The staged source MUST survive for the entire orchestrator run
	// — downstream steps (extract_clips, compose_chunks) need the real
	// files on disk. Cleanup now lives at the orchestrator level
	// (orchestrator.go::RunResilient), fired after ALL steps complete.

	for _, plan := range plans {
		stageKey := plan.SourceID
		if in := runner.RunInput(); in != nil && in.DownloadMode == "sections_only" {
			stageKey = plan.StageKey
		}
		if seen[stageKey] {
			continue
		}
		seen[stageKey] = true

		ref := assets.SourceRef{URL: stagingSourceURL(plan)}
		if runner.RunInput() != nil && runner.RunInput().DownloadMode == "sections_only" {
			ref.DownloadSection = fmt.Sprintf("*%s-%s", formatDuration(plan.StartSec), formatDuration(plan.EndSec))
			ref.MergeFormat = "mp4"
		}
		sa, stageErr := stager.StageSource(ctx, ref)
		if stageErr != nil {
			// Graceful degradation: stage failure logs Warn + continues.
			// Mirrors YouTube (process_segment.go Step 4a) + Artlist pattern.
			// The downstream extract_clips step can still proceed with
			// cached/pre-staged sources if available; no staged asset
			// for this URL means clips referencing it will fail at cut.
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.stage_sources: StageSource failed — graceful degradation",
					zap.String("source_id", plan.SourceID),
					zap.Error(stageErr))
			}
			continue
		}
		if sa == nil {
			// Defensive nil-asset path: StageSource returned (nil, nil).
			// Treated as soft failure (Warn + continue, no defer).
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.stage_sources: StageSource returned nil asset — defensive skip",
					zap.String("source_id", plan.SourceID))
			}
			continue
		}
		// Phase 1 (July 2026): stamp the SourceID on the StagedAsset
		// so downstream steps can map ClipPlan.SourceID → LocalPath.
		sa.SourceID = stageKey
		staged = append(staged, sa)
		// Publish immediately to the shared RunState so the
		// orchestrator-level deferred cleanup can see this asset
		// even if a later iteration (or a downstream step) panics
		// before the step returns.
		runner.State().StagedAssets = staged

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.stage_sources: staged source",
				zap.String("source_id", plan.SourceID),
				zap.String("stage_key", stageKey),
				zap.Int("section_count", 1),
				zap.Float64("requested_section_seconds", plan.EndSec-plan.StartSec),
				zap.String("download_mode", runner.RunInput().DownloadMode),
				zap.Float64("downloaded_file_duration_seconds", sa.DurationSec),
				zap.String("local_path", sa.LocalPath),
				zap.Int64("bytes", sa.Bytes))
		}
	}

	// Fail-closed gate (PR-STOCK-FAKE-AVAILABILITY-REMOVAL, 2026-07-04):
	// if the stager was wired (non-nil check above) AND we had plans
	// (len(plans) > 0 check above) AND every source failed to stage
	// (zero *assets.StagedAsset appended to the staged slice), surface
	// ErrStockStageSourcesAllFailed as a job failure. This closes the
	// godlike/07 no-fake-availability class where a job could report
	// SUCCEEDED with zero staged assets on Drive. The per-source
	// graceful degradation (Warn + continue on err/nil) is preserved
	// above so partial successes still produce partial artifacts — only
	// the all-failed case surfaces this sentinel.
	if len(staged) == 0 {
		return ErrStockStageSourcesAllFailed
	}
	// A multi-source stock request is only successful when every planned
	// source is available. Previously the step treated partial staging as
	// graceful degradation, allowing a 10-video request with one usable
	// video to publish successfully while silently dropping the other nine.
	if len(staged) < len(seen) {
		return fmt.Errorf("%w: staged=%d requested=%d", ErrStockStageSourcesIncomplete, len(staged), len(seen))
	}

	runner.State().StagedAssets = staged

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.stage_sources: SUCCEEDED",
			zap.Int("staged_count", len(staged)),
			zap.Int("plan_count", len(plans)))
	}
	return nil
}

// stagingSourceURL canonicalizes YouTube URLs before handing them to
// the acquisition stager. The stager rejects query-string variants
// such as `...?pp=...`; the stock pipeline keeps the original SourceID
// for downstream grouping, but downloads use the canonical watch URL.
func stagingSourceURL(plan ClipPlan) string {
	raw := strings.TrimSpace(plan.SourceID)
	if raw == "" {
		return raw
	}
	lower := strings.ToLower(raw)
	if plan.SourceProvider != SourceProviderYouTube &&
		!strings.Contains(lower, "youtube.com") &&
		!strings.Contains(lower, "youtu.be") {
		return raw
	}
	if id := extractVideoID(raw); id != "" {
		return "https://www.youtube.com/watch?v=" + id
	}
	return raw
}
