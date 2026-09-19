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
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/ingest"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
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
			phaseMetric.SetItems(int64(uniqueSourceCount), int64(len(staged)))
			phaseMetric.SetItemsFailed(int64(uniqueSourceCount - len(staged)))
			phaseMetric.SetBytes(0, bytes)
		}
		finishStockPhase(runner, phaseMetric, "stock.stage_sources", err)
	}()
	// godlike/07 composition-time guarantee (PR-STOCK-PRODUCTION-DEPS,
	// July 2026): runner.SourceStager() is non-nil. The canonical
	// composition root (NewProductionStockPipeline + orchestrator.RunResilient)
	// rejects nil stager with ErrStockPipelineNilSourceStager /
	// ErrOrchestratorNilDeps BEFORE the step body runs. The previous
	// runtime nil-check (test-fixture path) is RETIRED per godlike/07
	// no-fake-availability: a production run cannot reach here with a
	// nil stager, and a test fixture that passes nil must update to
	// wire a non-nil stub (mapStager / recordingStager).
	preparer := &stockIngestPreparer{
		stager:        runner.SourceStager(),
		policyVersion: runner.PolicyVersion(),
	}

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

	sources := ingestSourcesFromClipPlans(plans)

	plansBySource := make(map[string]ClipPlan, len(plans))
	for _, plan := range plans {
		if _, exists := plansBySource[plan.SourceID]; !exists {
			plansBySource[plan.SourceID] = plan
		}
	}
	state := runner.State()
	if state.SourceErrors == nil {
		state.SourceErrors = make(map[string]string)
	}

	// Phase 1 (July 2026): REMOVED defer Cleanup from this step.
	// The staged source MUST survive for the entire orchestrator run
	// — downstream steps (extract_clips, compose_chunks) need the real
	// files on disk. Cleanup now lives at the orchestrator level
	// (orchestrator.go::RunResilient), fired after ALL steps complete.

	uniqueSources := ingest.UniqueSources(sources)

	// Bounded fan-out (September 2026): independent source downloads are
	// network/disk-bound, so they run in a bounded pool of
	// Cfg().MaxConcurrentJobs workers — the same knob stock.extract_clips
	// uses. The previous implementation staged sources strictly in series,
	// so an N-source request paid the SUM of N downloads and dominated the
	// run wall (the pipeline's single largest cost). Results are merged in
	// uniqueSources order so StagedAssets, SourceErrors, checkpoints, and
	// run fingerprints stay deterministic regardless of completion order.
	staged, stageFailures := stageUniqueSourcesBounded(ctx, runner, preparer, uniqueSources, plansBySource)

	// Publish partial success BEFORE the fail-closed gates below: on the
	// incomplete-sources path the successfully staged assets must stay
	// visible to the orchestrator-level deferred cleanup
	// (context.WithoutCancel), otherwise their temp dirs leak.
	for sourceID, reason := range stageFailures {
		state.SourceErrors[sourceID] = reason
	}
	state.StagedAssets = staged

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
		return fmt.Errorf("%w: failed_sources=%s", ErrStockStageSourcesAllFailed, formatSourceErrors(state.SourceErrors))
	}
	// A multi-source stock request is only successful when every planned
	// source is available. Previously the step treated partial staging as
	// graceful degradation, allowing a 10-video request with one usable
	// video to publish successfully while silently dropping the other nine.
	if len(staged) < len(uniqueSources) {
		return fmt.Errorf("%w: staged=%d requested=%d failed_sources=%s", ErrStockStageSourcesIncomplete, len(staged), len(uniqueSources), formatSourceErrors(state.SourceErrors))
	}

	runner.State().StagedAssets = staged

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.stage_sources: SUCCEEDED",
			zap.Int("staged_count", len(staged)),
			zap.Int("plan_count", len(plans)))
	}
	return nil
}

// stagedSourceOutcome is the per-source result of one staging fan-out worker.
// Workers only ever write to their own index in the outcomes slice, so the
// bounded pool needs no shared mutable state and stays race-free.
type stagedSourceOutcome struct {
	asset   *ports.StagedAsset
	errText string
}

