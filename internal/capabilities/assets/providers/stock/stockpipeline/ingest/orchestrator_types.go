// Package stockpipeline — orchestrator_types.go (split July 2026).
//
// This file owns the canonical types, constants, and struct for the
// Orchestrator. Extracted from orchestrator.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: OrchestratorConfig is the single canonical config shape;
// Orchestrator is the single canonical pipeline entrypoint struct.
package assets

import (
	"errors"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// OrchestratorConfig parameterises Orchestrator at construction.
// Zero values are NOT a valid runtime config — the explicit fixture
// constructor applies defaults for the optional fields (JobID, MaxConcurrentJobs)
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
	// ArtifactManifest emits one entry per chunk; the cutter + renderer
	// ladder (Phase 1, July 2026) produces real .mp4 files.
	ChunkDurationSec int
	// ClipDurationSec is the per-clip video budget (passed through
	// to the planner for budget-vs-clipDuration validation).
	ClipDurationSec int
	// MaxConcurrentJobs bounds the per-source parallelism the
	// orchestrator fans out to. 0 means "use the default 3" so
	// operators can rely on the legacy run.go semaphore.
	MaxConcurrentJobs int
	// StrictDurationValidation is enabled only by the production
	// constructor. It prevents unknown source durations from bypassing
	// timestamp bounds checks before FFmpeg receives a clip.
	StrictDurationValidation bool
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
// tests/CLI callers. Production HandleJob traffic passes the real
// broker JobID via runOrchestratorResilient so the placeholder is
// NOT used in production.
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

// ErrOrchestratorNilDeps surfaces a missing required dep at
// construction. The orchestrator cannot run with nil Planner /
// Steps / Stager; the caller side (Service.RunOrchestrator or
// the composition root) is expected to validate-or-default.
var ErrOrchestratorNilDeps = errors.New("orchestrator: planner/stager/renderer/stepStore must be non-nil")

// Orchestrator is the canonical pipeline entrypoint (STATO ATTUALE).
// Service.Run coexists for ServiceRunner interface back-compat
// (DEPRECATO); production traffic routes via runOrchestratorResilient.
//
// Resilience ports (builder / writer / projection) are wired and
// exercised by RunResilient. The fluent setters WithAssetPreparation
// and WithJobFinalizer thread the finalizer-side ports. The ports
// use fixture-only defaults
// (stockManifestBuilder + noopWriter + noopProjection) from
// NewTestStockOrchestrator; production wiring uses NewProductionStockPipeline. RunResilient is the canonical entry
// point that exercises the full resilience flow (manifest build →
// gate → atomic dispatch → Qdrant projection); Run is a thin wrapper
// that returns the manifest component of the RunSummary for legacy
// callers (existing run_orchestrator_test.go tests).
type Orchestrator struct {
	cfg      OrchestratorConfig
	planner  ClipPlanner
	stager   acquisition.SourceStager
	cutter   VideoCutter
	renderer StockRenderer
	// builder emits the typed *job.ArtifactManifest from (workflowID,
	// jobID). Default: stockManifestBuilder wrapping buildStockManifest.
	builder ManifestBuilder
	// writer performs atomic UPSERT + outbox-enqueue per planned clip.
	// Test fixture default: noopWriter. Production injection point is
	// the SQLite-backed outbox adapter supplied through
	// ProductionStockPipelineDeps.
	writer TransactionalAssetWriter
	// projection wraps the post-emission Qdrant sync. Default:
	// noopProjection (returns nil => SUCCEEDED). Production wiring
	// injects the Qdrant-backed adapter; a non-nil return from
	// projection.Project flips RunResilient's FinalStatus to
	// StatusIndexPending (instead of returning an error to the
	// caller) so the Qdrant-reconciler task can retry asynchronously.
	projection ProjectionPort
	// stepStore is the canonical §12-3 ports.Store the Orchestrator
	// uses for per-step checkpointing (MarkStarted / MarkCompleted /	// MarkFailed). Default in NewTestStockOrchestrator +
	// NewOrchestratorWithResilience use steps.NewInMemoryStore() — a

	// production-grade in-memory impl that satisfies the canonical
	// interface and survives concurrent goroutines. Forward-pointer:
	// the SQLite-backed impl lands in a follow-up commit (the
	// production composition root will switch to it).
	stepStore steps.Store
	// dispatchSteps is the canonical 6-step typed []Step slice the
	// Orchestrator iterates in RunResilient, in pipeline order.
	// Default in NewTestStockOrchestrator is DefaultStockSteps() (the typed
	// Steps declared in orchestrator_steps.go). Production wiring
	// uses the canonical DefaultStockSteps slice; test helpers may replace
	// it in-package when exercising fork-points (e.g. dry-run modes that skip
	// stock.publish).
	dispatchSteps []Step
	// executorLog is the per-orchestrator logger passed to each
	// step's StepRunner. Optional — falls back to a no-op logger
	// when nil (composition roots that haven't yet wired a logger
	// shouldn't crash the orchestrator at runtime).
	executorLog *zap.Logger

	// §12-7 (July 2026): Finalizer-side ports are explicit fields.
	// Test fixtures may set them through the retained fixture setters;
	// production composition supplies them via ProductionStockPipelineDeps.
	// Defaulted to
	// nil in NewTestStockOrchestrator; production composition roots in
	// run_orchestrator.go::runOrchestratorResilient wire the canonical
	// finalizer.NewArtifactPreparation(s.publisher, s.log) and
	// s.finalizer respectively. Test fixtures leave nil so the
	// share-test-faxture-compat path (StockPublishStep uploads
	// skipped, StockFinalizeStep spine write skipped) is the canonical
	// no-op behavior.
	artifactPreparation finalization.ArtifactPreparationService // nil ⇒ StockPublishStep logs+skips upload
	jobFinalizer        finalization.JobFinalizer               // nil ⇒ StockFinalizeStep logs+skips spine write

	// sourceProbe is the required production port used to validate
	// ClipPlan.EndSec against source duration before cutting.
	// Production composition supplies it via ProductionStockPipelineDeps;
	// fixture construction may leave it nil when the test does not exercise
	// duration probing.
	sourceProbe SourceDurationProbe

	// batchRepository is the durable stock batch/group/artifact repository.
	// Production composition supplies it via ProductionStockPipelineDeps;
	// fixture construction may leave it nil when batch persistence is not
	// under test.
	batchRepository StockBatchRepository

	// localFS is the Pattern 0 typed port for local filesystem I/O.
	// Production composition supplies it via ProductionStockPipelineDeps;
	// fixture construction may use the retained WithLocalFS setter.
	localFS LocalFSPort
}
