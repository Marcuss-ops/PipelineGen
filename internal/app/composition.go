// Package app — composition root decomposed into capability bundles (PR4).
//
// This file replaces the previous `type services struct` (in dependencies.go)
// and `CoreDeps` (in bootstrap.go) with a layered set of focused bundles:
//
//	┌─────────────────────────────────────────────────────────────┐
//	│                      ComposeRoot                              │
//	│   DB · child bundles                                         │
//	└──────┬──────────────────────────────────────────────────────┘
//	       │
//	┌──────┴──────────────────────────────────────────────────────┐
//	│  DriveBundle │ RepoBundle │ SearchBundle │ ProcessBundle     │
//	│  AIBundle    │ DomainBundle│ JobsBundle  │ OutboxBundle     │
//	│  SyncBundle  │ MaintBundle │ UtilityBundle                  │
//	└─────────────────────────────────────────────────────────────┘
//
// Each Wire*Module() takes a NARROW subset (≤10 deps) by constructor injection,
// not the whole root.
//
// Construction is pure — NO cross-injections, NO setters. All dependencies
// are wired via constructor injection. ProviderRegistry.Freeze happens in
// WireRegistry after adapter registrations; bundle boundaries are stable.
//
// Bundle constructors live in per-bundle files under
// `internal/app/build_<bundle>.go` (commit ci/composition-split wave-1 of
// the 5-commit refactor for problem #8). BuildDriveBundle +
// startDriveBackgroundFolders moved to `build_drive_bundle.go`.
// composition.go retains the bundle type definitions, NewComposition,
// helper functions (configOnlyDestinations, buildIngestService, etc.),
// and the BuildUtilityBundle + buildHealthService helpers.
//
// Lifecycle (lifecycle.go) and Shutdown (shutdown.go) operate on the
// assembled ComposeRoot.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"
	infrahealth "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/health"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	associationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/association"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"

	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/embeddings"
	pkgffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	ytinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/videomuscles"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// ── Bundle types (≤10 fields each) ───────────────────────────────────────

// DriveBundle owns all Google Drive adapters + the derivation of the
// MediaStore/DestResolver + StyleRegistry. Other bundles that need a Drive
// reach for these.
//
// The bundle BODY is constructed in build_drive_bundle.go (split out in
// commit ci/composition-split wave-1 of problem #8's refactor); this file
// only declares the type so NewComposition can hold a *DriveBundle pointer.
type DriveBundle struct {
	DriveClient   *gdrive.Service
	DriveUploader *drive.Uploader
	DocClient     drive.DocClient
	DriveDests    *DriveDestinations
	MediaStore    *drive.Store
	DestResolver  asset.Resolver
	StyleRegistry *generation.StyleRegistry
}

// RepoBundle owns all SQLite-backed repositories that are NOT specific to a
// capability bundle. PR4.A (June 2026) relocated MemoryRepo to AIBundle
// since it's only consumed by the AI/memory pipeline (BuildAIBundle +
// startGemmaMemorySweeper); the move eliminates the redundant RepoBundle
// entry and keeps the ownership aligned with its single consumer.
type RepoBundle struct {
	ScriptsRepo   *sqlitescripts.ScriptRepository
	ImageRepo     *assets.ImagesRepository
	ClipsRepo     *assets.ClipsRepository
	Assets        *asset.Service
	MonitorsRepo  *assets.MonitorsRepository
	VoiceoverRepo *assets.VoiceoversRepository
	CatalogRepo   *catalog.Repository
	SQRepo        *assets.SearchQueriesRepository
}

// SearchBundle holds the asset metadata search/index pair and resolver.
// ProviderRegistry also lives here — it's an asset-search dispatch table
// (artlist/youtube/stock adapters), not a Drive sync concern. WireRegistry
// performs Freeze() at end after adapter registrations.
type SearchBundle struct {
	AssetIndexService *assetindex.Service
	AssetTreeService  *assettree.Service
	AssetResolver     *assetindex.Resolver
	ProviderRegistry  *providers.Registry
}

// ProcessBundle holds the heavy media-processing adapters.
//
// commit ci/composition-split wave-2 (planned, June 2026): BuildProcessBundle
// + startQdrantCollection are about to be extracted to
// `build_process_bundle.go` and `vectorSvc.SetRetryPolicy` eliminated.
type ProcessBundle struct {
	MediaProcessor     asset.Processor
	ClipIndexerService *clipindexer.Service
	VectorSvc          *qdrant.Service
	VLMClient          *vlm.Client
}

