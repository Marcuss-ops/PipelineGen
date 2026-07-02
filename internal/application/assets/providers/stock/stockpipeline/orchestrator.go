// Package stock — orchestrator.go (Stock Cutover Commit 1, July 2026).
//
// Orchestrator is the new code-driven pipeline entrypoint that
// replaces the legacy Service.Run path. Uses the deterministic
// ClipPlanner + ExecutionStepStore + SourceStager ladder, emitting
// a typed domain/job.ArtifactManifest so the worker can route it
// through the JobFinalizer.
//
// Commit 1 DUAL WRITE: the Orchestrator type and Service.Run coexist;
// Commit 2 flips media.stock traffic to the orchestrator.
//
// This Commit 1 implementation is intentionally minimal — it
// exercises the planner + steps ladder on a demo source but does
// NOT yet produce real chunks. Commit 2 wires the cutter+renderer
// legacy -> ArtifactPreparationService pipeline so the orchestrator
// emits full chunk entries.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// OrchestratorConfig parameterises Orchestrator at construction.
// Zero values are NOT a valid runtime config — NewOrchestrator
// validates PolicyVersion + ChunkDurationSec + ClipDurationSec.
type OrchestratorConfig struct {
	PolicyVersion string
	// ChunkDurationSec is the per-chunk video budget. The output
	// ArtifactManifest emits one entry per chunk; today only the
	// planner ladder runs, no chunk entries are produced.
	ChunkDurationSec int
	// ClipDurationSec is the per-clip video budget (passed through
	// to the planner for budget-vs-clipDuration validation).
	ClipDurationSec int
	// MaxConcurrentJobs bounds the per-source parallelism the
	// orchestrator fans out to. 0 means "use the default 3" so
	// operators can rely on the legacy run.go semaphore.
	MaxConcurrentJobs int
}

// DefaultMaxConcurrentJobs is the orchestrator's fallback when
// OrchestratorConfig.MaxConcurrentJobs is zero. Matches the
// legacy run.go `sem := make(chan struct{}, 3)` literal.
const DefaultMaxConcurrentJobs = 3

// ErrOrchestratorNilDeps surfaces a missing required dep at
// construction. The orchestrator cannot run with nil Planner /
// Steps / Stager; the caller side (Service.RunOrchestrator or
// the composition root) is expected to validate-or-default.
var ErrOrchestratorNilDeps = errors.New("orchestrator: planner/steps/stager must be non-nil")

// Orchestrator is the new pipeline entrypoint. Dual-writes with
// legacy Service.Run during Commit 1; the legacy path is retired
// in Commit 5.
type Orchestrator struct {
	cfg      OrchestratorConfig
	planner  ClipPlanner
	steps    ExecutionStepStore
	stager   SourceStager
	cutter   VideoCutter
	renderer StockRenderer
}

// NewOrchestrator returns the canonical orchestrator. Caller-side
// code is responsible for providing non-nil Planner, Steps, and
// Stager — the lazy-default pattern is centralised in
// Service.RunOrchestrator so production wiring can reach for
// concrete deps without re-validating here.
func NewOrchestrator(cfg OrchestratorConfig, planner ClipPlanner, steps ExecutionStepStore, stager SourceStager, cutter VideoCutter, renderer StockRenderer) *Orchestrator {
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	return &Orchestrator{
		cfg:      cfg,
		planner:  planner,
		steps:    steps,
		stager:   stager,
		cutter:   cutter,
		renderer: renderer,
	}
}

// Run executes the orchestrator pipeline. The typed ArtifactManifest
// returned contains the manifest schema version + JobID + zero or
// more populated Artifact entries — Commit 1 ships the empty
// manifest shape (the chunk-emit step is wired in Commit 2 when
// Cut → Render → Stage → Publish are co-ordinated inside
// Orchestrator.Run).
func (o *Orchestrator) Run(ctx context.Context, input *RunInput) (*job.ArtifactManifest, error) {
	if o.planner == nil || o.steps == nil || o.stager == nil {
		return nil, ErrOrchestratorNilDeps
	}

	// Step 1: resolve_sources — stub for Commit 1.
	//
	// Production wiring will iterate SearchQueries via
	// SourceSearchProvider.Search + direct-URL additions via
	// stager.Stage. For Commit 1 we just Begin/Complete the step
	// to assert the types fit together; the real resolve logic
	// lands alongside the SourceStager registry in Commit 7.
	if err := o.steps.Begin("resolve_sources"); err != nil {
		return nil, fmt.Errorf("orchestrator.resolve_sources.begin: %w", err)
	}
	if err := o.steps.Complete("resolve_sources", "ok"); err != nil {
		return nil, fmt.Errorf("orchestrator.resolve_sources.complete: %w", err)
	}

	// Step 2: plan_clips — exercise the deterministic planner
	// round-trip on the FIRST source so the round-trip test in
	// planner_test.go is replicated at runtime. Future commits
	// will plan across all resolved sources.
	if err := o.steps.Begin("plan_clips"); err != nil {
		return nil, fmt.Errorf("orchestrator.plan_clips.begin: %w", err)
	}
	demoSrc, ok := firstSource(input)
	if !ok {
		err := errors.New("orchestrator: no sources to plan")
		_ = o.steps.Fail("plan_clips", err)
		return nil, err
	}
	planBudget := input.TotalMinutes * 60
	if planBudget <= 0 {
		planBudget = o.cfg.ChunkDurationSec
	}
	plans, err := o.planner.Plan(ctx, demoSrc, planBudget, o.cfg.ClipDurationSec, o.cfg.PolicyVersion)
	if err != nil {
		_ = o.steps.Fail("plan_clips", err)
		return nil, fmt.Errorf("orchestrator.plan_clips: %w", err)
	}
	if err := o.steps.Complete("plan_clips", fmt.Sprintf("%d clips planned", len(plans))); err != nil {
		return nil, fmt.Errorf("orchestrator.plan_clips.complete: %w", err)
	}

	// Step 3: stage_sources — Begin/Complete the step to mark the
	// planner's output as "staged". The actual stager.Stage call
	// lands in Commit 2 alongside the chunk-emission ladder.
	if err := o.steps.Begin("stage_sources"); err != nil {
		return nil, fmt.Errorf("orchestrator.stage_sources.begin: %w", err)
	}
	if err := o.steps.Complete("stage_sources", fmt.Sprintf("%d staged", len(plans))); err != nil {
		return nil, fmt.Errorf("orchestrator.stage_sources.complete: %w", err)
	}

	// Build the typed ArtifactManifest. Today: empty (the chunk
	// ladder is Commit 2). Production: one entry per chunk with
	// the chunk's Cut→Rendered local path + the future remote
	// asset ID stamped by the JobFinalizer.
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    input.FolderID,
		JobID:         "stock_orchestrator_v1", // Commit 2 uses the real job ID
		Artifacts:     nil,                     // populated by Commit 2
	}
	return manifest, nil
}

// firstSource returns the first source the orchestrator can plan
// against. Used by Run as a Commit 1 round-trip target.
func firstSource(input *RunInput) (VideoSource, bool) {
	if input == nil {
		return VideoSource{}, false
	}
	if len(input.DirectURLs) > 0 {
		return VideoSource{
			URL:    input.DirectURLs[0],
			Title:  "demo-direct",
			Source: input.DirectURLs[0],
		}, true
	}
	if len(input.SearchQueries) > 0 {
		return VideoSource{
			URL:    input.SearchQueries[0],
			Title:  "demo-query",
			Source: input.SearchQueries[0],
		}, true
	}
	return VideoSource{}, false
}
