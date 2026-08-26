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
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushBarrier is the run-scoped final barrier for incremental VidRush
// enrichment. The runner awaits it after generation completes; it blocks only
// for enrichments still running and never re-runs the whole-document
// EntitiesProcessor.
type VidRushBarrier interface {
	WaitForVidRush(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error)
}

// VidRushIncrementalCoordinator consumes SceneCommitted events and enriches
// each scene concurrently with generation. It never mutates shared scene
// state: every enrichment produces an immutable VidRushSegmentResult that is
// merged in canonical SceneIndex order at the barrier. Enrichment is bounded
// by a concurrency limit so a slow provider cannot spawn unbounded goroutines.
type VidRushIncrementalCoordinator struct {
	enricher     SegmentEnricher
	resolver     SegmentProviderResolver
	materializer SegmentMaterializer
	plan         *scriptpkg.ResolvedGenerationPlan
	backpressure VidRushBackpressure
	gate         *GenerationGate

	metrics VidRushMetrics

	mu             sync.Mutex
	runID          string
	latest         map[int]SceneCommitted
	records        map[int]segmentResultRecord
	staleCount     int
	extractSem     chan struct{}
	providerSem    chan struct{}
	materializeSem chan struct{}
	wg             sync.WaitGroup

	// Timing surface (guarded by mu). genStart/genEnd are marked by the
	// runner; firstEnrichmentStart/barrierStart/barrierEnd by the coordinator.
	genStart             time.Time
	genEnd               time.Time
	firstEnrichmentStart time.Time
	barrierStart         time.Time
	barrierEnd           time.Time
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
// maxConcurrency bounds the provider-search stage; values <= 0 default to 4.
// Extraction is single-slot by default (the local Ollama model) and
// materialization is bounded at 2.
func NewVidRushIncrementalCoordinator(enricher SegmentEnricher, plan *scriptpkg.ResolvedGenerationPlan, maxConcurrency int) *VidRushIncrementalCoordinator {
	backpressure := DefaultVidRushBackpressure()
	if maxConcurrency > 0 {
		backpressure.ProviderSearchLimit = maxConcurrency
	}
	return NewVidRushIncrementalCoordinatorWithBackpressure(enricher, plan, backpressure)
}

// NewVidRushIncrementalCoordinatorWithBackpressure constructs an incremental
// coordinator with explicit per-stage concurrency limits.
func NewVidRushIncrementalCoordinatorWithBackpressure(enricher SegmentEnricher, plan *scriptpkg.ResolvedGenerationPlan, backpressure VidRushBackpressure) *VidRushIncrementalCoordinator {
	bp := backpressure.resolved()
	return &VidRushIncrementalCoordinator{
		enricher:       enricher,
		plan:           plan,
		backpressure:   bp,
		latest:         make(map[int]SceneCommitted),
		records:        make(map[int]segmentResultRecord),
		extractSem:     make(chan struct{}, bp.ExtractionLimit),
		providerSem:    make(chan struct{}, bp.ProviderSearchLimit),
		materializeSem: make(chan struct{}, bp.MaterializationLimit),
	}
}

// compile-time contract: the coordinator is both the SceneCommitObserver seam
// that reacts to stable scenes and the run-scoped final barrier the runner
// awaits once generation completes.
var _ SceneCommitObserver = (*VidRushIncrementalCoordinator)(nil)
var _ VidRushBarrier = (*VidRushIncrementalCoordinator)(nil)
var _ VidRushTimingRecorder = (*VidRushIncrementalCoordinator)(nil)

// SetSegmentProviderResolver wires the per-segment provider fanout that runs
// after entity extraction. A nil resolver is safe and leaves the enrichment
// at the entities+queries stage (no candidate media is resolved).
func (c *VidRushIncrementalCoordinator) SetSegmentProviderResolver(resolver SegmentProviderResolver) {
	if c != nil {
		c.resolver = resolver
	}
}

// SetSegmentMaterializer wires the per-segment acquire/verify/finalize stage
// that runs after provider search. A nil materializer is safe and leaves the
// enrichment at the search stage (candidates are not downloaded/persisted).
func (c *VidRushIncrementalCoordinator) SetSegmentMaterializer(materializer SegmentMaterializer) {
	if c != nil {
		c.materializer = materializer
	}
}

// SetGenerationGate wires the capacity-bounded priority gate shared with
// scene generation. Entity extraction acquires it with low priority, so
// generation can preempt extraction when both use the same local Ollama
// model.
func (c *VidRushIncrementalCoordinator) SetGenerationGate(gate *GenerationGate) {
	if c != nil {
		c.gate = gate
	}
}

// SetMetrics wires the bounded per-scene VidRush metrics recorder. A nil
// recorder is safe and disables emission.
func (c *VidRushIncrementalCoordinator) SetMetrics(metrics VidRushMetrics) {
	if c != nil {
		c.metrics = metrics
	}
}

// MarkGenerationStart records the moment scene-text generation began. Only
// the first mark wins (resume/retry must not reset an earlier window).
func (c *VidRushIncrementalCoordinator) MarkGenerationStart(t time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.genStart.IsZero() {
		c.genStart = t
	}
	c.mu.Unlock()
}

// MarkGenerationComplete records the moment generation finished emitting
// stable scenes. The latest mark wins.
func (c *VidRushIncrementalCoordinator) MarkGenerationComplete(t time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.genEnd.IsZero() || t.After(c.genEnd) {
		c.genEnd = t
	}
	c.mu.Unlock()
}

// markEnrichmentStart records the first time an enrichment goroutine began
// actual work. It is called from enrichment goroutines; only the first wins.
func (c *VidRushIncrementalCoordinator) markEnrichmentStart() {
	c.mu.Lock()
	if c.firstEnrichmentStart.IsZero() {
		c.firstEnrichmentStart = time.Now()
	}
	c.mu.Unlock()
}

// RunTimings returns the per-run wall-clock timing surface. It is safe to
// call after the barrier has completed (before that, barrier fields are zero
// and VidRushTotalMS/BarrierWaitMS are not yet known).
func (c *VidRushIncrementalCoordinator) RunTimings() VidRushRunTimings {
	if c == nil {
		return VidRushRunTimings{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t := VidRushRunTimings{}
	if !c.genStart.IsZero() && !c.genEnd.IsZero() {
		t.GenerationTotalMS = durationMilliseconds(c.genEnd.Sub(c.genStart))
	}
	if !c.firstEnrichmentStart.IsZero() && !c.barrierEnd.IsZero() {
		t.VidRushTotalMS = durationMilliseconds(c.barrierEnd.Sub(c.firstEnrichmentStart))
	}
	if !c.barrierStart.IsZero() && !c.barrierEnd.IsZero() {
		t.BarrierWaitMS = durationMilliseconds(c.barrierEnd.Sub(c.barrierStart))
	}
	t.OverlapMS = c.overlapMSLocked()
	return t
}

// overlapMSLocked computes the generation↔VidRush overlap. It is non-zero
// only when the first enrichment began before generation finished. Callers
// must hold c.mu.
func (c *VidRushIncrementalCoordinator) overlapMSLocked() int64 {
	if c.firstEnrichmentStart.IsZero() || c.genEnd.IsZero() {
		return 0
	}
	if !c.firstEnrichmentStart.Before(c.genEnd) {
		return 0
	}
	return durationMilliseconds(c.genEnd.Sub(c.firstEnrichmentStart))
}

// durationMilliseconds rounds a positive duration up to one millisecond. The
// timing contract uses a positive value as the overlap success signal; flooring
// sub-millisecond test and fast-path windows to zero would misreport real
// overlap as sequential execution.
func durationMilliseconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	ms := d / time.Millisecond
	if d%time.Millisecond != 0 {
		ms++
	}
	return int64(ms)
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
	if c.runID == "" {
		c.runID = event.RunID
	} else if event.RunID != "" && c.runID != event.RunID {
		c.mu.Unlock()
		return fmt.Errorf("vidrush incremental coordinator: scene commit run %q does not match coordinator run %q", event.RunID, c.runID)
	}
	c.latest[event.SceneIndex] = event
	c.wg.Add(1)
	c.mu.Unlock()

	if c.metrics != nil {
		c.metrics.SceneCommitted()
	}

	scene := scriptpkg.SpecScene{
		ID:    event.SceneID,
		Index: event.SceneIndex,
		Text:  event.Text,
	}
	go func() {
		defer c.wg.Done()
		c.markEnrichmentStart()
		if c.metrics != nil {
			c.metrics.EnrichmentStarted()
		}
		start := time.Now()

		// Stage 1 — entity extraction. Single-slot by default (local Ollama),
		// and low-priority against the generation gate so scene generation is
		// never starved when both share the same model.
		result, err := c.enrichSegment(ctx, scene)
		if err == nil {
			result, err = c.searchProviders(ctx, result)
		}
		if err == nil {
			result, err = c.materializeSegment(ctx, result)
		}
		c.recordResult(event, result, err)
		if c.metrics != nil {
			c.metrics.EnrichmentCompleted(time.Since(start))
		}
	}()
	return nil
}

// enrichSegment runs the entity-extraction stage under its own bounded
// semaphore, with low priority against the shared generation gate.
func (c *VidRushIncrementalCoordinator) enrichSegment(ctx context.Context, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	if c.gate != nil {
		if err := c.gate.AcquireLow(ctx); err != nil {
			return scriptpkg.VidRushSegmentResult{}, err
		}
		defer c.gate.Release()
	}
	if err := c.acquire(ctx, c.extractSem); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	defer c.release(c.extractSem)
	// scene_analysis is the per-scene entity/phrase/word extraction
	// boundary. Each enrichment is recorded as an nlp.extract operation on
	// the canonical Run clock, so fan-out reports expose calls / accumulated
	// work / max call separate from the run wall time.
	var out scriptpkg.VidRushSegmentResult
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     StageSceneAnalysis,
		Component: kernobs.ComponentNLP,
		Operation: kernobs.OperationExtract,
	}, func(opCtx context.Context) error {
		var enrichErr error
		out, enrichErr = c.enricher.Enrich(opCtx, c.plan, scene)
		return enrichErr
	}); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	return out, nil
}

