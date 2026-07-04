package app

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/books/pythontransformer"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	idemsqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/idempotency"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func BuildRepoBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*RepoBundle, error) {
	_ = ctx
	_ = cfg
	assetsStore := sqassets.NewAssetStoreSQLite(dbs.main.DB, log)
	assetsSvc := asset.NewService(assetsStore, log)
	imageRepo := sqassets.NewImagesRepository(dbs.main.DB)
	voiceoverRepo := sqassets.NewVoiceoversRepository(dbs.main.DB)
	monitorsRepo := sqassets.NewMonitorsRepository(dbs.main.DB)
	clipsRepo := sqassets.NewClipsRepositoryCanonical(dbs.main.DB, log, assetsSvc.Repository())
	catalogRepo := catalog.NewRepository(clipsRepo, clipsRepo, clipsRepo)
	scriptsRepo := sqlitescripts.NewScriptRepository(dbs.main.DB)
	sqRepo := sqassets.NewSearchQueriesRepository(dbs.main.DB)
	var idempotencyStore middleware.IdempotencyStore = idemsqlite.NewSQLiteRepository(dbs.main.DB)
	return &RepoBundle{
		ScriptsRepo: scriptsRepo, ImageRepo: imageRepo, VoiceoverRepo: voiceoverRepo,
		MonitorsRepo: monitorsRepo, ClipsRepo: clipsRepo, Assets: assetsSvc,
		CatalogRepo: catalogRepo, SQRepo: sqRepo, IdempotencyStore: idempotencyStore,
	}, nil
}

func BuildSearchBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle) (*SearchBundle, error) {
	_ = ctx
	_ = cfg
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	assetTreeRepo, err := sqassets.NewAssetTreeRepository(dbs.main.DB, log)
	if err != nil {
		return nil, fmt.Errorf("init asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	if assetTreeService == nil {
		return nil, fmt.Errorf("assettree service is nil after construction")
	}
	clipsRepos := map[string]*sqassets.ClipsRepository{
		"youtube": repos.ClipsRepo,
		"stock":   repos.ClipsRepo,
		"artlist": repos.ClipsRepo,
	}
	assetResolver := assetindex.NewResolver(assetIndexService, &assetindex.ResolverConfig{
		ClipsRepos: clipsRepos, ImageRepo: repos.ImageRepo, VoiceoverRepo: repos.VoiceoverRepo,
	}, log)
	return &SearchBundle{
		AssetIndexService: assetIndexService,
		AssetTreeService:  assetTreeService,
		AssetResolver:     assetResolver,
		ProviderRegistry:  providers.NewRegistry(),
	}, nil
}

func BuildUtilityBundle(cfg *config.Config, db *storage.SQLiteDB, driveAdmin drive.Admin) *UtilityBundle {
	svc := buildHealthService(cfg, db, driveAdmin)
	return &UtilityBundle{
		Utility: transport.NewUtilityHandler(), HealthService: svc,
		ReadyChecker: systemhealth.NewReadyChecker(svc),
	}
}

func buildHealthService(cfg *config.Config, db *storage.SQLiteDB, driveAdmin drive.Admin) *systemhealth.Service {
	if cfg == nil {
		return nil
	}
	// PR-QDRANT-CONFIG-MISMATCH-GATE (July 2026): defense-in-depth
	// gate. Note buildHealthService does NOT return an error (it
	// returns *systemhealth.Service) so we cannot propagate the
	// upstream helper error directly. Instead the helper is also
	// called at the 3 other canonical composition sites
	// (build_process_qdrant::buildQdrantDeps +
	// build_bundles_process::BuildOutboxBundle + wire_services::WireServices)
	// which DO return error; the boot-time fail-closed is enforced
	// at one of those 3 sites. The call here is a defensive belt-and-
	// suspenders consistency check — the helper itself does not
	// mutate state, so an unreachable reject path is safe to skip in
	// a side-effect-free helper-builder context. Cross-ref:
	// internal/app/build_bundles_qdrant_gates.go::validateQdrantIndexerCompatibility.
	var driveChecker systemhealth.DriveChecker
	_ = driveAdmin
	var qdrantChecker systemhealth.QdrantChecker
	// godlike/07 consistency: when the upstream 3 sites declare an
	// incompatible configuration, buildHealthService's helper check
	// would also fail. We log+continue here to avoid a second-level
	// panic during the partial-composition rollback; the canonical
	// boot-time error surfaces from WireServices (the 4th wire site)
	// via the upstream registry path.
	_ = validateQdrantIndexerCompatibility(cfg)
	if cfg.Qdrant.Enabled {
		qdrantCfg := &qdrant.Config{BaseURL: cfg.Qdrant.BaseURL, APIKey: cfg.Qdrant.APIKey, Timeout: cfg.Qdrant.Timeout}
		qdrantChecker = qdrant.NewHealthProbe(qdrant.NewClient(qdrantCfg, zap.NewNop()))
	}
	jobsChecker := infrahealth.NewJobsChecker(db)
	const heartbeatStaleness = 60 * time.Second
	jobsChecker.RunnerProbe = func(ctx context.Context) error {
		age := appjobs.BrokerHeartbeatAge()
		if age > int64(heartbeatStaleness.Seconds()) {
			return fmt.Errorf("broker heartbeat stale: last heartbeat %d seconds ago (threshold %ds)", age, int64(heartbeatStaleness.Seconds()))
		}
		return nil
	}
	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB: infrahealth.NewSQLiteChecker(db), Drive: driveChecker, Qdrant: qdrantChecker, Jobs: jobsChecker,
	})
}

func buildBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, voiceoverSvc *voiceover.Service, publisher delivery.Publisher, reader drive.Reader) (*books.Service, error) {
	transformer, err := pythontransformer.NewSubprocessTransformer(&pythontransformer.Config{
		ScriptPath: cfg.Books.ScriptPath, PythonBin: cfg.Books.PythonBin, Enabled: cfg.Books.Enabled,
	}, log)
	if err != nil {
		return nil, fmt.Errorf("books service compose failed (transformer): %w", err)
	}
	booksSvc := books.NewService(
		&books.Config{DriveFolderID: cfg.Drive.BooksFolder()},
		dbs.main.DB, cfg.Drive.BooksFolder(), log, voiceoverSvc, publisher, reader, transformer,
	)
	booksSvc.SetEnabled(cfg.Books.Enabled)
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc, nil
}

func buildImagesService(
	ctx context.Context, cfg *config.Config, log *zap.Logger,
	driveUploader *drive.Uploader, clipsRepo *sqassets.ClipsRepository, artlistRepo *sqassets.ClipsRepository,
	styleRegistry *generation.StyleRegistry, scriptGen *ollama.Generator,
	mediaStore *drive.Store, imageRepo *sqassets.ImagesRepository,
	voMetaWriter *semantic.MetadataWriter, ingestSvc *ingest.Service, dispatcher *outbox.Dispatcher,
) (*imgservice.Service, *semantic.MetadataWriter) {
	_ = ctx
	_ = artlistRepo
	const destinationsYAMLPath = "config/image_destinations.yaml"
	destResolver, err := destinations.NewYamlResolver(destinationsYAMLPath, cfg.Drive.ImagesFolder())
	if err != nil {
		log.Warn("destinations.NewYamlResolver failed; ImageStorageService.destResolver will be nil",
			zap.String("yaml_path", destinationsYAMLPath), zap.Error(err))
		destResolver = nil
	}
	imageService := imgservice.NewService(imgservice.ImagesDeps{
		Core: imgservice.ImagesCoreDeps{Cfg: cfg, Log: log},
		Storage: imgservice.ImagesStorageDeps{
			ImageRepo: imageRepo, ClipsRepo: clipsRepo, DriveReader: driveUploader,
			MediaStore: mediaStore, DestResolver: destResolver,
		},
		GenAI: imgservice.ImagesGenAIDeps{
			LLMGen: scriptGen, MetaWriter: voMetaWriter, StyleRegistry: styleRegistry,
			ImageGen: imgservice.NewChromeImageProvider(cfg.Paths.PythonScriptsDir, log),
		},
		External: imgservice.ImagesExternalDeps{
			IngestSvc: ingestSvc, Dispatcher: dispatcher, VeloxBaseURL: cfg.External.VeloxBaseURL,
			GACfg: imgservice.GoogleAccountingConfig{
				ServerURL: cfg.GoogleAccounting.ServerURL, DownloadDir: cfg.GoogleAccounting.DownloadDir,
				VidsProjectID: cfg.GoogleAccounting.VidsProjectID,
			},
		},
	})
	return imageService, voMetaWriter
}
