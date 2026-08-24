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
package ingest

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.uber.org/zap"

	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
)

// ErrStockClipsOutOfRange (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026)
// surfaces a clip whose EndSec exceeds the probed source duration.
var (
	ErrStockClipsOutOfRange      = errors.New("stock.extract_clips: clip EndSec exceeds source duration")
	ErrStockClipsUnknownDuration = errors.New("stock.extract_clips: source duration is unknown")
)

// maxDriveUploadWorkers caps concurrent Drive uploads per source group.
const maxDriveUploadWorkers = 3

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips (Step 3 of the 6-step pipeline).
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

// stockSourceGroup is an ordered source partition of the clip plan. The
// order is intentionally derived from the plan rather than a map so output
// paths, artifact ordinals, metadata ordering, and retries remain stable.
type stockSourceGroup struct {
	sourceID string
	plans    []ClipPlan
	index    int
}

type stockSourceCutResult struct {
	group     stockSourceGroup
	result    CutBatchResult
	processed bool
}

// orderedPlanGroups groups plans by SourceID while preserving the first
// occurrence order. A map is still used by groupPlans for compatibility, but
// extraction must never range over that map because Go deliberately randomizes
// map iteration order.
func orderedPlanGroups(plans []ClipPlan) []stockSourceGroup {
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
func boundedSourceCuts(ctx context.Context, runner StepRunner, groups []stockSourceGroup,
	stagedBySource map[string]*assets.StagedAsset, noAudio bool) ([]stockSourceCutResult, error) {
	results := make([]stockSourceCutResult, len(groups))
	if len(groups) == 0 {
		return results, nil
	}

	parallelism := runner.Cfg().MaxConcurrentJobs
	if parallelism <= 0 {
		parallelism = DefaultMaxConcurrentJobs
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

				sourceDuration, _, validationErr := validateAndProbeSourceDuration(
					workCtx, runner, group.sourceID, staged.LocalPath, staged, group.plans)
				if validationErr != nil {
					if ctx.Err() == nil {
						setError(validationErr)
					}
					return
				}
				result, cutErr := executeCuts(
					workCtx, runner, group.sourceID, staged.LocalPath, sourceDuration,
					group.plans, group.index, noAudio,
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

func (s StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	cutter := runner.Cutter()

	plans := runner.State().Plan

	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.extract_clips: starting",
			zap.Int("plan_count", len(plans)),
			zap.Int("staged_sources", len(runner.State().StagedAssets)))
	}

	if cutter == nil {
		if len(plans) > 0 {
			return ErrStockExtractClipsCutterRequired
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

	// Build sourceID → *StagedAsset map.
	stagedBySource := make(map[string]*assets.StagedAsset)
	for _, sa := range runner.State().StagedAssets {
		if sa.SourceID != "" && sa.LocalPath != "" {
			stagedBySource[sa.SourceID] = sa
		}
	}

	in := runner.RunInput()
	// Staging is source-scoped even in sections_only mode. The source file
	// is downloaded once; each plan keeps its original timestamps for the
	// local cutter below.
	groups := orderedPlanGroups(plans)
	noAudio := in != nil && in.NoAudio
	batchID := runner.JobID()
	rootFolderName := stockRootFolderName(in)
	resolvedFolderID := stockResolvedFolderID(in)
	timestampGroupName := stockTimestampGroupName(in)
	if in != nil && len(in.Clips) > 0 {
		timestampGroupName = stockTimestampParentGroupName(in)
	}

	// Create all durable batch/group/artifact rows before fan-out. This keeps
	// parent creation deterministic and prevents concurrent SQLite writers from
	// racing on the one batch row. Per-artifact lifecycle updates still happen
	// in the existing publication path and retain their idempotent keys.
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
		batchEnsured, err = prepareBatchState(ctx, runner, durableSourceIDForGroup(group.sourceID, group.plans), group.plans, len(groups), len(plans), batchID, batchEnsured)
		if err != nil {
			reconcileCtx := context.WithoutCancel(ctx)
			if reconcileErr := markGroupsRetryable(reconcileCtx, runner, batchID, preparedGroups, err.Error()); reconcileErr != nil {
				return fmt.Errorf("%w; durable extraction reconciliation failed: %v", err, reconcileErr)
			}
			return err
		}
		if err := markArtifactsExtracting(ctx, runner, batchID, durableSourceIDForGroup(group.sourceID, group.plans), group.plans); err != nil {
			reconcileCtx := context.WithoutCancel(ctx)
			if reconcileErr := markGroupsRetryable(reconcileCtx, runner, batchID, preparedGroups, err.Error()); reconcileErr != nil {
				return fmt.Errorf("%w; durable extraction reconciliation failed: %v", err, reconcileErr)
			}
			return err
		}
	}

	cutResults, cutErr := boundedSourceCuts(ctx, runner, groups, stagedBySource, noAudio)
	if cutErr != nil {
		// The fan-out marks artifacts EXTRACTING before any cutter starts.
		// Reconcile that durable state on both internal failures and caller
		// cancellation so retries do not inherit stranded in-flight rows.
		reconcileCtx := context.WithoutCancel(ctx)
		if reconcileErr := markGroupsRetryable(reconcileCtx, runner, batchID, preparedGroups, cutErr.Error()); reconcileErr != nil {
			return fmt.Errorf("%w; durable extraction reconciliation failed: %v", cutErr, reconcileErr)
		}
		return cutErr
	}
	// Merge and publish strictly in plan order. This is deliberate: it keeps
	// segment filenames and timestamp metadata deterministic, and avoids
	// requiring every transactional writer/metadata buffer implementation to
	// become concurrently mutable. The expensive FFmpeg work already ran in
	// the bounded fan-out above.
	var cutPaths []string
	var publishedChunks []ChunkState
	groupBuckets := make(map[string]*timestampGroupBuffer)
	segmentCounts := make(map[string]int)
	contractCounts := make(map[string]int)
	contractDurations := make(map[string]float64)

	for _, cut := range cutResults {
		if !cut.processed {
			continue
		}
		group := cut.group
		durableSourceID := durableSourceIDForGroup(group.sourceID, group.plans)
		result := cut.result
		if in != nil && in.ClipsPerSource > 0 {
			for i, plan := range group.plans {
				if i < len(result.Items) && result.Items[i].Status != CutItemStatusFailed {
					contractCounts[plan.SourceID]++
					contractDurations[plan.SourceID] += result.Items[i].DurationSec
				}
			}
		}

		sourceCutPaths, sourceChunks, pubErr := publishCuts(ctx, runner, durableSourceID, group.index, group.plans,
			result, segmentCounts, groupBuckets, rootFolderName, resolvedFolderID, timestampGroupName, in, batchID)
		if pubErr != nil {
			reconcileCtx := context.WithoutCancel(ctx)
			if reconcileErr := markGroupsRetryable(reconcileCtx, runner, batchID, preparedGroups, pubErr.Error()); reconcileErr != nil {
				return fmt.Errorf("%w; durable extraction reconciliation failed: %v", pubErr, reconcileErr)
			}
			return pubErr
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

	if in != nil && in.ClipsPerSource > 0 {
		for source := range uniquePlanSources(plans) {
			count := contractCounts[source]
			duration := contractDurations[source]
			if count != in.ClipsPerSource || duration < float64(in.TargetDurationPerSourceSeconds-3) || duration > float64(in.TargetDurationPerSourceSeconds+3) {
				return fmt.Errorf("orchestrator: stock.extract_clips: source %q violates produced duration contract: clips=%d duration=%.3fs", source, count, duration)
			}
		}
	}

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

// durableSourceIDForGroup keeps SQLite identity on the original source while
// allowing sections_only to use one local staging key per clip.
func durableSourceIDForGroup(stageKey string, plans []ClipPlan) string {
	if len(plans) > 0 && plans[0].SourceID != "" {
		return plans[0].SourceID
	}
	return stageKey
}
