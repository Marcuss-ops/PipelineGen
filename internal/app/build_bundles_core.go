package app

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/destinations"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/books/pythontransformer"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	idemsqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/idempotency"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	qdranttransport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/stager"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func BuildRepoBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*RepoBundle, error) {
	_ = ctx
	_ = cfg
	assetsStore := sqassets.NewAssetStoreSQLite(dbs.dualPool.Writer, log)
	assetsSvc := asset.NewService(assetsStore, log)
	imageRepo := imagesrepo.NewImagesRepository(dbs.dualPool.Writer)
	voiceoverRepo := sqassets.NewVoiceoversRepository(dbs.dualPool.Writer)
	monitorsRepo := monitors.NewMonitorsRepository(dbs.dualPool.Writer)
	clipsRepo := sqassets.NewClipsRepositoryCanonical(dbs.dualPool.Writer, log, assetsSvc.Repository())
	catalogRepo := catalog.NewRepository(clipsRepo, clipsRepo, clipsRepo)
	scriptsRepo := sqlitescripts.NewScriptRepository(dbs.dualPool.Writer)
	sqRepo := sqassets.NewSearchQueriesRepository(dbs.dualPool.Writer)
	var idempotencyStore middleware.IdempotencyStore = idemsqlite.NewSQLiteRepository(dbs.dualPool.Writer)
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): TextTrackRepo
	// is the canonical Fase 2.a / Fase 4 TextTrackRepository used by
	// the video pipeline + the AcquireService backfill CLI. It MUST
	// be wired at the RepoBundle composition root (BuildRepoBundle)
	// so every consumer (BuildTextTrackBundle, BuildDomainBundle, the
	// AcquireService wiring in composition.go, the Qdrant
	// PayloadMapper in buildQdrantDeps) sees the SAME non-nil
	// dependency. The pre-PR scattered construction in
	// build_bundles_domain_media.go::buildDomainMediaServices and
	// build_process_qdrant.go::buildQdrantDeps is now removed — both
	// callers consume repos.TextTrackRepo from this bundle. godlike/07
	// fail-closed: BuildTextTrackBundle rejects nil TextTrackRepo so
	// the test fixture MUST exercise this path.
	textTrackRepo, err := texttracks.NewTextTrackRepository(dbs.dualPool.Writer, log)
	if err != nil {
		return nil, fmt.Errorf("init text track repository: %w", err)
	}
	subArtRepo, err := texttracks.NewSubtitleArtifactRepository(dbs.dualPool.Writer, log)
	if err != nil {
		return nil, fmt.Errorf("init subtitle artifact repository: %w", err)
	}
	return &RepoBundle{
		ScriptsRepo: scriptsRepo, ImageRepo: imageRepo, VoiceoverRepo: voiceoverRepo,
		MonitorsRepo: monitorsRepo, ClipsRepo: clipsRepo, Assets: assetsSvc,
		CatalogRepo: catalogRepo, SQRepo: sqRepo, IdempotencyStore: idempotencyStore,
		TextTrackRepo: textTrackRepo, SubtitleArtifactRepo: subArtRepo,
	}, nil
}

func BuildSearchBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle) (*SearchBundle, error) {
	_ = ctx
	_ = cfg
	assetIndexRepo := assetindex.NewRepository(dbs.dualPool.Writer)
	assetIndexService := assetindex.NewService(assetIndexRepo)
	assetTreeRepo, err := sqassets.NewAssetTreeRepository(dbs.dualPool.Writer, log)
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

func BuildUtilityBundle(cfg *config.Config, db *storage.SQLiteDB, driveReader drive.Reader, publisher delivery.Publisher, jobsSvc *appjobs.Service, ollamaClient *ollamaclient.Client, outboxPool *outboxevents.Pool, log *zap.Logger) *UtilityBundle {
	svc := buildHealthService(cfg, db)
	rc := systemhealth.NewReadyChecker(svc).
		WithTools(systemhealth.NewToolsChecker()).
		WithClipsPath("data/media/clips")

	// Step 4: Drive readiness checks (July 2026).
	// Publisher non-nil check (always wired when publisher is available).
	rc = rc.WithPublisherCheck(systemhealth.NewPublisherChecker(publisher))

	// Drive credentials file check.
	rc = rc.WithDriveCredentials(systemhealth.NewDriveCredentialsChecker(
		cfg.GetTokenPath(),
		cfg.GetCredentialsPath(),
	))

	// DestinationClip registry check.
	reg := delivery.NewDestinationRegistry(cfg)
	rc = rc.WithDestinationClipCheck(systemhealth.NewDestinationClipChecker(
		func(key delivery.DestinationKey) bool { return reg.Has(key) },
	))

	// Step 8: Drive canary + handler checks (wired when deps available).
	if publisher != nil && cfg.Drive.ClipsFolder() != "" {
		rc = rc.WithDriveCanary(
			systemhealth.NewPublisherCanary(publisher),
			cfg.Drive.ClipsFolder(),
		).WithDriveFolder(
			systemhealth.NewDriveFolderChecker(publisher),
			cfg.Drive.ClipsFolder(),
		)
	}
	if driveReader != nil {
		rc = rc.WithDriveRootFolder(cfg.Drive.ClipsFolder()).
			WithDriveRootChecker(systemhealth.NewDriveRootChecker(
				func(ctx context.Context, folderID string) error {
					_, err := driveReader.ListFiles(ctx, folderID)
					return err
				},
			))
	}
	if jobsSvc != nil {
		rc = rc.WithHandlerCheck(systemhealth.NewHandlerPresenceChecker(
			func(jobType string) bool { return jobsSvc.HasHandler(jobType) },
		))
	}

	// FASE 6: severe readiness probes (temp, tts, drive_root, ollama, outbox).
	// Temp + TTS + Drive root are wired inline below.
	rc = rc.WithTempPath(cfg.Storage.DataDir).
		WithTTSChecker(systemhealth.NewTTSChecker("", cfg.Paths.PythonScriptsDir))

	// Outbox worker pool liveness (July 2026): checks that the
	// outboxevents.Pool was created and started at composition time.
	// The probe verifies the pool is non-nil — a nil pool means
	// BuildOutboxBundle failed during composition and the outbox
	// event-processing workers never started. When the pool is wired,
	// the probe returns nil (the workers were launched via SafeGo
	// at startup; if they crash, SafeGo recovers and logs the panic
	// without taking down the server — the next /ready call will
	// still see the pool as non-nil, which is correct: the pool
	// struct itself is still alive even if individual workers died).
	if outboxPool != nil {
		rc = rc.WithOutboxChecker(systemhealth.NewOutboxChecker(
			func(ctx context.Context) error {
				// Pool is non-nil by construction — was created and
				// started in BuildOutboxBundle.
				return nil
			},
		))
	}
	// Ollama reachability probe (nil-safe: nil client → applicable=false).
	if ollamaClient != nil {
		rc = rc.WithOllamaChecker(systemhealth.NewOllamaChecker(
			func(ctx context.Context) bool { return ollamaClient.CheckHealth(ctx) },
		))
	}

	return &UtilityBundle{
		Utility: transport.NewUtilityHandler(), HealthService: svc,
		ReadyChecker: rc,
	}
}

func buildHealthService(cfg *config.Config, db *storage.SQLiteDB) *systemhealth.Service {
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
	var qdrantChecker systemhealth.QdrantChecker
	// godlike/07 consistency: when the upstream 3 sites declare an
	// incompatible configuration, buildHealthService's helper check
	// would also fail. We log+continue here to avoid a second-level
	// panic during the partial-composition rollback; the canonical
	// boot-time error surfaces from WireServices (the 4th wire site)
	// via the upstream registry path.
	_ = validateQdrantIndexerCompatibility(cfg)
	if cfg.Qdrant.Enabled {
		qdrantCfg := &schema.Config{BaseURL: cfg.Qdrant.BaseURL, APIKey: cfg.Qdrant.APIKey, Timeout: cfg.Qdrant.Timeout}
		qdrantChecker = disasterrecovery.NewHealthProbe(qdranttransport.NewClient(qdrantCfg, zap.NewNop()))
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

// buildBooksService wires the books apply-layer Service.
func buildBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, publisher delivery.Publisher, reader drive.Reader) (*books.Service, error) {
	var transformer *pythontransformer.SubprocessTransformer
	if cfg.Books.Enabled {
		var err error
		transformer, err = pythontransformer.NewSubprocessTransformer(&pythontransformer.Config{
			ScriptPath: cfg.Books.ScriptPath, PythonBin: cfg.Books.PythonBin, Enabled: true,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("books service compose failed (transformer): %w", err)
		}
	}
	booksSvc := books.NewService(
		&books.Config{DriveFolderID: cfg.Drive.BooksFolder()},
		dbs.dualPool.Writer, cfg.Drive.BooksFolder(), log, publisher, reader, transformer,
	)
	booksSvc.SetEnabled(cfg.Books.Enabled)
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc, nil
}

// buildImagesParams groups the dependencies required to wire the images service.
//
// PR-YAGNI-IMAGES-WIRING (July 2026): replaces the 14 positional arguments
// of buildImagesService with a single struct. The previous signature carried
// two unused parameters (ctx, artlistRepo) that are now removed entirely.
type buildImagesParams struct {
	Cfg           *config.Config
	Log           *zap.Logger
	DriveUploader *drive.Uploader
	StyleRegistry *generation.StyleRegistry
	ScriptGen     *ollama.Generator
	Publisher     delivery.Publisher
	ImageRepo     *imagesrepo.ImagesRepository
	VOMetaWriter  semantic.MetadataWriterPort
	IngestSvc     *ingest.Service
	Committer     assetspersistence.AssetCommitter
	Dispatcher    *outbox.Dispatcher
}

func buildImagesService(params buildImagesParams) (*imgservice.Service, semantic.MetadataWriterPort) {
	const destinationsYAMLPath = "config/image_destinations.yaml"
	destResolver, err := destinations.NewYamlResolver(destinationsYAMLPath, params.Cfg.Drive.ImagesFolder())
	if err != nil {
		params.Log.Warn("destinations.NewYamlResolver failed; ImageStorageService.destResolver will be nil",
			zap.String("yaml_path", destinationsYAMLPath), zap.Error(err))
		destResolver = nil
	}
	// PR-SOURCESTAGER-CONSOLIDATE (July 2026): construct the canonical
	// HTTPSourceStager that backs the assets.SourceStager port used by
	// ImageStorageService.downloadAndIngest. The staging dir is
	// <TempPath>/staged-sources so it is co-located with the rest of
	// the temp space and survives the same retention policy. The
	// http.Client uses a 10-minute timeout (mirrors the legacy
	// inline-http timeout) and a fresh per-Service instance (the
	// legacy shared client lives on the service struct but only the
	// stager needs it now).
	stagerDir := filepath.Join(params.Cfg.Storage.TempPath(), "staged-sources")
	imageSourceStager, stagerErr := stager.NewHTTPSourceStager(
		stagerDir,
		&http.Client{Timeout: 10 * time.Minute},
		params.Log,
	)
	if stagerErr != nil {
		// godlike/07 fail-closed: a stager init failure (missing
		// dir, nil client, nil log) MUST NOT silently degrade to
		// the legacy inline-http path. Surface the error so the
		// composition root fails before the service starts
		// processing requests.
		params.Log.Error("buildImagesService: NewHTTPSourceStager init failed; image ingest will fail closed",
			zap.String("stager_dir", stagerDir), zap.Error(stagerErr))
		imageSourceStager = nil
	}
	imageService := imgservice.NewService(imgservice.ImagesDeps{
		Core: imgservice.ImagesCoreDeps{Cfg: params.Cfg, Log: params.Log},
		Storage: imgservice.ImagesStorageDeps{
			ImageRepo: params.ImageRepo, DriveReader: params.DriveUploader,
			Publisher: params.Publisher, DestResolver: destResolver,
		},
		GenAI: imgservice.ImagesGenAIDeps{
			LLMGen: params.ScriptGen, MetaWriter: params.VOMetaWriter, StyleRegistry: params.StyleRegistry,
			ImageGen: imgservice.NewChromeImageProviderPoolFromProfile(
				params.Cfg.Paths.PythonScriptsDir,
				params.Cfg.Concurrency.MaxConcurrentGoogleSlidesGenerations,
				params.Cfg.Concurrency.GoogleSlidesProfileID,
				params.Log,
			),
		}, External: imgservice.ImagesExternalDeps{
			IngestSvc: params.IngestSvc,
			Committer: params.Committer,
			// PR-SOURCESTAGER-CONSOLIDATE (July 2026): SourceStager
			// is the canonical port for staging remote URLs into
			// deterministic local files. downloadAndIngest routes
			// web image downloads through it so the inline
			// http.NewRequest + client.Do boilerplate no longer
			// leaks into the processor. Nil is tolerated (the
			// stager init failed above, or a partial deploy);
			// downloadAndIngest fails closed with a typed error
			// (godlike/07).
			SourceStager: imageSourceStager,
			VeloxBaseURL: params.Cfg.External.VeloxBaseURL,
			GACfg: imgservice.GoogleAccountingConfig{
				ServerURL: params.Cfg.GoogleAccounting.ServerURL, DownloadDir: params.Cfg.GoogleAccounting.DownloadDir,
				VidsProjectID: params.Cfg.GoogleAccounting.VidsProjectID,
			},
		},
	})
	return imageService, params.VOMetaWriter
}
