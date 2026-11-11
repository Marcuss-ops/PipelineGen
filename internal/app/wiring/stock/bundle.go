package stock

import (
	"fmt"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	stockapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/stock"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

// BatchModuleBuilder is the one composition hook retained by the wiring root:
// the batch coordinator depends on application-level composition helpers, while
// this module owns stock service and API assembly.
type BatchModuleBuilder func(StockBundleDeps, *stockpipeline.Service) (api.Module, error)

// BuildBundle assembles the stock capability from its typed subsystem deps.
// BatchModuleBuilder is supplied by the application root to avoid a reverse
// dependency from this subsystem into the root composition package.
func BuildBundle(deps StockBundleDeps, buildBatchModule BatchModuleBuilder) (*StockPipelineWiring, error) {
	if err := ValidateStockSymmetricGate(deps.Delivery.Publisher, deps.Delivery.Finalizer); err != nil {
		return nil, err
	}
	isProduction := deps.Delivery.Publisher != nil || deps.Delivery.Finalizer != nil
	if isProduction && deps.Runtime.DB == nil {
		return nil, stockpipeline.ErrStockProductionDBMissing
	}
	if isProduction && deps.Acquisition.BatchRepository == nil {
		return nil, stockpipeline.ErrStockProductionBatchRepositoryMissing
	}

	svc, err := stockpipeline.NewProductionStockPipeline(stockpipeline.Deps{
		Runtime: stockpipeline.RuntimeDeps{
			Cfg: RuntimeConfig(deps.Runtime.Cfg), Log: deps.Runtime.Log,
			JobCreator: deps.Runtime.JobCreator, StepStore: deps.Runtime.StepStore,
		},
		Storage: stockpipeline.StorageDeps{
			Dispatcher: deps.Acquisition.Dispatcher, BatchRepository: deps.Acquisition.BatchRepository,
		},
		Media: stockpipeline.MediaDeps{Cutter: deps.Media.Cutter},
		Execution: stockpipeline.ExecutionDeps{
			Jobs: deps.Orchestration.Jobs, SourceStager: deps.Acquisition.SourceStager,
			SourceProbe: deps.Acquisition.SourceProbe, ChannelLister: deps.Orchestration.ChannelLister,
		},
		SourceCache: stockpipeline.SourceCacheDeps{
			Reader: deps.SourceCache.Reader, Writer: deps.SourceCache.Writer, LocalFS: deps.SourceCache.LocalFS,
		},
		Delivery: stockpipeline.DeliveryDeps{
			Publisher: deps.Delivery.Publisher, PublisherPort: deps.Delivery.PublisherPort,
			FolderCreator: deps.Orchestration.FolderCreator, DriveReader: ChooseDriveReader(deps.Acquisition),
			Finalizer: deps.Delivery.Finalizer, Projection: deps.Delivery.Projection,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockpipeline.NewProductionStockPipeline: %w", err)
	}

	useCase := stockpipeline.NewStockUseCase(svc, deps.Orchestration.Jobs, deps.Runtime.Log)
	if err := WireEnrichment(deps); err != nil {
		return nil, err
	}
	sd, err := stockapi.Build(stockapi.Dependencies{
		UseCase: useCase, EnabledFunc: deps.Feature.StockPipelineEnabled, Logger: deps.Runtime.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build: %w", err)
	}
	typed, ok := sd.(*stockapi.StockDescriptor)
	if !ok || typed == nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build returned unexpected descriptor type %T (want *stockapi.StockDescriptor)", sd)
	}
	if buildBatchModule == nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: batch module builder is required")
	}
	batchModule, err := buildBatchModule(deps, svc)
	if err != nil {
		return nil, err
	}
	return &StockPipelineWiring{Module: typed.Module, BatchModule: batchModule, Service: svc}, nil
}

// RuntimeConfig maps operator config into stock capability defaults.
func RuntimeConfig(cfg *config.Config) *stockpipeline.RuntimeConfig {
	if cfg == nil {
		return nil
	}
	v := cfg.Video.WithDefaults()
	return &stockpipeline.RuntimeConfig{
		WorkDir: cfg.Storage.TempPath(), ClipDurationSec: v.ClipDuration,
		ChunkDurationSec: v.ChunkDuration, MaxResults: v.MaxClipsPerSource,
		PolicyVersion: "v1", MaxConcurrentDownloads: cfg.Concurrency.MaxConcurrentStockDownloads,
		MaxConcurrentCuts: cfg.Concurrency.MaxConcurrentStockCuts, SkipMetadataUpload: true,
	}
}
