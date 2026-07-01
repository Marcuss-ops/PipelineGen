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
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	idemsqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/idempotency"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/health"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	gdrive "google.golang.org/api/drive/v3"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildRepoBundle constructs the canonical Repositories.
//
// PR8 (June 2026): added IdempotencyStore — the canonical port backing the
// reusable Gin idempotency middleware (internal/api/middleware/idempotency.go).
// All write handlers route replay requests through this store; a single
// repository instance is shared across the application so concurrent writes
// share an in_flight mutex-via-PRIMARY-KEY.
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

	// PR8: idempotency store (compiles cleanly against the port).
	var idempotencyStore middleware.IdempotencyStore = idemsqlite.NewSQLiteRepository(dbs.main.DB)

	return &RepoBundle{
		ScriptsRepo:      scriptsRepo,
		ImageRepo:        imageRepo,
		VoiceoverRepo:    voiceoverRepo,
		MonitorsRepo:     monitorsRepo,
		ClipsRepo:        clipsRepo,
		Assets:           assetsSvc,
		CatalogRepo:      catalogRepo,
		SQRepo:           sqRepo,
		IdempotencyStore: idempotencyStore,
	}, nil
}

// BuildSearchBundle builds the asset metadata search index + tree + resolver.
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

	// ── clipsRepos: the canonical map[string]*ClipsRepository literal site ──
	//
	// Action P1-3 of cleanup plan (June 2026) — canonical resolver
	// call shape documentation. This file is the ONLY site in the
	// repository that constructs a `map[string]*sqassets.ClipsRepository`
	// literal. The CI Check 45 (scripts/ci-architecture-checks.sh)
	// bans the same literal shape appearing anywhere else under internal/.
	//
	// Two canonical resolver patterns for ClipRepository access:
	//
	//   1. CONSUMER pattern (handler/service callers):
	//      - Receive a SINGLE concrete *sqassets.ClipsRepository via
	//        struct field (e.g. `ClipsRepo *sqassets.ClipsRepository`).
	//      - If multi-source dispatch is needed, expose a per-handler
	//        helper like `repoForSource(source) *sqassets.ClipsRepository`
	//        (see internal/api/assets/clips/{search,ingest,ops}.go).
	//      - DO NOT receive a map[string]*... repo bag in the consumer's
	//        constructor signature. Pass single-repo dependencies.
	//
	//   2. COMPOSITION-ROOT pattern (THIS file):
	//      - Build the bag ONCE during composition (clipsRepos below).
	//      - Inject the bag into the infrastructure-layer adapter
	//        (assetindex.ResolverConfig{ClipsRepos: clipsRepos}).
	//      - Adapter's per-source dispatch logic (resolver.go's
	//        Resolver method) reads the bag.
	//
	// Rationale: the typed-port surface in
	// internal/application/clips/ports.go (ClipRepositoryPort) is the
	// SSOT for production caller access. Composition root is the ONLY
	// site responsible for mapping source-name => concrete repo, because
	// the mapping is a deployment-time concern (the deployed clip-store
	// backend determines which concrete repo handles which source).
	//
	// Future operator action: if a new clip-store backend (e.g.
	// Qdrant-only or in-memory mock) appears, the change must happen
	// HERE, NOT in a consumer file. Check 45 will catch any out-of-band
	// re-introduction.

	clipsRepos := map[string]*sqassets.ClipsRepository{
		"youtube": repos.ClipsRepo,
		"stock":   repos.ClipsRepo,
		"artlist": repos.ClipsRepo,
	}
	resolverCfg := &assetindex.ResolverConfig{
		ClipsRepos:    clipsRepos,
		ImageRepo:     repos.ImageRepo,
		VoiceoverRepo: repos.VoiceoverRepo,
	}
	assetResolver := assetindex.NewResolver(assetIndexService, resolverCfg, log)

	return &SearchBundle{
		AssetIndexService: assetIndexService,
		AssetTreeService:  assetTreeService,
		AssetResolver:     assetResolver,
		ProviderRegistry:  providers.NewRegistry(),
	}, nil
}