// AIBundle owns script generation, engine, and the configuration pieces
// other bundles need at composition time. Notes:
//   - StyleRegistry lives ONLY on DriveBundle (loaded at top of BuildDriveBundle
//     so ensureStyleDriveFolders can call it before AI is constructed).
//     Domain code that needs StyleRegistry reads it via root.Drive.StyleRegistry.
//     PR4.A (June 2026) dropped the AI-side mirror; consumers now reference
//     drive.StyleRegistry directly.
//   - MemoryRepo is constructed inside BuildAIBundle (dbs.main.DB) and exposed
//     on this bundle so the single consumer (startGemmaMemorySweeper in
//     lifecycle.go) reads root.AI.MemoryRepo directly without going through
//     RepoBundle. PR4.A (June 2026) relocated it from RepoBundle.
//   - ScriptFlowHandler is NOT carried here — it is constructed inside
//     registry.go::WireRegistry with the canonical batchSvc/curationSvc
//     deps (real voiceoverSvc from DomainBundle + root.Jobs.Service).
//     Root.AI.ScriptFlowHandler has zero external readers (verified via
//     grep across internal/, cmd/, docs/ — PR4-H followup-3, June 2026).
//
// commit ci/composition-split wave-3 (planned): `ollamaClient.SetNvidiaConfig +
// SetWebSearcher` + `scriptGen.SetTranslationCache` replaced with ctor args.
type AIBundle struct {
	OllamaClient  *client.Client
	ScriptGen     *ollama.Generator
	MemoryRepo    *gemmamemory.Repository
	MemoryService *gemmamemory.Service
	ScriptEngine  *scriptcore.Engine
}

// DomainBundle is everything media-specific that lives at the application
// layer. Currently 11 fields — wave-3 splits AssocService out to ProcessBundle
// (or a dedicated type) to honour the ≤10-fields convention from AGENTS.md.
type DomainBundle struct {
	YoutubeClipService *youtube.Service
	VoiceoverService   *voiceover.Service
	VoiceoverSync      *voiceoversync.Service
	ImageService       *imgservice.Service
	IngestService      *ingest.Service
	BooksService       *books.Service
	LessonsService     *lessonsSvc.Service
	MetaWriter         *semantic.MetadataWriter
	RealtimeService    *realtime.Service
	AutotagService     *autotag.Service
	AssocService       *associationpkg.Service
}

// OutboxBundle aggregates the canonical ingestion-path outbox dispatcher and
// the outbox_events.Pool.
//
// commit ci/composition-split wave-3: BuildOutboxBundle + startOutboxEventsPool
// → build_outbox_bundle.go.
type OutboxBundle struct {
	Dispatcher     *outbox.Dispatcher
	EventsRepo     *outboxevents.Repository
	EventsRegistry *outboxevents.HandlerRegistry
	EventsPool     *outboxevents.Pool
}

// SyncBundle owns ONLY the catalog→Drive sync. ProviderRegistry moved to
// SearchBundle (PR4 review): it's a search/dispatch concern, not Drive sync.
//
// commit ci/composition-split wave-3: BuildSyncBundle → build_sync_bundle.go +
// `catalogSync.SetDispatcher` replaced with ctor arg.
type SyncBundle struct {
	CatalogSync *catalogsync.Service
}

// MaintBundle owns the periodic maintenance + deletion services.
//
// commit ci/composition-split wave-3: BuildMaintBundle → build_maint_bundle.go.
type MaintBundle struct {
	MaintenanceSvc *maintenance.Service
	DeletionSvc    *deletion.DeletionService
}

// UtilityBundle owns the lightweight non-domain HTTP utility handlers.
//
// commit ci/composition-split wave-4: BuildUtilityBundle → build_utility_bundle.go.
type UtilityBundle struct {
	Utility       *common.UtilityHandler
	HealthService *systemhealth.Service
}

// ComposeRoot is the assembled root tree. NewComposition returns this.
// Cleanup is nil — shutdown.go's buildCleanup is the single source of truth
// for teardown (constructor-aware LIFO orchestration).
//
// Ctx is the bootstrap-time context (carried by NewComposition). It is
// needed by WireRegistry to pass it as the first arg to Wire<Module>()s
// that still accept a context (e.g. WireArtlist, which uses ctx to ensure
// the clipcatalog schema). PR4d-final (June 2026) replaces this field with
// root.ExposedCtx(); keeping it on ComposeRoot keeps the API symmetric
// between WireServices (caller) and Wire<Module>() (callees).
//
// Utility.HealthService is the per-request health Service consumed by the
// router (thin-transport healthHandler). It is wired in BuildUtilityBundle
// so the router can construct healthHandler from root.Utility.HealthService.
type ComposeRoot struct {
	DB *storage.SQLiteDB

	Drive   *DriveBundle
	Repos   *RepoBundle
	Search  *SearchBundle
	Process *ProcessBundle

	AI      *AIBundle
	Domains *DomainBundle
	Jobs    *JobsBundle
	Outbox  *OutboxBundle
	Sync    *SyncBundle
	Maint   *MaintBundle
	Utility *UtilityBundle

	// DriveStart is the deferred side-effect closure extracted from
	// BuildDriveBundle (PR9-A, June 2026). It ensures Drive folders,
	// validates critical paths, and creates storage directories.
	// Invoked by the lifecycle AFTER WireRegistry so all modules are
	// wired before background Drive I/O begins.
	DriveStart IOpaqueStartFunc

	// OutboxStart is the deferred side-effect closure extracted from
	// BuildOutboxBundle (PR9-B, June 2026). It starts the outbox events
	// pool and registers a shutdown goroutine.
	// Invoked by the lifecycle AFTER WireRegistry alongside DriveStart.
	OutboxStart IOpaqueStartFunc

	// ProcessStart is the deferred side-effect closure extracted from
	// BuildProcessBundle (PR9-C, June 2026). It ensures the Qdrant
	// vector collection exists (idempotent, best-effort).
	// Invoked by the lifecycle AFTER WireRegistry alongside other
	// deferred-start closures.
	ProcessStart IOpaqueStartFunc

	Ctx context.Context
}