// stageUniqueSourcesBounded stages every unique source with a bounded worker
// pool of Cfg().MaxConcurrentJobs (default DefaultMaxConcurrentJobs). Source
// downloads are network/disk-bound and independent, so overlapping them is the
// single largest wall-time win for a multi-source request; the previous
// sequential loop paid their sum.
//
// Contract (mirrors boundedSourceCuts in step_extract_clips.go):
//   - per-source failures are GRACEFUL: collected, never fatal, and never
//     cancel sibling sources — the caller owns the fail-closed gates;
//   - the returned assets are ordered by uniqueSources, so StagedAssets,
//     checkpoints, and run fingerprints do NOT depend on completion order;
//   - workers never mutate shared run state; the parent merges only after
//     wg.Wait(), and each goroutine is panic-isolated (concurrent.SafeGo) so a
//     single bad source cannot take down the process or strand its siblings.
func stageUniqueSourcesBounded(
	ctx context.Context,
	runner StepRunner,
	preparer ingest.SourcePreparer,
	uniqueSources []ingest.Source,
	plansBySource map[string]ClipPlan,
) ([]*ports.StagedAsset, map[string]string) {
	failures := make(map[string]string)
	if len(uniqueSources) == 0 {
		return nil, failures
	}

	parallelism := runner.Cfg().MaxConcurrentJobs
	if parallelism <= 0 {
		parallelism = DefaultMaxConcurrentJobs
	}
	if parallelism > len(uniqueSources) {
		parallelism = len(uniqueSources)
	}
	if parallelism < 1 {
		parallelism = 1
	}

	outcomes := make([]stagedSourceOutcome, len(uniqueSources))
	jobs := make(chan int)
	var wg sync.WaitGroup

	stageOne := func() {
		defer wg.Done()
		for idx := range jobs {
			source := uniqueSources[idx]
			plan := plansBySource[source.ID]
			// Stage the complete source once. In sections_only mode the clip
			// timestamps are cut locally by stock.extract_clips; using StageKey
			// or DownloadSection here would create a distinct cache key per
			// clip and invoke yt-dlp repeatedly for the same YouTube video.
			stageKey := source.ID
			prepared, stageErr := preparer.Prepare(ctx, source)
			switch {
			case stageErr != nil:
				// Graceful degradation: stage failure is recorded and skipped.
				// Mirrors YouTube (process_segment.go Step 4a) + Artlist pattern.
				// The downstream extract_clips step can still proceed with
				// cached/pre-staged sources if available; no staged asset for
				// this URL means clips referencing it will fail at cut.
				outcomes[idx] = stagedSourceOutcome{errText: stageErr.Error()}
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.stage_sources: Prepare failed — graceful degradation",
						zap.String("source_id", plan.SourceID),
						zap.Error(stageErr))
				}
			case prepared == nil:
				// Defensive nil-asset path: Prepare returned (nil, nil).
				outcomes[idx] = stagedSourceOutcome{errText: "source stager returned nil context without an error"}
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.stage_sources: Prepare returned nil context — defensive skip",
						zap.String("source_id", plan.SourceID))
				}
			default:
				asset := &ports.StagedAsset{
					LocalPath: prepared.LocalPath,
					Bytes:     prepared.Bytes,
					SourceID:  prepared.SourceID,
				}
				outcomes[idx] = stagedSourceOutcome{asset: asset}
				if runner.Log() != nil {
					runner.Log().Info("orchestrator: stock.stage_sources: staged source",
						zap.String("source_id", plan.SourceID),
						zap.String("stage_key", stageKey),
						zap.Int("section_count", 1),
						zap.Float64("requested_section_seconds", plan.EndSec-plan.StartSec),
						zap.String("download_mode", runner.RunInput().DownloadMode),
						zap.Float64("downloaded_file_duration_seconds", asset.DurationSec),
						zap.String("local_path", asset.LocalPath),
						zap.Int64("bytes", asset.Bytes))
				}
			}
		}
	}

	wg.Add(parallelism)
	for worker := 0; worker < parallelism; worker++ {
		concurrent.SafeGo("stock-stage-source", stageOne)
	}
	for idx := range uniqueSources {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()

	// Merge strictly in uniqueSources order. The plan SourceID is the canonical
	// SourceErrors key (matches RunState.SourceErrors consumers and
	// formatSourceErrors); fall back to the ingest source ID for the degenerate
	// case where no plan carries that source.
	staged := make([]*ports.StagedAsset, 0, len(uniqueSources))
	for idx, source := range uniqueSources {
		key := plansBySource[source.ID].SourceID
		if key == "" {
			key = source.ID
		}
		outcome := outcomes[idx]
		if outcome.asset != nil {
			staged = append(staged, outcome.asset)
			continue
		}
		if outcome.errText == "" {
			// A worker must always record a reason when it produces no asset;
			// surface the degenerate case instead of an empty failure reason.
			failures[key] = "source staging produced no asset and no error"
			continue
		}
		failures[key] = outcome.errText
	}
	return staged, failures
}

// stagingSourceURL canonicalizes YouTube URLs before handing them to
// the acquisition stager. The stager rejects query-string variants
// such as `...?pp=...`; the stock pipeline keeps the original SourceID
// for downstream grouping, but downloads use the canonical watch URL.
func formatSourceErrors(sourceErrors map[string]string) string {
	if len(sourceErrors) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(sourceErrors))
	for key := range sourceErrors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+": "+sourceErrors[key])
	}
	return strings.Join(parts, "; ")
}

func countUniquePlanSources(plans []ClipPlan) int {
	seen := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if plan.SourceID != "" {
			seen[plan.SourceID] = struct{}{}
		}
	}
	return len(seen)
}

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
