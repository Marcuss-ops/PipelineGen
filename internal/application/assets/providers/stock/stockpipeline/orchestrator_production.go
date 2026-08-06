package stockpipeline

import (
	"errors"
	"reflect"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
)

// ProductionStockPipelineDeps is the complete dependency graph required by
// the live stock pipeline. Unlike NewTestStockOrchestrator, this bundle has no
// implicit defaults: every port that can affect a production verdict is
// supplied by the composition root.
type ProductionStockPipelineDeps struct {
	Planner             ClipPlanner
	Stager              assets.SourceStager
	Cutter              VideoCutter
	Renderer            StockRenderer
	Builder             ManifestBuilder
	Writer              TransactionalAssetWriter
	Projection          ProjectionPort
	StepStore           steps.Store
	ArtifactPreparation finalization.ArtifactPreparationService
	JobFinalizer        finalization.JobFinalizer
	SourceProbe         SourceDurationProbe
	BatchRepository     StockBatchRepository
	LocalFS             LocalFSPort
	Logger              *zap.Logger
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
// for a live capability. NewTestStockOrchestrator is never called by this
// constructor.
func NewProductionStockOrchestrator(cfg OrchestratorConfig, deps ProductionStockPipelineDeps) (*Orchestrator, error) {
	checks := []struct {
		value any
		err   error
	}{
		{deps.Planner, ErrProductionPlannerMissing},
		{deps.Stager, ErrProductionStagerMissing},
		{deps.Cutter, ErrProductionCutterMissing},
		{deps.Renderer, ErrProductionRendererMissing},
		{deps.Builder, ErrProductionManifestBuilderMissing},
		{deps.Writer, ErrProductionWriterMissing},
		{deps.Projection, ErrProductionProjectionMissing},
		{deps.StepStore, ErrProductionStepStoreMissing},
		{deps.ArtifactPreparation, ErrProductionArtifactPreparationMissing},
		{deps.JobFinalizer, ErrProductionJobFinalizerMissing},
		{deps.SourceProbe, ErrProductionSourceProbeMissing},
		{deps.BatchRepository, ErrProductionBatchRepositoryMissing},
		{deps.LocalFS, ErrProductionLocalFSMissing},
		{deps.Logger, ErrProductionLoggerMissing},
	}
	for _, check := range checks {
		if isNilProductionDependency(check.value) {
			return nil, check.err
		}
	}

	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	if cfg.JobId == "" {
		cfg.JobId = DefaultOrchestratorJobId
	}
	cfg.StrictDurationValidation = true
	return &Orchestrator{
		cfg:                 cfg,
		planner:             deps.Planner,
		stager:              deps.Stager,
		cutter:              deps.Cutter,
		renderer:            deps.Renderer,
		builder:             deps.Builder,
		writer:              deps.Writer,
		projection:          deps.Projection,
		stepStore:           deps.StepStore,
		dispatchSteps:       DefaultStockSteps(),
		artifactPreparation: deps.ArtifactPreparation,
		jobFinalizer:        deps.JobFinalizer,
		sourceProbe:         deps.SourceProbe,
		batchRepository:     deps.BatchRepository,
		localFS:             deps.LocalFS,
		executorLog:         deps.Logger,
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
