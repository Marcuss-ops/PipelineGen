// Package scriptgeneration — the CORE_READY durable boundary of an execution run.
// Verbatim sibling split of runner_execution.go per the 600-LOC split wave;
// behaviour unchanged (same package, same types, same call sites).
package scriptgeneration

import (
	"fmt"
	"time"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// markCoreReady records the CORE_READY boundary. At this point the certified
// overlay render, the published final audio and the canonical script row are
// durable, and only the post-processing legs remain: Google Docs publication
// inside this run, then the artifact/Drive finalization the worker performs
// after this run returns.
//
// The boundary is observability, never a terminal state. It exists because
// SUCCEEDED has to keep meaning "every requested artifact is published": the
// core becoming available earlier must be visible WITHOUT pretending that
// Docs and Drive are done. Consumers read `current_stage == CORE_READY` (and
// the core_ready_ms KPI) instead of inferring availability from the tail.
//
// It deliberately stays silent in two cases:
//   - no post-processing leg is deferred (docs disabled), where CORE_READY
//     would just be a second name for COMPLETED;
//   - the core contract does not hold, because the milestone must never
//     advertise a core that is not durable (NO-FAKE-AVAILABILITY).
//
// markCoreReady records the CORE_READY boundary and, when the deferred Docs
// leg is wired, hands the post-core Docs work to the `script.docs_publish`
// child. It reports whether the terminal flip was deferred: true means the
// caller must skip the inline documents() phase and complete() — the run stays
// non-terminal at CORE_READY and the parent aggregator owns completion once
// the child publishes. False preserves the pre-cutover inline behaviour.
func (e *executionRun) markCoreReady() bool {
	docsEnabled, docsLangs, _ := e.req.ResolveDocsConfig()
	if !docsEnabled || len(docsLangs) == 0 {
		return false
	}
	if !IsCoreCompletable(e.result, docsLangs) {
		return false
	}

	started := time.Now()
	kernobs.RecordStage(e.ctx, kernobs.StageInfo{Stage: kernobs.StageRunCoreReady}, started, time.Now(), nil)
	if run := kernobs.FromContext(e.ctx); run != nil {
		kernobs.RecordKPIMilestone(e.ctx, "core_ready_ms", run.ElapsedMs())
	}
	// Durable projection: CurrentStage becomes CORE_READY, which is both what
	// a resumed attempt reads to re-enter at the post-processing legs
	// (ResumeFrom) and what an availability consumer polls.
	if err := e.r.updateStage(e.ctx, e.runID, RunStatusRunning, StageCoreReady); err != nil {
		// An observation must never fail a run whose core work succeeded.
		// No child was enqueued on this path, so report non-deferred and
		// keep the pre-cutover inline documents behaviour.
		e.log("scriptgeneration: core ready stage update failed", zap.String("error", err.Error()))
		return false
	}
	e.log("scriptgeneration: core ready",
		zap.Bool("core_ready", true),
		zap.String("current_stage", string(StageCoreReady)),
		zap.Int("pending_document_languages", len(docsLangs)),
	)

	// ── Deferred Docs leg (step 3) ────────────────────────────────────────
	// Submit the child that publishes the documents, so the Docs work starts
	// beside the worker's own finalization instead of chaining behind it.
	//
	// ORDERING IS LOAD-BEARING: the CORE_READY projection above is written
	// FIRST. The child re-reads the durable snapshot rather than receiving a
	// copy of it, so submitting it before the stage write would race the very
	// reference it depends on — the child could load a run whose Result is not
	// yet the core the boundary just advertised.
	//
	// A nil enqueuer means the deferred leg is not wired: the run keeps
	// publishing documents inline, which is the pre-cutover behaviour. A
	// non-nil enqueuer is FAIL-CLOSED — the alternative to failing here is a
	// parent that deferred its terminal flip to a child nobody ever submitted,
	// i.e. a run that hangs instead of a run that fails.
	if e.r.docsPublishEnqueuer != nil {
		enqueueErr := e.r.docsPublishEnqueuer.EnqueueDocsPublish(e.ctx, DocsPublishEnqueue{
			ParentJobID:   e.exec.JobID,
			RunID:         e.runID,
			CorrelationID: e.exec.CorrelationID,
		})
		if enqueueErr != nil {
			e.log("scriptgeneration: docs publish child enqueue failed",
				zap.String("error", enqueueErr.Error()))
			e.fail(StagePublishingDocuments,
				fmt.Errorf("enqueue docs publish child for run %s: %w", e.runID, enqueueErr))
			return false
		}
		e.log("scriptgeneration: docs publish child enqueued",
			zap.String("parent_job_id", e.exec.JobID))
		// The child owns the Docs leg from here: the pipeline must skip the
		// inline documents() phase (which would publish every document a
		// second time — harmless by idempotency but no longer on this wall
		// clock) and must NOT mark the run COMPLETED. The parent aggregator
		// flips the terminal state once the child publishes.
		e.docsDeferred = true
		return true
	}
	return false
}

// documents runs the document (Docs) publishing phase with the pre-rendered
// per-language skeletons.