// IOpaqueStartFunc is the opaque type for deferred initialisation closures
// returned by Build*Bundle constructors (PR9 series, June 2026).
// BuildDriveBundle is the first adopter; other bundles followed in
// PR9-B/C; commit ci/composition-split retains the IOpaqueStartFunc for
// backward source compatibility with NewComposition's gather-then-fire
// pattern.
type IOpaqueStartFunc func()

// ── Bundle constructors (per-bundle files under build_<bundle>.go) ───────

// BuildDriveBundle + startDriveBackgroundFolders live in build_drive_bundle.go
// (split out in commit ci/composition-split wave-1 of problem #8).
// BuildRepoBundle, BuildSearchBundle, BuildProcessBundle, BuildAIBundle,
// BuildDomainBundle, BuildOutboxBundle, BuildSyncBundle, BuildMaintBundle,
// BuildUtilityBundle, buildIngestService, buildHealthService stay here for
// incremental migration (waves 2-5). The end state of the 5-commit refactor
// leaves composition.go with ONLY NewComposition + the bundle types + IOpaqueStartFunc.

// resolveRuntimeDestinations is split out so BuildDriveBundle can fall back
// to configOnlyDestinations when Drive is offline. wave-5 will DELETE this
// function (proven no-op): until then it remains as a thin alias for the
// in-flight ensureResolveDrive calls.
func resolveRuntimeDestinations(ctx context.Context, db *sql.DB, driveClient *gdrive.Service, cfg *config.Config, log *zap.Logger) *DriveDestinations {
	_ = ctx
	_ = db
	_ = driveClient
	_ = log
	return configOnlyDestinations(cfg)
}

// BuildRepoBundle constructs the canonical Repositories.
func BuildRepoBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*RepoBundle, error) {
	_ = ctx
	_ = cfg
	assetsStore := asset.NewAssetStoreSQLite(dbs.main.DB, log)
	assetsSvc := asset.NewService(assetsStore, log)
	imageRepo := assets.NewImagesRepository(dbs.main.DB)
	voiceoverRepo := assets.NewVoiceoversRepository(dbs.main.DB)
	monitorsRepo := assets.NewMonitorsRepository(dbs.main.DB)
	clipsRepo := assets.NewClipsRepositoryCanonical(dbs.main.DB, log, assetsSvc.Repository())
	catalogRepo := catalog.NewRepository(clipsRepo, clipsRepo, clipsRepo)
	scriptsRepo := sqlitescripts.NewScriptRepository(dbs.main.DB)
	sqRepo := assets.NewSearchQueriesRepository(dbs.main.DB)

	return &RepoBundle{
		ScriptsRepo:   scriptsRepo,
		ImageRepo:     imageRepo,
		ClipsRepo:     clipsRepo,
		Assets:        assetsSvc,
		MonitorsRepo:  monitorsRepo,
		VoiceoverRepo: voiceoverRepo,
		CatalogRepo:   catalogRepo,
		SQRepo:        sqRepo,
	}, nil
}

