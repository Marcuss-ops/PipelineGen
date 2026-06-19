package app

import (
	"database/sql"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/mediaasset"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/platform/ffmpeg"
)

// initMediaProcessor initializes the media processing engine.
func initMediaProcessor(
	cfg *config.Config,
	db *sql.DB,
	assetsRepo assets.Repository,
	querySvc *assets.Service,
	locations assets.LocationRepository,
	processing assets.ProcessingRepository,
	log *zap.Logger,
	driveUploader *drive.Uploader,
) processor.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.New(cfg)
	clipsRegistry := artifacts.NewClipsRegistry(db, assetsRepo, querySvc, locations, processing)

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
