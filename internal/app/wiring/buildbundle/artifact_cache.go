package buildbundle

import (
	"database/sql"
	"time"

	assetsmod "github.com/Marcuss-ops/PipelineGen/internal/app/modules/assets"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	platformcache "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/artifactcache"
	"go.uber.org/zap"
)

// InitMediaProcessor wires the media processor over the canonical media
// committer. The cache is fail-soft: an unavailable cache degrades to
// uncached processing rather than failing composition.
func InitMediaProcessor(cfg *config.Config, db *storage.SQLiteDB, cacheDB *storage.SQLiteDB, processing assetspersistence.AssetProcessingWriter, committer assetspersistence.AssetCommitter, log *zap.Logger, publisher delivery.Publisher, mediaConfig mediaexec.ExecutionConfig) detail.MediaProcessor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := rustexec.NewConfiguredVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log)
	clipsRegistry := newClipsRegistry(mediaDetailsReaderFromCommitter(committer), processing, committer, log)
	profile := mediaConfig.Profile
	policy := mediaConfig.Policy
	videoCfg := mediaexec.NormalizeOptions{Profile: profile, Policy: policy, Duration: cfg.Video.CanonicalClip().Duration,
		Width: profile.Width, Height: profile.Height, FPSNum: profile.FPSNum, FPSDen: profile.FPSDen, Codec: policy.Codec, Preset: policy.Preset,
		CRF: policy.CRF, KeyframeInterval: profile.KeyframeInterval}
	proc := processor.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, processor.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: videoCfg, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, publisher)
	if cacheDB != nil && cacheDB.DB != nil {
		if cache, cacheErr := newArtifactCache(cfg, cacheDB.DB, db.DB, log); cacheErr == nil {
			proc.SetArtifactCache(cache)
			log.Info("media processor artifact cache wired")
		} else {
			log.Warn("media processor artifact cache unavailable; media runs uncached", zap.Error(cacheErr))
		}
	}
	return proc
}

func newArtifactCache(cfg *config.Config, cacheDB, contentDB *sql.DB, log *zap.Logger) (*platformcache.Cache, error) {
	return assetsmod.NewArtifactCache(assetsmod.ArtifactCacheDeps{
		Config:    cfg,
		CacheDB:   cacheDB,
		ContentDB: contentDB,
		Log:       log,
	})
}
