// Package stockpipeline — orchestrator_steps.go (Stock Cutover
// §12-5, July 2026).
//
// §12-5 separates the orchestrator from the monolithic Run. Six
// typed Step structs are declared with canonical step_keys
// (stock.plan / stock.stage_sources / stock.extract_clips /
// stock.compose_chunks / stock.publish / stock.finalize). The
// Orchestrator iterates a typed []Step slice in canonical
// pipeline order; each step is checkpointed via the canonical
// §12-3 steps.Store (MarkStarted → Run → MarkCompleted /
// MarkFailed). The Orchestrator's RunResilient body becomes a
// thin dispatch loop; per-step internals live in this file.
//
// SSOT (godlike/06): this file is the single owner of "what are
// the stock pipeline's six canonical steps and what is each
// step's Run contract". The orchestrator.go field `dispatchSteps
// []Step` is initialised in NewOrchestrator to the canonical six
// order; orchestration-logic churn lands here, not in
// orchestrator.go.
//
// Resume semantics (godlike/07): the Orchestrator iterates its
// typed []Step slice in declaration order. For each step, it
// computes a StepKey triple with InputFingerprint derived from
// (JobID, stepName) — retries with the same triple MarkStarted
// idempotently per §12-3 mark-started semantics. A prior
// Completed row for the triple is allowed only if the fingerprint
// matches; in §12-5 the fingerprint is stable per (JobID,
// stepName), so re-runs re-MarkStarted idempotently (bumps
// attempt counter).
package stockpipeline

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// ── Canonical step keys (PipelineGen §12-5) ──────────────────────────
//
// These constants are the canonical step_keys the Orchestrator
// stamps on the §12-3 steps.Store rows. The §12-3 doc-comment
// requires "step_keys to use a lexically sortable naming
// convention" for FirstNonCompleted to return the pipeline-correct
// row; the orchestrator's typed []Step slice carries the
// canonical pipeline order (so resume reads the typed slice, not
// the lexical sort), but the step keys themselves follow the
// `stock.<stage>` naming convention as the user spec requires.
//
// SSOT: changing any of these constants is a wire-format break
// for the canonical §12-3 step store. New stages MUST add new
// constants; existing ones MUST NOT be renamed without a
// migration forward-pointer via architecture/current.yaml.
const (
	StepKeyStockPlan          = "stock.plan"
	StepKeyStockStageSources  = "stock.stage_sources"
	StepKeyStockExtractClips  = "stock.extract_clips"
	StepKeyStockComposeChunks = "stock.compose_chunks"
	StepKeyStockPublish       = "stock.publish"
	StepKeyStockFinalize      = "stock.finalize"
)

// ── Step contract ────────────────────────────────────────────────────

// Step is the canonical typed contract for a single
// orchestrator-side step. The Orchestrator iterates over a typed
// []Step slice and dispatches each step's Run with a StepRunner.
//
// Name() returns the canonical step_key (one of the
// StepKeyStockXxx constants above) — used to build the
// steps.StepKey triple on checkpoint rows. The Name() output
// MUST match the typed slice's position; changing it is a
// pipeline-order break.
//
// Run() executes the step body. Returns nil error on success
// (orchestrator dispatches MarkCompleted); returns non-nil error
// on failure (orchestrator dispatches MarkFailed + aborts the
// run with the typed error). Optional non-fatal outcomes (like
// the projection-resilience INDEX_PENDING flip) MUST be expressed
// via state mutation (e.g. StepRunner.SetFinalStatus) rather than
// returning error — error means abort.
type Step interface {
	Name() string
	Run(ctx context.Context, runner StepRunner) error
}

// ── StepRunner contract (state + deps seam) ─────────────────────────

// StepRunner is the typed context each step body sees during
// execution. The Orchestrator constructs an *orchestratorRunner
// per RunResilient call and threads it through each Step.Run.
//
// godlike/06 SSOT: StepRunner is the single seam between per-step
// bodies and the Orchestrator. Steps MUST NOT access Orchestrator
// fields directly — they go through the StepRunner accessors so
// test fakes can implement StepRunner without dragging the
// Orchestrator's full surface into the test fixture.
//
// Each step reads immutable inputs via Cfg() / RunInput() — the
// run is parameterised by the caller's (cfg, in) tuple. Each step
// writes/reads mutable state via State() — per-RunResilient
// state survives across steps in the same call. Each step
// invokes port dependencies (Planner / SourceStager / Cutter /
// Renderer / Builder / Writer / Projection / Log) via typed
// accessors.
type StepRunner interface {
	// Immutable per-call inputs.
	Cfg() OrchestratorConfig
	RunInput() *RunInput
	JobID() string
	PolicyVersion() string

	// Port dependencies (read-only accessors).
	Planner() ClipPlanner
	SourceStager() assets.SourceStager
	Cutter() VideoCutter
	Renderer() StockRenderer
	Builder() ManifestBuilder
	Writer() TransactionalAssetWriter
	Projection() ProjectionPort
	Log() *zap.Logger

	// Mutable state (per-RunResilient accumulator).
	State() *runState
}

