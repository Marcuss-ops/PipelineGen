package support

import (
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"errors"
	"reflect"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// ProductionPipelineDeps groups the execution ports for the live stock run.
type ProductionPipelineDeps struct {
	Planner  stockpipeline.ClipPlanner
	Stager   acquisition.SourceStager
	Cutter   stockpipeline.VideoCutter
	Renderer stockpipeline.StockRenderer
	Builder  stockpipeline.ManifestBuilder
}

// ProductionPersistenceDeps groups durable state and finalization ports.
type ProductionPersistenceDeps struct {
	Writer              stockpipeline.TransactionalAssetWriter
	Projection          stockpipeline.ProjectionPort
	StepStore           steps.Store
	ArtifactPreparation finalization.ArtifactPreparationService
	JobFinalizer        finalization.JobFinalizer
	BatchRepository     stockpipeline.StockBatchRepository
}

// ProductionRuntimeDeps groups probing, filesystem, and observability ports.
type ProductionRuntimeDeps struct {
	SourceProbe stockpipeline.SourceDurationProbe
	LocalFS     stockpipeline.LocalFSPort
	Logger      *zap.Logger
}

// ProductionStockPipelineDeps is the complete dependency graph required by
// the live stock pipeline. Unlike stockpipeline.NewTestStockOrchestrator, this bundle has no
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
	ErrProductionRendererMissing            = errors.New("stock production orchestrator: renderer is required")
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
// for a live capability. stockpipeline.NewTestStockOrchestrator is never called by this
// constructor.
func NewProductionStockOrchestrator(cfg stockpipeline.OrchestratorConfig, deps ProductionStockPipelineDeps) (*stockpipeline.Orchestrator, error) {
	checks := []struct {
		value any
		err   error
	}{
		{deps.Pipeline.Planner, ErrProductionPlannerMissing},
		{deps.Pipeline.Stager, ErrProductionStagerMissing},
		{deps.Pipeline.Cutter, ErrProductionCutterMissing},
		{deps.Pipeline.Renderer, ErrProductionRendererMissing},
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
		cfg.MaxConcurrentJobs = stockpipeline.DefaultMaxConcurrentJobs
	}
	if cfg.JobId == "" {
		cfg.JobId = stockpipeline.DefaultOrchestratorJobId
	}
	cfg.StrictDurationValidation = true
	return &stockpipeline.Orchestrator{
		cfg:                 cfg,
		planner:             deps.Pipeline.Planner,
		stager:              deps.Pipeline.Stager,
		cutter:              deps.Pipeline.Cutter,
		renderer:            deps.Pipeline.Renderer,
		builder:             deps.Pipeline.Builder,
		writer:              deps.Persistence.Writer,
		projection:          deps.Persistence.Projection,
		stepStore:           deps.Persistence.StepStore,
		dispatchSteps:       stockpipeline.DefaultStockSteps(),
		artifactPreparation: deps.Persistence.ArtifactPreparation,
		jobFinalizer:        deps.Persistence.JobFinalizer,
		sourceProbe:         deps.Runtime.SourceProbe,
		batchRepository:     deps.Persistence.BatchRepository,
		localFS:             deps.Runtime.LocalFS,
		executorLog:         deps.Runtime.Logger,
	}, nil
}

func isNilProductionDependency(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