// BuildUtilityBundle constructs the lightweight utility handlers
// and the health-check Service (PR1 Health boundary, June 2026).
//
// Wave A Item 31 (June 2026): driveAdmin is the only canonical path
// for the DriveProbe. The legacy token-file + raw HTTP GET fallback
// is REMOVED — when driveAdmin is nil (admin CLI path or Drive
// feature disabled), the DriveChecker is left nil and the health
// Service reports "applicable=false" for the Drive capability.
func BuildUtilityBundle(cfg *config.Config, db *storage.SQLiteDB, driveAdmin drive.Admin) *UtilityBundle {
	svc := buildHealthService(cfg, db, driveAdmin)
	return &UtilityBundle{
		Utility:       transport.NewUtilityHandler(),
		HealthService: svc,
		ReadyChecker:  systemhealth.NewReadyChecker(svc),
	}
}

// buildHealthService constructs the health.Service from infrastructure
// checkers. Lives here because it's the only place that wires concrete
// adapters (PR1 Health boundary, June 2026).
//
// PG-011 typed-handle migration (June 2026): the previous
// implementation unwrapped `db *storage.SQLiteDB` to `*sql.DB` via
// `var sqlDB *sql.DB; if db != nil { sqlDB = db.DB }` so it could hand
// a raw handle to infrahealth.NewSQLiteChecker / NewJobsChecker. The
// checkers now accept *storage.SQLiteDB directly (the underlying
// *sql.DB is reached via the embedded field), which removes the
// `database/sql` import from this file. The `db` arg may itself be
// nil — infrahealth.Checker constructors accept nil and the zero
// value remains safe.
func buildHealthService(cfg *config.Config, db *storage.SQLiteDB, driveAdmin drive.Admin) *systemhealth.Service {
	if cfg == nil {
		return nil
	}

	var driveChecker systemhealth.DriveChecker


	// QDRANT-005 Blocker 3 (June 2026): consolidated health+readiness.
	// HealthProbe satisfies BOTH the /health QdrantChecker contract AND the
	// auth wiring, or semantic drift between the two code paths.
	// When qdrant.enabled=false, the checker is nil (ServiceDeps handles nil
	// checkers gracefully — returns "not applicable").
	//
	// APIKey propagation: the qdrant.Client carries cfg.Qdrant.APIKey and
	// the probe sends X-Api-Key on every request via client.APIKey() — the
	// previous infrahealth.NewQdrantChecker(cfg.Qdrant.BaseURL, "", true)
	// hardcoded an empty API key (Phase 1 Blocker 1, now closed).
	var qdrantChecker systemhealth.QdrantChecker
	if cfg.Qdrant.Enabled {
		qdrantCfg := &qdrant.Config{
			BaseURL: cfg.Qdrant.BaseURL,
			APIKey:  cfg.Qdrant.APIKey,
			Timeout: cfg.Qdrant.Timeout,
		}
		probe := qdrant.NewHealthProbe(qdrant.NewClient(qdrantCfg, zap.NewNop()))
		qdrantChecker = probe
	}

	jobsChecker := infrahealth.NewJobsChecker(db)

	// codex/health-ready-contract (June 2026): wire RunnerProbe with an
	// in-memory heartbeat tracker (BrokerLastHeartbeat atomic.Int64).
	// The local broker's Heartbeat() updates this timestamp on every
	// successful DB write; this closure checks that the last heartbeat
	// is within the 60s staleness window.  A nil RunnerProbe means
	// "DB-only check" — this adapter adds goroutine-liveness.
	//
	// Staleness threshold: 60s. The heartbeat ticker runs every 25s;
	// 60s gives 2 full cycles of grace for slow DB writes.
	const heartbeatStaleness = 60 * time.Second
	jobsChecker.RunnerProbe = func(ctx context.Context) error {
		age := appjobs.BrokerHeartbeatAge()
		if age > int64(heartbeatStaleness.Seconds()) {
			return fmt.Errorf("broker heartbeat stale: last heartbeat %d seconds ago (threshold %ds)",
				age, int64(heartbeatStaleness.Seconds()))
		}
		return nil
	}

	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB:     infrahealth.NewSQLiteChecker(db),
		Drive:  driveChecker,
		Qdrant: qdrantChecker,
		Jobs:   jobsChecker,
	})
}