// runState is the mutable per-call accumulator phases write to
// and read from. Each step writes to ITS designated field(s) and
// reads from its predecessor's field(s); the typed []Step slice
// encodes the canonical ordering.
//
// SSOT: this is the ONLY state shared across steps. Steps MUST
// NOT cross-pad via package-level globals or external state.
type runState struct {
	// Plan is the output of stock.plan (ClipPlanner.Plan) and
	// the input of stock.extract_clips (writer.WriteAndEnqueue loop).
	Plan []ClipPlan

	// StagedAssets is the output of stock.stage_sources and the
	// input of stock.extract_clips (future Commit 6 wiring).
	// Today (Commit 5) it's nil — staging is a Begin/Complete stub.
	StagedAssets []*assets.StagedAsset

	// CutPaths is the output of stock.extract_clips. For
	// Commit 5 wire-format holdover: each CutPath is the
	// logical OutputLogicalID from the corresponding ClipPlan.
	// Post-Commit-7 the literal file-paths from a real cutter
	// invocation will replace the prototype IDs.
	CutPaths []string

	// ComposedPaths is the output of stock.compose_chunks. Today
	// (Commit 5): 1:1 mirror of CutPaths (the renderer isn't
	// invoked yet). Post-Commit-7: literal rendered file-paths.
	ComposedPaths []string

	// Published is the output of stock.publish — the manifest's
	// published artifact entries. Today (Commit 5): begin/complete
	// stub (chunk-upload is forward-pointer for Commit 8).
	Published []job.Artifact

	// Manifest is the output of stock.finalize. The
	// ManifestBuilder.Build + Validate gate runs before the
	// projection resilience step.
	Manifest *job.ArtifactManifest

	// FinalStatus is the orchestrator's per-run job status
	// stamp on the ResultMap. stock.finalize sets it to
	// StatusSucceeded, then conditionally flips to
	// StatusIndexPending if projection.Project returned an error.
	FinalStatus job.Status
}

// ── orchestratorRunner — StepRunner impl backed by Orchestrator ─────

// orchestratorRunner is the canonical StepRunner implementation.
// One is constructed per RunResilient call so the per-call
// (in, state) pair survives across steps without leaking into
// Orchestrator fields.
type orchestratorRunner struct {
	orch  *Orchestrator
	in    *RunInput
	state *runState
	log   *zap.Logger
}

// Compile-time assertion: *orchestratorRunner satisfies StepRunner.
var _ StepRunner = (*orchestratorRunner)(nil)

func (a *orchestratorRunner) Cfg() OrchestratorConfig             { return a.orch.cfg }
func (a *orchestratorRunner) RunInput() *RunInput                 { return a.in }
func (a *orchestratorRunner) JobID() string                       { return a.orch.cfg.JobId }
func (a *orchestratorRunner) PolicyVersion() string               { return a.orch.cfg.PolicyVersion }
func (a *orchestratorRunner) Planner() ClipPlanner                { return a.orch.planner }
func (a *orchestratorRunner) SourceStager() assets.SourceStager   { return a.orch.stager }
func (a *orchestratorRunner) Cutter() VideoCutter                 { return a.orch.cutter }
func (a *orchestratorRunner) Renderer() StockRenderer             { return a.orch.renderer }
func (a *orchestratorRunner) Builder() ManifestBuilder            { return a.orch.builder }
func (a *orchestratorRunner) Writer() TransactionalAssetWriter    { return a.orch.writer }
func (a *orchestratorRunner) Projection() ProjectionPort          { return a.orch.projection }
func (a *orchestratorRunner) Log() *zap.Logger                    { return a.log }
func (a *orchestratorRunner) State() *runState                    { return a.state }

// defaultStepRunnerLog falls back to a no-op logger when no
// composition-root logger is wired. Mirrors the canonical
// no-panic no-fail-closed convention for ephermeral adapters.
func defaultStepRunnerLog() *zap.Logger {
	return zap.NewNop()
}

// ── Step 1: stock.plan ──────────────────────────────────────────────

// StockPlanStep is the canonical implementation of stock.plan.
// It exercises the deterministic ClipPlanner.Plan round-trip on
// the first source, populating runState.Plan for downstream steps.
//
// Body:
//  1. Resolve the canonical source via firstSource(RunInput).
//  2. Compute planBudget from RunInput.TotalMinutes (fallback to
//     Cfg().ChunkDurationSec when 0).
//  3. Planner.Plan round-trip with clip duration + policy version.
//  4. State.Plan populated for stock.extract_clips.
//
// Failures surface typed errors so the orchestrator's MarkFailed
// path can stamp the canonical ErrPlannerBudgetTooSmall envelope.
type StockPlanStep struct{}

