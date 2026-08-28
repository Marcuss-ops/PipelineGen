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

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/cleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
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
	// composition root (NewProductionStockPipeline) already validates
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
		if stager == nil {
			return
		}
		// Keep staged sources available only across retryable failures.
		// Terminal failures and cancellation must release the temporary
		// workspace; otherwise failed jobs leak staged source files.
		if err != nil {
			_, retryable := retry.Classify(err)
			if retryable {
				return
			}
		}
		cleanupCtx := context.WithoutCancel(ctx)
		resources := make([]cleanup.Resource, 0, len(state.StagedAssets))
		for _, sa := range state.StagedAssets {
			if sa == nil {
				continue
			}
			resources = append(resources, cleanup.Resource{
				SourceID:  sa.SourceID,
				LocalPath: sa.LocalPath,
			})
		}
		failures := cleanup.ReleaseAll(cleanupCtx, &stockCleanupReleaser{stager: stager}, resources)
		for _, failure := range failures {
			if o.executorLog != nil {
				o.executorLog.Warn("orchestrator: staged source cleanup failed",
					zap.String("local_path", failure.Resource.LocalPath),
					zap.String("source_id", failure.Resource.SourceID),
					zap.Error(failure.Err))
			}
		}
	}()

	// §12-3 crash-resume: load the durable step history once so
	// pre-completed steps can rehydrate their produced state.
	// We build a step-key → completed row map from the latest row
	// per step (Design A: latest row per (job_id, step_key)).
	completedRows, loadErr := o.loadCompletedStepRows(ctx, o.cfg.JobId)
	if loadErr != nil {
		return nil, fmt.Errorf("orchestrator: load completed step rows: %w: %w", ErrStockResumeStateReadFailed, loadErr)
	}

	var previousState *RunState
	for _, step := range o.dispatchSteps {
		// With no effects or transitions, stock.extract_clips already
		// produced and verified the canonical final artifacts. Do not
		// invoke or checkpoint stock.compose_chunks at all; its output
		// is forwarded from CutPaths by the extract step (and backfilled
		// here for checkpoints written by older versions).
		if shouldBypassStockCompose(step.Name(), input) {
			if len(state.ComposedPaths) == 0 && len(state.CutPaths) > 0 {
				state.ComposedPaths = append([]string(nil), state.CutPaths...)
				// Keep the next step's fingerprint projection aligned
				// with the current state when resuming a legacy extract
				// checkpoint that predates ComposedPaths.
				var cloneErr error
				previousState, cloneErr = cloneRunState(state)
				if cloneErr != nil {
					return nil, fmt.Errorf("orchestrator: %s resume clone: %w: %v", step.Name(), ErrStockResumeStateInvalid, cloneErr)
				}
			}
			continue
		}

		fingerprint := stepInputFingerprint(o.cfg.JobId, step.Name(), o.cfg, input, previousState)
		// Rows written before fingerprint v2 used jobID|stepKey. Keep
		// those checkpoints resumable during the migration, but only
		// fall back for that explicit legacy format; a mismatching v2
		// fingerprint must create a new attempt instead of skipping work.
		if row, ok := completedRows[step.Name()]; ok {
			switch {
			case row.Fingerprint == legacyStepInputFingerprint(o.cfg.JobId, step.Name()) && legacyCheckpointEligible(o.cfg, input, previousState):
				fingerprint = row.Fingerprint
			case row.Fingerprint == legacyV2StepInputFingerprint(o.cfg.JobId, step.Name(), o.cfg, input, previousState):
				// v2 checkpoints remain resumable during the explicit
				// v3 migration. New checkpoints always use v3.
				fingerprint = row.Fingerprint
			}
		}
		key := steps.StepKey{
			JobID:            o.cfg.JobId,
			StepKey:          step.Name(),
			InputFingerprint: fingerprint,
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
						return nil, fmt.Errorf("orchestrator: %s resume: %w: empty completed checkpoint", step.Name(), ErrStockResumeStateReadFailed)
					} else {
						rehydrated, rehydrateErr := o.rehydrateRunState(row.Result)
						if rehydrateErr != nil {
							return nil, fmt.Errorf("orchestrator: %s resume: %w: %v", step.Name(), ErrStockResumeStateInvalid, rehydrateErr)
						}
						*state = rehydrated
						var cloneErr error
						previousState, cloneErr = cloneRunState(state)
						if cloneErr != nil {
							return nil, fmt.Errorf("orchestrator: %s resume clone: %w: %v", step.Name(), ErrStockResumeStateInvalid, cloneErr)
						}
						if o.executorLog != nil {
							o.executorLog.Info("orchestrator: rehydrated RunState from completed step",
								zap.String("step", step.Name()),
								zap.String("job_id", o.cfg.JobId))
						}
					}
				} else {
					return nil, fmt.Errorf("orchestrator: %s resume: %w: completed row missing", step.Name(), ErrStockResumeStateReadFailed)
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

		stateBytes, marshalErr := marshalRunStateCheckpoint(state)
		if marshalErr != nil {
			return nil, fmt.Errorf("orchestrator: %s checkpoint: %w: %v", step.Name(), ErrStockResumeStateInvalid, marshalErr)
		}

		var cloneErr error
		previousState, cloneErr = cloneRunState(state)
		if cloneErr != nil {
			return nil, fmt.Errorf("orchestrator: %s checkpoint clone: %w: %v", step.Name(), ErrStockResumeStateInvalid, cloneErr)
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
	stages, stageErr := o.stageSnapshots(ctx, input)
	if stageErr != nil {
		return nil, fmt.Errorf("orchestrator: stage snapshot: %w", stageErr)
	}
	summary = &RunSummary{Manifest: state.Manifest, FinalStatus: state.FinalStatus, Counts: state.Counts, Stages: stages}

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

// shouldBypassStockCompose is the single dispatch gate for the
// canonical cutter fast path. Keeping the step-name check here makes
// the complete bypass explicit: no Run, checkpoint, or resume lookup
// is performed for stock.compose_chunks in this mode.
func shouldBypassStockCompose(stepName string, input *RunInput) bool {
	return stepName == StepKeyStockComposeChunks && isCanonicalFinalCut(input)
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
func legacyCheckpointEligible(cfg OrchestratorConfig, input *RunInput, _ *RunState) bool {
	if input == nil {
		return false
	}
	return cfg.PolicyVersion == "" && input.PolicyVersion == "" && cfg.Lease.LeaseID == "" && cfg.Lease.JobID == "" && cfg.Lease.WorkerID == "" && cfg.Lease.Attempt == 0 && cfg.Lease.ExpiresAt.IsZero() && cfg.ChunkDurationSec == 0 && cfg.ClipDurationSec == 0 &&
		len(input.SearchQueries) == 0 && len(input.DirectURLs) == 0 && len(input.DriveURLs) == 0 && len(input.Clips) == 0 &&
		input.TotalMinutes == 0 && input.TargetTotalDurationSeconds == 0 && input.TargetDurationPerSourceSeconds == 0 &&
		input.ClipsPerSource == 0 && input.ClipDurationSeconds == 0 && input.DownloadMode == "" && input.MaxVideos == 0 &&
		input.ChunkDuration == 0 && input.ClipDuration == 0 && input.SecondsPerSegment == 0 && !input.NoAudio &&
		!input.NoEffects && !input.NoTransitions && input.Subfolder == "" && input.FolderName == "" &&
		input.DriveFolderID == "" && input.FolderID == "" && !input.DriveFolderResolved && input.Metadata == nil && !input.Persist &&
		input.FinalizationLease.LeaseID == "" && input.FinalizationLease.JobID == "" && input.FinalizationLease.WorkerID == "" && input.FinalizationLease.Attempt == 0 && input.FinalizationLease.ExpiresAt.IsZero()
}

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
		// Design A: latest row per (job_id, step_key) wins, regardless
		// of status. A newer Failed row must supersede an older Completed
		// row so a retry executes the step instead of skipping stale work.
		if existing, ok := latest[row.StepKey]; !ok || row.ID > existing.ID {
			latest[row.StepKey] = row
		}
	}
	completed := make(map[string]steps.StepState, len(latest))
	for stepKey, row := range latest {
		if row.Status == steps.StatusCompleted {
			completed[stepKey] = row
		}
	}
	return completed, nil
}

const currentRunStateCheckpointVersion = 1

// runStateCheckpoint is a flat, versioned envelope. Embedding RunState
// keeps the pre-versioning JSON shape intact: old checkpoints with fields
// such as "Plan" remain readable and new checkpoints add only the
// checkpoint_version discriminator.
type runStateCheckpoint struct {
	CheckpointVersion int `json:"checkpoint_version"`
	RunState
}

func marshalRunStateCheckpoint(state *RunState) ([]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("nil RunState")
	}
	return json.Marshal(runStateCheckpoint{
		CheckpointVersion: currentRunStateCheckpointVersion,
		RunState:          *state,
	})
}

