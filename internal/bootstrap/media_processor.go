package bootstrap

import (
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/service/assetregistry"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

// initMediaProcessor initializes the media processing engine.
func initMediaProcessor(cfg *config.Config, clipsOnlyRepo *clips.Repository, log *zap.Logger) processor.Processor {
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
			DataDir:  cfg.Storage.DataDir,
			TempDir:  cfg.Storage.TempDir,
			VideoCfg: ffmpeg.DefaultNormalizeOptions(cfg),
		},
		clipsRegistry,
	)
	return mediaasset.ToCoreProcessor(mediaProcessorInternal)
}
