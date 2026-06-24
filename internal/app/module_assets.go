package app

import (
	"context"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	assetsdiag "github.com/Marcuss-ops/PipelineGen/internal/api/assets/diagnostics"
	assetregister "github.com/Marcuss-ops/PipelineGen/internal/api/assets/register"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
	assetsfx "github.com/Marcuss-ops/PipelineGen/internal/api/assets/soundeffect"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	assetvoice "github.com/Marcuss-ops/PipelineGen/internal/api/assets/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	assetclipssearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/clipssearch"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	appdiag "github.com/Marcuss-ops/PipelineGen/internal/application/assets/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	appsearchsvc "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	appstorage "github.com/Marcuss-ops/PipelineGen/internal/application/assets/storage"
	voiceoverpkg "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	infraassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"
)

// processRunnerAdapter is a package-level adapter for the infrastructure ProcessRunner port.
// Used by ScraperHandler and other handlers in registry.go that need subprocess execution.
var processRunnerAdapter = infraassets.NewProcessRunnerAdapter()

// toolCheckerAdapter is a package-level adapter for the infrastructure ToolChecker port.
// Used by YouTubeClipHandler and system handler to check external tool availability.
var toolCheckerAdapter = infraassets.NewToolCheckerAdapter()

// dbHealthCheckerAdapter is a package-level adapter for the infrastructure DBHealthChecker port.
// Used by system handler to check database health.
var dbHealthCheckerAdapter = infraassets.NewDBHealthCheckerAdapter(nil)

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
	Module      module.Module
	DeletionSvc *deletion.DeletionService
}

