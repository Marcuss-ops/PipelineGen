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

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
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
	// legacySteps is the pre-§12-5 in-process step store; kept as a
	// constructor-injected field for backwards compatibility with the
	// Service.runOrchestrator signature (which continues to pass a
	// *InMemoryStepStore). The canonical §12-3 step checkpointing in
	// RunResilient uses stepStore (below); legacySteps is no longer
	// referenced from any production path. Forward-pointer: §12-5.x
	// can retire this field once composition-root wiring drops the
	// local InMemoryStepStore calls entirely.
	legacySteps ExecutionStepStore
	stager      assets.SourceStager
	cutter      VideoCutter
	renderer    StockRenderer
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
	// stepStore is the canonical §12-3 ports.Store the Orchestrator
	// uses for per-step checkpointing (MarkStarted / MarkCompleted /
	// MarkFailed). Default in NewOrchestrator +
	// NewOrchestratorWithResilience is steps.NewInMemoryStore() — a
	// production-grade in-memory impl that satisfies the canonical
	// interface and survives concurrent goroutines. Forward-pointer:
	// the SQLite-backed impl lands in a follow-up commit (the
	// production composition root will switch to it).
	stepStore steps.Store
	// dispatchSteps is the canonical 6-step typed []Step slice the
	// Orchestrator iterates in RunResilient, in pipeline order.
	// Default in NewOrchestrator is DefaultStockSteps() (the typed
	// Steps declared in orchestrator_steps.go). Production wiring
	// can inject a custom slice via NewOrchestratorWithResilience
	// to thread fork-points (e.g. dry-run modes that skip
	// stock.publish).
	dispatchSteps []Step
	// executorLog is the per-orchestrator logger passed to each
	// step's StepRunner. Optional — falls back to a no-op logger
	// when nil (composition roots that haven't yet wired a logger
	// shouldn't crash the orchestrator at runtime).
	executorLog *zap.Logger
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
func NewOrchestrator(cfg OrchestratorConfig, planner ClipPlanner, legacySteps ExecutionStepStore, stager assets.SourceStager, cutter VideoCutter, renderer StockRenderer) *Orchestrator {
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if cfg.JobId == "" {
		cfg.JobId = DefaultOrchestratorJobId
	}
	return &Orchestrator{
		cfg:           cfg,
		planner:       planner,
		legacySteps:   legacySteps,
		stager:        stager,
		cutter:        cutter,
		renderer:      renderer,
		builder:       stockManifestBuilder{},
		writer:        noopWriter{},
		projection:    noopProjection{},
		stepStore:     steps.NewInMemoryStore(),
		dispatchSteps: DefaultStockSteps(),
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
	legacySteps ExecutionStepStore,
	stager assets.SourceStager,
	cutter VideoCutter,
	renderer StockRenderer,
	builder ManifestBuilder,
	writer TransactionalAssetWriter,
	projection ProjectionPort,
) *Orchestrator {
	o := NewOrchestrator(cfg, planner, legacySteps, stager, cutter, renderer)
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
// Stock Cutover §12-5 (July 2026) resilience flow. It threads the
// typed *job.ArtifactManifest + the per-run FinalStatus through a
// single RunSummary envelope so the broker JobFinalizer can stamp
// the right job-status without re-inferring it from the manifest
// alone.
//
// §12-5 step ladder: the 6 typed Steps declared in
// orchestrator_steps.go iterate dispatchSteps (a typed []Step
// slice) in canonical pipeline order:
//
//  1. stock.plan           — deterministic ClipPlanner.Plan round-trip.
//  2. stock.stage_sources  — SourceStager.Prepare (future Commit 6 wires real Stage).
//  3. stock.extract_clips  — atomic TransactionalAssetWriter
//                            WriteAndEnqueue per ClipPlan
//                            (test (a) verifies writer error ⇒ ErrAtomicDispatchFailed).
//  4. stock.compose_chunks — StockRenderer.Render (future Commit 7 wires real Render).
//  5. stock.publish        — chunk upload to Drive + Qdrant sync
//                            (future Commit 8 wires real upload).
//  6. stock.finalize       — ManifestBuilder.Build + Validate +
//                            ProjectionPort.Project (test (b) verifies
//                            Validate failure ⇒ ErrManifestIncomplete;
//                            test (c) verifies Project failure ⇒
//                            StatusIndexPending flip with nil error).
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
// Resume semantics: the orchestrator iterates the typed []Step
// slice in declaration order — NOT §12-3's lexically-sorted
// FirstNonCompleted ordering (the §12-3 doc-comment warns that
// lexical sort assumes step_keys use the "01_stage / 02_render"
// convention; the user spec for §12-5 mandates the `stock.<stage>`
// naming, so we read the typed slice for canonical order). On
// retry-after-crash, FirstNonCompleted differentiates "earlier
// step did not complete" (handled by re-entering that step's
// slot via the typed slice) from "all steps complete" (handled
// by returning RunSummary immediately).
func (o *Orchestrator) RunResilient(ctx context.Context, input *RunInput) (*RunSummary, error) {
	if o.planner == nil || o.stager == nil || o.stepStore == nil || len(o.dispatchSteps) == 0 {
		return nil, ErrOrchestratorNilDeps
	}

	state := &runState{}
	runner := &orchestratorRunner{orch: o, in: input, state: state, log: o.executorLogOrNop()}

	for _, step := range o.dispatchSteps {
		key := steps.StepKey{
			JobID:            o.cfg.JobId,
			StepKey:          step.Name(),
			InputFingerprint: stepInputFingerprint(o.cfg.JobId, step.Name()),
		}

		if err := o.stepStore.MarkStarted(ctx, key); err != nil {
			return nil, fmt.Errorf("orchestrator: %s MarkStarted: %w", step.Name(), err)
		}
		if runErr := step.Run(ctx, runner); runErr != nil {
			// MarkFailed is best-effort: the typed sentinel is
			// what callers errors.Is on, not the row's LastError.
			// We still call MarkFailed so the §12-3 audit log
			// captures the failure path.
			_ = o.stepStore.MarkFailed(ctx, key, runErr.Error())
			return nil, runErr
		}
		if err := o.stepStore.MarkCompleted(ctx, key, nil, nil); err != nil {
			// ErrStepAlreadyCompleted cannot fire here (we just
			// MarkStarted the same key); any other error
			// (ErrStoreNotWired, ErrInvalidStepKey) is a
			// programming error and surfaces loudly.
			return nil, fmt.Errorf("orchestrator: %s MarkCompleted: %w", step.Name(), err)
		}
	}

	return &RunSummary{Manifest: state.Manifest, FinalStatus: state.FinalStatus}, nil
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

// stepInputFingerprint returns the canonical input fingerprint
// for a step within (JobID, stepName). Per §12-3 Design A (per-row
// canonical): each (JobID, StepKey, fingerprint) triple is a
// distinct row. For §12-5 minimal scope the fingerprint is a
// concatenated stable string so retries with the same triple
// MarkStarted idempotently (§12-3 MarkStarted semantics).
//
// Future commits can tighten the fingerprint to a chained SHA256
// of the previous step's Result JSON so retries with different
// inputs fragment cleanly per Design A's "Retries with a
// different fingerprint INSERT a new row" rule.
func stepInputFingerprint(jobID, stepName string) string {
	return jobID + "|" + stepName
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
