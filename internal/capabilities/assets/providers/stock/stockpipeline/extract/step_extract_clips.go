// Package stockpipeline — step_extract_clips.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026;
// PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// Slim orchestrator for StockExtractClipsStep. The Run() method
// delegates to helper functions in sister files:
//
//	groupPlans        → step_extract_clips_batch.go
//	prepareBatchState → step_extract_clips_batch.go
//	executeCuts       → step_extract_clips_cut.go
//	publishCuts       → step_extract_clips_publish.go
//	writeTimestampGroups → step_extract_clips_metadata.go
//	validateAndProbeSourceDuration → step_extract_clips_validation.go
//	buildRichStockAsset → step_extract_clips_assets.go
//
// GOP-16 (November 2026): Run is now the ordered orchestration only;
// durable preparation, the publish sweep, the duration contract and the
// final state write live in the helpers below.
package extract

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/publish"

	"go.uber.org/zap"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// ErrStockClipsOutOfRange (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026)
// surfaces a clip whose EndSec exceeds the probed source duration.
var (
	ErrStockClipsOutOfRange      = errors.New("stock.extract_clips: clip EndSec exceeds source duration")
	ErrStockClipsUnknownDuration = errors.New("stock.extract_clips: source duration is unknown")
	// ErrStockExtractClipsBeforeSection fires when a clip window sits before
	// the start of the file it must be cut from — a negative seek. It is a
	// fail-closed guard, not a data condition: the section offset is derived from
	// the very plans being cut, so a negative window means the two drifted.
	ErrStockExtractClipsBeforeSection  = errors.New("stock.extract_clips: clip window precedes the staged section")
	ErrStockStagedSourceWindowMismatch = errors.New("stock.extract_clips: staged source window differs from the planned section")
)

// maxDriveUploadWorkers caps concurrent Drive uploads per source group.
const maxDriveUploadWorkers = 3

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips (Step 3 of the 6-step pipeline).
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return core.StepKeyStockExtractClips }

// stockSourceGroup is an ordered source partition of the clip plan. The
// order is intentionally derived from the plan rather than a map so output
// paths, artifact ordinals, metadata ordering, and retries remain stable.
type stockSourceGroup struct {
	sourceID string
	plans    []core.ClipPlan
	index    int
	// cutOffsetSec is the seconds to subtract from the group's absolute plan
	// timestamps before seeking inside the STAGED file. Non-zero only for a
	// sections_only run whose source was staged as a time slice: the staged
	// file starts at the section start, so plan timestamps are absolute while
	// the cutter works in file-relative time. Derived from the plans by the same
	// helper the stager used to pick the section, so the two cannot drift.
	cutOffsetSec float64
}

type stockSourceCutResult struct {
	group     stockSourceGroup
	result    core.CutBatchResult
	processed bool
}

// orderedPlanGroups groups plans by SourceID while preserving the first
// occurrence order. A map is still used by groupPlans for compatibility, but
// extraction must never range over that map because Go deliberately randomizes
// map iteration order.
func orderedPlanGroups(plans []core.ClipPlan) []stockSourceGroup {
	groups := make([]stockSourceGroup, 0)
	bySource := make(map[string]int)
	for _, plan := range plans {
		idx, ok := bySource[plan.SourceID]
		if !ok {
			idx = len(groups)
			bySource[plan.SourceID] = idx
			groups = append(groups, stockSourceGroup{sourceID: plan.SourceID, index: idx})
		}
		groups[idx].plans = append(groups[idx].plans, plan)
	}
	return groups
}