// WireAssets creates the unified Assets handler and module.
//
// PR4d-chunk2 (June 2026): takes *AssetsBundle + 8 narrow direct args
// (VectorStore, JobsBundle, voiceoverSvc, voiceoverSync, realtimeSvc,
// catalogRepo, maintenanceSvc). ClipIndexer is in the bundle now.
// PR3 (June 2026): providerRegistry added for constructor injection
// (replaces post-construction SetProviderRegistry).
// AGENT-1 cascade fix (June 2026, cmd/admin recovery collateral): the
// `realtime` package was removed in commit d61068b3. The WireAssets
// signature below accepts a realtimeSvc parameter typed as `interface{}`
// (was `*realtime.Service`) so the caller in registry.go can pass
// `root.Domains.RealtimeService` (also interface{} post-fix) without any
// type assertions. The diagnostic / search adapters downstream of this
// function already use `interface{}` for the realtimeSvc field type
// (see internal/app/assets_adapters.go: diagIndexHealthAdapter.realtime
// + searchVectorAdapter.realtimeSvc), so this change re-aligns the
// caller signature with the adapter field types.
// PG-005 (June 2026): the clips Handler now receives typed ports from
// `newClipsAdapterBundle` instead of reaching through concrete
// infrastructure types. The composition root is the ONLY place allowed
// to import internal/infrastructure/* types.
func WireAssets(cfg *config.Config, log *zap.Logger, bundle *AssetsBundle, vectorStore *qdrant.Service, jobs *JobsBundle, voiceoverSvc *voiceoverpkg.Service, voiceoverSync *voiceoversync.Service, realtimeSvc interface{}, catalogRepo *catalog.Repository, maintenanceSvc *maintenance.Service, providerRegistry *providers.Registry) (*AssetsWiring, error) {
	var driveUploader *driveutil.Uploader
	if bundle.DriveClient != nil {
		driveUploader = &driveutil.Uploader{Service: bundle.DriveClient, Log: log}
	}
	var assetRepo asset.Repository
	if bundle.Assets != nil {
		assetRepo = bundle.Assets.Repository()
	}

	// PG-005 (June 2026): one canonical adapter bundle for the 11
	// clips-wide ports. Each adapter wraps a concrete infra type
	// and exposes a narrow structural interface — the api/ layer
	// stays port-only. `assetRepo` is the canonical asset.Service
	// adapter above (NOT a wrapped ClipsRepository) so the
	// AssetRepo field on clips.Deps type-checks against
	// asset.Repository.
	folderMemSvc := foldermemory.NewService(log, bundle.ClipsRepo)
	metaWriter := semantic.NewMetadataWriter(cfg.Paths.PythonScriptsDir, cfg.Storage.TempPath(), cfg.External.OllamaURL, cfg.External.OllamaModel, log)
	deletionSvc := deletion.NewDeletionService(bundle.ClipsRepo, bundle.ClipsRepo, bundle.ClipsRepo, bundle.VoiceoverRepo, bundle.ImageRepo, driveUploader, bundle.AssetTreeService, bundle.AssetIndexService, log)
	clipsBundle := newClipsAdapterBundle(
		cfg,
		log,
		bundle.ClipsRepo, // artlist slot
		bundle.ClipsRepo, // clips slot
		bundle.ClipsRepo, // stock slot
		bundle.VoiceoverRepo,
		bundle.ImageRepo,
		driveUploader,
		metaWriter,
		bundle.ClipIndexerService,
		folderMemSvc,
		bundle.AssetTreeService,
		vectorStore,
	)
	clipsSearchFunc := assetclipssearch.NewService(log, map[string]assetclipssearch.AdvancedSearchRepo{
		"youtube": bundle.ClipsRepo,
		"artlist": bundle.ClipsRepo,
		"stock":   bundle.ClipsRepo,
	})

	clipsHandler := clipsapi.NewHandler(clipsapi.Deps{
		SourceResolver: clipsBundle.SourceResolver,
		AssetRepo:      assetRepo,
		ClipsRepo:      clipsBundle.ClipsRepo,
		StockRepo:      clipsBundle.StockRepo,
		ArtlistRepo:    clipsBundle.ArtlistRepo,
		DeletionSvc:    deletionSvc,
		DriveUploader:  clipsBundle.DriveUploader,
		MediaProcessor: bundle.MediaProcessor,
		AssetTreeSvc:   bundle.AssetTreeService,
		MetaWriter:     clipsBundle.MetaWriter,
		ClipIndexer:    clipsBundle.ClipIndexer,
		VectorStore:    clipsBundle.VectorStore,
		JobsSvc:        jobs.Facade,
		Cfg:            clipsBundle.Cfg,
		Log:            log,
		VoiceoverRepo:  clipsBundle.VoiceoverRepo,
		ImagesRepo:     clipsBundle.ImagesRepo,
		HashSvc:        clipsBundle.HashSvc,
		TreeBuilderSvc: clipsBundle.TreeBuilderSvc,
		SearchSvc:      clipsSearchFunc,
		ProcessRunner:  processRunnerAdapter,
	})

	// ── PR 3 (June 2026): storage thin-transport handler ─────
	var drivePort appstorage.DrivePort
	if driveUploader != nil {
		drivePort = &storageDriveAdapter{up: driveUploader}
	}
	storageSvc := appstorage.NewService(drivePort, &zapLogAdapter{log})
	storageHandler := assetstorage.NewHandler(storageSvc, jobs.Facade, bundle.CatalogSyncService, log)

	// ── PR 3 (June 2026): diagnostics + search wired with real ports ─
	// Diagnostics: IndexHealth via realtime.Service + AssetStats via ClipsRepository.
	// When realtimeSvc is nil (vector search disabled), the handler falls back to 503.
	var diagSvc *appdiag.Service
	if realtimeSvc != nil {
		diagSvc = appdiag.NewService(
			&diagIndexHealthAdapter{realtime: realtimeSvc, vectorSvc: vectorStore},
			&diagAssetStatsAdapter{clips: bundle.ClipsRepo},
			&zapDiagLogAdapter{log: log},
		)
	}
	diagHandler := assetsdiag.NewHandler(diagSvc, log)

	// Search: providers registry + vector search + local catalog/clips.
	// Only wired when both vectorStore and realtimeSvc are non-nil.
	var searchSvc *appsearchsvc.Service
	if vectorStore != nil && realtimeSvc != nil {
		searchSvc = appsearchsvc.NewService(
			&searchRegistryAdapter{registry: providerRegistry},
			&searchVectorAdapter{embedder: qdrant.NewSearchAdapter(vectorStore), realtimeSvc: realtimeSvc},
			&searchCatalogAdapter{catalog: catalogRepo},
			&searchClipAdapter{catalog: catalogRepo},
			&searchConfigAdapter{cfg: cfg},
			&zapSearchLogAdapter{log: log},
		)
	}
	searchHandler := assetsearch.NewHandler(searchSvc, log)
	if diagSvc != nil && searchSvc != nil {
		log.Info("diagnostics and search services wired with production ports")
	} else {
		log.Warn("diagnostics and/or search services NOT fully wired — some routes will return 503")
	}

	// ── PR 4 (June 2026): extract voiceover, soundeffect, register ─
	// Voiceover: GroupsResolver is built here for the standalone handler.
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

	// SoundEffect: wrapped repos + uploader + metaWriter via sfxports
	// adapters. PG-003 (June 2026) replaces the four concrete
	// infrastructure reach-throughs with structural ports so the api/
	// layer stays thin (per AGENTS.md Pattern 0).
	sfxClips := &sfxClipsRepoAdapter{repo: bundle.ClipsRepo}
	sfxMeta := &sfxSemanticWriterAdapter{w: metaWriter}
	sfxResolver := &sfxResolverAdapter{r: driveutil.NewResolver("data", "")}
	var sfxDriveUp sfxports.DriveUploaderPort
	if driveUploader != nil {
		sfxDriveUp = &sfxDriveUploaderAdapter{up: driveUploader}
	}
	sfxHandler := assetsfx.NewHandler(sfxClips, sfxDriveUp, sfxMeta, sfxResolver, cfg.Drive.SoundEffectsRootFolder, processRunnerAdapter, log)

	// Register: the HTTP layer now depends on a single sourcing use case.
	registerSvc := newAssetRegisterService(cfg, log, bundle.ClipsRepo, driveUploader, bundle.AssetTreeService, providerRegistry, clipsHandler)
	registerHandler := assetregister.NewHandler(registerSvc, log)

	assetsMod := assetsapi.NewModule(assetsapi.Dependencies{
		Storage:     storageHandler,
		Diagnostics: diagHandler,
		Search:      searchHandler,
		Clips:       clipsHandler,
		Voiceover:   voiceoverHandler,
		SoundEffect: sfxHandler,
		Register:    registerHandler,
	}, log)
	assetsRouteMod := module.NewRouteModule(
		"assets",
		func() bool { return true },
		"/media",
		assetsMod,
		log,
	)
	log.Info("created unified Assets module (thin transport)")

	return &AssetsWiring{
		Module:      assetsRouteMod,
		DeletionSvc: deletionSvc,
	}, nil
}

// storageDriveAdapter adapts drive.Uploader to storage.DrivePort.
type storageDriveAdapter struct {
	up *driveutil.Uploader
}

// Compile-time assertion: storageDriveAdapter satisfies appstorage.DrivePort.
var _ appstorage.DrivePort = (*storageDriveAdapter)(nil)

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
