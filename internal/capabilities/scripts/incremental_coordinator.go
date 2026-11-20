// Package scriptgeneration — incremental_coordinator.go owns the
// VidRushIncrementalCoordinator: it reacts to SceneCommitted events by
// enriching each stable scene as soon as it is committed, so scene generation
// and VidRush enrichment overlap instead of running as two sequential blocks.
// Results are immutable and merged in canonical scene order at the barrier;
// a result derived from older scene text is fenced out and never applied.
package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushBarrier is the run-scoped final barrier for incremental VidRush
// enrichment. The runner awaits it after generation completes; it blocks only
// for enrichments still running and never re-runs a whole-document extractor.
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
	research     SegmentResearcher
	materializer SegmentMaterializer
	plan         *scriptpkg.ResolvedGenerationPlan
	backpressure VidRushBackpressure
	// optionalFixedScenes is the number of fixed intro/outro SpecScenes the
	// non-verbatim streaming path may commit before the VidRush barrier. They
	// are optional there (fixed sections can be materialized later), but when
	// they are already committed the barrier must accept them instead of
	// reporting them as extra indexes (r10: expected 5 body scenes, got 7).
	optionalFixedScenes int

	// providerResolveBudget bounds the WHOLE run's provider fan-out, not each
	// scene independently: a late scene must not get a fresh budget after the
	// barrier has already waited. Zero uses vidRushProviderResolveTimeout;
	// tests shorten it to exercise degradation.
	providerResolveBudget time.Duration
	// providerBudgetStart is the first provider fan-out observation of this
	// run. It is guarded by mu and set lazily so construction/generation time
	// is not charged to the provider stage.
	providerBudgetStart time.Time

	metrics VidRushMetrics

	mu             sync.Mutex
	runID          string
	latest         map[int]SceneCommitted
	records        map[int]segmentResultRecord
	sceneDone      map[int]chan struct{}
	staleCount     int
	staleScenes    []StaleScene
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

// Per-scene enrichment must have a finite budget. The worker context can live
// for the whole job, but a remote/provider stall must never hold the terminal
// job transition (or its lease) forever. Image materialization tries a bounded
// set of fallback URLs, with each acquisition and verification independently
// time-limited. A 20-minute, five-scene job with entity images can fan out to
// dozens of candidates per scene; fifteen minutes gives those per-candidate
// budgets room to run while still bounding a stalled scene.
const vidRushSceneEnrichmentTimeout = 15 * time.Minute

