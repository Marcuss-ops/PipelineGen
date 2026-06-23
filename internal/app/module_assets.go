package app

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	assetsdiag "github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/api/assets/register"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/api/assets/soundeffect"
	assetvoice "github.com/Marcuss-ops/PipelineGen/internal/api/assets/voiceover"
	sourcesapi "github.com/Marcuss-ops/PipelineGen/internal/api/sources"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
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

	// NewAssetsModule is the unified thin-transport assets module (PR 3).
	// Contains storage, diagnostics, and search handlers. Coexists with
	// SourcesModule during the migration; SourcesModule still owns
	// voiceover, soundeffect, register-from-youtube, sync-drive-folder,
	// and local-to-drive routes.
	NewAssetsModule module.Module
}

// WireAssets creates the unified Assets handler and module.
//
// PR4d-chunk2 (June 2026): takes *AssetsBundle + 8 narrow direct args
// (VectorStore, JobsBundle, voiceoverSvc, voiceoverSync, realtimeSvc,
// catalogRepo, maintenanceSvc). ClipIndexer is in the bundle now.
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
func WireAssets(cfg *config.Config, log *zap.Logger, bundle *AssetsBundle, vectorStore *qdrant.Service, jobs *JobsBundle, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoversync.Service, realtimeSvc *realtime.Service, catalogRepo *catalog.Repository, maintenanceSvc *maintenance.Service, providerRegistry *providers.Registry) (*AssetsWiring, error) {
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
	handler := sourcesapi.NewSourcesHandler(cfg, jobs.Facade, catalogRepo, bundle.AssetIndexService, bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo, driveCleanupSvc, folderMemSvc, bundle.AssetTreeService, driveUploader, bundle.MediaProcessor, deletionSvc, bundle.CatalogSyncService, maintenanceSvc, providerRegistry, log)
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
	sourcesMod := module.NewRouteModule(
		"assets",
		func() bool { return handler != nil },
		"/media",
		handler,
		log,
	)

	// ── PR 3 (June 2026): storage thin-transport handler ─────
	var drivePort appstorage.DrivePort
	if driveUploader != nil {
		drivePort = &storageDriveAdapter{up: driveUploader}
	}
	storageSvc := appstorage.NewService(drivePort, &zapLogAdapter{log})
	storageHandler := assetstorage.NewHandler(storageSvc, log)

	// Diagnostics and Search: deferred (TODO: wire port adapters)
	diagHandler := assetsdiag.NewHandler(nil, log)
	searchHandler := assetsearch.NewHandler(nil, log)
	log.Warn("diagnostics and search services NOT wired — returning 503 until port adapters are implemented")

	// ── PR 4 (June 2026): extract voiceover, soundeffect, register ─
	// Voiceover: GroupsResolver already constructed in NewSourcesHandler below;
	// we build it here too for the standalone handler.
	var groupsResolver *voiceoverpkg.GroupsResolver
	if bundle.AssetTreeService != nil {
		gr, grErr := voiceoverpkg.NewGroupsResolver(bundle.AssetTreeService, log)
		if grErr != nil {
			log.Warn("voiceover groups_resolver not initialized (PR4)", zap.Error(grErr))
		} else {
			groupsResolver = gr
		}
	}
	defaultVoiceoverRoot := cfg.Drive.VoiceoverRootFolder
	if defaultVoiceoverRoot != "" {
		log.Info("voiceover groups_resolver enabled (PR4)", zap.String("root", defaultVoiceoverRoot))
	}
	voiceoverHandler := assetvoice.NewHandler(voiceoverSvc, voiceoverSync, jobs.Facade, groupsResolver, defaultVoiceoverRoot, log)

	// SoundEffect: wired with real repos + uploader + metaWriter
	metaWriter = semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	sfxHandler := assetsfx.NewHandler(bundle.ClipsRepo, driveUploader, metaWriter, cfg.Drive.SoundEffectsRootFolder, log)

	// Register: YouTube registration handler with full deps
	registerHandler := assetregister.NewHandler(cfg, bundle.ClipsRepo, driveUploader, bundle.AssetTreeService, providerRegistry, bundle.ClipIndexerService, vectorStore, metaWriter, handler.Clips(), log)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageHandler,
		Diagnostics: diagHandler,
		Search:      searchHandler,
		Voiceover:   voiceoverHandler,
		SoundEffect: sfxHandler,
		Register:    registerHandler,
	}, log)
	assetsRouteMod := module.NewRouteModule(
		"assets-v2",
		func() bool { return true },
		"/media",
		assetsMod,
		log,
	)
	log.Info("created unified Assets module (v2 thin transport)")

	return &AssetsWiring{
		Handler:         handler,
		Module:          sourcesMod,
		DeletionSvc:     deletionSvc,
		NewAssetsModule: assetsRouteMod,
	}, nil
}

// storageDriveAdapter adapts drive.Uploader to storage.DrivePort.
type storageDriveAdapter struct {
	up *driveutil.Uploader
}

func (a *storageDriveAdapter) ListFiles(ctx context.Context, folderID string) ([]appstorage.DriveFile, error) {
	files, err := a.up.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]appstorage.DriveFile, len(files))
	for i, f := range files {
		out[i] = appstorage.DriveFile{ID: f.ID, Name: f.Name, MimeType: f.MimeType}
	}
	return out, nil
}

func (a *storageDriveAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.up.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

func (a *storageDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	return a.up.GetOrCreateFolder(ctx, name, parentID)
}

func (a *storageDriveAdapter) RenameFile(ctx context.Context, fileID, newName string) error {
	return a.up.RenameFile(ctx, fileID, newName)
}

// zapLogAdapter adapts *zap.Logger to storage.Logger.
type zapLogAdapter struct{ log *zap.Logger }

func (a *zapLogAdapter) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapLogAdapter) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapLogAdapter) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapLogAdapter) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}