// BuildSearchBundle builds the asset metadata search index + tree + resolver.
func BuildSearchBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle) (*SearchBundle, error) {
	_ = ctx
	_ = cfg
	assetIndexRepo := assetindex.NewRepository(dbs.main.DB)
	assetIndexService := assetindex.NewService(assetIndexRepo)

	assetTreeRepo, err := assets.NewAssetTreeRepository(dbs.main.DB, log)
	if err != nil {
		return nil, fmt.Errorf("init asset tree repository: %w", err)
	}
	assetTreeService := assettree.NewService(assetTreeRepo, log)
	if assetTreeService == nil {
		return nil, fmt.Errorf("assettree service is nil after construction")
	}

	clipsRepos := map[string]*assets.ClipsRepository{
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

// BuildProcessBundle builds media-processing adapters. VectorSvc nil when
// VectorSearch is disabled. driveUploader passed in directly.
//
// PR9-C (June 2026): BuildProcessBundle returns an IOpaqueStartFunc closure
// that defers the Qdrant EnsureCollection call to the lifecycle.
// The bundle itself is fully populated on return (VectorSvc and
// ClipIndexerService are complete).
//
// commit ci/composition-split wave-2 (planned): extracted to
// build_process_bundle.go + `vectorSvc.SetRetryPolicy` replaced with
// retry fields absorbed in qdrant.NewService Config.
func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, driveUploader *drive.Uploader) (*ProcessBundle, IOpaqueStartFunc, error) {
	mediaProcessor := initMediaProcessor(cfg, dbs.main.DB, repos.Assets.Repository(), repos.Assets,
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
	}, dbs.main.DB, dbs.main.Path(), log)

	var vectorSvc *qdrant.Service
	if cfg.VectorSearch.Enabled {
		qdrantCfg := qdrant.Config{
			URL:                  cfg.VectorSearch.URL,
			Collection:           cfg.VectorSearch.Collection,
			TextVectorName:       cfg.VectorSearch.TextVectorName,
			VisualVectorName:     cfg.VectorSearch.VisualVectorName,
			AudioVectorName:      cfg.VectorSearch.AudioVectorName,
			TranscriptVectorName: cfg.VectorSearch.TranscriptVectorName,
			SparseVectorName:     cfg.VectorSearch.SparseVectorName,
			TextDimensions:       cfg.VectorSearch.TextDimensions,
			VisualDimensions:     cfg.VectorSearch.VisualDimensions,
			AudioDimensions:      cfg.VectorSearch.AudioDimensions,
			TranscriptDimensions: cfg.VectorSearch.TranscriptDimensions,
			MinInstantScore:      cfg.VectorSearch.MinInstantScore,
			TimeoutMs:            cfg.VectorSearch.TimeoutMs,
			CollectionVersion:    cfg.VectorSearch.CollectionVersion,
			CollectionAlias:      cfg.VectorSearch.CollectionAlias,
			DisableAlias:         cfg.VectorSearch.DisableAlias,
		}
		if cfg.VectorSearch.CollectionVersion != "" {
			mode := "alias-routed"
			if cfg.VectorSearch.DisableAlias {
				mode = "versioned-direct"
			}
			log.Info("Qdrant collection versioning enabled",
				zap.String("collection", cfg.VectorSearch.Collection),
				zap.String("version", cfg.VectorSearch.CollectionVersion),
				zap.String("alias", cfg.VectorSearch.CollectionAlias),
				zap.String("routing", mode))
		}
		qdrantClient := qdrant.NewQdrantClient(qdrantCfg)
		vectorSvc = qdrant.NewService(qdrantClient, qdrantCfg, log)
		vectorSvc.SetRetryPolicy(
			cfg.VectorSearch.RetryAttempts,
			time.Duration(cfg.VectorSearch.RetryInitialWaitMs)*time.Millisecond,
			time.Duration(cfg.VectorSearch.RetryMaxWaitMs)*time.Millisecond,
		)
		log.Info("Qdrant retry policy applied",
			zap.Int("attempts", cfg.VectorSearch.RetryAttempts),
			zap.Int("initial_wait_ms", cfg.VectorSearch.RetryInitialWaitMs),
			zap.Int("max_wait_ms", cfg.VectorSearch.RetryMaxWaitMs),
		)
		// PR9-C (June 2026): EnsureCollection is deferred to the closure.
		// clipIndexerAdapter wiring stays here (pure construction).
		clipIndexerAdapter := qdrant.NewClipIndexerAdapter(dbs.main.DB, vectorSvc, qdrantCfg, log)
		if clipIndexerAdapter != nil {
			clipIndexerService.SetVectorStore(clipIndexerAdapter)
			log.Info("vector store enabled for clip indexer")
		}
	}

	// PR9-C (June 2026): side-effecting Qdrant collection setup is deferred
	// to startQdrantCollection (defined below).
	startClosure := func() {
		startQdrantCollection(ctx, vectorSvc, log)
	}

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: clipIndexerService,
		VectorSvc:          vectorSvc,
		VLMClient:          vlmClient,
	}, startClosure, nil
}

// startQdrantCollection performs the side-effecting Qdrant collection
// setup that was previously inlined in BuildProcessBundle (PR9-C, June 2026).
// It calls EnsureCollection which is idempotent — if the collection already
// exists the call is a fast no-op.
//
// Invoked by the lifecycle after WireRegistry completes, before the HTTP
// server begins accepting requests.
func startQdrantCollection(
	ctx context.Context,
	vectorSvc *qdrant.Service,
	log *zap.Logger,
) {
	if vectorSvc == nil {
		return
	}
	if err := vectorSvc.EnsureCollection(ctx); err != nil {
		log.Warn("vector store collection setup failed (will retry on upsert)", zap.Error(err))
	}
}

// BuildAIBundle constructs the LLM/script/memory stack. Uses Drive.DocClient
// and Drive.DriveUploader (which were constructed earlier).
// PR4.A (June 2026): MemoryRepo is created here (dbs.main.DB), not in BuildRepoBundle,
// so that the single consumer (startGemmaMemorySweeper) reads it from root.AI
// without going through RepoBundle.
//
// commit ci/composition-split wave-3: extracted to build_ai_bundle.go +
// `ollamaClient.SetNvidiaConfig + SetWebSearcher + scriptGen.SetTranslationCache`
// replaced with ctor args/options.
func BuildAIBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, drive *DriveBundle) (*AIBundle, error) {
	_ = ctx
	_ = drive
	ollamaClient := client.NewClient(cfg.External.OllamaURL, cfg.External.OllamaModel, cfg.External.OllamaTimeoutSeconds)
	ollamaClient.SetNvidiaConfig(cfg.External.UseNvidiaForLLM, cfg.External.NvidiaAPIKey, cfg.External.NvidiaLLMModel)

	if cfg.External.SearxngURL != "" {
		ws := client.NewWebSearcher(cfg.External.SearxngURL, cfg.External.SearxngMaxResults)
		ollamaClient.SetWebSearcher(ws)
		log.Info("SearXNG web search enabled for LLM context",
			zap.String("searxng_url", cfg.External.SearxngURL),
			zap.Int("max_results", cfg.External.SearxngMaxResults),
		)
	}

	scriptGen := ollama.NewGenerator(ollamaClient)
	translationCache := sqlitescripts.NewCache(dbs.main.DB)
	scriptGen.SetTranslationCache(translationCache)
	log.Info("translation cache initialized", zap.String("db", dbs.main.Path()))

	memoryRepo := gemmamemory.NewRepository(dbs.main.DB)
	memorySvc := gemmamemory.NewService(memoryRepo, log)
	log.Info("Gemma Memory Gate service initialized")

	scriptsRepoAdapter := scriptcore.NewRepositoryAdapter(repos.ScriptsRepo)
	engine := scriptcore.NewEngine(scriptGen, memorySvc, scriptsRepoAdapter, log)

	return &AIBundle{
		OllamaClient:  ollamaClient,
		ScriptGen:     scriptGen,
		MemoryRepo:    memoryRepo,
		MemoryService: memorySvc,
		ScriptEngine:  engine,
	}, nil
}

