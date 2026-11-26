// Package scriptgeneration — incremental_coordinator_stages.go: the
// per-segment research → provider fan-out → materialize stage pipeline driven
// by the incremental coordinator.
//
// Each stage is independently bounded (its own semaphore) so provider fan-out
// never competes with extraction, and every stage runs off the generation
// goroutine: scene generation never waits on VidRush work.
//
// Extracted 2026-09-12 from incremental_coordinator.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrProviderResolveBudget marks a provider fan-out that exhausted the run's
// provider budget while the scene context was still alive. The coordinator
// degrades such a scene to its already-enriched text instead of failing the
// whole run: missing media is a quality regression, a failed run is a
// delivery regression.
var ErrProviderResolveBudget = errors.New("vidrush provider resolve budget exhausted")

// vidRushProviderResolveTimeout bounds the provider fan-out of the WHOLE run.
// The measured tail (Nov 2026: 5.92 h of provider resolve across 96 runs,
// 16 scenes parked on the old five-minute per-scene cap) showed that a
// per-scene cap only multiplied the wait by the number of slow scenes.
// Two minutes is still ~17x the measured median (6.8 s) and leaves thirteen
// of the scene's fifteen minutes for acquisition/verification, while a
// stalled provider can no longer hold the run barrier for five minutes.
const vidRushProviderResolveTimeout = 2 * time.Minute

// vidRushProviderSegmentResolveTimeout caps a SINGLE scene's fan-out. A
// contextual SearXNG search measured 28 s just to return candidates, so a
// 20-second cap cancelled useful searches before they could be materialized.
// The run-wide budget still bounds cumulative provider time across scenes.
const vidRushProviderSegmentResolveTimeout = 45 * time.Second

func (c *VidRushIncrementalCoordinator) providerSegmentTimeout() time.Duration {
	if c != nil && c.providerSegmentBudget > 0 {
		return c.providerSegmentBudget
	}
	return vidRushProviderSegmentResolveTimeout
}

func (c *VidRushIncrementalCoordinator) providerResolveTimeout() time.Duration {
	if c != nil && c.providerResolveBudget > 0 {
		return c.providerResolveBudget
	}
	return vidRushProviderResolveTimeout
}

// remainingProviderBudget starts the run-scoped provider clock on the first
// fan-out and returns what is left of it. A scene that begins after the budget
// is exhausted gets no provider work at all; the caller degrades it to text.
func (c *VidRushIncrementalCoordinator) remainingProviderBudget() time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.providerBudgetStart.IsZero() {
		c.providerBudgetStart = time.Now()
	}
	remaining := time.Until(c.providerBudgetStart.Add(c.providerResolveTimeout()))
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// searchProviders runs the provider fan-out stage under its own bounded
// semaphore, independent of the extraction limit, and under the run-scoped
// provider budget: every scene shares one clock, so a late scene cannot
// restart the wait after the barrier has already been held.
func (c *VidRushIncrementalCoordinator) researchSegment(ctx context.Context, result scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if c.research == nil {
		return result, nil
	}
	var report *scriptpkg.ResearchReport
	err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage: StageSceneAnalysis, Component: kernobs.ComponentNLP, Operation: kernobs.OperationSearch,
	}, func(opCtx context.Context) error {
		var researchErr error
		report, researchErr = c.research.ResearchSegment(opCtx, c.plan, result)
		return researchErr
	})
	if err != nil {
		return result, err
	}
	if report != nil {
		result.Insights.ResearchSources = report.Sources
	}
	return result, nil
}

func (c *VidRushIncrementalCoordinator) searchProviders(ctx context.Context, result scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if c.resolver == nil {
		return result, nil
	}
	timeout := c.remainingProviderBudget()
	if segmentTimeout := c.providerSegmentTimeout(); segmentTimeout > 0 && segmentTimeout < timeout {
		// A single scene never gets more than its own slice, even while the run
		// budget still has room.
		timeout = segmentTimeout
	}
	if timeout <= 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"provider resolve exceeded %s; continuing without provider candidates", c.providerResolveTimeout()))
		return result, nil
	}
	// Admission is MEASURED as its own operation: the wait for a fan-out slot
	// belongs to the coordinator's contention, not to the provider (ELIM-02).
	// Folding it into resolve made a semaphore wait look like a slow provider
	// and left the phase residual unexplained.
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage: StageSceneAnalysis, Component: kernobs.ComponentProviderResolver, Operation: kernobs.OperationAdmissionWait,
	}, func(opCtx context.Context) error {
		return c.acquire(opCtx, c.providerSem)
	}); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	defer c.release(c.providerSem)
	// The scene context may live longer than a stalled provider deserves. A
	// separate deadline lets the enrichment continue with the text it already
	// has instead of burning the whole scene budget on one fan-out.
	resolveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var resolved scriptpkg.VidRushSegmentResult
	err := kernobs.MeasureOperation(resolveCtx, kernobs.OperationInfo{
		Stage: StageSceneAnalysis, Component: kernobs.ComponentProviderResolver, Operation: kernobs.OperationResolve,
	}, func(opCtx context.Context) error {
		var resolveErr error
		resolved, resolveErr = c.resolver.ResolveProviders(opCtx, c.plan, result)
		return resolveErr
	})
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || (resolveCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil)) {
		err = fmt.Errorf("%w: %w", ErrProviderResolveBudget, err)
	}
	if errors.Is(err, ErrProviderResolveBudget) && ctx.Err() == nil {
		// Resolvers may finish one provider while another is still blocked. Keep
		// candidates already merged by the resolver instead of turning a partial
		// success into an empty scene when the bounded context expires.
		partial := result
		if strings.TrimSpace(resolved.SegmentID) != "" {
			partial = resolved
			if len(partial.Assets.Candidates) == 0 {
				partial.Assets.Candidates = result.Assets.Candidates
			}
			if len(partial.Assets.SecondaryImages) == 0 {
				partial.Assets.SecondaryImages = result.Assets.SecondaryImages
			}
			if len(partial.Warnings) == 0 {
				partial.Warnings = append([]string(nil), result.Warnings...)
			}
		}
		candidateCount := len(partial.Assets.Candidates)
		partial.Warnings = append(partial.Warnings, fmt.Sprintf(
			"provider resolve exceeded %s; continuing with %d candidate(s) retained", timeout, candidateCount))
		return partial, nil
	}
	return resolved, err
}

