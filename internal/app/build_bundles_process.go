// Package app — Drive bundle construction (split out from composition.go
// in commit ci/composition-split wave-1 of the 5-commit refactor for
// problem #8).
//
// This file owns the Drive adapters + MediaStore derivation + StyleRegistry
// loading for the canonical Google Drive integration. Extracted from
// composition.go so bundle debt is split per AGENTS.md Pattern 5 (1 concept
// per focused file) and BuildDriveBundle's own body remains pure (no
// concurrent goroutine spawns — composition_test.go::
// TestComposition_NoGoroutinesSpawned_FrozenSiteCount).
//
// commit ci/composition-split wave-1 (June 2026): replaced the legacy
// post-ctor setter pair (`mediaStore.SetAssetTree + SetTreeSource`) with
// a single `drive.NewStoreWithOptions(..., drive.StoreOptions{AssetTree,
// TreeSources})` call so the dependency graph lands at the ctor boundary.
package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// BuildDriveBundle constructs the Drive adapters + MediaStore + DestResolver.
// Loads StyleRegistry at the top so ensureStyleDriveFolders (called via the
// returned startDriveBackgroundFolders closure) receives the non-nil pointer.
//
// PR9-A (June 2026): BuildDriveBundle returns an IOpaqueStartFunc closure
// that defers side-effecting initialisation (Drive folder validation,
// style-folder pre-creation, storage directory creation) to the lifecycle.
// The bundle itself is fully populated on return.
func BuildDriveBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, search *SearchBundle) (*DriveBundle, IOpaqueStartFunc, error) {
	styleRegistry, _ := generation.NewStyleRegistry("config/generation_styles.yaml")

	docClient, err := drive.NewDocClient(ctx, cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	driveClient, err := drive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// PG-011-residual-cleanup (June 2026): the previous
	// resolveRuntimeDestinations function (a no-op alias for
	// configOnlyDestinations — both pre-existing branches converged
	// on the same cfg-derived *DriveDestinations) was deleted;
	// dests is now derived once, unconditionally. driveClient
	// remains a dependency for driveUploader construction, the
	// mediaStore block below, and the startClosure's folder
	// validation, but it is no longer threaded through a
	// dests-resolution alias that ignored it.
	var driveUploader *drive.Uploader
	var dests = configOnlyDestinations(cfg)
	if driveClient != nil {
		driveUploader = &drive.Uploader{Service: driveClient, Log: log}
	}

	var mediaStore *drive.Store
	var destResolver asset.Resolver
	if driveClient != nil {
		storageResolver := drive.NewResolver(
			drive.MediaRoot(cfg.Storage.MediaPath()),
			drive.DriveRoot(dests.RootFolder()),
		)

		// Construct the StoreOptions at the ctor boundary — no post-ctor
		// SetAssetTree / SetTreeSource calls. TreeSources maps Drive folder
		// IDs to their logical tree source names.
		storeOpts := drive.StoreOptions{}
		if search != nil && search.AssetTreeService != nil {
			storeOpts.AssetTree = search.AssetTreeService
			storeOpts.TreeSources = map[string]string{
				dests.ImagesFolder(): "image",
			}
			log.Info("mediaStore: Drive roots configured",
				zap.String("images_folder_id", dests.ImagesFolder()))
		}

		mediaStore = drive.NewStoreWithOptions(
			storageResolver,
			driveUploader,
			dests.RootFolder(),
			dests.ImagesFolder(),
			"", // VideoAIRoot removed (PR June 2026) — pass empty string
			dests.SoundEffectsRoot,
			log,
			storeOpts,
		)

		destResolver = drive.NewDestinationResolver(mediaStore)
	}

	// PR9-A (June 2026): side-effecting initialisation is delegated to
	// startDriveBackgroundFolders (defined below). Package-level function
	// so the source-level goroutine-count freeze test reports zero spawns
	// in BuildDriveBundle's own body.
	// Lifecycle-runtime-ownership (June 2026): now returns error so
	// serverLifecycle.Start can abort on required folder validation failure.
	startClosure := func() error {
		return startDriveBackgroundFolders(ctx, cfg, driveClient, driveUploader, dests, styleRegistry, log)
	}

	return &DriveBundle{
		DriveClient:   driveClient,
		DriveUploader: driveUploader,
		DocClient:     docClient,
		DriveDests:    dests,
		MediaStore:    mediaStore,
		DestResolver:  destResolver,
		StyleRegistry: styleRegistry,
	}, startClosure, nil
}

// startDriveBackgroundFolders performs the side-effecting Drive init that
// was previously inlined in BuildDriveBundle (PR9-A, June 2026). It
// pre-creates style folders on Drive, validates critical Drive folder
// paths, and ensures local storage directories exist.
//
// Lifecycle-runtime-ownership (June 2026): now returns error on required
// folder validation failure. Style folder creation remains async (background
// after readiness passes). Local storage directory creation errors are
// logged as warnings (they are non-fatal).
//
// Invoked by the lifecycle after WireRegistry completes, before the HTTP
// server begins accepting requests.
func startDriveBackgroundFolders(
	ctx context.Context,
	cfg *config.Config,
	driveClient *gdrive.Service,
	driveUploader *drive.Uploader,
	dests *DriveDestinations,
	styleRegistry *generation.StyleRegistry,
	log *zap.Logger,
) error {
	// Style folder pre-creation: async after readiness (optional).
	if driveClient != nil && dests.ImagesFolder() != "" && dests.ImagesFolder() != dests.MediaRoot {
		concurrent.SafeGo("drive-style-folders", func() {
			ensureStyleDriveFolders(ctx, driveUploader, dests.ImagesFolder(), styleRegistry, log)
		})
		log.Info("Style Drive folders using Images root", zap.String("folder_id", dests.ImagesFolder()))
	}

	// Required folder validation: synchronous, returns error on failure.
	if driveClient != nil {
		for name, folderID := range map[string]string{
			"images": dests.ImagesFolder(),
		} {
			if folderID == "" {
				continue
			}
			if _, err := driveClient.Files.Get(folderID).Fields("id, name").Context(ctx).Do(); err != nil {
				return fmt.Errorf("required Drive folder %q (id=%s) validation failed: %w", name, folderID, err)
			}
			log.Info("Drive folder validated",
				zap.String("folder_name", name), zap.String("folder_id", folderID))
		}
	}

	// Local storage directories: optional (logged as warnings).
	for _, dir := range []string{
		cfg.Storage.DataDir, cfg.Storage.VoiceoversPath(), cfg.Storage.AssetsPath(),
		cfg.Storage.DownloadsPath(), cfg.Storage.BackupsPath(), cfg.Storage.TempPath(),
		cfg.Storage.AnimationsPath(), cfg.Storage.YoutubeClipsPath(),
		cfg.Storage.ArtlistPath(), cfg.Storage.ImagesPath(),
	} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Warn("Failed to create storage directory", zap.String("path", dir), zap.Error(err))
		}
	}
	return nil
}

