// Package app wires the stock pipeline capability at the composition root.
package app

import (
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func WireStockPipeline(cfg *config.Config, log *zap.Logger, root *ComposeRoot) (*StockPipelineWiring, error) {
	if cfg == nil {
		return nil, fmt.Errorf("wire stock pipeline: cfg is nil")
	}
	if log == nil {
		return nil, fmt.Errorf("wire stock pipeline: log is nil")
	}
	if root == nil {
		return nil, fmt.Errorf("wire stock pipeline: root is nil")
	}
	if !cfg.Features.StockPipelineEnabled {
		log.Info("WireStockPipeline: stock pipeline disabled")
		return nil, nil
	}

	stockDB := (*sql.DB)(nil)
	if root.DB != nil {
		stockDB = root.DB.DB
	}
	ffmpegPath := cfg.External.FfmpegPath
	ytdlp := downloader.NewYTDLP(cfg)
	deliveryPorts := StockDeliveryPorts{
		Finalizer: wireStockFinalizer(stockDB, root, log),
	}
	if root.Drive != nil {
		deliveryPorts.Publisher = root.Drive.Publisher
	}

	deps := StockBundleDeps{
		Runtime: StockRuntimeDeps{
			Cfg:     cfg,
			Log:     log,
			Jobs:    root.Jobs.Service,
			Enabled: func() bool { return cfg.Features.StockPipelineEnabled },
		},
		Persistence: StockPersistencePorts{
			DB:           stockDB,
			SourceStager: wireStockSourceStager(cfg, log, ytdlp),
			ClipsRepo:    root.Repos.ClipsRepo,
			AssetIndex:   root.Search.AssetIndexService,
			Dispatcher:   root.Outbox.Dispatcher,
		},
		Media: StockMediaPorts{
			Cutter:   render.NewFFmpegCutter(ffmpegPath, log),
			Renderer: render.NewFFmpegRenderer(ffmpegPath, nil, log),
		},
		Delivery: deliveryPorts,
		Enrichment: StockEnrichmentPorts{
			ChannelLister: ytdlp,
		},
	}

	log.Info("WireStockPipeline: capability groups prepared",
		zap.Bool("publisher_wired", deps.Delivery.Publisher != nil),
		zap.Bool("finalizer_wired", deps.Delivery.Finalizer != nil),
		zap.Bool("source_stager_wired", deps.Persistence.SourceStager != nil),
		zap.String("ffmpeg_path", ffmpegPath),
	)
	return BuildStockBundle(deps)
}
