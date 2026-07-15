// Package app contains the stock pipeline composition root.
package app

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	stockapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/stock"
	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	stockenrich "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/enrichment"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	assetindex "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// StockBundleDeps is the typed input to BuildStockBundle. The next dependency
// cleanup can group these ports without changing the builder's public entrypoint.
type StockBundleDeps struct {
	Cfg       *config.Config
	Log       *zap.Logger
	DB        *sql.DB
	Publisher delivery.Publisher
	Finalizer finalization.JobFinalizer

	SourceStager  acquisition.SourceStager
	ClipsRepo     *sqassets.ClipsRepository
	AssetIndex    *assetindex.Service
	Dispatcher    *outbox.Dispatcher
	Cutter        stockpipeline.VideoCutter
	Renderer      stockpipeline.StockRenderer
	Jobs          *appjobs.Service
	ChannelLister stockpipeline.ChannelLister

	StockPipelineEnabled func() bool
	EnrichmentLLMClient  stockenrich.EnrichmentLLMClient
	EnrichmentEnabled    func() bool
	EnrichmentEmitter    stockenrich.AssetPublishedEmitter
}

func validateStockSymmetricGate(publisher delivery.Publisher, finalizer finalization.JobFinalizer) error {
	if publisher != nil && finalizer == nil {
		return stockpipeline.ErrStockProductionJobFinalizerMissing
	}
	if publisher == nil && finalizer != nil {
		return stockpipeline.ErrStockProductionArtifactPrepMissing
	}
	return nil
}

// BuildStockBundle assembles the stock service, optional enrichment worker and
// API descriptor through the canonical typed constructors.
func BuildStockBundle(deps StockBundleDeps) (*StockPipelineWiring, error) {
	if err := validateStockSymmetricGate(deps.Publisher, deps.Finalizer); err != nil {
		return nil, err
	}

	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Cfg:       deps.Cfg,
		Log:       deps.Log,
		Publisher: deps.Publisher,
		Storage: stockpipeline.StorageDeps{
			ClipsRepo:  deps.ClipsRepo,
			AssetIndex: deps.AssetIndex,
			Dispatcher: deps.Dispatcher,
		},
		Media: stockpipeline.MediaDeps{
			Cutter:   deps.Cutter,
			Renderer: deps.Renderer,
		},
		Jobs:         deps.Jobs,
		Finalizer:    deps.Finalizer,
		SourceStager: deps.SourceStager,
		DB:           deps.DB,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockpipeline.NewService: %w", err)
	}

	useCase := stockpipeline.NewStockUseCase(svc, deps.Jobs, deps.Log)
	if err := wireStockEnrichment(deps); err != nil {
		return nil, err
	}

	descriptor, err := stockapi.Build(stockapi.Dependencies{
		UseCase:     useCase,
		EnabledFunc: deps.StockPipelineEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build: %w", err)
	}
	typed, ok := descriptor.(*stockapi.StockDescriptor)
	if !ok || typed == nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build returned unexpected descriptor type %T (want *stockapi.StockDescriptor)", descriptor)
	}

	return &StockPipelineWiring{
		Module:  typed.Module,
		Service: svc,
	}, nil
}