// BuildDomainBundle builds the media-domain services.
//
// commit ci/composition-split wave-3: extracted to build_domain_bundle.go +
// `DomainBundle.AssocService` separated out (decreasing field count from
// 11 → 10 per AGENTS.md).
func BuildDomainBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, ai *AIBundle) (*DomainBundle, error) {
	clipsRegistry := artifacts.NewClipsRegistry(
		dbs.main.DB,
		repos.Assets.Repository(),
		repos.Assets,
		repos.Assets.LocationRepository(),
		repos.Assets.ProcessingRepository(),
	)
	youtubeLifecycle := NewLifecycleFromDeps(&LifecycleDeps{
		Registry:    clipsRegistry,
		DriveClient: drive.DriveClient,
		AssetIndex:  search.AssetIndexService,
	}, log)

	voMetaWriter := semantic.NewMetadataWriter(
		cfg.Paths.PythonScriptsDir,
		cfg.Storage.TempPath(),
		cfg.External.OllamaURL,
		cfg.External.OllamaModel,
		log,
	)

	clipProcessor := pkgffmpeg.NewFromConfig(cfg)
	videoPipeline := videomuscles.NewPipeline(cfg, log, clipProcessor)
	videoPipelineAdapter := ytinfra.NewVideoPipelineAdapter(videoPipeline)

	folderMemSvc := foldermemory.NewService(log, repos.ClipsRepo)
	metaFetcher := ytinfra.NewMetadataFetcherAdapter(cfg, nil)
	driveFolderMgr := newDriveFolderMgrAdapter(drive.DriveUploader, log)

	var clipIndexerAdapterValue youtubeports.ClipIndexerPort
	if process.ClipIndexerService != nil {
		clipIndexerAdapterValue = &clipIndexerAdapter{inner: process.ClipIndexerService}
	}

	searchRunnerAdapter := ytinfra.NewSearchRunnerAdapter(cfg, log)
	if searchRunnerAdapter == nil {
		return nil, fmt.Errorf("compose domains: youtube SearchRunnerPort nil (cfg or log missing — fail-closed per PR2)")
	}
	if portutil.IsNilPort(searchRunnerAdapter) {
		return nil, fmt.Errorf("compose domains: youtube SearchRunnerPort typed-nil (portutil.IsNilPort true — fail-closed per PR2)")
	}

	youtubeClipService := youtube.NewService(youtube.ServiceDeps{
		Cfg:               cfg,
		Log:               log,
		MediaProcessor:    process.MediaProcessor,
		VideoPipeline:     videoPipelineAdapter,
		LifecycleService:  youtubeLifecycle,
		AssetDestResolver: drive.DestResolver,
		AssetRepo:         repos.Assets.Repository(),
		Clips:             newClipStoreAdapter(repos.ClipsRepo),
		Monitors:          newMonitorsStoreAdapter(repos.MonitorsRepo),
		CacheStore:        newCacheStoreAdapter(repos.ClipsRepo),
		Indexer:           clipIndexerAdapterValue,
		Ollama:            ai.OllamaClient,
		MetaFetcher:       metaFetcher,
		DriveFolderMgr:    driveFolderMgr,
		FolderMemory:      newFolderMemoryAdapter(folderMemSvc),
		SearchRunner:      searchRunnerAdapter,
	})

	voiceoverSvc, voiceoverRepo := initVoiceoverService(ctx, cfg, dbs, log,
		drive.DriveClient, drive.DriveUploader,
		search.AssetIndexService, process.ClipIndexerService,
		drive.DestResolver,
		voMetaWriter, ai.ScriptGen,
	)

	booksSvc := initBooksService(cfg, dbs, log, drive.DriveUploader, voiceoverSvc)

	ingestSvc := buildIngestService(cfg, log, dbs, drive.DriveClient, repos, search)

	imageSvc, metaWriter := initImageService(ctx, cfg, log,
		drive.DriveClient, repos.ClipsRepo, repos.ClipsRepo,
		drive.StyleRegistry, ai.ScriptGen,
		drive.MediaStore, process.VectorSvc, repos.ImageRepo,
		voMetaWriter, ingestSvc,
	)

	var realtimeSvc *realtime.Service
	if cfg.VectorSearch.Enabled && cfg.VectorSearch.RealtimeEnabled && process.VectorSvc != nil {
		embedder := realtime.NewPythonEmbeddingAdapter(cfg.ClipIndexer.ServerURL)
		rerankerClient := reranker.NewClient(reranker.Config{
			Enabled:   cfg.Reranker.Enabled,
			URL:       cfg.Reranker.URL,
			Model:     cfg.Reranker.Model,
			TopK:      cfg.Reranker.TopK,
			TimeoutMs: cfg.Reranker.TimeoutMs,
			Weight:    cfg.Reranker.Weight,
		})
		realtimeSvc = realtime.NewService(process.VectorSvc, embedder, nil, rerankerClient,
			cfg.Reranker, &cfg.VectorSearch, repos.ClipsRepo, nil, log)
		log.Info("real-time matching service enabled",
			zap.Bool("reranker_enabled", cfg.Reranker.Enabled),
			zap.Int("reranker_top_k", cfg.Reranker.TopK),
			zap.Int("reranker_timeout_ms", cfg.Reranker.TimeoutMs),
		)
	}

	var autotagVectorStore clipindexer.VectorStoreIndexer
	if process.ClipIndexerService != nil {
		autotagVectorStore = process.ClipIndexerService.VectorStore()
	}
	autotagSvc := autotag.NewService(dbs.main.DB, repos.Assets.Repository(), process.VLMClient, autotagVectorStore, log)

	embedder := embeddings.NewPythonScriptEmbedder("python3", cfg.Paths.PythonScriptsDir)
	assocService := associationpkg.NewService(
		cfg.Storage.DataDir, "node-scraper", cfg.Paths.PythonScriptsDir,
		repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo, repos.CatalogRepo,
		qdrant.NewSearchAdapter(process.VectorSvc), embedder,
	)
	log.Info("embedding.Embedder injected into association service (infrastructure/embeddings/python)")
	if process.VectorSvc != nil {
		log.Info("vector store wired into association service for hybrid search")
	}

	lessonsS := lessonsSvc.NewService(
		&lessonsSvc.LessonsConfig{
			Enabled:             cfg.Lessons.Enabled,
			DefaultModel:        cfg.Lessons.DefaultModel,
			DefaultTone:         cfg.Lessons.DefaultTone,
			DefaultLanguage:     cfg.Lessons.DefaultLanguage,
			DefaultImageModel:   cfg.Lessons.DefaultImageModel,
			MaxParallelChapters: cfg.Lessons.MaxParallelChapters,
			OllamaURL:           cfg.External.OllamaURL,
		},
		ai.ScriptGen, imageSvc, drive.DocClient, log,
	)
	log.Info("Lessons service initialized", zap.Bool("enabled", cfg.Lessons.Enabled))

	var vosyncSvc *voiceoversync.Service
	if voFolder := cfg.Drive.VoiceoverFolder(); voFolder != "" && voiceoverRepo != nil {
		vosyncSvc = voiceoversync.NewService(drive.DriveUploader, voiceoverRepo, search.AssetTreeService, voFolder, log)
		log.Info("Voiceover sync service initialized", zap.String("root_folder_id", voFolder))
	}

	return &DomainBundle{
		YoutubeClipService: youtubeClipService,
		VoiceoverService:   voiceoverSvc,
		VoiceoverSync:      vosyncSvc,
		ImageService:       imageSvc,
		IngestService:      ingestSvc,
		BooksService:       booksSvc,
		LessonsService:     lessonsS,
		MetaWriter:         metaWriter,
		RealtimeService:    realtimeSvc,
		AutotagService:     autotagSvc,
		AssocService:       assocService,
	}, nil
}

