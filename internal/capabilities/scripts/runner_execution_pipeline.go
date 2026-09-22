package scriptgeneration

import (
	"context"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// runExecutionPhases owns the ordered business pipeline. Keeping the phase
// sequence separate from Runner wiring makes the resume/stop contract visible
// and keeps ExecuteWithContext small.
func (r *Runner) runExecutionPhases(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext) {
	e := &executionRun{
		r:     r,
		ctx:   ctx,
		runID: runID,
		req:   req,
		exec:  exec,
	}

	if !e.start() {
		return
	}
	stockPrefetchDone := r.startStockPrefetch(ctx, req.StockBindings)
	defer func() { r.waitStockPrefetch(stockPrefetchDone) }()
	if !e.normalize() {
		return
	}
	if !e.mediaPreflightPhase() {
		return
	}
	if !e.beginVidRushPhase() {
		return
	}
	// The VidRush coordinator wiring lives for the whole run: release it only
	// after every phase that consumes the fan-out completes (or fails).
	if e.coordinator != nil {
		defer r.endVidRush(runID)
	}
	if !e.generate() {
		return
	}
	r.waitStockPrefetch(stockPrefetchDone)
	stockPrefetchDone = nil
	e.ensureResult()
	if !e.translate() {
		return
	}
	if !e.sceneTextReady() {
		return
	}
	if !e.audioCompile() {
		return
	}
	if !e.persist() {
		return
	}
	if !e.documents() {
		return
	}
	e.complete()
}

// RunnerPhaseSequence returns the canonical ordered phase sequence executed by
// the durable Runner — the phases runExecutionPhases calls above, in order,
// named by the observability stage each one is measured under. The phase
// LITERALS are owned by internal/kernel/observability (registry.go); this
// function owns the ORDER, which is the fact the pipeline actually guarantees.
//
// It is the contract a report is read against: a phase missing from this
// sequence is not part of script.generate, and a phase measured in a different
// order is a defect in the pipeline, not a new phase. Pinned by
// runner_phase_sequence_test.go.
//
// Deliberately NOT included:
//
//   - CORE_READY — a milestone emitted at the persist seam, with no work of
//     its own (see observability.StageMilestones).
//   - the cover/thumbnail lane — produced outside this pipeline, so it must
//     never become a phase here.
//   - the worker-side artifact/Drive finalization
//     (observability.StagePostWriterFinalize), which runs after the Runner
//     returns (capabilities/jobs/worker_execution.go).
func RunnerPhaseSequence() []kernobs.StageName {
	return []kernobs.StageName{
		kernobs.StageRunNormalize,
		kernobs.StageRunMediaPreflight,
		kernobs.StageBeginVidRush,
		kernobs.StageGenerate,
		kernobs.StageRunTranslation,
		kernobs.StageRunVoiceover,
		kernobs.StageRunAudioCompile,
		kernobs.StageRunPersistence,
		kernobs.StageRunDocument,
	}
}

// runStagePhase maps a durable-run stage (model_run.go) to the observability
// phase the run reports while it is in that stage. It is the bridge between the
// two deliberately separate vocabularies: the run stage is the durable,
// caller-visible progress value, the phase is where the wall time is measured.
//
// It reports ok=false for the milestones and terminal stages, which are not
// phases and must never be mapped onto one.
func runStagePhase(stage Stage) (kernobs.StageName, bool) {
	switch stage {
	case StageNormalizing:
		return kernobs.StageRunNormalize, true
	case StagePreflight:
		return kernobs.StageRunMediaPreflight, true
	case StageGeneratingSceneText:
		return kernobs.StageGenerate, true
	case StageTranslatingScenes:
		return kernobs.StageRunTranslation, true
	case StageGeneratingVoiceovers:
		return kernobs.StageRunVoiceover, true
	case StageCompilingAudio:
		return kernobs.StageRunAudioCompile, true
	case StagePublishingDocuments:
		return kernobs.StageRunDocument, true
	default:
		return "", false
	}
}

// SetStockPrefetcher wires the best-effort acquisition hook for the stock
// bindings already present in the payload.
func (r *Runner) SetStockPrefetcher(prefetcher scriptports.StockPrefetcher) {
	if r != nil {
		r.stockPrefetcher = prefetcher
	}
}

func (r *Runner) startStockPrefetch(ctx context.Context, bindings []scriptpkg.StockBindingInput) chan scriptports.StockPrefetchReport {
	if r == nil || r.stockPrefetcher == nil || len(bindings) == 0 {
		return nil
	}
	copyBindings := append([]scriptpkg.StockBindingInput(nil), bindings...)
	done := make(chan scriptports.StockPrefetchReport, 1)
	go func() {
		done <- r.stockPrefetcher.Prefetch(ctx, copyBindings)
	}()
	return done
}

func (r *Runner) waitStockPrefetch(done chan scriptports.StockPrefetchReport) {
	if done == nil {
		return
	}
	report := <-done
	if r == nil || r.log == nil {
		return
	}
	r.log.Info("script.generate: stock prefetch completed",
		zap.Int("requested", report.Requested),
		zap.Int("warmed", report.Warmed),
		zap.Int("cached", report.Cached),
		zap.Int("failed", report.Failed),
		zap.Int("skipped", report.Skipped))
}
