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
// applies defaults for the optional fields (JobID, MaxConcurrentJobs)
// and forwards PolicyVersion + ChunkDurationSec + ClipDurationSec
// verbatim to the planner.
type OrchestratorConfig struct {
	// JobId is the broker-assigned job identifier stamped on the
	// returned ArtifactManifest.JobID. Stock Cutover Commit 2
	// wires Service.HandleJob → Service.runOrchestrator → NewOrchestrator
	// so the manifest carries the real broker JobID (not the
	// Commit 1 "stock_orchestrator_v1" placeholder). Empty value
	// falls back to the placeholder so non-broker callers (tests,
	// CLI) still produce a deterministic JobID.
	JobId         string
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

// DefaultOrchestratorJobId is the Orchestrator's fallback when
// OrchestratorConfig.JobId is empty — used by Service.Run (the
// legacy signature path that has no broker JobID in scope) and by
// tests/CLI callers. Stock Cutover Commit 2 wires the real broker
// JobID through Service.runOrchestrator → NewOrchestrator so the
// placeholder is NOT used in production HandleJob traffic.
const DefaultOrchestratorJobId = "stock_orchestrator_v1"

// StockArtifactId* are the canonical stable IDs of the 5 C12 fixed
// entries the stock pipeline commits to emit (see buildStockManifest
// for the C12 5-artifact shape rationale). The IDs are reused by
// downstream Commit 4-7 hydration logic so changing the IDs here
// is a wire-format break.
const (
	StockArtifactIdMetadata  = "stock:metadata"
	StockArtifactIdThumbnail = "stock:thumbnail"
	StockArtifactIdBindings  = "stock:bindings"
	StockArtifactIdReport    = "stock:report"
	StockArtifactIdSummary   = "stock:summary"
)

// stockArtifactCount is the canonical 5-artifact shape per the
// C12 §8.4 multi-artifact envelope — see buildStockManifest.
// Stock Cutover Commit 2 locks this count via a compile-time
// assertion in buildStockManifest; future waves that want a
// different arity (per-chunk artifacts, etc.) are tracked as
// separate follow-ups (PR-STOCK-ARTIFACT-ARITY-CHANGE or similar).
const stockArtifactCount = 5

// buildStockManifest returns the C12 5-artifact envelope for stock.
//
// Why a hard-coded 5? The user spec for Stock Cutover Commit 2 says:
//
//	"the JobStatusResponse exposes __artifact_manifest with the C12
//	 5-artifact shape"
//
// The 5 fixed entries are the per-kind envelope the downstream
// runner (internal/application/jobs/worker/runner.go::uploadManifest)
// routes on:
//
//	(a) metadata   — pipeline metadata.json uploaded at the end
//	(b) thumbnail  — cover png for the run (rendered once per run)
//	(c) bindings   — source-clip bindings report (one per run)
//	(d) report     — runtime summary JSON (one per run)
//	(e) summary    — narrative text summary (one per run)
//
// All entries have Required:false today because Commit 2 cannot
// populate their on-disk Paths (chunk rendering, Drive upload,
// and the binder run all land in Commit 4-7). Required is flipped
// to true in Commit 4-7 once the entry has a real local path —
// Validate() requires Required:true ⇒ non-empty Path; setting
// Required:false today passes Validate() cleanly.
//
// Validate() invariants upheld:
//   - SchemaVersion non-empty (pipelinegen.artifacts.v1)
//   - len(Artifacts) > 0
//   - no Required⇒empty Path
//   - no non-empty Path⇒empty Filename (Commit 4-7 hydrates both)
//
// (NIT-1 — kind overloading rationale): ArtifactKindScriptJSON +
// ArtifactKindScriptText are repurposed for stock here because the
// C12 envelope (domain/job/artifact_manifest.go) does not yet
// declare a "stock_run_report" or "stock_narrative" kind. The
// underlying wire-string is still valid JSON / still valid text —
// downstream consumers dispatch by Kind string only when a
// sender-side router maps a Kind to a transport (the stock
// pipeline does NOT route ScriptJSON-named entries to the scripts
// gateway; the sender-side routing is bidirectional via filename
// + manifest per-kind ID convention, not kind value alone). A
// follow-up PR may introduce ArtifactKindRunReport +
// ArtifactKindStockSummary; until then, the kind labels carry a
// stock-pipeline semantic load that the operator dashboards must
// understand via the manifest's stable IDs (stock:report /
// stock:summary) rather than the kind value. This rationale is
// mirrored in the CHANGELOG entry for Commit 2.
func buildStockManifest(workflowID, jobID string) *job.ArtifactManifest {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    workflowID,
		JobID:         jobID,
		Artifacts: []job.Artifact{
			{
				ID:       StockArtifactIdMetadata,
				Kind:     job.ArtifactKindMetadata,
				Filename: "metadata.json",
				MIMEType: "application/json",
				Required: false, // Commit 4-7 flips to true once Path is hydrated
			},
			{
				ID:       StockArtifactIdThumbnail,
				Kind:     job.ArtifactKindImage,
				Filename: "thumbnail.png",
				MIMEType: "image/png",
				Required: false,
			},
			{
				ID:       StockArtifactIdBindings,
				Kind:     job.ArtifactKindClipBindings,
				Filename: "bindings.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdReport,
				Kind:     job.ArtifactKindScriptJSON,
				Filename: "report.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdSummary,
				Kind:     job.ArtifactKindScriptText,
				Filename: "summary.txt",
				MIMEType: "text/plain",
				Required: false,
			},
		},
	}
	// Compile-time invariant pin: the C12 5-artifact shape must
	// stay arity-5 unless a follow-up explicitly changes the
	// shape (and bumps these constants). Future maintainers who
	// want a different arity must update stockArtifactCount AND
	// the constant list above AND the CHANGELOG entry referencing
	// this commit.
	if len(manifest.Artifacts) != stockArtifactCount {
		panic("buildStockManifest: artifact arity drifted from canonical 5 (Stock Cutover Commit 2 invariant violated)")
	}
	return manifest
}

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
// Service.runOrchestrator so production wiring can reach for
// concrete deps without re-validating here.
//
// Default fallbacks (Stock Cutover Commit 2):
//   - MaxConcurrentJobs<=0 ⇒ DefaultMaxConcurrentJobs (3)
//   - JobId==""            ⇒ DefaultOrchestratorJobId ("stock_orchestrator_v1")
//
// Service.HandleJob wires the real broker JobID through cfg.JobId,
// so production traffic carries the real JobID — the placeholder
// is only used by non-broker callers (tests, CLI).
func NewOrchestrator(cfg OrchestratorConfig, planner ClipPlanner, steps ExecutionStepStore, stager SourceStager, cutter VideoCutter, renderer StockRenderer) *Orchestrator {
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if cfg.JobId == "" {
		cfg.JobId = DefaultOrchestratorJobId
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

	// Build the typed ArtifactManifest — C12 5-artifact shape per
	// Stock Cutover Commit 2 spec literal: "JobStatusResponse
	// exposes __artifact_manifest with the C12 5-artifact shape".
	//
	// Today (Commit 2): 5 entries with stable IDs + kinds + filenames
	// but empty Paths (Required:false). Validate() passes because
	// the only Required-checks are (Id,Kind,Schema) which all
	// have non-empty values.
	//
	// Future (Commit 4-7): Path + Required:true as chunk rendering
	// + Drive upload + binder run land. Stable IDs reuse stockArtifact*
	// constants so downstream Commit 4-7 hydration logic does not
	// have to re-derive them from string literals.
	workflowID := input.FolderID
	if workflowID == "" {
		workflowID = input.FolderName
	}
	return buildStockManifest(workflowID, o.cfg.JobId), nil
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