// boundedSourceCuts runs only the source-local validation and FFmpeg cut
// phase in parallel. Durable publication and the shared metadata accumulators
// remain in plan order in Run, which avoids races in transactional writers,
// artifact preparation, segment numbering, and checkpoint-visible state while
// still allowing independent source downloads to consume bounded CPU/FFmpeg.
func boundedSourceCuts(ctx context.Context, runner core.StepRunner, groups []stockSourceGroup,
	stagedBySource map[string]*assets.StagedAsset, noAudio bool) ([]stockSourceCutResult, error) {
	results := make([]stockSourceCutResult, len(groups))
	if len(groups) == 0 {
		return results, nil
	}

	parallelism := runner.Cfg().MaxConcurrentJobs
	if parallelism <= 0 {
		parallelism = core.DefaultMaxConcurrentJobs
	}
	if parallelism > len(groups) {
		parallelism = len(groups)
	}
	if parallelism < 1 {
		parallelism = 1
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error

	setError := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}

	worker := func() {
		defer wg.Done()
		for {
			select {
			case <-workCtx.Done():
				return
			case groupIndex, ok := <-jobs:
				if !ok {
					return
				}
				group := groups[groupIndex]
				staged := stagedBySource[group.sourceID]
				if staged == nil {
					// Missing sources are handled by the existing production
					// gate after all groups have been considered. Do not turn
					// this into a worker error or cancel valid sources.
					continue
				}

				cutOffsetSec, windowErr := stagedSourceCutOffset(staged, group.plans, group.cutOffsetSec)
				if windowErr != nil {
					if ctx.Err() == nil {
						setError(fmt.Errorf("orchestrator: stock.extract_clips: source window for %s: %w", group.sourceID, windowErr))
					}
					return
				}
				sourceDuration, _, validationErr := validateAndProbeSourceDuration(
					workCtx, runner, group.sourceID, staged.LocalPath, staged, group.plans, cutOffsetSec)
				if validationErr != nil {
					if ctx.Err() == nil {
						setError(validationErr)
					}
					return
				}
				result, cutErr := ExecuteCuts(
					workCtx, runner, group.sourceID, staged.LocalPath, sourceDuration,
					group.plans, group.index, noAudio, cutOffsetSec,
				)
				successful := result.SuccessfulItems()
				if cutErr != nil && len(successful) == 0 {
					if ctx.Err() == nil {
						setError(fmt.Errorf("orchestrator: stock.extract_clips: executeCuts failed for source %s: %w", group.sourceID, cutErr))
					}
					return
				}
				results[groupIndex] = stockSourceCutResult{
					group:     group,
					result:    result,
					processed: true,
				}
			}
		}
	}

	wg.Add(parallelism)
	for i := 0; i < parallelism; i++ {
		go worker()
	}

	dispatching := true
	for i := range groups {
		if !dispatching {
			break
		}
		select {
		case jobs <- i:
		case <-workCtx.Done():
			dispatching = false
		}
	}
	close(jobs)
	wg.Wait()

	errMu.Lock()
	deferredErr := firstErr
	errMu.Unlock()
	if deferredErr != nil {
		return nil, deferredErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s StockExtractClipsStep) Run(ctx context.Context, runner core.StepRunner) error {
	cutter := runner.Cutter()
	plans := runner.State().Plan

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: starting",
			zap.Int("plan_count", len(plans)),
			zap.Int("staged_sources", len(runner.State().StagedAssets)))
	}

	if cutter == nil {
		if len(plans) > 0 {
			return core.ErrStockExtractClipsCutterRequired
		}
		if runner.Log() != nil {
			runner.Log().Debug("orchestrator: stock.extract_clips: VideoCutter nil + empty plan — test-fixture path")
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

	stagedBySource := stagedAssetMap(runner.State().StagedAssets)
	in := runner.RunInput()
	// Staging is source-scoped, and each source is downloaded once. The plan
	// keeps ABSOLUTE timestamps (they are the published clip identity: titles,
	// metadata, artifact rows), while the staged file — in sections_only runs —
	// starts at the source's section start. cutOffsetSec bridges the two, so seek
	// arithmetic only ever happens at the cutter.
	groups := orderedPlanGroups(plans)
	for i := range groups {
		groups[i].cutOffsetSec = SectionCutOffsetSec(in, groups[i].plans)
	}
	noAudio := in != nil && in.NoAudio
	batchID := runner.JobID()
	rootFolderName := publish.RunInputRootFolderName(in)
	resolvedFolderID := publish.RunInputResolvedFolderID(in)
	timestampGroupName := publish.RunInputTimestampGroupName(in)
	if in != nil && len(in.Clips) > 0 {
		timestampGroupName = publish.RunInputTimestampParentGroupName(in)
	}

	preparedGroups, err := prepareDurableGroups(ctx, runner, groups, stagedBySource, batchID)
	if err != nil {
		return err
	}

	cutResults, cutErr := boundedSourceCuts(ctx, runner, groups, stagedBySource, noAudio)
	if cutErr != nil {
		// The fan-out marks artifacts EXTRACTING before any cutter starts.
		// Reconcile that durable state on both internal failures and caller
		// cancellation so retries do not inherit stranded in-flight rows.
		return reconcileRetryable(ctx, runner, batchID, preparedGroups, cutErr)
	}

	// Merge and publish strictly in plan order. This is deliberate: it keeps
	// segment filenames and timestamp metadata deterministic, and avoids
	// requiring every transactional writer/metadata buffer implementation to
	// become concurrently mutable. The expensive FFmpeg work already ran in
	// the bounded fan-out above.
	cutPaths, publishedChunks, contractCounts, contractDurations, groupBuckets, err :=
		publishSourceGroups(ctx, runner, cutResults, preparedGroups, batchID,
			rootFolderName, resolvedFolderID, timestampGroupName, in)
	if err != nil {
		return err
	}

	if err := validateProducedDurationContract(in, plans, contractCounts, contractDurations); err != nil {
		return err
	}
	return finalizeExtractRun(ctx, runner, in, groups, cutPaths, publishedChunks, groupBuckets, rootFolderName, resolvedFolderID)
}

// stagedAssetMap indexes staged sources by their non-empty SourceID.
func stagedAssetMap(stagedAssets []*assets.StagedAsset) map[string]*assets.StagedAsset {
	stagedBySource := make(map[string]*assets.StagedAsset)
	for _, sa := range stagedAssets {
		if sa.SourceID != "" && sa.LocalPath != "" {
			stagedBySource[sa.SourceID] = sa
		}
	}
	return stagedBySource
}

// prepareDurableGroups creates all durable batch/group/artifact rows before
// fan-out and marks the group's artifacts EXTRACTING. Durable errors are
// reconciled to RETRY_WAIT for every group already prepared.
func prepareDurableGroups(ctx context.Context, runner core.StepRunner, groups []stockSourceGroup, stagedBySource map[string]*assets.StagedAsset, batchID string) ([]stockSourceGroup, error) {
	batchEnsured := false
	preparedGroups := make([]stockSourceGroup, 0, len(groups))
	for _, group := range groups {
		staged := stagedBySource[group.sourceID]
		if staged == nil {
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.extract_clips: source not staged — skipping",
					zap.String("source_id", group.sourceID),
					zap.Int("clip_count", len(group.plans)))
			}
			continue
		}
		var err error
		// Include the group before preparation so a partially-created
		// group/artifact set is also eligible for retry reconciliation.
		preparedGroups = append(preparedGroups, group)
		batchEnsured, err = prepareBatchState(ctx, runner, DurableSourceIDForGroup(group.sourceID, group.plans), group.plans, len(groups), len(runner.State().Plan), batchID, batchEnsured)
		if err != nil {
			return nil, reconcileRetryable(ctx, runner, batchID, preparedGroups, err)
		}
		if err := markArtifactsExtracting(ctx, runner, batchID, DurableSourceIDForGroup(group.sourceID, group.plans), group.plans); err != nil {
			return nil, reconcileRetryable(ctx, runner, batchID, preparedGroups, err)
		}
	}
	return preparedGroups, nil
}