func (StockPlanStep) Name() string { return StepKeyStockPlan }

func (StockPlanStep) Run(ctx context.Context, runner StepRunner) error {
	in := runner.RunInput()
	src, ok := firstSource(in)
	if !ok {
		return errors.New("orchestrator: stock.plan: no sources to plan (DirectURLs and SearchQueries are empty)")
	}

	planBudget := in.TotalMinutes * 60
	if planBudget <= 0 {
		planBudget = runner.Cfg().ChunkDurationSec
	}
	plans, err := runner.Planner().Plan(
		ctx, src, planBudget,
		runner.Cfg().ClipDurationSec, runner.Cfg().PolicyVersion,
	)
	if err != nil {
		return fmt.Errorf("orchestrator: stock.plan: planner.Plan: %w", err)
	}
	runner.State().Plan = plans
	return nil
}

// ── Step 2: stock.stage_sources ─────────────────────────────────────

// StockStageSourcesStep is the canonical implementation of
// stock.stage_sources. Today (Commit 5) the body is a Begin/Complete
// stub — the per-plan SourceStager.Prepare loop wires in Commit 6
// (see Stock Cutover Plan, §12-4.3 in CHANGELOG).
//
// On MarkCompleted, the orchestrator has persisted an audit-trail
// row confirming "this run executed stock.stage_sources" — even
// though the body is empty. Future Commit 6 will thread real
// Prepare calls through this step without changing its public
// surface.
type StockStageSourcesStep struct{}

func (StockStageSourcesStep) Name() string { return StepKeyStockStageSources }

func (StockStageSourcesStep) Run(_ context.Context, runner StepRunner) error {
	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.stage_sources stub (Commit 6 wires real SourceStager.Prepare)",
			zap.Int("plan_count", len(runner.State().Plan)))
	}
	return nil
}

// ── Step 3: stock.extract_clips ─────────────────────────────────────

// StockExtractClipsStep is the canonical implementation of
// stock.extract_clips. For each ClipPlan entry the step constructs
// a typed *asset.Asset and invokes Writer.WriteAndEnqueue — the
// canonical atomic UPSERT + outbox-enqueue entry-point. A
// returned non-nil error aborts the orchestrator with the typed
// ErrAtomicDispatchFailed envelope (per the run_upload_indexing_test.go
// contract, test (a)).
//
// The per-plan IDs populate runState.CutPaths as the proto-stage
// output (today), so the downstream composer has a typed 1:1 list
// to operate on. Post-Commit-7 the literal file-paths from the
// cutter invocation replace the OutputLogicalID placeholders.
type StockExtractClipsStep struct{}

func (StockExtractClipsStep) Name() string { return StepKeyStockExtractClips }

func (StockExtractClipsStep) Run(ctx context.Context, runner StepRunner) error {
	plans := runner.State().Plan
	var cutPaths []string
	for i, plan := range plans {
		clip := &asset.Asset{
			ID:        plan.OutputLogicalID,
			Name:      fmt.Sprintf("chunk_%d", i),
			Source:    asset.Source("stock"),
			MediaType: asset.MediaType("video"),
		}
		if err := runner.Writer().WriteAndEnqueue(ctx, clip, ""); err != nil {
			return fmt.Errorf("%w: chunk %d (%s): %v",
				ErrAtomicDispatchFailed, i, clip.ID, err)
		}
		cutPaths = append(cutPaths, plan.OutputLogicalID)
	}
	runner.State().CutPaths = cutPaths
	return nil
}

// ── Step 4: stock.compose_chunks ─────────────────────────────────────

// StockComposeChunksStep is the canonical implementation of
// stock.compose_chunks. Today (Commit 5) the body is
// Begin/Complete stub — the per-cut StockRenderer.Render loop
// wires in Commit 7 (Stock Cutover Plan, §12-7.4 in CHANGELOG).
//
// State.ComposedPaths is set 1:1 from State.CutPaths so downstream
// stock.publish has a typed list. Post-Commit-7 the literal
// rendered file-paths populate ComposedPaths.
type StockComposeChunksStep struct{}

func (StockComposeChunksStep) Name() string { return StepKeyStockComposeChunks }

func (StockComposeChunksStep) Run(_ context.Context, runner StepRunner) error {
	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.compose_chunks stub (Commit 7 wires real StockRenderer.Render)",
			zap.Int("cut_count", len(runner.State().CutPaths)))
	}
	runner.State().ComposedPaths = append([]string(nil), runner.State().CutPaths...)
	return nil
}

