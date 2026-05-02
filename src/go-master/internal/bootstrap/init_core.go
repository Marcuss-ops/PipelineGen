package bootstrap

import (
	"context"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/cron"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	jobservice "velox/go-master/internal/service/jobs"

	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/monitor"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/security"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

// CoreDeps holds the minimal runtime dependencies needed by the stripped-down server.
type CoreDeps struct {
	ScriptGen            *ollama.Generator
	DocClient            drive.DocClient
	DriveClient          *gdrive.Service
	Utility              *common.UtilityHandler
	DB                   *storage.SQLiteDB // Unified database
	ImagesDB             *storage.SQLiteDB // Images database
	ScriptsRepo          *scripts.ScriptRepository
	ImageRepo            *images.Repository
	ImageService         *imgservice.Service
	StockDriveRepo       *clips.Repository
	ArtlistRepo          *clips.Repository
	ClipsOnlyRepo        *clips.Repository
	VoiceoverService     *voiceover.Service
	IndexingService      *indexing.Service
	HarvesterCronService *cron.HarvesterCronService
	CatalogSyncService   *catalogsync.Service
	CatalogSyncJob       *cron.CatalogSyncJob
	ChannelMonitor       *monitor.ChannelMonitor
	StockScheduler       *scheduler.StockScheduler
	CatalogRepo          *catalog.Repository
	AssocService         *association.Service
	JobsService          *jobservice.Service
}

func ExportInitCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	return initCoreMinimal(cfg, log)
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 1. Security & Infrastructure
	for _, host := range cfg.Security.AllowedDownloadHosts {
		security.AddAllowedHost(host)
		log.Debug("Added allowed download host from config", zap.String("host", host))
	}

	// 2. Databases
	dbs, err := initDatabases(cfg, log)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// 3. Migrations
	if err := runAllMigrations(dbs, log); err != nil {
		cancel()
		return nil, nil, err
	}

	// 4. Services
	svcs, err := initServices(ctx, cfg, dbs, log)
	if err != nil {
		cancel()
		return nil, nil, err
	}

	// 5. Background Jobs
	jobs := startBackgroundJobs(ctx, cfg, dbs, svcs, log)

	// 6. Cleanup
	cleanup := buildCleanup(dbs, jobs, cancel, log)

	return &CoreDeps{
		ScriptGen:            svcs.scriptGen,
		DocClient:            svcs.docClient,
		DriveClient:          svcs.driveClient,
		Utility:              svcs.utility,
		DB:                   dbs.main,
		ImagesDB:             dbs.images,
		ScriptsRepo:          svcs.scriptsRepo,
		ImageRepo:            svcs.imageRepo,
		ImageService:         svcs.imageService,
		StockDriveRepo:       svcs.stockDriveRepo,
		ArtlistRepo:          svcs.artlistRepo,
		ClipsOnlyRepo:        svcs.clipsOnlyRepo,
		VoiceoverService:     svcs.voiceoverService,
		IndexingService:      svcs.indexingService,
		HarvesterCronService: jobs.harvesterCronSvc,
		CatalogSyncService:   svcs.catalogSync,
		CatalogSyncJob:       jobs.catalogSyncJob,
		ChannelMonitor:       jobs.channelMonitor,
		StockScheduler:       jobs.stockScheduler,
		CatalogRepo:          svcs.catalogRepo,
		AssocService:         svcs.assocService,
		JobsService:          svcs.jobsService,
	}, cleanup, nil
}