// Compile-time assertions for QDRANT-003 wiring.
var (
	_ clipindexer.VectorStoreIndexer = (*qdrant.IndexWriter)(nil)
)

// BuildProcessBundle builds media-processing adapters. driveUploader
// passed in directly.
//
// QDRANT-003 (June 2026): Qdrant vector-store capability reintroduced.
// IndexWriter is created and wired as the clipindexer's VectorStoreIndexer.
// EnsureSchema is deferred to wire_services.go startup plan (startup-time).
func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, driveUploader *drive.Uploader) (*ProcessBundle, error) {
	_ = ctx
	mediaProcessor := initMediaProcessor(cfg, dbs.main, repos.Assets.Repository(), repos.Assets,
		repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), log, driveUploader)

	vlmClient := vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		MaxConcurrentIndexing: cfg.ClipIndexer.MaxConcurrentIndexing,
		DBPath:                dbs.main.Path(),
	}, dbs.main, dbs.main.Path(), log)

	// QDRANT-003: wire IndexWriter as clipindexer VectorStoreIndexer.
	// Only when Qdrant is enabled AND the clip indexer is enabled.
	var collectionMgr *qdrant.CollectionManager
	var indexDeleter qdrant.QdrantDeleter
	// Wave 15 (June 2026): typed-nil setter for the canonical
	// assetsearch.VectorStorePort. NewSearchAdapter returns the typed
	// port directly (compile-time assertion at
	// internal/infrastructure/qdrant/search_adapter.go confirms the
	// concrete satisfies the port).
	var vectorSvc assetsearch.VectorStorePort
	var qdrantClient *qdrant.Client
	var qdrantHealthProbe any // qdrant.HealthProbe or nil — typed `any` so this layer doesn't force every composition path to import health.go

	if cfg.Qdrant.Enabled && clipIndexerService.IsEnabled() {
		qdrantCfg := &qdrant.Config{
			BaseURL: cfg.Qdrant.BaseURL,
			Timeout: cfg.Qdrant.Timeout,
		}
		schema := qdrant.DefaultV3Schema()
		qdrantClient = qdrant.NewClient(qdrantCfg, log)
		assetStore := qdrant.NewSQLiteAssetStore(dbs.main.DB)
		mapper := qdrant.NewPayloadMapper(assetStore, log)
		indexWriter := qdrant.NewIndexWriter(qdrantClient, schema, mapper, log)
		indexDeleter = indexWriter

		// QDRANT-004: create Searcher + SearchAdapter for the mediasearch API.
		searcher := qdrant.NewSearcher(qdrantClient, schema, log)
		searchAdapter := qdrant.NewSearchAdapter(searcher, log)
		vectorSvc = searchAdapter

		collectionMgr = qdrant.NewCollectionManager(qdrantClient, schema, log)

		// QDRANT-005 (June 2026): bind the canonical health probe so
		// /ready actually checks Qdrant reachability instead of silently
		// reporting the Qdrant capability as "not applicable". The probe
		// is exposed on ProcessBundle.QdrantHealthProbe (typed `any`) and
		// picked up by wire_services.go / cmd/server/main.go via
		// lifecycle.AddProbe.
		qdrantHealthProbe = qdrant.NewHealthProbe(qdrantClient)

		clipIndexerService.SetVectorStore(indexWriter)
		log.Info("QDRANT-003: IndexWriter wired as clipindexer VectorStoreIndexer",
			zap.String("qdrant_url", cfg.Qdrant.BaseURL),
			zap.String("schema_version", schema.Version),
			zap.String("runtime_alias", schema.RuntimeAlias))
		log.Info("QDRANT-004: Searcher + SearchAdapter wired for mediasearch API")
		log.Info("QDRANT-005: HealthProbe wired for /ready readiness barrier")
	} else {
		log.Info("QDRANT-003: Qdrant disabled — vector store upserts will be skipped")
	}

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: clipIndexerService,
		VLMClient:          vlmClient,
		CollectionManager:  collectionMgr,
		QdrantDeleter:      indexDeleter,
		VectorSvc:          vectorSvc,
		QdrantHealthProbe:  qdrantHealthProbe,
	}, nil
}

