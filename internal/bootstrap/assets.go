package bootstrap

import (
	"go.uber.org/zap"

	"velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/foldermemory"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/media"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/module"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/pkg/config"
)

// AssetsWiring holds the Assets module wiring
type AssetsWiring struct {
	Handler     *sources.Handler
	Module      module.Module
}

// WireAssets creates the unified Assets handler and module
func WireAssets(
	cfg *config.Config,
	log *zap.Logger,
	coreDeps *CoreDeps,
	artlistSvc *artlist.Service,
	youtubeSvc *youtubeclip.Service,
	voiceoverSvc *voiceover.Service,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
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
	if coreDeps.DriveClient != nil {
		driveCleanupSvc = drivecleanup.NewService(coreDeps.ArtlistRepo, coreDeps.DriveClient, log, true)
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
		log,
	)

	handler := sources.NewHandler(
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
	}, nil
}