// NewVidRushIncrementalCoordinator constructs an incremental coordinator.
// maxConcurrency bounds the provider-search stage; values <= 0 default to 4.
// Semantic extraction uses its bounded Rust worker pool and materialization
// is bounded at 2.
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
	bp := backpressure.Resolved()
	return &VidRushIncrementalCoordinator{
		enricher:       enricher,
		plan:           plan,
		backpressure:   bp,
		latest:         make(map[int]SceneCommitted),
		records:        make(map[int]segmentResultRecord),
		sceneDone:      make(map[int]chan struct{}),
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

// SetSegmentResearcher wires the per-segment research stage that runs after
// extraction and before provider search. A nil researcher is safe: the stage
// is a no-op, which is the only configuration the production wiring uses
// today. No SegmentResearcher implementation ships in this repository, so the
// setter has no production caller by design; it exists because the stage
// (researchSegment) is real and merges the report's sources into the
// segment's insights. Do not remove it as dead code without also deciding to
// drop the research stage: deleting the setter would make that stage
// permanently unreachable. The seam is pinned by
// TestIncrementalCoordinator_ResearchStageRunsOnlyWhenResearcherIsWired.
func (c *VidRushIncrementalCoordinator) SetSegmentResearcher(researcher SegmentResearcher) {
	if c != nil {
		c.research = researcher
	}
}

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

// OnSceneCommitted records the stable scene and launches its enrichment
// asynchronously so generation can continue while VidRush works on the
// previous scene. It fails closed on a missing enricher or an invalid event;
// enrichment failures are attributed to their scene and surfaced at Wait.
func (c *VidRushIncrementalCoordinator) OnSceneCommitted(ctx context.Context, event SceneCommitted) error {
	if event.SceneID == "" {
		return fmt.Errorf("vidrush incremental coordinator: scene commit missing scene id")
	}
	if !event.ExecutionMode.IsFixedMedia() && event.ExtractEntities != scriptpkg.ToggleDisabled && c.enricher == nil {
		// Validate before wg.Add. Returning after incrementing the barrier
		// counter would permanently block Wait for a coordinator that cannot
		// perform enrichment.
		return fmt.Errorf("vidrush incremental coordinator: SegmentEnricher not configured")
	}

	c.mu.Lock()
	if c.runID == "" {
		c.runID = event.RunID
	} else if event.RunID != "" && c.runID != event.RunID {
		c.mu.Unlock()
		return fmt.Errorf("vidrush incremental coordinator: scene commit run %q does not match coordinator run %q", event.RunID, c.runID)
	}
	c.latest[event.SceneIndex] = event
	if previous := c.sceneDone[event.SceneIndex]; previous != nil {
		// Wake waiters for a superseded revision. They will re-check the
		// revision/text fence and attach themselves to the new channel.
		close(previous)
	}
	c.sceneDone[event.SceneIndex] = make(chan struct{})
	if !event.ExecutionMode.IsFixedMedia() {
		c.wg.Add(1)
	}
	c.mu.Unlock()

	if c.metrics != nil {
		c.metrics.SceneCommitted()
	}
	if event.ExecutionMode.IsFixedMedia() {
		// Fixed media is authoritative. It remains visible at the commit
		// boundary for ordering, but never enters NLP, provider search, or
		// materialization enrichment.
		return nil
	}
	if event.ExtractEntities == scriptpkg.ToggleDisabled {
		// The explicit per-scene opt-out bypasses extraction and every
		// downstream provider/materialization stage. Keep a fully fenced empty
		// result so the scene barrier cannot mistake this for another scene.
		result := scriptpkg.VidRushSegmentResult{
			SegmentID: event.SceneID,
			SceneID:   event.SceneID,
			Position:  event.SceneIndex,
			Text:      event.Text,
			TextHash:  event.TextHash,
			Insights: scriptpkg.SegmentInsights{
				SegmentID: event.SceneID,
				TextHash:  event.TextHash,
			},
		}
		c.recordResult(event, result, nil)
		c.wg.Done()
		return nil
	}
	scene := scriptpkg.SpecScene{
		ID:            event.SceneID,
		Index:         event.SceneIndex,
		Text:          event.Text,
		ExecutionMode: event.ExecutionMode,
	}

	if event.SourceText != "" || event.SemanticProfile != nil {
		scene.Metadata = &scriptpkg.SceneMetadata{SourceText: event.SourceText}
	}
	if event.SemanticProfile != nil {
		profile := event.SemanticProfile.Clone()
		if scene.Metadata == nil {
			scene.Metadata = &scriptpkg.SceneMetadata{}
		}
		scene.Metadata.SemanticProfile = &profile
	}
	go func() {
		defer c.wg.Done()
		// Scene generation owns its own ctx and may cancel it when it advances
		// or returns. Enrichment is run-scoped work that must outlive that
		// caller: strip cancellation/deadline while preserving values (run
		// observer, tracing), then apply the scene's own budget.
		enrichCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), vidRushSceneEnrichmentTimeout)
		defer cancel()
		completed := false
		defer func() {
			if recovered := recover(); recovered != nil && !completed {
				// A provider/materializer panic must become this scene's error,
				// not kill the worker and leave every other scene waiting on a
				// WaitGroup that can never finish.
				c.recordResult(event, scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush scene %q enrichment panic: %v", event.SceneID, recovered))
			}
		}()
		c.markEnrichmentStart()
		if c.metrics != nil {
			c.metrics.EnrichmentStarted()
		}
		start := time.Now()

		// The full stage chain (extraction → stamping → research → provider
		// fan-out → materialize) lives in incremental_coordinator_enrich.go,
		// where it also records the scene's nlp and asset trace stages.
		result, err := c.enrichAndMaterialize(enrichCtx, event, scene)
		if err == nil {
			err = validateVidRushResultIdentity(event, result)
		}
		c.recordResult(event, result, err)
		completed = true
		if c.metrics != nil {
			c.metrics.EnrichmentCompleted(time.Since(start))
		}
	}()
	return nil
}