// BuildOutboxBundle constructs the canonical ingestion outbox + outbox_events.Pool.
//
// PR9-B (June 2026): BuildOutboxBundle returns an IOpaqueStartFunc closure
// that defers the outbox events pool goroutines (Start + shutdown) to the
// lifecycle. The bundle itself is fully populated on return.
//
// commit ci/composition-split wave-3: extracted to build_outbox_bundle.go
// + startOutboxEventsPool split out as package-level function (preserved
// for back-compat during the wave-3 file split transition).
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
	outboxTxMgr := outbox.NewManager(dbs.main.DB, log)
	dispatcher := outbox.NewDispatcher(multiClipsUp, outboxEventsRepo, outboxTxMgr, log)
	log.Info("outbox dispatcher instantiated: canonical upsert+outbox_events enqueue path")

	eventsRegistry := outboxevents.NewHandlerRegistry()

	httpClient := &http.Client{Timeout: 30 * time.Second}

	var hmacSecrets [][]byte
	if cur := strings.TrimSpace(cfg.Security.DeliveryHMACSecret); cur != "" {
		hmacSecrets = append(hmacSecrets, []byte(cur))
	}
	if prev := strings.TrimSpace(cfg.Security.DeliveryHMACSecretPrevious); prev != "" {
		hmacSecrets = append(hmacSecrets, []byte(prev))
	}

	outboxDeps := &jobsoutbox.Deps{
		DB:          dbs.main.DB,
		HTTPClient:  httpClient,
		MetadataDir: cfg.Storage.FullPath("asset_metadata"),
		HMACSecrets: hmacSecrets,
		InsecureDev: cfg.Security.DeliveryInsecureDev,
		Jobs:        jobs.Service,
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

	startClosure := func() {
		startOutboxEventsPool(ctx, eventsPool, outboxEventsCfg, log)
	}

	return &OutboxBundle{
		Dispatcher:     dispatcher,
		EventsRepo:     outboxEventsRepo,
		EventsRegistry: eventsRegistry,
		EventsPool:     eventsPool,
	}, startClosure, nil
}

