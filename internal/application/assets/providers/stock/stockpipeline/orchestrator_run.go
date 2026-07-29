// Package stockpipeline — orchestrator_run.go (split July 2026).
//
// This file owns the canonical orchestration entry points (Run,
// RunResilient) and the executor-log fallback helper. Extracted
// from orchestrator.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: RunResilient is the single canonical production
// orchestration entry point. Run is a thin manifest-only wrapper
// for legacy callers.
package stockpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Run is a thin wrapper around RunResilient that drops the
// FinalStatus + Project surface. This keeps the existing
// Service.runOrchestrator callers chain-stable (Manifest-shaped
// return) while the resilience flow lives behind RunResilient.
//
// PROSSIMO STEP: migrate Service.runOrchestrator callers to
// runOrchestratorResilient, then collapse Run into RunResilient.
//
// For the canonical 3-test failure-mode contract (outbox rollback,
// manifest-completeness gate, Qdrant-offline → INDEX_PENDING) see
// RunResilient + run_upload_indexing_test.go.
func (o *Orchestrator) Run(ctx context.Context, input *RunInput) (*job.ArtifactManifest, error) {
	summary, err := o.RunResilient(ctx, input)
	if err != nil {
		return nil, err
	}
	return summary.Manifest, nil
}

// STATO ATTUALE: RunResilient is the canonical orchestrator entry point
// for production traffic. It threads the typed *job.ArtifactManifest +
// per-run FinalStatus + per-run FinalizationResult through a single
// RunSummary envelope so the broker JobStatusResponse can render all three.
//
// The 6 typed Steps declared in orchestrator_steps.go iterate in
// canonical pipeline order:
//
//  1. stock.plan           — ClipPlanner.Plan round-trip.
//  2. stock.stage_sources  — SourceStager.StageSource per unique URL.
//  3. stock.extract_clips  — VideoCutter.Cut per source group
//     (test (a): writer error ⇒ ErrAtomicDispatchFailed).
//  4. stock.compose_chunks — StockRenderer.Render per cut path.
//  5. stock.publish        — ArtifactPreparation.Prepare per chunk
//     + per metadata.json. Uploads Drive;
//     nil ArtifactPreparation ⇒ test-fixture skip.
//  6. stock.finalize       — ManifestBuilder.Build + Validate +
//     ProjectionPort.Project (best-effort Qdrant
//     → flips FinalStatus to StatusIndexPending) +
//     BuildFinalizationRequest +
//     JobFinalizer.CompleteWithArtifacts
//     (single-TX spine write).
//     nil JobFinalizer ⇒ test-fixture skip.
//
// PROSSIMO STEP: SQLite-backed step store per §12-3 Design A for
// crash-resume across process restarts. Currently in-memory only.
//
// Per-step checkpointing: every step calls
//
//	o.stepStore.MarkStarted(ctx, steps.StepKey{JobID, StepKey, InputFingerprint})
//	o.stepStore.MarkCompleted(ctx, key, result, artifactRefs)  // on success
//	o.stepStore.MarkFailed(ctx, key, errMessage)               // on failure
//
// via the canonical §12-3 ports.Store (godlike/06 SSOT). A step's
// Run return is the abort signal: nil ⇒ MarkCompleted + continue;
// non-nil ⇒ MarkFailed + return (nil RunSummary, err) so the broker
// runner can stamp the typed JobFailed state.
//
// Resume semantics (Step 10 C2/4, July 2026): on retry-after-crash,
// MarkStarted returns steps.ErrStepAlreadyCompleted for any step
// whose latest row is Completed in the steps.Store (typically from
// a prior interrupted run that persisted progress to SQLite before
// the SIGKILL). The orchestrator continues to the next step via
// `continue` — skipping re-execution of the Completed step's body
// while preserving the typed RowID/lease_until audit trail. The
// §12-3 FirstNonCompleted surface remains available for the
// operator-diagnostic "what stage is currently in-flight?" query
// but is NOT used as the orchestrator's primary resume mechanism
// (lex-smallest non-completed ≠ pipeline-order).
func (o *Orchestrator) RunResilient(ctx context.Context, input *RunInput) (summary *RunSummary, err error) {
	// ── Entry log ──────────────────────────────────────────
	start := time.Now()
	log := o.executorLogOrNop()
	log.Info("orchestrator: RunResilient: starting",
		zap.String("job_id", o.cfg.JobId),
		zap.Int("explicit_clips", len(input.Clips)),
		zap.Int("search_queries", len(input.SearchQueries)),
		zap.Int("direct_urls", len(input.DirectURLs)),
	)

	// ── Exit log (deferred) ───────────────────────────────
	// Named return values let the deferred closure inspect
	// whether RunResilient succeeded or failed.
	// This defer fires before the cleanup defer below (LIFO order),
	// so the SUCCEEDED/FAILED verdict is logged before staged-source
	// cleanup runs.
	defer func() {
		elapsed := time.Since(start)
		if recov := recover(); recov != nil {
			log.Error("orchestrator: RunResilient: PANIC",
				zap.String("job_id", o.cfg.JobId),
				zap.Duration("elapsed", elapsed),
				zap.Any("panic_value", recov))
			panic(recov)
		}
		if err != nil {
			log.Warn("orchestrator: RunResilient: FAILED",
				zap.String("job_id", o.cfg.JobId),
				zap.Duration("elapsed", elapsed),
				zap.Error(err))
			return
		}
		log.Info("orchestrator: RunResilient: SUCCEEDED",
			zap.String("job_id", o.cfg.JobId),
			zap.Duration("elapsed", elapsed))
	}()
	// §12-1 (July 2026) + PR-STOCK-PRODUCTION-DEPS (P2_media,
	// 2026-07-04): composition-time fail-closed gate. The canonical
	// composition root (stockpipeline.NewService) already validates
	// planner/stager/renderer/jobs/sourceStager etc. at ctor time;
	// this gate is the defense-in-depth seam for direct Orchestrator
	// callers (tests, internal packages that bypass Service). Per
	// godlike/07 no-fake-availability: a production run must NOT
	// reach the step bodies (StockStageSourcesStep.Run /
	// StockComposeChunksStep.Run) with a nil stager or renderer —
	// both steps assume the runner.SourceStager() / runner.Renderer()
	// accessors return non-nil. The Service-level gate (cutter
	// optional; stager + renderer required) is the canonical
	// fail-closed surface; this gate mirrors the same contract
	// for direct Orchestrator callers.
	if o.planner == nil || o.stager == nil || o.renderer == nil || o.stepStore == nil || len(o.dispatchSteps) == 0 {
		return nil, ErrOrchestratorNilDeps
	}

	// §12-1 P0 #2 production gate (July 2026): asymmetric wiring is a
	// composition error. When EITHER JobFinalizer or ArtifactPreparation
	// is non-nil (production path), BOTH must be non-nil. A partially-
	// wired root that has ArtifactPreparation but no JobFinalizer means
	// stock.finalize cannot call CompleteWithArtifacts; the opposite
	// means stock.publish cannot upload. Returning nil error in either
	// state is a silent-success false-positive.
	//
	// Test-fixture mode: both nil → gate passes (existing back-compat).
	// Production mode: both non-nil → gate passes.
	// Asymmetric: one nil, one non-nil → typed error.
	hasFinalizer := o.jobFinalizer != nil
	hasArtPrep := o.artifactPreparation != nil
	switch {
	case hasFinalizer && !hasArtPrep:
		return nil, fmt.Errorf("orchestrator §12-1 P0 #2 production gate: %w", ErrStockProductionArtifactPrepMissing)
	case hasArtPrep && !hasFinalizer:
		return nil, fmt.Errorf("orchestrator §12-1 P0 #2 production gate: %w", ErrStockProductionJobFinalizerMissing)
	}

	state := &RunState{}
	runner := &orchestratorRunner{
		orch:                o,
		in:                  input,
		state:               state,
		log:                 o.executorLogOrNop(),
		artifactPreparation: o.artifactPreparation,
		jobFinalizer:        o.jobFinalizer,
	}

	// Phase 1 (July 2026): orchestrator-level cleanup of staged
	// sources. The staged files MUST survive the entire run —
	// extract_clips and compose_chunks read them. Cleanup fires
	// AFTER all 6 steps complete (success or failure) so the
	// deferred body always runs even when a step aborts.
	//
	// Uses context.WithoutCancel(ctx) so cleanup survives even
	// when the original ctx is cancelled (e.g. a step returned an
	// error and the caller cancelled the context). Per AGENTS.md
	// §Known Issues context.Background() allowlist pattern.
	defer func() {
		stager := o.stager
		// Keep staged sources and extracted artifacts available across
		// retryable failures. A retry must resume from the workspace,
		// not download and cut the source again.
		if stager == nil || err != nil {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		for _, sa := range state.StagedAssets {
			if cleanErr := stager.Cleanup(cleanupCtx, sa); cleanErr != nil {
				if o.executorLog != nil {
					o.executorLog.Warn("orchestrator: staged source cleanup failed",
						zap.String("local_path", sa.LocalPath),
						zap.String("source_id", sa.SourceID),
						zap.Error(cleanErr))
				}
			}
		}
	}()

	// §12-3 crash-resume: load the durable step history once so
	// pre-completed steps can rehydrate their produced state.
	// We build a step-key → completed row map from the latest row
	// per step (Design A: latest row per (job_id, step_key)).
	completedRows, loadErr := o.loadCompletedStepRows(ctx, o.cfg.JobId)
	if loadErr != nil && o.executorLog != nil {
		o.executorLog.Warn("orchestrator: failed to load completed step rows for resume",
			zap.String("job_id", o.cfg.JobId),
			zap.Error(loadErr))
	}

	for _, step := range o.dispatchSteps {
		key := steps.StepKey{
			JobID:            o.cfg.JobId,
			StepKey:          step.Name(),
			InputFingerprint: stepInputFingerprint(o.cfg.JobId, step.Name()),
		}

		if err := o.stepStore.MarkStarted(ctx, key); err != nil {
			if errors.Is(err, steps.ErrStepAlreadyCompleted) {
				// Step 10 C2/4 resume contract: this step is
				// already Completed in the steps.Store (likely
				// from a prior SIGKILL'd run that persisted
				// progress before crashing). Skip re-execution
				// — do NOT call step.Run, do NOT call
				// MarkCompleted (terminal-immutability). The
				// next stage in dispatchSteps proceeds.
				if o.executorLog != nil {
					o.executorLog.Info("orchestrator: skip already-completed step (recovery)",
						zap.String("step", step.Name()),
						zap.String("job_id", o.cfg.JobId))
				}
				// Rehydrate the accumulated RunState produced by
				// this completed step. The row's result_json holds
				// the full RunState snapshot taken at the step's
				// MarkCompleted, so resuming here restores exactly
				// the state the step left behind.
				if row, ok := completedRows[step.Name()]; ok {
					if len(row.Result) == 0 {
						// Backward compatibility: rows completed before
						// checkpoint persistence stored an empty result.
						// Continue with the current accumulator; later
						// steps may fail closed if they need the state.
						if o.executorLog != nil {
							o.executorLog.Warn("orchestrator: completed step has empty checkpoint (legacy row); resuming without state",
								zap.String("step", step.Name()),
								zap.String("job_id", o.cfg.JobId))
						}
					} else {
						rehydrated, rehydrateErr := o.rehydrateRunState(row.Result)
						if rehydrateErr != nil {
							return nil, fmt.Errorf("orchestrator: %s resume: %w: %v", step.Name(), ErrStockResumeStateInvalid, rehydrateErr)
						}
						*state = rehydrated
						if o.executorLog != nil {
							o.executorLog.Info("orchestrator: rehydrated RunState from completed step",
								zap.String("step", step.Name()),
								zap.String("job_id", o.cfg.JobId))
						}
					}
				}
				continue
			}
			return nil, fmt.Errorf("orchestrator: %s MarkStarted: %w", step.Name(), err)
		}
		if runErr := step.Run(ctx, runner); runErr != nil {
			// MarkFailed is best-effort: the typed sentinel is
			// what callers errors.Is on, not the row's LastError.
			// We still call MarkFailed so the §12-3 audit log
			// captures the failure path. P3 fix: log MarkFailed
			// errors at WARN rather than silently swallowing.
			if markErr := o.stepStore.MarkFailed(ctx, key, runErr.Error()); markErr != nil {
				if o.executorLog != nil {
					o.executorLog.Warn("orchestrator: MarkFailed failed (checkpoint persistence lost)",
						zap.String("step", step.Name()),
						zap.String("job_id", o.cfg.JobId),
						zap.Error(markErr))
				}
			}
			return nil, runErr
		}

		stateBytes, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			return nil, fmt.Errorf("orchestrator: %s checkpoint: %w: %v", step.Name(), ErrStockResumeStateInvalid, marshalErr)
		}

		if err := o.stepStore.MarkCompleted(ctx, key, stateBytes, nil); err != nil {
			// ErrStepAlreadyCompleted cannot fire here (we just
			// MarkStarted the same key); any other error
			// (ErrStoreNotWired, ErrInvalidStepKey) is a
			// programming error and surfaces loudly.
			return nil, fmt.Errorf("orchestrator: %s MarkCompleted: %w", step.Name(), err)
		}
	}

	// §12-1 P0 #1 (July 2026) — orchestrator-level fail-closed gate.
	// The post-publish gate-level layers (in finalizer_gates.go) catch
	// populate-and-validate failures AFTER Drive upload; this gate closes
	// the verdict's earlier-stage false-success class — Orchestrator.Run
	// must NOT return nil error unless RunSummary.Manifest declares at
	// least one Required:true chunk AND one Required:true metadata.json
	// entry. Pre-Commit-4-7 every stock run hits ErrMetadataMissing
	// (buildStockManifest emits 5 entries all Required:false today); the
	// chunk-rendering ladder flips entries to Required:true once their
	// LocalPath is hydrated, after which the gate starts passing.
	//
	// godlike/06 SSOT: this gate is the sole orchestrator-layer owner of
	// "manifest declares canonical artifacts". Wired inline (NOT in a
	// dedicated Step type) because the gate checks RunSummary state, not
	// per-step progress; threading it through a Step would duplicate
	// state and break the §12-5 typed-slice ingress invariant.
	// ── PR-007 (July 2026) — LLM enrichment plumbing-on-nil ──────────
	// After the Plan step (and the rest of the 6-step ladder:
	// plan / stage_sources / extract_clips / compose_chunks /
	// publish / finalize) completes, the 6 LLM-enrichment fields
	// on StockRunMetadata (Category / Event / Round / Scene /
	// Subject / Entities) stay at zero-value per the
	// plumbing-on-nil contract. The wire shape IS the plumbing —
	// struct-field declarations + omitempty JSON tags ensure
	// zero-value fields are dropped from the metadata.json payload.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: NEVER populate these fields
	// with placeholder strings (e.g. Category:"unknown" or
	// Event:"n/a"). The LLM enrichment pass is the SOLE authority
	// on when to populate them. The forward-pointer lives in
	// StockRunMetadata (orchestrator_metadata.go) so the step body
	// implementations do not need to change.
	state.Counts = deriveRunCounts(input, state)
	if state.FinalStatus == job.StatusSucceeded && (state.Counts.PlannedClipCount > 0 || state.Counts.SelectedVideoCount > 0) {
		if err := ValidateRunCounts(state.Counts); err != nil {
			return nil, fmt.Errorf("stock run cannot be SUCCEEDED: %w", err)
		}
	}
	summary = &RunSummary{Manifest: state.Manifest, FinalStatus: state.FinalStatus, Counts: state.Counts}

	// §12-1 P0 #1 gate: enforce manifest-completeness BEFORE
	// returning nil. The gate fires in production mode (JobFinalizer
	// wired) to close the silent-success class where a run declares
	// nil error without declaring Required:true artifacts.
	//
	// In test-fixture mode (JobFinalizer nil), the gate is skipped —
	// mirroring StockFinalizeStep's spine-write skip. A nil
	// JobFinalizer means the orchestrator is not wired for production
	// finalization; the manifest may legitimately be empty (the 6
	// steps all ran in stub mode). See run_success_gate_test.go for
	// the gate's TDD contract; see "§12-7 test-fixture path" in
	// orchestrator_steps.go for the skip rationale.
	//
	// godlike/07 no-fake-availability note (July 2026): the silent-
	// success class (both ArtifactPreparation AND JobFinalizer nil
	// + work attempted → orchestrator declares SUCCEEDED) is closed
	// at step_finalize.go Phase 1's call to manifest.Validate()
	// (returns ErrManifestIncomplete on empty manifest). The P0 #1
	// gate at the bottom of RunResilient is the second line of
	// defence: it fires the typed ErrMetadataMissing / ErrNoProducedChunk
	// when a manifest with Required:false entries slips through
	// step_finalize (which only happens when step_finalize is
	// bypassed or its validation is removed in a future refactor).
	if o.jobFinalizer != nil {
		if gateErr := AssertRunSummaryArtifactsRequired(summary); gateErr != nil {
			// Wrap with stage prefix so log scanners trace to the
			// §12-1 P0 #1 gate seam; errors.Is(sentinel) still
			// probes the typed error via %w.
			return nil, fmt.Errorf("orchestrator §12-1 P0 #1 success gate (pre-CompleteWithArtifacts): %w", gateErr)
		}
	}

	return summary, nil
}

