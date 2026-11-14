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
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ErrProviderResolveBudget marks a provider fan-out that exhausted its
// per-scene budget while the scene context was still alive. The coordinator
// degrades such a scene to its already-enriched text instead of failing the
// whole run: missing media is a quality regression, a failed run is a
// delivery regression.
var ErrProviderResolveBudget = errors.New("vidrush provider resolve budget exhausted")

// vidRushProviderResolveTimeout leaves at least ten of the scene's fifteen
// minutes for acquisition/verification after a stalled provider fan-out.
const vidRushProviderResolveTimeout = 5 * time.Minute

func (c *VidRushIncrementalCoordinator) providerResolveTimeout() time.Duration {
	if c != nil && c.providerResolveBudget > 0 {
		return c.providerResolveBudget
	}
	return vidRushProviderResolveTimeout
}

// searchProviders runs the provider fan-out stage under its own bounded
// semaphore, independent of the extraction limit.
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
	if err := c.acquire(ctx, c.providerSem); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	defer c.release(c.providerSem)
	timeout := c.providerResolveTimeout()
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
		result.Warnings = append(result.Warnings, fmt.Sprintf(
			"provider resolve exceeded %s; continuing without provider candidates", timeout))
		return result, nil
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