// rehydrateRunState validates and unmarshals a RunState checkpoint.
//
// Compatibility contract:
//   - checkpoints without checkpoint_version are legacy v0 and remain
//     readable;
//   - checkpoint_version=1 is the current flat envelope;
//   - an empty object has no canonical RunState fields and is invalid;
//   - a future, malformed, or non-object versioned payload fails closed.
func (o *Orchestrator) rehydrateRunState(result json.RawMessage) (RunState, error) {
	if len(result) == 0 {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: empty checkpoint result")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: unmarshal: %w", err)
	}
	if len(fields) == 0 {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: checkpoint has no RunState fields")
	}

	versionRaw, versioned := fields["checkpoint_version"]
	if !versioned {
		// v0 compatibility: decode the historical flat RunState directly.
		var legacy RunState
		if err := json.Unmarshal(result, &legacy); err != nil {
			return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: legacy unmarshal: %w", err)
		}
		return legacy, nil
	}

	var version int
	if string(versionRaw) == "null" {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: checkpoint_version is null")
	}
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: checkpoint_version: %w", err)
	}
	if version != currentRunStateCheckpointVersion {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: unsupported checkpoint_version=%d (want %d)", version, currentRunStateCheckpointVersion)
	}
	if !hasRunStateField(fields) {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: versioned checkpoint has no RunState fields")
	}

	var checkpoint runStateCheckpoint
	if err := json.Unmarshal(result, &checkpoint); err != nil {
		return RunState{}, fmt.Errorf("orchestrator: rehydrateRunState: versioned unmarshal: %w", err)
	}
	return checkpoint.RunState, nil
}

// hasRunStateField distinguishes a valid v1 envelope from a versioned
// payload that only carries an unknown/future field. Unknown fields remain
// ignorable when a known RunState field is present, preserving additive JSON
// compatibility within the same checkpoint version.
func hasRunStateField(fields map[string]json.RawMessage) bool {
	for key := range fields {
		switch key {
		case "Plan", "StagedAssets", "CutPaths", "ComposedPaths", "Published",
			"MetadataPublished", "Manifest", "FinalStatus", "FinalizationResult",
			"Counts", "SourceErrors":
			return true
		}
	}
	return false
}