// ── Step 5: stock.publish ────────────────────────────────────────────

// StockPublishStep is the canonical implementation of
// stock.publish. Today (Commit 5) the body is a Begin/Complete
// stub — real Drive upload + Qdrant projection wire in Commit 8
// (Stock Cutover Plan, §12-8.5 in CHANGELOG). State.Published is
// populated from State.ComposedPaths so downstream stock.finalize
// has a typed list of "publishable" clip entries.
type StockPublishStep struct{}

func (StockPublishStep) Name() string { return StepKeyStockPublish }

func (StockPublishStep) Run(_ context.Context, runner StepRunner) error {
	if runner.Log() != nil {
		runner.Log().Info("orchestrator: stock.publish stub (Commit 8 wires real Drive upload + Qdrant index)",
			zap.Int("composed_count", len(runner.State().ComposedPaths)))
	}
	published := make([]job.Artifact, 0, len(runner.State().ComposedPaths))
	runner.State().Published = published
	return nil
}

// ── Step 6: stock.finalize ───────────────────────────────────────────

// StockFinalizeStep is the canonical implementation of
// stock.finalize. The body unifies the pre-§12-5 inline ladder's
// build_manifest + validate_manifest + project_manifest triple
// into a single typed gated step:
//
//  1. ManifestBuilder.Build(workflowID, jobID) — defaults to
//     buildStockManifest when no Builder is injected.
//  2. manifest.Validate() — the canonical C12 wire-format gate.
//     Returns ErrManifestIncomplete on Required:true + Path:"".
//  3. ProjectionPort.Project(manifest) — best-effort Qdrant sync.
//     Error flips FinalStatus to StatusIndexPending (per the
//     run_upload_indexing_test.go contract, test (c)); the step
//     does NOT abort — Run returns nil so the orchestrator can
//     stamp MarkCompleted and RunResilient's caller receives
//     (manifest, nil) per the resilience contract.
//
// godlike/07 typed-error contract: ErrManifestIncomplete is the
// canonical surfaced sentinel for Validate-failure; callers can
// errors.Is on it from any seam.
type StockFinalizeStep struct{}

func (StockFinalizeStep) Name() string { return StepKeyStockFinalize }

func (StockFinalizeStep) Run(ctx context.Context, runner StepRunner) error {
	in := runner.RunInput()
	workflowID := in.FolderID
	if workflowID == "" {
		workflowID = in.FolderName
	}

	var manifest *job.ArtifactManifest
	var buildErr error
	if runner.Builder() != nil {
		manifest, buildErr = runner.Builder().Build(workflowID, runner.JobID())
		if buildErr != nil {
			return fmt.Errorf("orchestrator: stock.finalize: ManifestBuilder.Build: %w", buildErr)
		}
	} else {
		manifest = buildStockManifest(workflowID, runner.JobID())
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrManifestIncomplete, err)
	}
	runner.State().Manifest = manifest

	// Projection resilience: best-effort, NOT fatal. A non-nil
	// error flips FinalStatus to StatusIndexPending so the broker
	// runner persists the index-pending state and the
	// Qdrant-reconciler task retries asynchronously.
	runner.State().FinalStatus = job.StatusSucceeded
	if runner.Projection() != nil {
		if projErr := runner.Projection().Project(ctx, manifest); projErr != nil {
			runner.State().FinalStatus = job.StatusIndexPending
			if runner.Log() != nil {
				runner.Log().Warn("orchestrator: stock.finalize projection failed — flipped FinalStatus to StatusIndexPending",
					zap.Error(projErr))
			}
		}
	}
	return nil
}

// ── Default 6-step dispatch ─────────────────────────────────────────

// DefaultStockSteps returns the canonical 6-step slice the
// Orchestrator iterates in RunResilient. The slice order is the
// canonical pipeline order: plan → stage_sources → extract_clips
// → compose_chunks → publish → finalize.
//
// SSOT (godlike/06): this is the single canonical pipeline order
// for stock. Future steps MUST be appended (preserving pipeline
// semantics) — never inserted mid-slice (that would re-rank the
// lexically-sorted step_store.FirstNonCompleted result and break
// resume semantics per §12-3 doc-comment).
func DefaultStockSteps() []Step {
	return []Step{
		StockPlanStep{},
		StockStageSourcesStep{},
		StockExtractClipsStep{},
		StockComposeChunksStep{},
		StockPublishStep{},
		StockFinalizeStep{},
	}
}

// Compile-time assertions: every default Step struct satisfies Step.
var (
	_ Step = StockPlanStep{}
	_ Step = StockStageSourcesStep{}
	_ Step = StockExtractClipsStep{}
	_ Step = StockComposeChunksStep{}
	_ Step = StockPublishStep{}
	_ Step = StockFinalizeStep{}
)
