package scriptgeneration

import (
	"context"
	"time"

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
	// P0-A (anti-muda): stock prefetch warms best-effort cache in background.
	// Translation consumes only scene text, so it must NOT join here. The
	// join lives at the first stock consumer (sceneTextReady) with an
	// explicit budget; this defer only drains the tail without blocking the
	// critical path indefinitely.
	e.stockPrefetchDone = r.startStockPrefetch(ctx, req.StockBindings)
	defer func() {
		done := e.stockPrefetchDone
		e.stockPrefetchDone = nil
		r.drainStockPrefetch(done)
	}()
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
	if !e.finalJob() {
		return
	}
	// Google Docs publication was removed completely: there is no post-core
	// document leg to defer or run. The run completes after the non-document
	// phases and the async voiceover pool is joined inside complete().
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
		kernobs.StageRunFinalJob,
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
		// Legacy resume marker for the post-core publication leg. Its work
		// is the remote final job, so it reports under that phase.
		return kernobs.StageRunFinalJob, true
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

// stockPrefetchJoinBudget bounds how long a stock consumer waits for the
// best-effort warm. Whatever is cached when the budget expires is kept; the
// remainder falls through to synchronous resolution (same contract as the
// audio prefetch budget).
const stockPrefetchJoinBudget = 10 * time.Second

// stockPrefetchDrainBudget bounds the tail drain after the pipeline returns.
// The run must never pay unbounded stock I/O after its work is done.
const stockPrefetchDrainBudget = 5 * time.Second

// joinStockPrefetchForConsumer waits for the stock warm at the first phase
// that can use warmed assets. It records its own stage so
// `stock_prefetch_join_wait_ms` measures pure prefetch wait on the critical
// path (often zero once the barrier before translate is gone).
func (e *executionRun) joinStockPrefetchForConsumer() {
	done := e.stockPrefetchDone
	if done == nil {
		return
	}
	e.stockPrefetchDone = nil
	started := time.Now()
	var report *scriptports.StockPrefetchReport
	measureErr := kernobs.MeasureStage(e.ctx, "stock_prefetch_join", func(stageCtx context.Context) error {
		select {
		case rep := <-done:
			report = &rep
			return nil
		case <-stageCtx.Done():
			return stageCtx.Err()
		case <-time.After(stockPrefetchJoinBudget):
			return context.DeadlineExceeded
		}
	})
	waitMS := time.Since(started).Milliseconds()
	if measureErr != nil && e.r != nil && e.r.log != nil {
		e.r.log.Warn("script.generate: stock prefetch join stage failed", zap.Error(measureErr))
	}
	if e.r != nil && e.r.log != nil {
		fields := []zap.Field{zap.Int64("stock_prefetch_join_wait_ms", waitMS)}
		if report != nil {
			fields = append(fields,
				zap.Int("requested", report.Requested),
				zap.Int("warmed", report.Warmed),
				zap.Int("cached", report.Cached),
				zap.Int("failed", report.Failed),
				zap.Int("skipped", report.Skipped))
			e.r.log.Info("script.generate: stock prefetch joined at consumer", fields...)
		} else {
			e.r.log.Warn("script.generate: stock prefetch budget exceeded at consumer; continuing with synchronous fallback", fields...)
			// Budget exceeded: detach the still-running warm so the consumer
			// never blocks; re-arm the drain so the tail still collects it
			// without stalling the run.
			e.stockPrefetchDone = done
		}
	}
}

// drainStockPrefetch collects a still-outstanding warm after the run without
// adding unbounded tail latency.
func (r *Runner) drainStockPrefetch(done chan scriptports.StockPrefetchReport) {
	if done == nil {
		return
	}
	select {
	case report := <-done:
		if r != nil && r.log != nil {
			r.log.Info("script.generate: stock prefetch drained",
				zap.Int("requested", report.Requested),
				zap.Int("warmed", report.Warmed),
				zap.Int("cached", report.Cached),
				zap.Int("failed", report.Failed),
				zap.Int("skipped", report.Skipped))
		}
	case <-time.After(stockPrefetchDrainBudget):
		if r != nil && r.log != nil {
			r.log.Warn("script.generate: stock prefetch drain budget exceeded; detaching")
		}
	}
}
