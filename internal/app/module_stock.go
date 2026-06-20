package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/stockpipeline"
	"go.uber.org/zap"
)

// StockPipelineWiring holds the StockPipeline module wiring.
type StockPipelineWiring struct {
	Handler *sources.StockHandler
	Module  module.Module
	Service *stockpipeline.Service
}

// WireStockPipeline creates the StockPipeline service, handler, and module.
func WireStockPipeline(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps) (*StockPipelineWiring, error) {
	if coreDeps.DriveClient == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}
	svc := stockpipeline.NewService(cfg, log, coreDeps.DriveClient)
	svc.SetJobsSvc(coreDeps.JobsService)
	svc.SetAssetIndex(coreDeps.AssetIndexService)
	if coreDeps.ClipsRepo != nil {
		svc.SetClipsRepo(coreDeps.ClipsRepo)
	}
	if coreDeps.YoutubeClipService != nil {
		svc.SetYoutubeService(coreDeps.YoutubeClipService)
	}
	if coreDeps.ClipIndexerService != nil {
		svc.SetClipIndexer(coreDeps.ClipIndexerService)
	}
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	svc.SetMetadataWriter(metaWriter)
	log.Info("metadata writer wired into stock pipeline")
	handler := sources.NewStockHandler(svc, coreDeps.JobServiceFacade, log)
	mod := sources.NewStockPipelineModule(cfg, log, handler)
	svc.RegisterHandler(coreDeps.JobsService)
	return &StockPipelineWiring{Handler: handler, Module: mod, Service: svc}, nil
}
