// Package stockpipeline — orchestrator_constructor.go (split July 2026).
//
// This file owns the canonical fixture constructors
// (NewTestStockOrchestrator), fluent setters (WithAssetPreparation,
// WithJobFinalizer), and pure helpers (stepInputFingerprint, firstSource).
// Extracted from orchestrator.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: NewProductionStockOrchestrator is the strict runtime
// constructor and NewTestStockOrchestrator is the explicit fixture constructor.
// Production orchestration is constructed by the strict constructor in
// orchestrator_production.go; fixture orchestration remains explicit here.
package pipeline

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
)

// NewTestStockOrchestrator builds the hermetic fixture orchestrator.
//
// Production code MUST use NewProductionStockOrchestrator. This builder keeps
// the deliberately permissive fixture defaults (in-memory step store,
// noop resilience ports) used by unit tests and never serves as a
// production composition path.
//
// Caller-side tests are responsible for providing non-nil Planner, Stager,
// and Cutter when they exercise RunResilient.
//
// Default fallbacks (Stock Cutover Commit 2):
//   - MaxConcurrentJobs<=0      ⇒ DefaultMaxConcurrentJobs (3)
//   - MaxConcurrentDownloads<=0 ⇒ DefaultMaxConcurrentDownloads (6)
//   - JobId==""                 ⇒ DefaultOrchestratorJobId ("stock_orchestrator_v1")
//
// Resilience default fallbacks (Stock Cutover Commit 4-expanded):
//   - builder    ⇒ stockManifestBuilder (5-entry C12 envelope)
//   - writer     ⇒ noopWriter
//   - projection ⇒ noopProjection
//
// Service.HandleJob wires the real broker JobID through cfg.JobId,
// so production traffic carries the real JobID — the placeholder
// is only used by non-broker callers (tests, CLI). Custom resilience
// ports are test-only and use the helper in
// orchestrator_test_helpers_test.go.
func NewTestStockOrchestrator(cfg core.OrchestratorConfig, planner core.ClipPlanner, stager acquisition.SourceStager, cutter core.VideoCutter) *core.Orchestrator {
	return NewStockPipeline(cfg, planner, stager, cutter)
}

func NewStockPipeline(cfg core.OrchestratorConfig, planner core.ClipPlanner, stager acquisition.SourceStager, cutter core.VideoCutter) *core.Orchestrator {
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = core.DefaultMaxConcurrentJobs
	}
	if cfg.MaxConcurrentDownloads <= 0 {
		cfg.MaxConcurrentDownloads = core.DefaultMaxConcurrentDownloads
	}
	if cfg.JobId == "" {
		cfg.JobId = core.DefaultOrchestratorJobId
	}
	stepStore := steps.NewInMemoryStore()
	if cfg.StepStore != nil {
		// Step 10 C2/4 (July 2026): the production composition root
		// supplies a concrete Store (typically
		// executionsteps.NewSQLiteStore(db) bound to the canonical
		// execution_steps table). The godlike/06 "one owner per
		// fact" invariant: caller is the sole injector and
		// NewTestStockOrchestrator never overrides a non-nil store (the
		// resume contract on retry-after-crash would be silently
		// broken by the in-memory default taking over).
		stepStore = cfg.StepStore
	}
	return core.NewOrchestrator(cfg, core.OrchestratorDeps{
		Execution: core.OrchestratorExecutionDeps{
			Planner:       planner,
			Stager:        stager,
			Cutter:        cutter,
			Builder:       core.StockManifestBuilder{},
			Writer:        core.NoopWriter{},
			Projection:    core.NoopProjection{},
			StepStore:     stepStore,
			DispatchSteps: DefaultStockSteps(),
		},
	})
}
