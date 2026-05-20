package app

import (
	"context"
	"fmt"

	"velox/go-master/internal/config"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/security"

	"go.uber.org/zap"
)

func ExportInitCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	return initCoreMinimal(cfg, log, "")
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger, mode string) (*CoreDeps, CleanupFunc, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 1. Security & Infrastructure - Set download host whitelist from config
	hosts := append(cfg.Security.AllowedDownloadHosts, "youtube.com", "youtu.be", "www.youtube.com")
	security.SetAllowedHosts(hosts)
	log.Info("Configured download host whitelist", zap.Int("hosts_count", len(hosts)))

	// 2. Databases
	dbs, err := initDatabases(cfg, log)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// Run all database migrations centrally
	if err := runAllMigrations(dbs, log); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("failed to run database migrations: %w", err)
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
	voRepo := voiceovers.NewRepository(dbs.media.DB)

	// 7. Cleanup
	cleanup := buildCleanup(dbs, jobs, cancel, log)

	return &CoreDeps{
		ScriptGen:          svcs.scriptGen,
		DocClient:          svcs.docClient,
		DriveClient:        svcs.driveClient,
		Utility:            svcs.utility,
		DB:                 dbs.main,
		MediaDB:            dbs.media,
		AssetsDB:           dbs.assets,
		ScriptsRepo:        svcs.scriptsRepo,
		ImageRepo:          svcs.imageRepo,
		ImageService:       svcs.imageService,
		StockDriveRepo:     svcs.stockDriveRepo,
		ArtlistRepo:        svcs.artlistRepo,
		ClipsOnlyRepo:      svcs.clipsOnlyRepo,
		MonitorsRepo:       svcs.monitorsRepo,
		VoiceoverRepo:      voRepo,
		VoiceoverService:   svcs.voiceoverService,
		VoiceoverSync:      svcs.voiceoverSync,
		IndexingService:    svcs.indexingService,
		ClipIndexerService: svcs.clipIndexerService,
		// NOTE: HarvesterCronService removed (cron system eliminated)
		CatalogSyncService: svcs.catalogSync,
		// NOTE: CatalogSyncJob removed (cron system eliminated)
		ChannelMonitor:     jobs.channelMonitor,
		StockScheduler:     jobs.stockScheduler,
		CatalogRepo:        svcs.catalogRepo,
		AssocService:       svcs.assocService,
		JobsService:        svcs.jobsService,
		JobsDB:             dbs.jobs.DB,
		MediaProcessor:     svcs.mediaProcessor,
		YoutubeClipService: svcs.youtubeClipService,
		AssetIndexService:  svcs.assetIndexService,
		AssetTreeService:   svcs.assetTreeService,
		MaintenanceService: svcs.maintenanceSvc,
	}, cleanup, nil
}