// reconcileRetryable marks the prepared groups RETRY_WAIT and returns the
// original cause (or a combined error when reconciliation itself fails).
func reconcileRetryable(ctx context.Context, runner core.StepRunner, batchID string, groups []stockSourceGroup, cause error) error {
	reconcileCtx := context.WithoutCancel(ctx)
	if reconcileErr := markGroupsRetryable(reconcileCtx, runner, batchID, groups, cause.Error()); reconcileErr != nil {
		return fmt.Errorf("%w; durable extraction reconciliation failed: %v", cause, reconcileErr)
	}
	return cause
}

// publishSourceGroups publishes each processed source group in plan order,
// accumulating cut paths, chunks and the produced-duration contract counters.
func publishSourceGroups(ctx context.Context, runner core.StepRunner, cutResults []stockSourceCutResult,
	preparedGroups []stockSourceGroup, batchID, rootFolderName, resolvedFolderID, timestampGroupName string,
	in *core.RunInput) ([]string, []core.ChunkState, map[string]int, map[string]float64, map[string]*timestampGroupBuffer, error) {

	var cutPaths []string
	var publishedChunks []core.ChunkState
	groupBuckets := make(map[string]*timestampGroupBuffer)
	segmentCounts := make(map[string]int)
	contractCounts := make(map[string]int)
	contractDurations := make(map[string]float64)

	for _, cut := range cutResults {
		if !cut.processed {
			continue
		}
		group := cut.group
		durableSourceID := DurableSourceIDForGroup(group.sourceID, group.plans)
		result := cut.result
		if in != nil && in.ClipsPerSource > 0 {
			for i, plan := range group.plans {
				if i < len(result.Items) && result.Items[i].Status != core.CutItemStatusFailed {
					contractCounts[plan.SourceID]++
					contractDurations[plan.SourceID] += result.Items[i].DurationSec
				}
			}
		}

		sourceCutPaths, sourceChunks, pubErr := publishCuts(ctx, runner, durableSourceID, group.index, group.plans,
			result, segmentCounts, groupBuckets, rootFolderName, resolvedFolderID, timestampGroupName, in, batchID)
		if pubErr != nil {
			return nil, nil, nil, nil, nil, reconcileRetryable(ctx, runner, batchID, preparedGroups, pubErr)
		}
		cutPaths = append(cutPaths, sourceCutPaths...)
		publishedChunks = append(publishedChunks, sourceChunks...)

		if runner.Log() != nil {
			runner.Log().Info("orchestrator: stock.extract_clips: source complete",
				zap.String("source_id", group.sourceID),
				zap.Int("planned", len(group.plans)),
				zap.Int("produced", len(sourceCutPaths)),
				zap.Int("source_index", group.index))
		}
	}

	return cutPaths, publishedChunks, contractCounts, contractDurations, groupBuckets, nil
}