// searchProviders runs the provider fan-out stage under its own bounded
// semaphore, independent of the extraction limit.
func (c *VidRushIncrementalCoordinator) searchProviders(ctx context.Context, result scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	if c.resolver == nil {
		return result, nil
	}
	if err := c.acquire(ctx, c.providerSem); err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	defer c.release(c.providerSem)
	return c.resolver.ResolveProviders(ctx, c.plan, result)
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

// recordResult stores an immutable enrichment outcome only when it still
// matches the latest committed scene identity. A result produced from
// superseded text (older revision or text hash) is discarded as stale.
func (c *VidRushIncrementalCoordinator) recordResult(event SceneCommitted, result scriptpkg.VidRushSegmentResult, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	latest, ok := c.latest[event.SceneIndex]
	if !ok || latest.Revision != event.Revision || latest.TextHash != event.TextHash {
		c.staleCount++
		if c.metrics != nil {
			c.metrics.StaleResult()
		}
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
	return c.waitForVidRush(ctx)
}

// WaitForVidRush is the run-scoped final barrier. It validates that the
// requested run owns this coordinator (fail closed on mismatch), then waits
// only for the enrichments still running — it never re-runs the whole-document
// EntitiesProcessor — and returns the immutable results in canonical order.
func (c *VidRushIncrementalCoordinator) WaitForVidRush(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
	if runID == "" {
		return nil, fmt.Errorf("vidrush barrier: missing run id")
	}
	c.mu.Lock()
	owner := c.runID
	c.mu.Unlock()
	if owner != "" && owner != runID {
		return nil, fmt.Errorf("vidrush barrier: run %q does not own this coordinator (owner %q)", runID, owner)
	}
	return c.waitForVidRush(ctx)
}

// waitForVidRush performs the actual barrier: block until every committed
// scene's enrichment has finished, then return the fenced, canonically ordered
// results. Pending scenes only — no re-extraction of the full document.
func (c *VidRushIncrementalCoordinator) waitForVidRush(ctx context.Context) ([]scriptpkg.VidRushSegmentResult, error) {
	barrierStart := time.Now()
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
	barrierEnd := time.Now()

	c.mu.Lock()
	c.barrierStart = barrierStart
	c.barrierEnd = barrierEnd
	firstStart := c.firstEnrichmentStart
	overlapMS := c.overlapMSLocked()
	indexes := make([]int, 0, len(c.latest))
	for idx := range c.latest {
		indexes = append(indexes, idx)
	}
	c.mu.Unlock()
	sort.Ints(indexes)

	var resultErr error
	results := make([]scriptpkg.VidRushSegmentResult, 0, len(indexes))
	for _, idx := range indexes {
		c.mu.Lock()
		latest := c.latest[idx]
		record, ok := c.records[idx]
		c.mu.Unlock()
		if !ok {
			continue
		}
		if record.err != nil {
			resultErr = fmt.Errorf("vidrush scene %q (index %d) enrichment: %w", latest.SceneID, idx, record.err)
			break
		}
		// Defensive final fence: never apply a result whose identity does not
		// match the latest committed scene text.
		if record.revision != latest.Revision || record.textHash != latest.TextHash {
			continue
		}
		results = append(results, record.result)
	}
	if resultErr == nil && c.metrics != nil {
		c.metrics.BarrierWait(barrierEnd.Sub(barrierStart).Seconds())
		c.metrics.GenerationOverlap(float64(overlapMS) / 1000.0)
	}
	c.recordSceneAnalysisStage(ctx, firstStart, barrierEnd, resultErr)
	if resultErr != nil {
		return nil, resultErr
	}
	return results, nil
}

// recordSceneAnalysisStage projects the owner-measured scene_analysis fan-out
// wall (first enrichment start → barrier end) onto the canonical Run clock as
// a stage, so the fan-out report exposes a nonzero scene_analysis wall_ms
// separate from the accumulated nlp.extract work instead of leaking the NLP
// wall into unattributed time. It is recorded only when at least one
// enrichment ran (firstStart non-zero); when no Run is bound to ctx it is a
// no-op (instrumentation must never change behaviour).
func (c *VidRushIncrementalCoordinator) recordSceneAnalysisStage(ctx context.Context, firstStart, barrierEnd time.Time, err error) {
	if firstStart.IsZero() {
		return
	}
	kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: StageSceneAnalysis}, firstStart, barrierEnd, err)
}

// StaleResults reports how many enrichment results were discarded because a
// newer scene revision superseded them. It is the bounded observability
// counter consumed by the vidrush_stale_results_total metric.
func (c *VidRushIncrementalCoordinator) StaleResults() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.staleCount
}
