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
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// OrchestratorConfig parameterises Orchestrator at construction.
// Zero values are NOT a valid runtime config — NewOrchestrator
// applies defaults for the optional fields (JobID, MaxConcurrentJobs)
// and forwards PolicyVersion + ChunkDurationSec + ClipDurationSec
// verbatim to the planner.
//
// §12-7 (July 2026): Lease is added so StockFinalizeStep can drive
// BuildFinalizationRequest + JobFinalizer.CompleteWithArtifacts
// inside the orchestrator (single-TX spine write SSOT). Service.HandleJob
// extracts Lease via extractLease(job) and threads it via cfg.Lease.
type OrchestratorConfig struct {
	// JobId is the broker-assigned job identifier stamped on the
	// returned ArtifactManifest.JobID. Stock Cutover Commit 2
	// wires Service.HandleJob → Service.runOrchestrator → NewOrchestrator
	// so the manifest carries the real broker JobID (not the
	// Commit 1 "stock_orchestrator_v1" placeholder). Empty value
	// falls back to the placeholder so non-broker callers (tests,
	// CLI) still produce a deterministic JobID.
	JobId string
	// Lease (§12-7) is the canonical finalization.Lease the
	// StockFinalizeStep reads to compose BuildFinalizationRequest.
	// Empty JobID/WorkerID/LeaseID surfaces ErrStockFinalizeLeaseMissing
	// loudly at compose time so composition-time wiring gaps don't
	// silently degrade the spine write.
	Lease finalization.Lease
	// PolicyVersion is the run-fingerprint salt (godlike/07
	// semantic). Empty value surfaces ErrStockFnRequired.
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
	// StepStore is the per-step checkpointing store (canonical
	// §12-3 / godlike/06 SSOT). nil ⇒ defaults to
	// steps.NewInMemoryStore() inside NewOrchestrator (the
	// hermetic default for tests + dev modes). Production
	// composition roots should inject steps.NewSQLiteStore(db) so
	// per-step state survives process restarts and the resume
	// contract (MarkStarted → ErrStepAlreadyCompleted → skip-on-
	// orchestrator-continue) takes effect.
	StepStore steps.Store
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

	// §12-7 (July 2026): Finalizer-side ports threaded via fluent
	// setters (WithAssetPreparation / WithJobFinalizer). Defaulted to
	// nil in NewOrchestrator; production composition roots in
	// run_orchestrator.go::runOrchestratorResilient wire the canonical
	// finalizer.NewArtifactPreparation(s.publisher, s.log) and
	// s.finalizer respectively. Test fixtures leave nil so the
	// share-test-faxture-compat path (StockPublishStep uploads
	// skipped, StockFinalizeStep spine write skipped) is the canonical
	// no-op behavior.
	artifactPreparation finalization.ArtifactPreparationService // nil ⇒ StockPublishStep logs+skips upload
	jobFinalizer        finalization.JobFinalizer              // nil ⇒ StockFinalizeStep logs+skips spine write
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
	stepStore := steps.NewInMemoryStore()
	if cfg.StepStore != nil {
		// Step 10 C2/4 (July 2026): the production composition root
		// supplies a concrete Store (typically
		// steps.NewSQLiteStore(db) bound to the canonical
		// execution_steps table). The godlike/06 "one owner per
		// fact" invariant: caller is the sole injector and
		// NewOrchestrator never overrides a non-nil store (the
		// resume contract on retry-after-crash would be silently
		// broken by the in-memory default taking over).
		stepStore = cfg.StepStore
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
		stepStore:     stepStore,
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
// Stock Cutover §12-7 (July 2026) resilience flow. It threads the
// typed *job.ArtifactManifest + the per-run FinalStatus + the
// per-run FinalizationResult through a single RunSummary envelope
// so the broker JobStatusResponse can render all three.
//
// §12-7 step ladder: the 6 typed Steps declared in
// orchestrator_steps.go iterate dispatchSteps (a typed []Step
// slice) in canonical pipeline order:
//
//  1. stock.plan           — deterministic ClipPlanner.Plan round-trip.
//  2. stock.stage_sources  — SourceStager.Prepare (future Commit 6 wires real Stage).
//  3. stock.extract_clips  — atomic TransactionalAssetWriter
//                            WriteAndEnqueue per ClipPlan
//                            (test (a) verifies writer error ⇒ ErrAtomicDispatchFailed).
//  4. stock.compose_chunks — StockRenderer.Render (future Commit 7 wires real Render).
//  5. stock.publish        — §12-7: ArtifactPreparation.Prepare per
//                            chunk + per metadata.json. Uploads
//                            Drive (or remote equivalent); the
//                            State.Published []ChunkState carries
//                            the per-chunk RemoteFileID + Location.
//                            nil ArtifactPreparation ⇒ test-fixture
//                            path (State.Published empty, spine
//                            write skipped).
//  6. stock.finalize       — §12-7: ManifestBuilder.Build +
//                            Validate + ProjectionPort.Project (best-
//                            effort Qdrant → flips FinalStatus to
//                            StatusIndexPending) + BuildFinalizationRequest
//                            + JobFinalizer.CompleteWithArtifacts
//                            SINGLE-TX SPINE WRITE (asset + version
//                            + location + outbox + SUCCEEDED).
//                            nil JobFinalizer ⇒ test-fixture path
//                            (spine write skipped).
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
func (o *Orchestrator) RunResilient(ctx context.Context, input *RunInput) (*RunSummary, error) {
	if o.planner == nil || o.stager == nil || o.stepStore == nil || len(o.dispatchSteps) == 0 {
		return nil, ErrOrchestratorNilDeps
	}

	state := &runState{}
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
		if err := o.stepStore.MarkCompleted(ctx, key, nil, nil); err != nil {
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
	summary := &RunSummary{Manifest: state.Manifest, FinalStatus: state.FinalStatus}

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

// WithAssetPreparation threads the canonical ArtifactPreparationService
// to the orchestrator's StockPublishStep. §12-7 fluent-setter pattern —
// keeps NewOrchestrator + NewOrchestratorWithResilience signatures
// stable so test (a/b/c) compile unchanged (godlike/06 backward
// minimal-surface-change principle). Composition-root production
// wiring in run_orchestrator.go::runOrchestratorResilient calls this
// once after NewOrchestrator returns. Returns the receiver for
// fluent chaining.
func (o *Orchestrator) WithAssetPreparation(svc finalization.ArtifactPreparationService) *Orchestrator {
	o.artifactPreparation = svc
	return o
}

// WithJobFinalizer threads the canonical JobFinalizer to the
// orchestrator's StockFinalizeStep. §12-7 fluent-setter pattern
// (same rationale as WithAssetPreparation). Composition-root
// production wiring in run_orchestrator.go::runOrchestratorResilient
// calls this once after WithAssetPreparation; nil pass-through is
// allowed so test fixtures can compile unchanged. Returns the
// receiver for fluent chaining.
func (o *Orchestrator) WithJobFinalizer(svc finalization.JobFinalizer) *Orchestrator {
	o.jobFinalizer = svc
	return o
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
