package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	artlistpkg "github.com/Marcuss-ops/PipelineGen/internal/sources/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"go.uber.org/zap"
)

// AssetsWiring holds the Assets module wiring.
type AssetsWiring struct {
	Handler     *sourcesapi.SourcesHandler
	Module      module.Module
	DeletionSvc *media.DeletionService
}

// WireAssets creates the unified Assets handler and module.
func WireAssets(cfg *config.Config, log *zap.Logger, coreDeps *CoreDeps, artlistSvc *artlistpkg.Service, youtubeSvc *youtube.Service, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoversync.Service, jobsSvc *jobservice.Service, catalogRepo *catalog.Repository, assetIndexSvc *assetindex.Service, maintenanceSvc *maintenance.Service) (*AssetsWiring, error) {
	folderMemSvc := foldermemory.NewService(log, coreDeps.ClipsRepo)
	var driveUploader *driveutil.Uploader
	if coreDeps.DriveClient != nil {
		driveUploader = &driveutil.Uploader{Service: coreDeps.DriveClient, Log: log}
	}
	var driveCleanupSvc *drivecleanup.Service
	if driveUploader != nil {
		driveCleanupSvc = drivecleanup.NewService()
	}
	deletionSvc := media.NewDeletionService(coreDeps.ClipsRepo, coreDeps.ClipsRepo, coreDeps.ClipsRepo, coreDeps.VoiceoverRepo, coreDeps.ImageRepo, driveUploader, coreDeps.AssetTreeService, coreDeps.AssetIndexService, log)
	handler := sourcesapi.NewSourcesHandler(cfg, artlistSvc, youtubeSvc, voiceoverSvc, voiceoverSync, jobsSvc, catalogRepo, assetIndexSvc, coreDeps.ClipsRepo, coreDeps.ClipsRepo, coreDeps.ClipsRepo, driveCleanupSvc, folderMemSvc, coreDeps.AssetTreeService, driveUploader, coreDeps.MediaProcessor, deletionSvc, coreDeps.CatalogSyncService, maintenanceSvc, log)
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
	metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	handler.SetMetaWriter(metaWriter)
	if coreDeps.ArtifactService != nil {
		handler.SetArtifactService(coreDeps.ArtifactService)
	}
	if coreDeps.Assets != nil {
		handler.SetAssetRepo(coreDeps.Assets.Repository())
	}
	mod := sourcesapi.NewSourcesModule(cfg, log, handler)
	log.Info("created unified Assets module")
	return &AssetsWiring{Handler: handler, Module: mod, DeletionSvc: deletionSvc}, nil
}
