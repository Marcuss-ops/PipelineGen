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

// WireStockPipeline constructs every stock pipeline dependency from the
// ComposeRoot and routes them through BuildStockBundle. The focused source and
// finalizer helpers own their respective construction details.
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
		log.Info("WireStockPipeline: stock pipeline disabled by cfg flag; returning nil wiring")
		return nil, nil
	}

	ffmpegPath := cfg.External.FfmpegPath
	stockDB := (*sql.DB)(nil)
	if root.DB != nil {
		stockDB = root.DB.DB
	}

	stockCutter := render.NewFFmpegCutter(ffmpegPath, log)
	stockRenderer := render.NewFFmpegRenderer(ffmpegPath, nil, log)
	ytdlp := downloader.NewYTDLP(cfg)
	stockSourceStager := wireStockSourceStager(cfg, log, ytdlp)
	stockFinalizer := wireStockFinalizer(stockDB, root, log)

	log.Info("WireStockPipeline: wiring summary",
		zap.Bool("publisher_wired", root.Drive != nil && root.Drive.Publisher != nil),
		zap.Bool("finalizer_wired", stockFinalizer != nil),
		zap.Bool("source_stager_wired", stockSourceStager != nil),
		zap.String("ffmpeg_path", ffmpegPath),
	)

	return BuildStockBundle(StockBundleDeps{
		Cfg:                  cfg,
		Log:                  log,
		DB:                   stockDB,
		Publisher:            root.Drive.Publisher,
		Finalizer:            stockFinalizer,
		SourceStager:         stockSourceStager,
		ClipsRepo:            root.Repos.ClipsRepo,
		AssetIndex:           root.Search.AssetIndexService,
		Dispatcher:           root.Outbox.Dispatcher,
		Cutter:               stockCutter,
		Renderer:             stockRenderer,
		Jobs:                 root.Jobs.Service,
		ChannelLister:        ytdlp,
		StockPipelineEnabled: func() bool { return cfg.Features.StockPipelineEnabled },
	})
}