// buildBooksService (moved from build_bundles_books.go, Phase 5 consolidation, June 2026).
//
// F2.10 (June 2026): the `driveUploader *drive.Uploader` arg was dropped.
// Per AGENTS.md godlike/06 "one owner per fact" + F2.7/F2.8/F2.9
// closure precedent, every Drive write from the books capability
// routes through `delivery.Publisher` and every Drive read fans out
// through `drive.Reader` (refactored from the missed F2.10 file
// `books/drive.go::ProcessBookFromDrive` — the download path used the
// legacy `s.driveUpload` surface that `service.go` retired). Composition
// root threads only the Publisher + the Reader into books.NewService —
// the legacy `*drive.Uploader` plumbing is retired from
// internal/application/books/ entirely (override brutal).
func buildBooksService(cfg *config.Config, dbs *databases, log *zap.Logger, voiceoverSvc *voiceover.Service, publisher delivery.Publisher, reader drive.Reader) *books.Service {
	booksSvc := books.NewService(
		&books.Config{
			Enabled:       cfg.Books.Enabled,
			ScriptPath:    cfg.Books.ScriptPath,
			PythonBin:     cfg.Books.PythonBin,
			DriveFolderID: cfg.Drive.BooksFolder(),
		},
		dbs.main.DB, cfg.Drive.BooksFolder(), log,
		voiceoverSvc, publisher, reader,
	)
	log.Info("Books service initialized", zap.Bool("enabled", cfg.Books.Enabled))
	return booksSvc
}

// buildImagesService (moved from build_bundles_images.go, Phase 5 consolidation, June 2026).
func buildImagesService(
	ctx context.Context, cfg *config.Config, log *zap.Logger,
	driveUploader *drive.Uploader, clipsRepo *sqassets.ClipsRepository, artlistRepo *sqassets.ClipsRepository,
	styleRegistry *generation.StyleRegistry, scriptGen *ollama.Generator,
	mediaStore *drive.Store, imageRepo *sqassets.ImagesRepository,
	voMetaWriter *semantic.MetadataWriter,
	ingestSvc *ingest.Service,
	dispatcher *outbox.Dispatcher,
) (*imgservice.Service, *semantic.MetadataWriter) {
	var driveSvc *gdrive.Service
	if driveUploader != nil {
		driveSvc = driveUploader.Service
	}
	imageService := imgservice.NewService(imgservice.ImagesDeps{
		Core: imgservice.ImagesCoreDeps{Cfg: cfg, Log: log},
		Storage: imgservice.ImagesStorageDeps{
			ImageRepo:  imageRepo,
			ClipsRepo:  clipsRepo,
			DriveSvc:   driveSvc,
			MediaStore: mediaStore,
		},
		GenAI: imgservice.ImagesGenAIDeps{
			LLMGen:        scriptGen,
			MetaWriter:    voMetaWriter,
			StyleRegistry: styleRegistry,
			ImageGen:      imgservice.NewChromeImageProvider(cfg.Paths.PythonScriptsDir, 3, log),
			NvidiaCfg:     imgservice.NvidiaConfig{APIKey: cfg.External.NvidiaAPIKey, Model: cfg.External.NvidiaModel},
			RemoteImageURL: cfg.External.RemoteImageEndpointURL,
		},
		External: imgservice.ImagesExternalDeps{
			IngestSvc:    ingestSvc,
			Dispatcher:   dispatcher,
			VeloxBaseURL: cfg.External.VeloxBaseURL,
			GACfg: imgservice.GoogleAccountingConfig{
				ServerURL:     cfg.GoogleAccounting.ServerURL,
				DownloadDir:   cfg.GoogleAccounting.DownloadDir,
				VidsProjectID: cfg.GoogleAccounting.VidsProjectID,
			},
		},
	})
	_ = ctx
	return imageService, voMetaWriter
}