// validateVidRushResultIdentity is the result-side fence for the committed
// scene identity. Provider/materialization code is untrusted with respect to
// routing: a successful result with another scene's identity must never be
// persisted or merged. Empty identity fields are rejected as well so a
// positional fallback cannot hide a scene mix-up.
func validateVidRushResultIdentity(event SceneCommitted, result scriptpkg.VidRushSegmentResult) error {
	if result.SegmentID == "" || result.SegmentID != event.SceneID {
		return fmt.Errorf("vidrush result identity mismatch: segment_id=%q scene_id=%q", result.SegmentID, event.SceneID)
	}
	if result.SceneID == "" || result.SceneID != event.SceneID {
		return fmt.Errorf("vidrush result identity mismatch: result scene_id=%q committed scene_id=%q", result.SceneID, event.SceneID)
	}
	if result.Position != event.SceneIndex {
		return fmt.Errorf("vidrush result identity mismatch: position=%d scene_index=%d", result.Position, event.SceneIndex)
	}
	if result.TextHash == "" || result.TextHash != event.TextHash {
		return fmt.Errorf("vidrush result identity mismatch: text_hash=%q committed_hash=%q", result.TextHash, event.TextHash)
	}
	// Older adapters may omit the optional insights identity block. The
	// authoritative fencing keys are the result's top-level identity checked
	// above; when insights identity is supplied, it must agree exactly.
	if (result.Insights.SegmentID != "" && result.Insights.SegmentID != event.SceneID) ||
		(result.Insights.TextHash != "" && result.Insights.TextHash != event.TextHash) {
		return fmt.Errorf("vidrush result identity mismatch: insights segment_id=%q text_hash=%q", result.Insights.SegmentID, result.Insights.TextHash)
	}
	return nil
}