// validateProducedDurationContract enforces clips-per-source and target
// duration (±3s) when the run declares them.
func validateProducedDurationContract(in *core.RunInput, plans []core.ClipPlan, counts map[string]int, durations map[string]float64) error {
	if in == nil || in.ClipsPerSource <= 0 {
		return nil
	}
	for source := range core.UniquePlanSources(plans) {
		count := counts[source]
		duration := durations[source]
		if count != in.ClipsPerSource || duration < float64(in.TargetDurationPerSourceSeconds-3) || duration > float64(in.TargetDurationPerSourceSeconds+3) {
			return fmt.Errorf("orchestrator: stock.extract_clips: source %q violates produced duration contract: clips=%d duration=%.3fs", source, count, duration)
		}
	}
	return nil
}

// finalizeExtractRun writes the timestamp groups, enforces the zero-cut
// production gate and publishes the successful state to the runner.
func finalizeExtractRun(ctx context.Context, runner core.StepRunner, in *core.RunInput, groups []stockSourceGroup,
	cutPaths []string, publishedChunks []core.ChunkState, groupBuckets map[string]*timestampGroupBuffer,
	rootFolderName, resolvedFolderID string) error {

	// Publish group metadata.
	if err := writeTimestampGroups(ctx, runner, in, rootFolderName, resolvedFolderID, groupBuckets, runner.ArtifactPreparation()); err != nil {
		return err
	}

	// Production gate: zero cut files → terminal error.
	if len(cutPaths) == 0 {
		return fmt.Errorf("orchestrator: stock.extract_clips: zero cut files produced across %d sources", len(groups))
	}

	runner.State().CutPaths = cutPaths
	// The cutter output is already the canonical final artifact when no
	// effects or transitions are requested. Populate the downstream state
	// before the extract checkpoint so publish can proceed even though the
	// compose step is omitted by the orchestrator.
	if isCanonicalFinalCut(in) {
		runner.State().ComposedPaths = append([]string(nil), cutPaths...)
	}
	if len(publishedChunks) > 0 {
		runner.State().Published = publishedChunks
	}

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: SUCCEEDED",
			zap.Int("cut_paths", len(cutPaths)),
			zap.Int("sources_processed", len(groups)),
			zap.Int("source_groups", len(groups)))
	}
	return nil
}

// DurableSourceIDForGroup keeps SQLite identity on the original source while
// allowing sections_only to use one local staging key per clip.
func DurableSourceIDForGroup(stageKey string, plans []core.ClipPlan) string {
	if len(plans) > 0 && plans[0].SourceID != "" {
		return plans[0].SourceID
	}
	return stageKey
}