// materializeSegment runs the acquire/verify/finalize stage under its own
// bounded semaphore, independent of extraction and search limits.
func (c *VidRushIncrementalCoordinator) materializeSegment(ctx context.Context, result scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if c.materializer == nil {
		return result, nil
	}
	if err := c.acquire(ctx, c.materializeSem); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	defer c.release(c.materializeSem)
	return c.materializer.Materialize(ctx, c.plan, result)
}

// acquire takes a slot from a bounded semaphore or returns ctx.Err().
func (c *VidRushIncrementalCoordinator) acquire(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release returns a slot to a bounded semaphore.
func (c *VidRushIncrementalCoordinator) release(sem chan struct{}) {
	<-sem
}

// enrichAndMaterialize runs the full per-scene enrichment chain and records
// the nlp and asset stage lifecycles on the run bound to ctx.
//
// §7.3 trace:
//   - nlp   — readyAt is the committed scene's text-ready instant; the stage
//     covers semantic extraction and research.
//   - asset — chains to the completed analysis (its readyAt) and covers the
//     provider fan-out plus the acquire/verify/finalize stage.
//
// Each entry records the real span the scene waited for that stage, including
// the stage's own bounded-pool waits, and carries the stage error (if any) so
// a failed scene is visible in the trace instead of silently absent.
func (c *VidRushIncrementalCoordinator) enrichAndMaterialize(ctx context.Context, event SceneCommitted, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	// Stage 1 — bounded BrainRust semantic extraction. This CPU worker is
	// independent from the local Ollama model used for scene generation.
	nlpStartedAt := time.Now().UTC()
	result, err := c.enrichSegment(ctx, scene)
	if err == nil {
		result, err = c.researchSegment(ctx, result)
	}
	nlpCompletedAt := time.Now().UTC()
	recordSceneStage(ctx, event.SceneID, kernobs.SceneStageNLP, kernobs.SceneStageTimes{
		ReadyAt: event.ReadyAt, StartedAt: nlpStartedAt, CompletedAt: nlpCompletedAt,
	}, "", err)
	if err != nil {
		return result, err
	}

	// Stage 2 — provider fan-out, then acquire/verify/finalize. This is the
	// asset stage: it cannot begin before the analysis that produced the
	// queries is complete.
	assetStartedAt := time.Now().UTC()
	result, err = c.searchProviders(ctx, result)
	if err == nil {
		result, err = c.materializeSegment(ctx, result)
	}
	if err == nil {
		result = c.annotateZeroImageReason(result)
	}
	recordSceneStage(ctx, event.SceneID, kernobs.SceneStageAsset, kernobs.SceneStageTimes{
		ReadyAt: nlpCompletedAt, StartedAt: assetStartedAt, CompletedAt: time.Now().UTC(),
	}, "", err)
	return result, err
}

// annotateZeroImageReason records on the persisted segment WHY the image
// target was missed. The fail-fast gate already carries the reason in its
// error; writing it to Insights.ImageSearchNoImageReason puts the same stable
// code in the run result (and therefore in /full), so one scene's zero does
// not require a second investigation of provider logs.
func (c *VidRushIncrementalCoordinator) annotateZeroImageReason(result scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	if c == nil {
		return result
	}
	c.mu.Lock()
	spec := c.imageFanoutSpec
	c.mu.Unlock()
	if spec == nil {
		return result
	}
	violation := mediacert.CheckSegmentImageFanout(*spec, mediacert.ResultSegment{
		SegmentID: result.SegmentID,
		Position:  result.Position,
		Assets:    result.Assets,
		Warnings:  result.Warnings,
	})
	if violation == nil {
		return result
	}
	result.Insights.ImageSearchNoImageReason = mediacert.ZeroImageReason(result.Assets, result.Warnings)
	return result
}
