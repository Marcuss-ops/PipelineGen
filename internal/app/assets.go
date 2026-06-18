package app

import (
	"go.uber.org/zap"

	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/config"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/module"
	assettreerepo "github.com/Marcuss-ops/PipelineGen/internal/repository/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/storage/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

func initAssetServices(dbs *databases, log *zap.Logger) (*assetindex.Service, *assettree.Service, error) {
	// Asset index service
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	log.Info("asset index service initialized", zap.String("db", "assets.db.sqlite"))

	// Asset tree service
	assetTreeRepo, err := assettreerepo.NewRepository(dbs.main.DB, log)
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
	voiceoverSync *voiceoversync.Service,
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
		voiceoverSync,
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
	if coreDeps.RealtimeService != nil {
		handler.SetRealtimeService(coreDeps.RealtimeService)
	}
	if coreDeps.ClipIndexerService != nil {
		handler.SetClipIndexer(coreDeps.ClipIndexerService)
	}
	if coreDeps.VectorStore != nil {
		handler.SetVectorStore(coreDeps.VectorStore)
	}
	// Create a minimal metaWriter for semantic enrichment on clip creation
	metaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)
	handler.SetMetaWriter(metaWriter)
	if coreDeps.ArtifactService != nil {
		handler.SetArtifactService(coreDeps.ArtifactService)
	}
	if coreDeps.AssetRepo != nil {
		handler.SetAssetRepo(coreDeps.AssetRepo)
	}
	log.Info("created unified Assets module")

	return &AssetsWiring{
		Handler:     handler,
		Module:      mod,
		DeletionSvc: deletionSvc,
	}, nil
}
