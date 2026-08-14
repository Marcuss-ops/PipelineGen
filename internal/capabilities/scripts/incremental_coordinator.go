// Package scriptgeneration — incremental_coordinator.go owns the
// VidRushIncrementalCoordinator: it reacts to SceneCommitted events by
// enriching each stable scene as soon as it is committed, so scene generation
// and VidRush enrichment overlap instead of running as two sequential blocks.
// Results are immutable and merged in canonical scene order at the barrier;
// a result derived from older scene text is fenced out and never applied.
package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// VidRushIncrementalCoordinator consumes SceneCommitted events and enriches
// each scene concurrently with generation. It never mutates shared scene
// state: every enrichment produces an immutable VidRushSegmentResult that is
// merged in canonical SceneIndex order at the barrier. Enrichment is bounded
// by a concurrency limit so a slow provider cannot spawn unbounded goroutines.
type VidRushIncrementalCoordinator struct {
	enricher       SegmentEnricher
	resolver       SegmentProviderResolver
	plan           *scriptpkg.ResolvedGenerationPlan
	maxConcurrency int

	mu         sync.Mutex
	latest     map[int]SceneCommitted
	records    map[int]segmentResultRecord
	staleCount int
	sem        chan struct{}
	wg         sync.WaitGroup
}

// segmentResultRecord is the immutable per-scene enrichment outcome. revision
// and textHash carry the identity of the commit that produced it so a result
// derived from superseded scene text can be discarded at the barrier.
type segmentResultRecord struct {
	revision int64
	textHash string
	result   scriptpkg.VidRushSegmentResult
	err      error
}

// NewVidRushIncrementalCoordinator constructs an incremental coordinator.
// maxConcurrency bounds concurrent enrichments; values <= 0 default to 4.
func NewVidRushIncrementalCoordinator(enricher SegmentEnricher, plan *scriptpkg.ResolvedGenerationPlan, maxConcurrency int) *VidRushIncrementalCoordinator {
	if maxConcurrency <= 0 {
		maxConcurrency = 4
	}
	return &VidRushIncrementalCoordinator{
		enricher:       enricher,
		plan:           plan,
		maxConcurrency: maxConcurrency,
		latest:         make(map[int]SceneCommitted),
		records:        make(map[int]segmentResultRecord),
		sem:            make(chan struct{}, maxConcurrency),
	}
}

// compile-time contract: the coordinator is the SceneCommitObserver seam that
// reacts to stable scenes emitted by the runner.
var _ SceneCommitObserver = (*VidRushIncrementalCoordinator)(nil)

// SetSegmentProviderResolver wires the per-segment provider fanout that runs
// after entity extraction. A nil resolver is safe and leaves the enrichment
// at the entities+queries stage (no candidate media is resolved).
func (c *VidRushIncrementalCoordinator) SetSegmentProviderResolver(resolver SegmentProviderResolver) {
	if c != nil {
		c.resolver = resolver
	}
}

// OnSceneCommitted records the stable scene and launches its enrichment
// asynchronously so generation can continue while VidRush works on the
// previous scene. It fails closed on a missing enricher or an invalid event;
// enrichment failures are attributed to their scene and surfaced at Wait.
func (c *VidRushIncrementalCoordinator) OnSceneCommitted(ctx context.Context, event SceneCommitted) error {
	if c.enricher == nil {
		return fmt.Errorf("vidrush incremental coordinator: SegmentEnricher not configured")
	}
	if event.SceneID == "" {
		return fmt.Errorf("vidrush incremental coordinator: scene commit missing scene id")
	}

	c.mu.Lock()
	c.latest[event.SceneIndex] = event
	c.wg.Add(1)
	c.mu.Unlock()

	scene := scriptpkg.SpecScene{
		ID:    event.SceneID,
		Index: event.SceneIndex,
		Text:  event.Text,
	}
	go func() {
		defer c.wg.Done()
		select {
		case c.sem <- struct{}{}:
		case <-ctx.Done():
			c.recordResult(event, scriptpkg.VidRushSegmentResult{}, ctx.Err())
			return
		}
		defer func() { <-c.sem }()

		result, err := c.enricher.Enrich(ctx, c.plan, scene)
		if err == nil && c.resolver != nil {
			// Provider fan-out starts only after entity extraction has produced
			// the segment's retrieval queries; generation of the next scene
			// continues meanwhile because this runs inside the bounded worker.
			result, err = c.resolver.ResolveProviders(ctx, c.plan, result)
		}
		c.recordResult(event, result, err)
	}()
	return nil
}

// recordResult stores an immutable enrichment outcome only when it still
// matches the latest committed scene identity. A result produced from
// superseded text (older revision or text hash) is discarded as stale.
func (c *VidRushIncrementalCoordinator) recordResult(event SceneCommitted, result scriptpkg.VidRushSegmentResult, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	latest, ok := c.latest[event.SceneIndex]
	if !ok || latest.Revision != event.Revision || latest.TextHash != event.TextHash {
		c.staleCount++
		return
	}
	c.records[event.SceneIndex] = segmentResultRecord{
		revision: event.Revision,
		textHash: event.TextHash,
		result:   result,
		err:      err,
	}
}

// Wait blocks until every committed scene has finished enriching, then returns
// the immutable results in canonical SceneIndex order. It waits only for
// scenes that were committed (never re-runs a whole-document extraction) and
// fails on the first enrichment error, attributed to its scene.
//
// Wait must be called after all OnSceneCommitted calls for the run have
// completed: the barrier observes the committed set at the moment it is
// awaited, so a scene committed after Wait has returned is not included.
func (c *VidRushIncrementalCoordinator) Wait(ctx context.Context) ([]scriptpkg.VidRushSegmentResult, error) {
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	indexes := make([]int, 0, len(c.latest))
	for idx := range c.latest {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	results := make([]scriptpkg.VidRushSegmentResult, 0, len(indexes))
	for _, idx := range indexes {
		latest := c.latest[idx]
		record, ok := c.records[idx]
		if !ok {
			continue
		}
		if record.err != nil {
			return nil, fmt.Errorf("vidrush scene %q (index %d) enrichment: %w", latest.SceneID, idx, record.err)
		}
		// Defensive final fence: never apply a result whose identity does not
		// match the latest committed scene text.
		if record.revision != latest.Revision || record.textHash != latest.TextHash {
			continue
		}
		results = append(results, record.result)
	}
	return results, nil
}

// StaleResults reports how many enrichment results were discarded because a
// newer scene revision superseded them. It is the bounded observability
// counter consumed by the vidrush_stale_results_total metric.
func (c *VidRushIncrementalCoordinator) StaleResults() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.staleCount
}
