package pipeline

import (
	"errors"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"reflect"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ProductionPipelineDeps groups the execution ports for the live stock run.
type ProductionPipelineDeps struct {
	Planner core.ClipPlanner
	Stager  acquisition.SourceStager
	Cutter  core.VideoCutter
	Builder core.ManifestBuilder
}

// ProductionPersistenceDeps groups durable state and finalization ports.
type ProductionPersistenceDeps struct {
	Writer              core.TransactionalAssetWriter
	Projection          core.ProjectionPort
	StepStore           steps.Store
	ArtifactPreparation finalization.ArtifactPreparationService
	JobFinalizer        finalization.JobFinalizer

	// core.DestinationReconciler is the OPTIONAL post-publish hygiene pass that
	// removes stale pipeline-owned artifacts left by earlier plans in the
	// destination folder (see step_publish.go, destination-hygiene section).
	// Optional by design:
	// nil keeps the publish path exactly as it was before, so this port can be
	// rolled out without a flag day.
	DestinationReconciler core.DestinationReconciler

	BatchRepository core.StockBatchRepository
}

// ProductionRuntimeDeps groups probing, filesystem, and observability ports.
type ProductionRuntimeDeps struct {
	SourceProbe core.SourceDurationProbe
	LocalFS     core.LocalFSPort
	Logger      *zap.Logger
}

// ProductionStockPipelineDeps is the complete dependency graph required by
// the live stock pipeline. Unlike NewTestStockOrchestrator, this bundle has no
// implicit defaults: every port that can affect a production verdict is
// supplied by the composition root.
type ProductionStockPipelineDeps struct {
	Pipeline    ProductionPipelineDeps
	Persistence ProductionPersistenceDeps
	Runtime     ProductionRuntimeDeps
}

var (
	ErrProductionPlannerMissing             = errors.New("stock production orchestrator: planner is required")
	ErrProductionStagerMissing              = errors.New("stock production orchestrator: source stager is required")
	ErrProductionCutterMissing              = errors.New("stock production orchestrator: cutter is required")
	ErrProductionManifestBuilderMissing     = errors.New("stock production orchestrator: manifest builder is required")
	ErrProductionWriterMissing              = errors.New("stock production orchestrator: transactional writer is required")
	ErrProductionProjectionMissing          = errors.New("stock production orchestrator: projection port is required")
	ErrProductionStepStoreMissing           = errors.New("stock production orchestrator: step store is required")
	ErrProductionArtifactPreparationMissing = errors.New("stock production orchestrator: artifact preparation is required")
	ErrProductionJobFinalizerMissing        = errors.New("stock production orchestrator: job finalizer is required")
	ErrProductionSourceProbeMissing         = errors.New("stock production orchestrator: source duration probe is required")
	ErrProductionBatchRepositoryMissing     = errors.New("stock production orchestrator: batch repository is required")
	ErrProductionLocalFSMissing             = errors.New("stock production orchestrator: local filesystem port is required")
	ErrProductionLoggerMissing              = errors.New("stock production orchestrator: logger is required")
)

// NewProductionStockOrchestrator is the sole strict runtime constructor. It validates
// the entire dependency graph before returning an executable pipeline;
// no nil dependency can reach RunResilient and no test noop can be mistaken
// for a live capability. NewTestStockOrchestrator is never called by this
// constructor.
func NewProductionStockOrchestrator(cfg core.OrchestratorConfig, deps ProductionStockPipelineDeps) (*core.Orchestrator, error) {
	checks := []struct {
		value any
		err   error
	}{
		{deps.Pipeline.Planner, ErrProductionPlannerMissing},
		{deps.Pipeline.Stager, ErrProductionStagerMissing},
		{deps.Pipeline.Cutter, ErrProductionCutterMissing},
		{deps.Pipeline.Builder, ErrProductionManifestBuilderMissing},
		{deps.Persistence.Writer, ErrProductionWriterMissing},
		{deps.Persistence.Projection, ErrProductionProjectionMissing},
		{deps.Persistence.StepStore, ErrProductionStepStoreMissing},
		{deps.Persistence.ArtifactPreparation, ErrProductionArtifactPreparationMissing},
		{deps.Persistence.JobFinalizer, ErrProductionJobFinalizerMissing},
		{deps.Runtime.SourceProbe, ErrProductionSourceProbeMissing},
		{deps.Persistence.BatchRepository, ErrProductionBatchRepositoryMissing},
		{deps.Runtime.LocalFS, ErrProductionLocalFSMissing},
		{deps.Runtime.Logger, ErrProductionLoggerMissing},
	}
	for _, check := range checks {
		if isNilProductionDependency(check.value) {
			return nil, check.err
		}
	}

	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = core.DefaultMaxConcurrentJobs
	}
	if cfg.MaxConcurrentDownloads <= 0 {
		cfg.MaxConcurrentDownloads = core.DefaultMaxConcurrentDownloads
	}
	if cfg.JobId == "" {
		cfg.JobId = core.DefaultOrchestratorJobId
	}
	cfg.StrictDurationValidation = true
	return core.NewOrchestrator(cfg, core.OrchestratorDeps{
		Execution: core.OrchestratorExecutionDeps{
			Planner:       deps.Pipeline.Planner,
			Stager:        deps.Pipeline.Stager,
			Cutter:        deps.Pipeline.Cutter,
			Builder:       deps.Pipeline.Builder,
			Writer:        deps.Persistence.Writer,
			Projection:    deps.Persistence.Projection,
			StepStore:     deps.Persistence.StepStore,
			DispatchSteps: DefaultStockSteps(),
		},
		Finalization: core.OrchestratorFinalizationDeps{
			ArtifactPreparation:   deps.Persistence.ArtifactPreparation,
			JobFinalizer:          deps.Persistence.JobFinalizer,
			DestinationReconciler: deps.Persistence.DestinationReconciler,
			SourceProbe:           deps.Runtime.SourceProbe,
			BatchRepository:       deps.Persistence.BatchRepository,
			LocalFS:               deps.Runtime.LocalFS,
		},
		ExecutorLog: deps.Runtime.Logger,
	}), nil
}

func isNilProductionDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
