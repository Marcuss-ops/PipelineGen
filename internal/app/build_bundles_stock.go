// Package app contains the stock pipeline composition root.
package app

import (
	"database/sql"
	"fmt"

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
	"go.uber.org/zap"
)

// StockRuntimeDeps owns configuration, jobs and capability activation.
type StockRuntimeDeps struct {
	Cfg     *config.Config
	Log     *zap.Logger
	Jobs    *appjobs.Service
	Enabled func() bool
}

// StockPersistencePorts owns source acquisition and canonical state changes.
type StockPersistencePorts struct {
	DB           *sql.DB
	SourceStager acquisition.SourceStager
	ClipsRepo    *sqassets.ClipsRepository
	AssetIndex   *assetindex.Service
	Dispatcher   *outbox.Dispatcher
}

// StockMediaPorts owns CPU media transformation.
type StockMediaPorts struct {
	Cutter   stockpipeline.VideoCutter
	Renderer stockpipeline.StockRenderer
}

// StockDeliveryPorts is a paired publish/finalize contract.
type StockDeliveryPorts struct {
	Publisher delivery.Publisher
	Finalizer finalization.JobFinalizer
}

// StockEnrichmentPorts owns optional query and LLM enrichment behavior.
type StockEnrichmentPorts struct {
	ChannelLister stockpipeline.ChannelLister
	LLMClient     stockenrich.EnrichmentLLMClient
	Enabled       func() bool
	Emitter       stockenrich.AssetPublishedEmitter
}

// StockBundleDeps exposes five real capability boundaries rather than a flat
// bag spanning configuration, persistence, media, delivery and enrichment.
type StockBundleDeps struct {
	Runtime     StockRuntimeDeps
	Persistence StockPersistencePorts
	Media       StockMediaPorts
	Delivery    StockDeliveryPorts
	Enrichment  StockEnrichmentPorts
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

// BuildStockBundle assembles immutable stock services, optional enrichment and
// the API descriptor through their canonical constructors.
func BuildStockBundle(deps StockBundleDeps) (*StockPipelineWiring, error) {
	if err := validateStockSymmetricGate(deps.Delivery.Publisher, deps.Delivery.Finalizer); err != nil {
		return nil, err
	}

	svc, err := stockpipeline.NewService(stockpipeline.Deps{
		Cfg:       deps.Runtime.Cfg,
		Log:       deps.Runtime.Log,
		Publisher: deps.Delivery.Publisher,
		Storage: stockpipeline.StorageDeps{
			ClipsRepo:  deps.Persistence.ClipsRepo,
			AssetIndex: deps.Persistence.AssetIndex,
			Dispatcher: deps.Persistence.Dispatcher,
		},
		Media: stockpipeline.MediaDeps{
			Cutter:   deps.Media.Cutter,
			Renderer: deps.Media.Renderer,
		},
		Jobs:          deps.Runtime.Jobs,
		Finalizer:     deps.Delivery.Finalizer,
		SourceStager:  deps.Persistence.SourceStager,
		DB:            deps.Persistence.DB,
		ChannelLister: deps.Enrichment.ChannelLister,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockpipeline.NewService: %w", err)
	}

	useCase := stockpipeline.NewStockUseCase(svc, deps.Runtime.Jobs, deps.Runtime.Log)
	if err := wireStockEnrichment(deps); err != nil {
		return nil, err
	}

	descriptor, err := stockapi.Build(stockapi.Dependencies{
		UseCase:     useCase,
		EnabledFunc: deps.Runtime.Enabled,
	})
	if err != nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build: %w", err)
	}
	typed, ok := descriptor.(*stockapi.StockDescriptor)
	if !ok || typed == nil {
		return nil, fmt.Errorf("stock.BuildStockBundle: stockapi.Build returned unexpected descriptor type %T", descriptor)
	}
	return &StockPipelineWiring{Module: typed.Module, Service: svc}, nil
}
