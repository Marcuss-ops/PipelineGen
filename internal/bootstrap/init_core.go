package bootstrap

import (
	"context"
	"database/sql"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/clipresolver"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/monitor"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/security"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)


func ExportInitCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	return initCoreMinimal(cfg, log, "")
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger, mode string) (*CoreDeps, CleanupFunc, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 1. Security & Infrastructure - Set download host whitelist from config
	security.SetAllowedHosts(cfg.Security.AllowedDownloadHosts)
	log.Info("Configured download host whitelist", zap.Int("hosts_count", len(cfg.Security.AllowedDownloadHosts)))

	// 2. Databases
	dbs, err := initDatabases(cfg, log)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// 3. Core Services
	svcs, err := initServices(ctx, cfg, dbs, log)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// 5. Background Jobs
	jobs := startBackgroundJobs(ctx, cfg, dbs, svcs, log, mode)

	// 6. Create VoiceoverRepo
	voRepo := voiceovers.NewRepository(dbs.voiceover.DB)

	// 7. Cleanup
	cleanup := buildCleanup(dbs, jobs, cancel, log)

	return &CoreDeps{
		ScriptGen:            svcs.scriptGen,
		DocClient:            svcs.docClient,
		DriveClient:          svcs.driveClient,
		Utility:              svcs.utility,
		DB:                   dbs.main,
		ArtlistDB:            dbs.artlist,
		ImagesDB:             dbs.images,
		AssetsDB:             dbs.assets,
		ScriptsRepo:          svcs.scriptsRepo,
		ImageRepo:            svcs.imageRepo,
		ImageService:         svcs.imageService,
		StockDriveRepo:       svcs.stockDriveRepo,
		ArtlistRepo:          svcs.artlistRepo,
		ClipsOnlyRepo:        svcs.clipsOnlyRepo,
		MonitorsRepo:         svcs.monitorsRepo,
		VoiceoverRepo:        voRepo,
		VoiceoverService:     svcs.voiceoverService,
		VoiceoverSync:        svcs.voiceoverSync,
		IndexingService:      svcs.indexingService,
		// NOTE: HarvesterCronService removed (cron system eliminated)
		CatalogSyncService:   svcs.catalogSync,
		// NOTE: CatalogSyncJob removed (cron system eliminated)
		ChannelMonitor:       jobs.channelMonitor,
		StockScheduler:       jobs.stockScheduler,
		CatalogRepo:          svcs.catalogRepo,
		AssocService:         svcs.assocService,
		JobsService:          svcs.jobsService,
		JobsDB:               dbs.jobs.DB,
		MediaProcessor:       svcs.mediaProcessor,
		YoutubeClipService:   svcs.youtubeClipService,
		AssetIndexService:    svcs.assetIndexService,
		AssetTreeService:     svcs.assetTreeService,
	}, cleanup, nil
}
