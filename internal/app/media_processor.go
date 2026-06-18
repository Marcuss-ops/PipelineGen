package app

import (
	"database/sql"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assetquery"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/mediaasset"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/downloader"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/ffmpeg"
)

// initMediaProcessor initializes the media processing engine.
func initMediaProcessor(
	cfg *config.Config,
	db *sql.DB,
	assets asset.Repository,
	querySvc *assetquery.Service,
	locations asset.LocationRepository,
	processing asset.ProcessingRepository,
	log *zap.Logger,
	driveUploader *drive.Uploader,
) processor.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.New(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db, assets, querySvc, locations, processing)

	return mediaasset.NewProcessor(
		ytDLPDownloader,
		httpDL,
		ffmpegProc,
		log,
		mediaasset.ProcessorConfig{
			DataDir:            cfg.Storage.DataDir,
			TempDir:            cfg.Storage.TempDir,
			VideoCfg:           ffmpeg.DefaultNormalizeOptions(cfg),
			ScraperServerURL:   cfg.External.ArtlistScraperServerURL,
			EmbeddingServerURL: cfg.ClipIndexer.ServerURL,
		},
		clipsRegistry,
		driveUploader,
	)
}
