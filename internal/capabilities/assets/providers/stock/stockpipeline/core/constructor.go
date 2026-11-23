package core

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// OrchestratorDeps is the explicit dependency bundle accepted by
// NewOrchestrator. The pipeline package owns the strict production/fixture
// validation; this constructor only performs the shared defaulting so a
// manually assembled Orchestrator cannot silently skip the execution bounds.
// OrchestratorExecutionDeps groups the ports that plan, stage, cut, build,
// write, and project one stock run.
type OrchestratorExecutionDeps struct {
	Planner       ClipPlanner
	Stager        acquisition.SourceStager
	Cutter        VideoCutter
	Builder       ManifestBuilder
	Writer        TransactionalAssetWriter
	Projection    ProjectionPort
	StepStore     steps.Store
	DispatchSteps []Step
}

// OrchestratorFinalizationDeps groups the persistence, finalization, and
// runtime ports used after extraction.
type OrchestratorFinalizationDeps struct {
	ArtifactPreparation   finalization.ArtifactPreparationService
	JobFinalizer          finalization.JobFinalizer
	DestinationReconciler DestinationReconciler
	SourceProbe           SourceDurationProbe
	BatchRepository       StockBatchRepository
	LocalFS               LocalFSPort
}

// OrchestratorDeps is the explicit dependency bundle accepted by
// NewOrchestrator. The pipeline package owns the strict production/fixture
// validation; this constructor only performs the shared defaulting so a
// manually assembled Orchestrator cannot silently skip the execution bounds.
type OrchestratorDeps struct {
	Execution    OrchestratorExecutionDeps
	Finalization OrchestratorFinalizationDeps
	ExecutorLog  *zap.Logger
}

// NewOrchestrator assembles the canonical Orchestrator from an explicit
// dependency bundle. Callers that need production validation use
// pipeline.NewProductionStockOrchestrator; callers that need the hermetic
// fixture defaults use pipeline.NewTestStockOrchestrator.
func NewOrchestrator(cfg OrchestratorConfig, deps OrchestratorDeps) *Orchestrator {
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if cfg.MaxConcurrentDownloads <= 0 {
		cfg.MaxConcurrentDownloads = DefaultMaxConcurrentDownloads
	}
	if cfg.JobId == "" {
		cfg.JobId = DefaultOrchestratorJobId
	}
	return &Orchestrator{
		cfg:                   cfg,
		planner:               deps.Execution.Planner,
		stager:                deps.Execution.Stager,
		cutter:                deps.Execution.Cutter,
		builder:               deps.Execution.Builder,
		writer:                deps.Execution.Writer,
		projection:            deps.Execution.Projection,
		stepStore:             deps.Execution.StepStore,
		dispatchSteps:         deps.Execution.DispatchSteps,
		artifactPreparation:   deps.Finalization.ArtifactPreparation,
		jobFinalizer:          deps.Finalization.JobFinalizer,
		destinationReconciler: deps.Finalization.DestinationReconciler,
		sourceProbe:           deps.Finalization.SourceProbe,
		batchRepository:       deps.Finalization.BatchRepository,
		localFS:               deps.Finalization.LocalFS,
		executorLog:           deps.ExecutorLog,
	}
}

// WithAssetPreparation threads the canonical ArtifactPreparationService.
func (o *Orchestrator) WithAssetPreparation(svc finalization.ArtifactPreparationService) *Orchestrator {
	o.artifactPreparation = svc
	return o
}

// WithJobFinalizer threads the canonical JobFinalizer.
func (o *Orchestrator) WithJobFinalizer(svc finalization.JobFinalizer) *Orchestrator {
	o.jobFinalizer = svc
	return o
}

// WithDestinationReconciler threads the post-publish destination hygiene pass.
func (o *Orchestrator) WithDestinationReconciler(svc DestinationReconciler) *Orchestrator {
	o.destinationReconciler = svc
	return o
}

// WithLocalFS threads the typed local filesystem port.
func (o *Orchestrator) WithLocalFS(fs LocalFSPort) *Orchestrator {
	o.localFS = fs
	return o
}

// Config returns the normalised configuration of this per-run orchestrator.
func (o *Orchestrator) Config() OrchestratorConfig {
	if o == nil {
		return OrchestratorConfig{}
	}
	return o.cfg
}