// BuildOutboxBundle constructs the canonical ingestion outbox + outbox_events.Pool.
//
// PR9-B (June 2026): BuildOutboxBundle returns an IOpaqueStartFunc closure
// that defers the outbox events pool goroutines (Start + shutdown) to the
// lifecycle. The bundle itself is fully populated on return.
func BuildOutboxBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, process *ProcessBundle, jobs *JobsBundle) (*OutboxBundle, IOpaqueStartFunc, error) {
	outboxEventsRepo := outboxevents.NewRepository(dbs.main.DB)

	multiClipsUp := outbox.NewMultiClipsUpserter(
		map[string]outbox.ClipsUpserter{
			"youtube": repos.ClipsRepo,
			"stock":   repos.ClipsRepo,
			"artlist": repos.ClipsRepo,
		},
		repos.ClipsRepo,
		log,
	)
	// QDRANT-002 PR7: wire *assets.ClipsRepository as the
	// ClipsStateWriter (same concrete that already implements
	// ClipsUpserter — methods are partitioned by go-type, not by
	// runtime class). Per-state dispatching is unnecessary post
	// PR2.6 media_assets consolidation because every per-source
	// shim funnels into the same SQLite table.
	stateWriter := outbox.ClipsStateWriter(repos.ClipsRepo)
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
	dispatcher := outbox.NewDispatcher(multiClipsUp, stateWriter, outboxEventsRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path AND canonical delete+outbox_events enqueue path (QDRANT-002 PR7)")

	eventsRegistry := outboxevents.NewHandlerRegistry()

	httpClient := &http.Client{Timeout: 30 * time.Second}

	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	// AssetSourceChecker is the load-bearing GetClip port used by
	// the IndexingHandler source_version supersede gate (QDRANT-002
	// item F). The production concrete is the same ClipsRepository
	// already wired into the dispatcher's MultiClipsUpserter; both
	// expose GetClip, so a single instance satisfies the interface.
	// nil ClipsRepo → nil AssetSourceChecker → IndexingHandler skips
	// the supersede gate (acceptable in test dbs; production always
	// wires non-nil).
	//
	// Wave 16 (June 2026): typed-port direct assignment per
	// AGENTS.md Pattern 0. The previous `interface{}(repos.ClipsRepo)
	// .(jobsoutbox.AssetSourceChecker)` raw cast is replaced because
	// *assets.ClipsRepository statically implements the port
	// (compile-time assertion at
	// internal/infrastructure/database/sqlite/assets/clips_repository.go).
	// Dropping the `, ok` form is safe: the assertion fails the build
	// if port drift ever breaks the static implementation contract.
	var assetSourceChecker jobsoutbox.AssetSourceChecker
	if repos.ClipsRepo != nil {
		assetSourceChecker = repos.ClipsRepo
	}

	outboxDeps := &jobsoutbox.Deps{
		DB:                 dbs.main.DB,
		HTTPClient:         httpClient,
		MetadataDir:        cfg.Storage.FullPath("asset_metadata"),
		HMACSecrets:        hmacSecrets,
		InsecureDev:        cfg.Security.DeliveryInsecureDev,
		Jobs:               jobs.Service,
		AssetSourceChecker: assetSourceChecker,
	}
	// QDRANT-003: wire IndexWriter as QdrantDeleter for index.delete_requested events.
	if process.QdrantDeleter != nil {
		if qd, ok := process.QdrantDeleter.(jobsoutbox.QdrantDeleter); ok {
			outboxDeps.QdrantDeleter = qd
		}
	}
	if err := jobsoutbox.RegisterAll(eventsRegistry, log, process.ClipIndexerService, outboxDeps); err != nil {
		log.Warn("failed to register outbox events handlers", zap.Error(err))
	}

	cfgPoll := 500 * time.Millisecond
	if cfg.Outbox.PollIntervalMs > 0 {
		cfgPoll = time.Duration(cfg.Outbox.PollIntervalMs) * time.Millisecond
	}
	cfgReclaim := 60 * time.Second
	if cfg.Outbox.ReclaimIntervalSeconds > 0 {
		cfgReclaim = time.Duration(cfg.Outbox.ReclaimIntervalSeconds) * time.Second
	}
	cfgProcess := 30 * time.Second
	if cfg.Outbox.ProcessTimeoutSeconds > 0 {
		cfgProcess = time.Duration(cfg.Outbox.ProcessTimeoutSeconds) * time.Second
	}
	outboxEventsCfg := outboxevents.WorkerPollConfig{
		PollInterval:    cfgPoll,
		ProcessTimeout:  cfgProcess,
		ReclaimInterval: cfgReclaim,
	}
	eventsPool := outboxevents.NewPool("outbox-events", outboxEventsRepo, eventsRegistry, log, outboxEventsCfg)

	startClosure := func() error {
		return startOutboxEventsPool(ctx, eventsPool, outboxEventsCfg, log)
	}

	return &OutboxBundle{
		Dispatcher:     dispatcher,
		EventsRepo:     outboxEventsRepo,
		EventsRegistry: eventsRegistry,
		EventsPool:     eventsPool,
	}, startClosure, nil
}

// startOutboxEventsPool performs the side-effecting outbox events pool
// initialisation.
//
// Lifecycle-runtime-ownership (June 2026): Pool.Start is void-returning
// so the goroutine is launched via SafeGo (panic-recovery). The shutdown
// goroutine drains the pool on ctx.Done(). The caller treats this as a
// required step — if the goroutine panics, SafeGo recovers and logs the
// panic without crashing the server.
func startOutboxEventsPool(
	ctx context.Context,
	eventsPool *outboxevents.Pool,
	cfg outboxevents.WorkerPollConfig,
	log *zap.Logger,
) error {
	if eventsPool == nil {
		return nil
	}
	concurrent.SafeGo("outbox-events-pool", func() {
		eventsPool.Start(ctx, 1)
	})
	concurrent.SafeGo("outbox-events-shutdown", func() {
		<-ctx.Done()
		if err := eventsPool.Stop(15 * time.Second); err != nil {
			log.Warn("outbox events pool stop returned error", zap.Error(err))
		}
	})
	log.Info("outbox events pool started", zap.Duration("poll_interval", cfg.PollInterval))
	return nil
}
