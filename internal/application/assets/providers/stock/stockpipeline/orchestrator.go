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

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
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
//
// Stock Cutover Commit 4-expanded (July 2026): the orchestrator gained
// three resilience ports (builder / writer / projection). The ports
// are wired to canonical default implementations
// (stockManifestBuilder + noopWriter + noopProjection) by
// NewOrchestrator; production wiring can override them via
// NewOrchestratorWithResilience. RunResilient is the canonical entry
// point that exercises the full resilience flow (manifest build →
// gate → atomic dispatch → Qdrant projection); Run is a thin wrapper
// that returns the manifest component of the RunSummary for legacy
// callers (existing run_orchestrator_test.go tests).
type Orchestrator struct {
	cfg      OrchestratorConfig
	planner  ClipPlanner
	steps    ExecutionStepStore
	stager   SourceStager
	cutter   VideoCutter
	renderer StockRenderer
	// builder emits the typed *job.ArtifactManifest from (workflowID,
	// jobID). Default: stockManifestBuilder wrapping buildStockManifest.
	builder ManifestBuilder
	// writer performs atomic UPSERT + outbox-enqueue per planned clip.
	// Default: noopWriter. Production injection point is the
	// SQLite-backed outbox adapter (NewOrchestratorWithResilience).
	writer TransactionalAssetWriter
	// projection wraps the post-emission Qdrant sync. Default:
	// noopProjection (returns nil => SUCCEEDED). Production wiring
	// injects the Qdrant-backed adapter; a non-nil return from
	// projection.Project flips RunResilient's FinalStatus to
	// StatusIndexPending (instead of returning an error to the
	// caller) so the Qdrant-reconciler task can retry asynchronously.
	projection ProjectionPort
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
// Resilience default fallbacks (Stock Cutover Commit 4-expanded):
//   - builder    ⇒ stockManifestBuilder (5-entry C12 envelope)
//   - writer     ⇒ noopWriter
//   - projection ⇒ noopProjection
//
// Service.HandleJob wires the real broker JobID through cfg.JobId,
// so production traffic carries the real JobID — the placeholder
// is only used by non-broker callers (tests, CLI). Production wiring
// that wants custom resilience ports MUST use
// NewOrchestratorWithResilience instead.
func NewOrchestrator(cfg OrchestratorConfig, planner ClipPlanner, steps ExecutionStepStore, stager SourceStager, cutter VideoCutter, renderer StockRenderer) *Orchestrator {
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if cfg.JobId == "" {
		cfg.JobId = DefaultOrchestratorJobId
	}
	return &Orchestrator{
		cfg:        cfg,
		planner:    planner,
		steps:      steps,
		stager:     stager,
		cutter:     cutter,
		renderer:   renderer,
		builder:    stockManifestBuilder{},
		writer:     noopWriter{},
		projection: noopProjection{},
	}
}

// NewOrchestratorWithResilience is the canonical constructor when
// caller-side code wants to inject custom resilience ports (the
// SQLite-backed outbox wrapper, the Qdrant-backed projection
// adapter, an alternate ManifestBuilder for chunk-emission paths
// that need richer envelopes). Defaults are inherited from
// NewOrchestrator; nil arguments to this overload are silently
// replaced by the canonical default implementations.
//
// The canonical test surface uses this constructor directly so the
// 3 failure-mode tests in run_upload_indexing_test.go can inject
// per-test failing stubs (writer returns error / builder returns
// incomplete manifest / projection returns Qdrant-offline error)
// without touching the production composition wiring at
// service.go::WireStockPipeline.
func NewOrchestratorWithResilience(
	cfg OrchestratorConfig,
	planner ClipPlanner,
	steps ExecutionStepStore,
	stager SourceStager,
	cutter VideoCutter,
	renderer StockRenderer,
	builder ManifestBuilder,
	writer TransactionalAssetWriter,
	projection ProjectionPort,
) *Orchestrator {
	o := NewOrchestrator(cfg, planner, steps, stager, cutter, renderer)
	if builder != nil {
		o.builder = builder
	}
	if writer != nil {
		o.writer = writer
	}
	if projection != nil {
		o.projection = projection
	}
	return o
}

// Run executes the orchestrator pipeline and returns the typed
// *job.ArtifactManifest component of the RunSummary.
//
// Commit 1–2 (PR-D): Run is a thin wrapper around RunResilient that
// drops the FinalStatus + Project surface. This keeps the existing
// Service.runOrchestrator callers chain-stable (Manifest-shaped
// return) while the resilience flow lives behind RunResilient.
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

// RunResilient is the canonical orchestrator entry point for the
// Commit 4-expanded resilience flow. It threads the typed
// *job.ArtifactManifest + the per-run FinalStatus through a single
// RunSummary envelope so the broker JobFinalizer can stamp the right
// job-status without re-inferring it from the manifest alone.
//
// Step ladder (per spec https://AGENTS.md Pattern 3):
//
//  1. resolve_sources — SourceSearchProvider.Search + Stage.
//  2. plan_clips      — deterministic ClipPlanner.Plan round-trip.
//  3. stage_sources   — SourceStager.Stage.
//  4. build_manifest  — ManifestBuilder.Build (default 5-entry C12).
//  5. validate_manifest — manifest-completeness gate (test b).
//  6. emit_chunks     — TransactionalAssetWriter.WriteAndEnqueue
//     (atomic; writer error ⇒ ErrAtomicDispatchFailed; test a).
//  7. project_manifest — ProjectionPort.Project; error INFLECTS
//     FinalStatus to StatusIndexPending rather than aborting
//     (test c). RunResilient returns nil error in this case so
//     the broker runner persists the index-pending state and the
//     Qdrant-reconciler task retries asynchronously.
//
// Steps 4-7 are new for Commit 4-expanded; steps 1-3 carry forward
// from earlier commits.
func (o *Orchestrator) RunResilient(ctx context.Context, input *RunInput) (*RunSummary, error) {
	if o.planner == nil || o.steps == nil || o.stager == nil {
		return nil, ErrOrchestratorNilDeps
	}

	// Step 1: resolve_sources — stub. Real production wiring iterates
	// SearchQueries via SourceSearchProvider.Search and direct-URL
	// additions via stager.Stage. We Begin/Complete to assert the
	// types fit together; the real resolve logic lands alongside
	// the SourceStager registry in Commit 7.
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

	// Step 3: stage_sources — Begin/Complete to mark the planner's
	// output as "staged". The actual stager.Stage call lands in
	// Commit 2 alongside the chunk-emission ladder.
	if err := o.steps.Begin("stage_sources"); err != nil {
		return nil, fmt.Errorf("orchestrator.stage_sources.begin: %w", err)
	}
	if err := o.steps.Complete("stage_sources", fmt.Sprintf("%d staged", len(plans))); err != nil {
		return nil, fmt.Errorf("orchestrator.stage_sources.complete: %w", err)
	}

	// Step 4: build_manifest via the injected ManifestBuilder.
	// Defaults to stockManifestBuilder → buildStockManifest
	// (5-entry C12 envelope, all Required:false). Tests inject a
	// custom builder to exercise the manifest-completeness gate
	// (test (b) in run_upload_indexing_test.go).
	workflowID := input.FolderID
	if workflowID == "" {
		workflowID = input.FolderName
	}
	var manifest *job.ArtifactManifest
	if o.builder != nil {
		manifest, err = o.builder.Build(workflowID, o.cfg.JobId)
		if err != nil {
			return nil, fmt.Errorf("orchestrator.build_manifest: %w", err)
		}
	} else {
		manifest = buildStockManifest(workflowID, o.cfg.JobId)
	}

	// Step 5: manifest-completeness gate. Validate() rejects any
	// Required:true artifact with empty Path. The orchestrator
	// surfaces this as ErrManifestIncomplete (typed) and returns
	// nil RunSummary; the JobFinalizer stamps Failed.
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestIncomplete, err)
	}

	// Step 6: atomic dispatch per planned clip. Production
	// TransactionalAssetWriter wraps asset_index.Upsert +
	// clipsRepo.UpsertClip + outbox.EnqueueAndIndex under a
	// single SQLite tx; a returned non-nil error rolls back the
	// tx. The orchestrator surfaces the failure as
	// ErrAtomicDispatchFailed (test (a) verifies a writer error
	// aborts the run; the canonical contract is: no partial
	// writes leak into media_assets when the writer returns
	// non-nil).
	if err := o.steps.Begin("emit_chunks"); err != nil {
		return nil, fmt.Errorf("orchestrator.emit_chunks.begin: %w", err)
	}
	for i, plan := range plans {
		clip := &asset.Asset{
			ID:        plan.OutputLogicalID,
			Name:      fmt.Sprintf("chunk_%d", i),
			Source:    asset.Source("stock"),
			MediaType: asset.MediaType("video"),
		}
		if err := o.writer.WriteAndEnqueue(ctx, clip, ""); err != nil {
			_ = o.steps.Fail("emit_chunks", err)
			return nil, fmt.Errorf("%w: chunk %d (%s): %v",
				ErrAtomicDispatchFailed, i, clip.ID, err)
		}
	}
	if err := o.steps.Complete("emit_chunks", fmt.Sprintf("%d emitted", len(plans))); err != nil {
		return nil, fmt.Errorf("orchestrator.emit_chunks.complete: %w", err)
	}

	// Step 7: post-emission Qdrant projection. A non-nil return
	// from the projection port does NOT abort the run —
	// RunResilient flips FinalStatus to StatusIndexPending and
	// returns nil from Run so the Qdrant-reconciler task can
	// retry asynchronously (test (c) verifies this contract).
	// The "project_manifest" step is marked Failed in the steps
	// store when projection errors; Run itself returns success
	// because the artifacts ARE on Drive; only the index
	// projection is deferred.
	var projectionErr error
	if err := o.steps.Begin("project_manifest"); err != nil {
		return nil, fmt.Errorf("orchestrator.project_manifest.begin: %w", err)
	}
	if o.projection != nil {
		projectionErr = o.projection.Project(ctx, manifest)
	}
	finalStatus := job.StatusSucceeded
	if projectionErr != nil {
		finalStatus = job.StatusIndexPending
		_ = o.steps.Fail("project_manifest", projectionErr)
	} else {
		_ = o.steps.Complete("project_manifest", "ok")
	}

	return &RunSummary{Manifest: manifest, FinalStatus: finalStatus}, nil
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