// enrichSegment runs semantic extraction under its own bounded semaphore.
// The semaphore wait is recorded separately so scene-analysis queue time stays
// distinct from Rust worker time.
func (c *VidRushIncrementalCoordinator) enrichSegment(ctx context.Context, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	releaseExtract, err := kernobs.AcquireSlot(ctx, c.extractSem, kernobs.ComponentNLP, kernobs.WaitSemaphore)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	defer releaseExtract()
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

// recordResult stores an immutable enrichment outcome only when it still
// matches the latest committed scene identity. A result produced from
// superseded text (older revision or text hash) is discarded as stale.
func (c *VidRushIncrementalCoordinator) recordResult(event SceneCommitted, result scriptpkg.VidRushSegmentResult, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	latest, ok := c.latest[event.SceneIndex]
	if !ok || latest.Revision != event.Revision || latest.TextHash != event.TextHash {
		c.staleCount++
		reason := "scene was not committed"
		if ok {
			switch {
			case latest.Revision != event.Revision:
				reason = fmt.Sprintf("revision %d superseded by %d", event.Revision, latest.Revision)
			default:
				reason = "text hash superseded"
			}
		}
		c.staleScenes = append(c.staleScenes, StaleScene{
			SceneID: event.SceneID, SceneIndex: event.SceneIndex, Revision: event.Revision,
			TextHash: event.TextHash, Reason: reason,
		})
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
	if done := c.sceneDone[event.SceneIndex]; done != nil {
		close(done)
		delete(c.sceneDone, event.SceneIndex)
	}
}

// WaitForScene waits for exactly one committed scene revision. It is the
// narrow barrier used by translated NLP in the SceneTextReady coordinator:
// later languages wait for their own source enrichment instead of holding the
// whole translated-NLP pass behind the document-wide VidRush barrier.
func (c *VidRushIncrementalCoordinator) WaitForScene(ctx context.Context, runID string, sceneIndex int) (scriptpkg.VidRushSegmentResult, error) {
	if runID == "" {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush scene barrier: missing run id")
	}
	for {
		c.mu.Lock()
		if c.runID != "" && c.runID != runID {
			owner := c.runID
			c.mu.Unlock()
			return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush scene barrier: run %q does not own coordinator (owner %q)", runID, owner)
		}
		latest, committed := c.latest[sceneIndex]
		if !committed {
			c.mu.Unlock()
			return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush scene barrier: scene index %d was not committed", sceneIndex)
		}
		if record, ok := c.records[sceneIndex]; ok && record.revision == latest.Revision && record.textHash == latest.TextHash {
			c.mu.Unlock()
			if record.err != nil {
				return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush scene %q enrichment: %w", latest.SceneID, record.err)
			}
			return record.result, nil
		}
		done := c.sceneDone[sceneIndex]
		c.mu.Unlock()
		if done == nil {
			return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("vidrush scene barrier: scene index %d has no pending result", sceneIndex)
		}
		select {
		case <-done:
		case <-ctx.Done():
			return scriptpkg.VidRushSegmentResult{}, ctx.Err()
		}
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
// only for the enrichments still running — it never re-runs whole-document
// extraction — and returns the immutable results in canonical order.
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
	expectedScenes := 0
	if c.plan != nil {
		expectedScenes = len(c.plan.Segments)
		// Fixed intro/outro sections are committed as real SpecScenes with
		// their own SceneIndex; they are not part of the generated body
		// segments. Only an explicit body plan defines an exact expected
		// count, so a run without explicit segments keeps the dynamic
		// scene-count contract and skips this barrier.
		if expectedScenes > 0 {
			if c.plan.Intro != nil {
				expectedScenes++
			}
			if c.plan.Outro != nil {
				expectedScenes++
			}
		}
	}
	c.mu.Unlock()
	sort.Ints(indexes)
	allowedScenes := expectedScenes + c.optionalFixedScenes
	if expectedScenes > 0 {
		committed := make(map[int]struct{}, len(indexes))
		for _, idx := range indexes {
			committed[idx] = struct{}{}
		}
		missing := make([]int, 0, expectedScenes)
		for idx := 0; idx < expectedScenes; idx++ {
			if _, ok := committed[idx]; !ok {
				missing = append(missing, idx)
			}
		}
		extra := make([]int, 0)
		for _, idx := range indexes {
			if idx < 0 || idx >= allowedScenes {
				extra = append(extra, idx)
			}
		}
		switch {
		case len(missing) > 0 && len(extra) > 0:
			return nil, fmt.Errorf("vidrush barrier: expected %d committed scenes, got %d (missing indexes %v, extra indexes %v)", expectedScenes, len(indexes), missing, extra)
		case len(missing) > 0:
			return nil, fmt.Errorf("vidrush barrier: expected %d committed scenes, got %d (missing indexes %v)", expectedScenes, len(indexes), missing)
		case len(extra) > 0:
			return nil, fmt.Errorf("vidrush barrier: expected %d committed scenes, got %d (extra indexes %v)", expectedScenes, len(indexes), extra)
		}
	}

	var resultErrors []error
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
			// Keep the successful scenes. A single failed scene must not
			// discard the rest of the run's enrichment data: the caller gets
			// partial results plus one aggregated, scene-attributed error.
			resultErrors = append(resultErrors, fmt.Errorf("vidrush scene %q (index %d) enrichment: %w", latest.SceneID, idx, record.err))
			continue
		}
		if latest.ExtractEntities == scriptpkg.ToggleDisabled {
			// Keep the fence record internal to the barrier; disabled scenes do
			// not expose semantic extraction results to consumers.
			continue
		}
		// Defensive final fence: never apply a result whose identity does not
		// match the latest committed scene text.
		if record.revision != latest.Revision || record.textHash != latest.TextHash {
			continue
		}
		results = append(results, record.result)
	}
	resultErr := errors.Join(resultErrors...)
	if resultErr == nil && c.metrics != nil {
		c.metrics.BarrierWait(barrierEnd.Sub(barrierStart).Seconds())
		c.metrics.GenerationOverlap(float64(overlapMS) / 1000.0)
	}
	c.recordSceneAnalysisStage(ctx, firstStart, barrierEnd, resultErr)
	return results, resultErr
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

// StaleScene is the observability record for one superseded enrichment. The
// counter alone hides which scene was discarded and why.
type StaleScene struct {
	SceneID    string `json:"scene_id"`
	SceneIndex int    `json:"scene_index"`
	Revision   int64  `json:"revision"`
	TextHash   string `json:"text_hash"`
	Reason     string `json:"reason"`
}

// StaleSceneDetails returns a copy of the superseded scene records.
func (c *VidRushIncrementalCoordinator) StaleSceneDetails() []StaleScene {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]StaleScene(nil), c.staleScenes...)
}
