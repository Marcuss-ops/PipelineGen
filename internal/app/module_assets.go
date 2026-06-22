package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/\1"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	gdrive "google.golang.org/api/drive/v3"
	"go.uber.org/zap"
)

// AssetsBundle is the capability bundle for the unified Assets module.
//
// PR4d-chunk2 (June 2026): wraps 10 cross-bundle reads of WireAssets.
// ClipIndexerService moved INTO the bundle (was a direct arg in earlier
// draft); RealtimeService moved OUT (single-use, fits clean as a 10th
// direct arg). AssetTreeService + AssetIndexService stay inside since they
// have multiple uses (deletion svc + handler ctor).
//
// Field budget: 10 fields (per AGENTS.md / arch constraint).
type AssetsBundle struct {
	ClipsRepo          *assets.ClipsRepository
	VoiceoverRepo      *assets.VoiceoversRepository
	ImageRepo          *assets.ImagesRepository
	Assets             *asset.Service
	DriveClient        *gdrive.Service
	AssetTreeService   *assettree.Service
	AssetIndexService  *assetindex.Service
	MediaProcessor     asset.Processor
	CatalogSyncService *catalogsync.Service
	ClipIndexerService *clipindexer.Service
}

// AssetsWiring holds the Assets module wiring.
type AssetsWiring struct {
	Handler     *sourcesapi.SourcesHandler
	Module      module.Module
	DeletionSvc *media.DeletionService
}

// WireAssets creates the unified Assets handler and module.
//
// PR4d-chunk2 (June 2026): takes *AssetsBundle + 8 narrow direct args
// (VectorStore, JobsBundle, voiceoverSvc, voiceoverSync, realtimeSvc,
// catalogRepo, maintenanceSvc). ClipIndexer is in the bundle now.
// 10 params total — matches the AGENTS.md / arch bundle cap.
func WireAssets(cfg *config.Config, log *zap.Logger, bundle *AssetsBundle, vectorStore *qdrant.Service, jobs *JobsBundle, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoversync.Service, realtimeSvc *realtime.Service, catalogRepo *catalog.Repository, maintenanceSvc *maintenance.Service) (*AssetsWiring, error) {
	folderMemSvc := foldermemory.NewService(log, bundle.ClipsRepo)
	var driveUploader *driveutil.Uploader
	if bundle.DriveClient != nil {
		driveUploader = &driveutil.Uploader{Service: bundle.DriveClient, Log: log}
	}
	var driveCleanupSvc *drivecleanup.Service
	if driveUploader != nil {
		driveCleanupSvc = drivecleanup.NewService()
	}
	deletionSvc := media.NewDeletionService(bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo, bundle.VoiceoverRepo, bundle.ImageRepo, driveUploader, bundle.AssetTreeService, bundle.AssetIndexService, log)
	handler := sourcesapi.NewSourcesHandler(cfg, voiceoverSvc, voiceoverSync, jobs.Facade, catalogRepo, bundle.AssetIndexService, bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo, driveCleanupSvc, folderMemSvc, bundle.AssetTreeService, driveUploader, bundle.MediaProcessor, deletionSvc, bundle.CatalogSyncService, maintenanceSvc, log)
	if bundle.VoiceoverRepo != nil {
		handler.SetVoiceoverRepo(bundle.VoiceoverRepo)
	}
	if bundle.ImageRepo != nil {
		handler.SetImagesRepo(bundle.ImageRepo)
	}
	if realtimeSvc != nil {
		handler.SetRealtimeService(realtimeSvc)
	}
	if bundle.ClipIndexerService != nil {
		handler.SetClipIndexer(bundle.ClipIndexerService)
	}
	if vectorStore != nil {
		handler.SetVectorStore(vectorStore)
	}
	metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	handler.SetMetaWriter(metaWriter)
	if bundle.Assets != nil {
		handler.SetAssetRepo(bundle.Assets.Repository())
	}
	mod := sourcesapi.NewSourcesModule(cfg, log, handler)
	log.Info("created unified Assets module")
	return &AssetsWiring{Handler: handler, Module: mod, DeletionSvc: deletionSvc}, nil
}
