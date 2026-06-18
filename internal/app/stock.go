package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/module"

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
	if coreDeps.StockDriveRepo != nil {
		svc.SetClipsRepo(coreDeps.StockDriveRepo)
	}
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

	mod := module.NewStockPipelineModule(cfg, log, handler)

	svc.RegisterHandler(coreDeps.JobsService)

	return &StockPipelineWiring{
		Handler: handler,
		Module:  mod,
		Service: svc,
	}, nil
}