// executorLogOrNop returns the per-orchestrator logger if one
// was injected at New-construction; otherwise returns a no-op
// logger so the steps' Log().Info calls don't panic.
func (o *Orchestrator) executorLogOrNop() *zap.Logger {
	if o.executorLog != nil {
		return o.executorLog
	}
	return defaultStepRunnerLog()
}

// loadCompletedStepRows builds a map of the latest Completed row
// for each step key for the given job. It is used once per
// RunResilient call so the resume path can rehydrate RunState
// without querying the store inside the dispatch loop.
func (o *Orchestrator) loadCompletedStepRows(ctx context.Context, jobID string) (map[string]steps.StepState, error) {
	if o.stepStore == nil {
		return nil, steps.ErrStoreNotWired
	}
	history, err := o.stepStore.ListByJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]steps.StepState)
	for _, row := range history {
		if row.Status != steps.StatusCompleted {
			continue
		}
		// Design A: latest row per (job_id, step_key) wins.
		// Select the row with the greatest ID for each step_key
		// so retries with the same fingerprint do not accidentally
		// pick an older row.
		if existing, ok := latest[row.StepKey]; !ok || row.ID > existing.ID {
			latest[row.StepKey] = row
		}
	}
	return latest, nil
}

// rehydrateRunState unmarshals a JSON snapshot of RunState.
// Returns a typed error when the snapshot is empty or malformed so
// the caller can fail the run loudly rather than resuming with an
// empty accumulator.
func (o *Orchestrator) rehydrateRunState(result json.RawMessage) (RunState, error) {
	if len(result) == 0 {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: empty checkpoint result")
	}
	var rehydrated RunState
	if err := json.Unmarshal(result, &rehydrated); err != nil {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: unmarshal: %w", err)
	}
	return rehydrated, nil
}
