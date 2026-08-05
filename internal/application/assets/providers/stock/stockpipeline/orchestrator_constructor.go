// Package stockpipeline — orchestrator_constructor.go (split July 2026).
//
// This file owns the canonical constructors (NewOrchestrator,
// NewOrchestratorWithResilience), fluent setters (WithAssetPreparation,
// WithJobFinalizer), and pure helpers (stepInputFingerprint, firstSource).
// Extracted from orchestrator.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: NewOrchestrator is the single canonical constructor;
// NewOrchestratorWithResilience is the single canonical resilience-port
// injection constructor.
package stockpipeline

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

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
// ResilienceDeps bundles the optional resilience ports for
// NewOrchestratorWithResilience so the constructor stays under the
// archcheck 8-parameter cap.
type ResilienceDeps struct {
	Builder    ManifestBuilder
	Writer     TransactionalAssetWriter
	Projection ProjectionPort
}

func NewOrchestratorWithResilience(
	cfg OrchestratorConfig,
	planner ClipPlanner,
	legacySteps ExecutionStepStore,
	stager assets.SourceStager,
	cutter VideoCutter,
	renderer StockRenderer,
	resilience ResilienceDeps,
) *Orchestrator {
	o := NewOrchestrator(cfg, planner, legacySteps, stager, cutter, renderer)
	if resilience.Builder != nil {
		o.builder = resilience.Builder
	}
	if resilience.Writer != nil {
		o.writer = resilience.Writer
	}
	if resilience.Projection != nil {
		o.projection = resilience.Projection
	}
	return o
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

// WithSourceProbe (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026)
// threads the optional ffprobe-backed SourceDurationProbe port
// to the step_extract_clips step. The probe is OPTIONAL: nil
// pass-through is allowed so existing composition roots + test
// fixtures compile unchanged (godlike/07 minimum-blast-radius:
// backward-compat; the step falls through to the legacy
// unvalidated path when probe is nil and StagedAsset.DurationSec
// is 0). Returns the receiver for fluent chaining. Production
// wiring injects the ffprobe-backed concrete in
// run_orchestrator.go::runOrchestratorResilient (forward-pointer
// PR-STOCK-SOURCE-DURATION-WIRE for the live ffprobe adapter).
func (o *Orchestrator) WithSourceProbe(probe SourceDurationProbe) *Orchestrator {
	o.sourceProbe = probe
	return o
}

// WithBatchRepository (Fase 2, July 2026) threads the durable
// stock batch/group/artifact repository into the orchestrator.
// nil is allowed for tests and back-compat. Production wiring
// injects the SQLite-backed adapter in
// run_orchestrator.go::runOrchestratorResilient.
func (o *Orchestrator) WithBatchRepository(repo StockBatchRepository) *Orchestrator {
	o.batchRepository = repo
	return o
}

// WithLogger threads a real zap.Logger into the Orchestrator so
// step-level logs (download sizes, FFmpeg errors, cut results)
// appear in the journal instead of being silently swallowed by
// defaultStepRunnerLog()'s no-op fallback. Returns the receiver
// for fluent chaining.
func (o *Orchestrator) WithLogger(log *zap.Logger) *Orchestrator {
	o.executorLog = log
	return o
}

// WithLocalFS (PR-REFACTOR-P0-IO-BINDER, July 2026) threads the
// canonical LocalFSPort into the Orchestrator so steps can
// perform filesystem I/O through the typed port instead of
// importing "os" directly. nil pass-through is allowed so
// test fixtures compile unchanged (godlike/07 minimum-blast-
// radius: backward-compat; steps fall back to os.* when nil).
// Returns the receiver for fluent chaining.
func (o *Orchestrator) WithLocalFS(fs LocalFSPort) *Orchestrator {
	o.localFS = fs
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
//
// Priority: DirectURLs > DriveURLs > SearchQueries.
// DriveURLs are YouTube URLs that should be downloaded via yt-dlp
// (Google Drive download is a forward-pointer for StockStager).
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
	if len(input.DriveURLs) > 0 {
		return VideoSource{
			URL:    input.DriveURLs[0],
			Title:  "demo-drive",
			Source: input.DriveURLs[0],
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
