package app

import (
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/media/mediaasset"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/downloader"
	"github.com/Marcuss-ops/PipelineGen/pkg/media/ffmpeg"
)

// initMediaProcessor initializes the media processing engine.
func initMediaProcessor(cfg *config.Config, clipsOnlyRepo *clips.Repository, log *zap.Logger, driveUploader *drive.Uploader) processor.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := ffmpeg.New(cfg)
	clipsRegistry := assetregistry.NewClipsRegistry(clipsOnlyRepo)

	mediaProcessorInternal := mediaasset.NewProcessor(
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
	return mediaasset.ToCoreProcessor(mediaProcessorInternal)
}