// startOutboxEventsPool performs the side-effecting outbox events pool
// initialisation. Preserved here for back-compat during the wave-3 file
// split (will move to build_outbox_bundle.go with BuildOutboxBundle).
func startOutboxEventsPool(
	ctx context.Context,
	eventsPool *outboxevents.Pool,
	cfg outboxevents.WorkerPollConfig,
	log *zap.Logger,
) {
	if eventsPool == nil {
		return
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
}

// BuildSyncBundle constructs ONLY the catalog→Drive sync. ProviderRegistry
// moved to BuildSearchBundle (PR4 review).
//
// commit ci/composition-split wave-3: extracted to build_sync_bundle.go +
// `catalogSync.SetDispatcher` replaced with ctor arg (`catalogsync.NewService`
// must accept dispatcher in its signature — pre-condition for wave-3).
func BuildSyncBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, search *SearchBundle, process *ProcessBundle, drive *DriveBundle, outbox *OutboxBundle) (*SyncBundle, error) {
	_ = ctx
	_ = cfg
	_ = dbs
	_ = repos
	syncTargets := buildSyncTargets(cfg, repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo)
	catalogSync := catalogsync.NewService(drive.DriveUploader, syncTargets,
		search.AssetIndexService, search.AssetTreeService, process.ClipIndexerService, log)
	if outbox != nil && outbox.Dispatcher != nil {
		catalogSync.SetDispatcher(outbox.Dispatcher)
	}

	return &SyncBundle{
		CatalogSync: catalogSync,
	}, nil
}

// BuildMaintBundle constructs the periodic maintenance + deletion services.
//
// commit ci/composition-split wave-3: extracted to build_maint_bundle.go.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle) (*MaintBundle, error) {
	_ = ctx
	deletionSvc := deletion.NewDeletionService(
		repos.ClipsRepo, repos.ClipsRepo, repos.ClipsRepo,
		repos.VoiceoverRepo, repos.ImageRepo,
		drive.DriveUploader, search.AssetTreeService, search.AssetIndexService, log,
	)
	maintenanceSvc := maintenance.NewService(cfg, log,
		search.AssetIndexService, search.AssetTreeService, deletionSvc,
		jobs.Service, dbs.main.DB,
	)
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		log.Warn("failed to register maintenance handler", zap.Error(err))
	}

	return &MaintBundle{
		MaintenanceSvc: maintenanceSvc,
		DeletionSvc:    deletionSvc,
	}, nil
}

// buildIngestService constructs the ingest.Service from the same deps
// that WireMediaIngest uses. Stays in composition.go (small helper) during
// the wave-3 incremental migration; will move to build_ai_bundle.go or
// build_domain_bundle.go when those waves extract.
func buildIngestService(cfg *config.Config, log *zap.Logger, dbs *databases, driveClient *gdrive.Service, repos *RepoBundle, search *SearchBundle) *ingest.Service {
	if driveClient == nil {
		return nil
	}
	if repos.ImageRepo == nil || repos.VoiceoverRepo == nil || repos.ClipsRepo == nil || search.AssetIndexService == nil {
		return nil
	}
	imagesRegistry := imgservice.NewRegistryAdapter(repos.ImageRepo, cfg.Storage.ImagesPath(), log)
	imagesLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: imagesRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voiceover.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: voiceoverRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())
	clipLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: clipRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())
	stockLifecycle := NewLifecycleFromDeps(&LifecycleDeps{Registry: stockRegistry, DriveClient: driveClient, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository())}, log)
	return ingest.NewService(cfg, log, driveClient, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
	})
}

// BuildUtilityBundle constructs the lightweight utility handlers
// and the health-check Service (PR1 Health boundary, June 2026).
//
// commit ci/composition-split wave-4: extracted to build_utility_bundle.go.
func BuildUtilityBundle(cfg *config.Config, db *storage.SQLiteDB) *UtilityBundle {
	return &UtilityBundle{
		Utility:       common.NewUtilityHandler(),
		HealthService: buildHealthService(cfg, db),
	}
}

