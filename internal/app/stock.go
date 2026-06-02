package app

import (
	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/stockpipeline"
	"velox/go-master/internal/module"

	"go.uber.org/zap"
)

type StockPipelineWiring struct {
	Handler *sources.StockHandler
	Module  module.Module
	Service *stockpipeline.Service
}

func WireStockPipeline(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
) (*StockPipelineWiring, error) {
	if coreDeps.DriveClient == nil {
		log.Warn("stock pipeline not wired: missing drive client")
		return nil, nil
	}

	svc := stockpipeline.NewService(cfg, log, coreDeps.DriveClient)
	svc.SetJobsSvc(coreDeps.JobsService)
	svc.SetAssetIndex(coreDeps.AssetIndexService)
	if coreDeps.YoutubeClipService != nil {
		svc.SetYoutubeService(coreDeps.YoutubeClipService)
	}
	if coreDeps.ClipIndexerService != nil {
		svc.SetClipIndexer(coreDeps.ClipIndexerService)
	}

	// Wire unified metadata writer for semantic enrichment of stock chunks
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	svc.SetMetadataWriter(metaWriter)
	log.Info("metadata writer wired into stock pipeline")

	handler := sources.NewStockHandler(svc, coreDeps.JobsService, log)
	if coreDeps.YoutubeClipService != nil {
		handler.SetYoutubeService(coreDeps.YoutubeClipService)
	}

	mod := module.NewStockPipelineModule(cfg, log, handler)

	svc.RegisterHandler(coreDeps.JobsService)

	return &StockPipelineWiring{
		Handler: handler,
		Module:  mod,
		Service: svc,
	}, nil
}
