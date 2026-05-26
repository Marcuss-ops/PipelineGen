package app

import (
	"go.uber.org/zap"

	"fmt"
	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/config"
	"velox/go-master/internal/core/maintenance"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/foldermemory"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/module"
	assettreerepo "velox/go-master/internal/repository/assettree"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/sources/artlist"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/storage/drivecleanup"
	"velox/go-master/internal/upload/drive"
)

func initAssetServices(dbs *databases, log *zap.Logger) (*assetindex.Service, *assettree.Service, error) {
	// Asset index service
	assetIndexRepo := assetindex.NewRepository(dbs.assets.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	log.Info("asset index service initialized", zap.String("db", "assets.db.sqlite"))

	// Asset tree service
	assetTreeRepo, err := assettreerepo.NewRepository(dbs.assets.DB, log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	log.Info("asset tree service initialized")

	return assetIndexService, assetTreeService, nil
}

// AssetsWiring holds the Assets module wiring
type AssetsWiring struct {
	Handler     *sources.Handler
	Module      module.Module
	DeletionSvc *media.DeletionService
}

// WireAssets creates the unified Assets handler and module
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
	artlistSvc *artlist.Service,
	youtubeSvc *youtube.Service,
	voiceoverSvc *voiceover.Service,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	maintenanceSvc *maintenance.Service,
) (*AssetsWiring, error) {
	// Create folder memory service
	folderMemSvc := foldermemory.NewService(log, coreDeps.ArtlistRepo)

	// Create drive uploader
	var driveUploader *drive.Uploader
	if coreDeps.DriveClient != nil {
		driveUploader = &drive.Uploader{Service: coreDeps.DriveClient, Log: log}
	}

	// Create drive cleanup service
	var driveCleanupSvc *drivecleanup.Service
	if driveUploader != nil {
		driveCleanupSvc = drivecleanup.NewService(coreDeps.ArtlistRepo, driveUploader, log, true)
	}

	// Create deletion service
	deletionSvc := media.NewDeletionService(
		coreDeps.ArtlistRepo,
		coreDeps.ClipsOnlyRepo,
		coreDeps.StockDriveRepo,
		coreDeps.VoiceoverRepo,
		coreDeps.ImageRepo,
		driveUploader,
		coreDeps.AssetTreeService,
		coreDeps.AssetIndexService,
		log,
	)

	handler := sources.NewHandler(
		cfg,
		artlistSvc,
		youtubeSvc,
		voiceoverSvc,
		jobsSvc,
		catalogRepo,
		assetIndexSvc,
		coreDeps.ArtlistRepo,
		coreDeps.ClipsOnlyRepo,
		coreDeps.StockDriveRepo,
		driveCleanupSvc,
		folderMemSvc,
		coreDeps.AssetTreeService,
		driveUploader,
		coreDeps.MediaProcessor,
		deletionSvc,
		coreDeps.CatalogSyncService,
		maintenanceSvc,
		log,
	)

	// Add voiceover and image repos
	if coreDeps.VoiceoverRepo != nil {
		handler.SetVoiceoverRepo(coreDeps.VoiceoverRepo)
	}
	if coreDeps.ImageRepo != nil {
		handler.SetImagesRepo(coreDeps.ImageRepo)
	}

	mod := module.NewAssetsModule(cfg, log, handler)
	log.Info("created unified Assets module")

	return &AssetsWiring{
		Handler:     handler,
		Module:      mod,
		DeletionSvc: deletionSvc,
	}, nil
}