// buildHealthService constructs the health.Service from infrastructure
// checkers. Lives in composition.go because it's the only place that
// wires concrete adapters (PR1 Health boundary, June 2026). Will move
// to build_utility_bundle.go in wave-4.
func buildHealthService(cfg *config.Config, db *storage.SQLiteDB) *systemhealth.Service {
	if cfg == nil {
		return nil
	}
	var sqlDB *sql.DB
	if db != nil {
		sqlDB = db.DB
	}

	var driveChecker systemhealth.DriveChecker
	credsPath := cfg.GetCredentialsPath()
	tokenPath := cfg.GetTokenPath()
	if credsPath != "" && tokenPath != "" {
		driveChecker = infrahealth.NewDriveChecker(credsPath, tokenPath)
	}

	var qdrantChecker systemhealth.QdrantChecker
	if cfg.VectorSearch.Enabled {
		qdrantChecker = infrahealth.NewQdrantChecker(
			cfg.VectorSearch.URL,
			cfg.VectorSearch.Collection,
			cfg.VectorSearch.Enabled,
		)
	}

	return systemhealth.NewService(systemhealth.ServiceDeps{
		DB:     infrahealth.NewSQLiteChecker(sqlDB),
		Drive:  driveChecker,
		Qdrant: qdrantChecker,
		Jobs:   infrahealth.NewJobsChecker(sqlDB),
	})
}

// ── Orchestrator: NewComposition ─────────────────────────────────────────

// NewComposition composes all bundles in dependency order and returns the
// fully-assembled ComposeRoot. Cleanup is owned by shutdown.go.
//
// commit ci/composition-split wave-1 (June 2026): BuildDriveBundle +
// startDriveBackgroundFolders moved to build_drive_bundle.go. Late-bindings,
// JobsBundle helper, and the NewComposition orchestrator stay here. Waves
// 2-5 will incrementally move each Build*Bundle to its own file. End state
// (wave-5) leaves composition.go with ONLY NewComposition + IOpaqueStartFunc
// + the bundle type declarations.
func NewComposition(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*ComposeRoot, error) {
	repos, err := BuildRepoBundle(ctx, cfg, dbs, log)
	if err != nil {
		return nil, fmt.Errorf("compose repos: %w", err)
	}

	search, err := BuildSearchBundle(ctx, cfg, dbs, log, repos)
	if err != nil {
		return nil, fmt.Errorf("compose search: %w", err)
	}

	driveBundle, driveStart, err := BuildDriveBundle(ctx, cfg, dbs, log, search)
	if err != nil {
		return nil, fmt.Errorf("compose drive: %w", err)
	}

	process, processStart, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.DriveUploader)
	if err != nil {
		return nil, fmt.Errorf("compose process: %w", err)
	}

	jobs, err := BuildJobsBundle(dbs.main.DB, log)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
	}

	ai, err := BuildAIBundle(ctx, cfg, dbs, log, repos, driveBundle)
	if err != nil {
		return nil, fmt.Errorf("compose ai: %w", err)
	}

	domains, err := BuildDomainBundle(ctx, cfg, dbs, log, driveBundle, repos, search, process, ai)
	if err != nil {
		return nil, fmt.Errorf("compose domains: %w", err)
	}

	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, process, jobs)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	sync, err := BuildSyncBundle(ctx, cfg, dbs, log, repos, search, process, driveBundle, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose sync: %w", err)
	}

	maint, err := BuildMaintBundle(ctx, cfg, dbs, log, driveBundle, repos, search, jobs)
	if err != nil {
		return nil, fmt.Errorf("compose maintenance: %w", err)
	}

	utility := BuildUtilityBundle(cfg, dbs.main)

	// Late-bindings: jobs.RegisterHandler for domain services that opt in.
	if sync.CatalogSync != nil && jobs.Service != nil {
		sync.CatalogSync.RegisterHandler(jobs.Service)
		sync.CatalogSync.RegisterDriveFolderSyncHandler(jobs.Service)
	}
	if domains.YoutubeClipService != nil && jobs.Service != nil {
		domains.YoutubeClipService.RegisterHandler(jobs.Service)
	}
	if domains.VoiceoverService != nil && jobs.Service != nil {
		domains.VoiceoverService.RegisterHandler(jobs.Service)
	}
	if domains.BooksService != nil && jobs.Service != nil {
		domains.BooksService.RegisterJobHandler(jobs.Service)
	}
	if process.ClipIndexerService != nil && jobs.Service != nil {
		process.ClipIndexerService.RegisterJobHandler(jobs.Service)
	}
	if domains.LessonsService != nil && jobs.Service != nil {
		domains.LessonsService.RegisterJobHandler(jobs.Service)
	}

	root := &ComposeRoot{
		DB:      dbs.main,
		Drive:   driveBundle,
		Repos:   repos,
		Search:  search,
		Process: process,

		AI:      ai,
		Domains: domains,
		Jobs:    jobs,
		Outbox:  outbox,
		Sync:    sync,
		Maint:   maint,
		Utility: utility,

		DriveStart:   driveStart,
		OutboxStart:  outboxStart,
		ProcessStart: processStart,
		Ctx:          ctx,
	}

	// NOTE: ProviderRegistry (on SearchBundle) is intentionally UNFROZEN here.
	// WireRegistry performs the Freeze() once all adapter registrations have
	// happened. Freezing at this point would BREAK the late-bound adapter
	// wiring (Reviewer Q8 fix).
	return root, nil
}
