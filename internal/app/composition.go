// Package app — composition root decomposed into capability bundles.
//
// Bundle constructors live in per-bundle files under
// `internal/app/build_<bundle>.go` (PG-028, June 2026).
// composition.go retains the bundle type definitions and NewComposition.
// Lifecycle (lifecycle.go) and Shutdown (shutdown.go) operate on the
// assembled ComposeRoot.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	gdrive "google.golang.org/api/drive/v3"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	apiMw "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"
	mwidem "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── Bundle types (≤10 fields each) ───────────────────────────────────────

// DriveBundle owns all Google Drive adapters + the derivation of the
// MediaStore/DestResolver + StyleRegistry.
type DriveBundle struct {
	DriveClient   *gdrive.Service
	DriveUploader *drive.Uploader
	DocClient     drive.DocClient
	DriveDests    *DriveDestinations
	MediaStore    *drive.Store
	DestResolver  asset.Resolver
	StyleRegistry *generation.StyleRegistry
	// Publisher is the canonical Drive upload canal (FASE 3, June 2026).
	// All endpoints and jobs that write to Drive MUST use Publisher.Publish
	// instead of calling DriveUploader or FolderManager directly.
	Publisher delivery.Publisher
}

// RepoBundle owns all SQLite-backed repositories not specific to a
// capability bundle. MemoryRepo was relocated to AIBundle (PR4.A, June 2026).
// PR8 (June 2026): IdempotencyStore — canonical typed port (not a duck
// interface) so the composition layer's compile-time assertions catch
// port drift immediately.
type RepoBundle struct {
	ScriptsRepo      *sqlitescripts.ScriptRepository
	ImageRepo        *assets.ImagesRepository
	ClipsRepo        *assets.ClipsRepository
	Assets           *asset.Service
	MonitorsRepo     *assets.MonitorsRepository
	VoiceoverRepo    *assets.VoiceoversRepository
	CatalogRepo      *catalog.Repository
	SQRepo           *assets.SearchQueriesRepository
	IdempotencyStore mwidem.IdempotencyStore
}

// SearchBundle holds the asset metadata search/index pair and resolver.
// ProviderRegistry also lives here. WireRegistry performs Freeze() after
// adapter registrations.
type SearchBundle struct {
	AssetIndexService *assetindex.Service
	AssetTreeService  *assettree.Service
	AssetResolver     *assetindex.Resolver
	ProviderRegistry  *providers.Registry
}

// ProcessBundle holds the heavy media-processing adapters.
// QDRANT-003 (June 2026): IndexWriter and CollectionManager re-added for schema-versioned
// vector store with real embeddings (Schema v3: E5 768d, SigLIP 768d, CLAP 512d).
// QDRANT-004 (June 2026): VectorSvc added — search.VectorStorePort adapter for
// the mediasearch unified search API.
type ProcessBundle struct {
	MediaProcessor     asset.Processor
	ClipIndexerService *clipindexer.Service
	VLMClient          *vlm.Client
	// QDRANT-003 (June 2026): canonical Qdrant adapters. All fields
	// are sourced from qd.Runtime in BuildProcessBundle (PR 4) — there
	// is exactly ONE *qdrant.Client per process.
	CollectionManager *qdrant.CollectionManager
	// QdrantDeleter is the outbox.VectorPointDeleter port satisfied by
	// qd.Runtime.Writer. Renamed from qdrant.QdrantDeleter in PR 4
	// (was a duplicate infra-side interface; consolidated to the
	// application-layer port per AGENTS.md Pattern 0).
	QdrantDeleter jobsoutbox.VectorPointDeleter
	// QdrantRuntime is the canonical facade (PR 4, refactor/single-qdrant-runtime).
	// Exposed as a first-class field on ProcessBundle so callers can read
	// any subsystem directly via root.Process.QdrantRuntime.{Client,Writer,
	// Searcher,Manager,Health,Cleaner} without going through the
	// individually-named fields above. The individually-named fields are
	// kept (one-to-one with the named subsystems) for backward-compat
	// with wire_services.go + composition_test.go canary assertions;
	// they all point into the SAME *QdrantRuntime instance.
	QdrantRuntime *qdrant.QdrantRuntime
	// QDRANT-004 (June 2026): search.VectorStorePort for the mediasearch API.
	// Populated by BuildProcessBundle when Qdrant is enabled.
	// Wave 15 (June 2026): typed port per AGENTS.md Pattern 0 — replaces
	// `interface{}` carrier. Compile-time assertion at
	// internal/infrastructure/qdrant/search_adapter.go catches drift.
	VectorSvc assetsearch.VectorStorePort
	// QDRANT-005 Fase 1 (June 2026): direct *qdrant.Client for diagnostics
	// (CountPoints). Populated by BuildProcessBundle when Qdrant is enabled.
	QdrantClient *qdrant.Client
	// QDRANT-005 Fase 2 (June 2026): canonical QdrantHealthProbe.
	// Concretely typed (PR 4, June 2026, refactor/single-qdrant-runtime)
	// as *qdrant.HealthProbe — compile-time assertions at
	// internal/infrastructure/qdrant/health.go satisfy both the
	// readiness-barrier Probe contract AND the /health endpoint
	// healthport.QdrantChecker contract, so the loose `any` carrier
	// the pre-PR4 ProcessBundle field had is replaced. nil-safe when
	// Qdrant is disabled (use IsNil-checks at call sites; see
	// wire_services.go::WireServices which assigns into AppDeps).
	QdrantHealthProbe *qdrant.HealthProbe
	// QDRANT-005 Fase 3 (June 2026): canonical LocatorCleaner for
	// scrubbing legacy drive_link / local_path payload keys from
	// historical Qdrant points. Constructed alongside the client
	// when Qdrant is enabled; nil-safe when Qdrant is disabled.
	LocatorCleaner *qdrant.LocatorCleaner

	// QdrantSearcher is the canonical ANN searcher. Exposed so
	// wire_script.go can construct ClipSearchPort adapters without
	// importing qdrant infrastructure directly.
	QdrantSearcher *qdrant.Searcher
}

// QdrantDeps is the tiny pre-phase bundle of canonical Qdrant adapters
// that BuildOutboxBundle needs. Constructed by buildQdrantDeps BEFORE
// BuildOutboxBundle in composition.go::NewComposition so the
// OutboxBundle can be built BEFORE ProcessBundle (PR 8, June 2026).
//
// This split is the ring-break for the ProcessBundle ↔ OutboxBundle
// composition graph: BuildOutboxBundle reads qd.ClipIndexerService +
// qd.QdrantDeleter; BuildProcessBundle consumes outbox.OutboxBundle +
// qd after that and constructs MediaProcessor inline.
//
// The 3-field shape is deliberate — only what BuildOutboxBundle needs
// is exported from the pre-phase. The remaining ProcessBundle fields
// (VLMClient, CollectionManager, VectorSvc, QdrantSearcher, MediaProcessor)
// stay in BuildProcessBundle because none of them depend on
// outbox.Dispatcher — they can be constructed inline once BuildOutboxBundle
// returns the canonical dispatcher.
//
// PR 4 (June 2026, refactor/single-qdrant-runtime): the canonical
// *qdrant.QdrantRuntime is exposed via qd.Runtime so BuildProcessBundle
// can wire its ProcessBundle fields (CollectionManager, VectorSvc,
// QdrantSearcher, LocatorCleaner) from the SAME *Client and *IndexSchema
// that the outbox used. There is exactly ONE qdrant.NewClient(...) call
// per process — see composition_test.go::TestComposition_FrozenClientConstructionSites.
//
// QdrantDeleter is the canonical typed port per AGENTS.md Pattern 0.
// Back-compat alias: kept as a typed field on QdrantDeps so existing
// PR 3 composition tests (composition_test.go::TestComposition_QdrantEnabled*
// that read qd.QdrantDeleter) keep compiling. The type is now
// jobsoutbox.VectorPointDeleter (PR 4 consolidated the previous
// qdrant.QdrantDeleter and outbox.QdrantDeleter interface pair into
// this single application-layer port; the compile-time assertion in
// internal/infrastructure/qdrant/index_writer.go pins the conformance).
// Nil when Qdrant is disabled. BuildOutboxBundle's `if qd.QdrantDeleter != nil`
// guard absorbs nil safely.
type QdrantDeps struct {
	Runtime            *qdrant.QdrantRuntime
	ClipIndexerService *clipindexer.Service
	// Deprecated: prefer qd.Runtime.Writer for new code. Retained as
	// a typed back-compat alias so existing PR 3 fail-closed tests
	// (composition_test.go::TestComposition_QdrantEnabledNoClipIndexer_*
	// reads qd.QdrantDeleter) keep compiling. Removal planned for
	// follow-up PR-CASCADE-001 once the pre-existing
	// scripts/usecase/types_aliases.go cascade is cleared.
	QdrantDeleter jobsoutbox.VectorPointDeleter
}

// AIBundle owns script generation, engine, and memory.
// StyleRegistry lives on DriveBundle (PR4.A). MemoryRepo is constructed
// in BuildAIBundle. ScriptFlowHandler lives in registry.go::WireRegistry.
type AIBundle struct {
	OllamaClient  *client.Client
	ScriptGen     *ollama.Generator
	MemoryRepo    *adapters.Repository
	MemoryService *adapters.Service
	ScriptEngine  *scriptcore.Engine
}

// DomainBundle is everything media-specific that lives at the application layer.
type DomainBundle struct {
	YoutubeClipService *youtube.Service
	VoiceoverService   *voiceover.Service
	VoiceoverSync      *voiceoversync.Service
	ImageService       *imgservice.Service
	IngestService      *ingest.Service
	BooksService       *books.Service
	LessonsService     *lessonsSvc.Service
	MetaWriter         *semantic.MetadataWriter
	// Wave 15 (June 2026): RealtimeService split into two typed ports (the
	// realtime package was removed in commit d61068b3). Both slots stay
	// typed-nil at composition. Asset-side consumer
	// (assetsapi.NewRealtimeMatchHandler) reads RealtimeMatcher; script-side
	// consumer (ScriptFlowDeps.Realtime) reads RealtimeSearch.
	RealtimeMatcher assetsapi.RealtimeMatcher
	RealtimeSearch  scriptcore.RealtimeSearchService
	AutotagService  *autotag.Service
	// Wave 15 (June 2026): typed port — replaces `interface{}` carrier.
	// Compile-time enforcement replaces the runtime safety net that
	// build_bundles_domain.go used to need.
	AssocService scriptcore.AssocSearchService
	// Catena A P0 (June 2026): the typed-port GenerateJobHandler for
	// voiceover.generate jobs. Populated by BuildDomainBundle when
	// the full Drive/destResolver/outbox/lifecycle/repo/audio/db
	// chain is wired; nil when any link is missing.
	VoiceoverGenerateHandler *voiceoverjobs.GenerateJobHandler
}

// OutboxBundle aggregates the canonical ingestion-path outbox dispatcher and
// the outbox_events.Pool.
type OutboxBundle struct {
	Dispatcher     *outbox.Dispatcher
	EventsRepo     *outboxevents.Repository
	EventsRegistry *outboxevents.HandlerRegistry
	EventsPool     *outboxevents.Pool
}

// SyncBundle owns ONLY the catalog→Drive sync.
type SyncBundle struct {
	CatalogSync *catalogsync.Service
}

// MaintBundle owns the periodic maintenance + deletion services.
type MaintBundle struct {
	MaintenanceSvc *maintenance.Service
	DeletionSvc    *deletion.DeletionService
}

// UtilityBundle owns the lightweight non-domain HTTP utility handlers.
type UtilityBundle struct {
	Utility       *transport.UtilityHandler
	HealthService *systemhealth.Service
	ReadyChecker  *systemhealth.ReadyChecker
}

// ComposeRoot is the assembled root tree. NewComposition returns this.
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

	// DriveStart ensures Drive folders and creates storage dirs (PR9-A).
	DriveStart IOpaqueStartFunc
	// OutboxStart starts the outbox events pool (PR9-B).
	OutboxStart IOpaqueStartFunc
	// PR8 (June 2026): the single canonical idempotency middleware
	// instance — constructed once at WireRegistry and shared across
	// clips/MediaIngest/YouTubeClip handlers. Stop() must be called on
	// shutdown to halt the cleanup ticker goroutine. Lifecycle owned
	// by shutdown.go.
	IdempotencyMiddleware *apiMw.Idempotency

	Ctx context.Context
}

// IOpaqueStartFunc is the opaque type for deferred initialisation closures
// returned by Build*Bundle constructors (PR9 series, June 2026).
type IOpaqueStartFunc func() error

// ── Helpers ─────────────────────────────────────────────────────────────

// configOnlyDestinations builds *DriveDestinations from config only (no runtime resolution).
func configOnlyDestinations(cfg *config.Config) *DriveDestinations {
	return &DriveDestinations{MediaRoot: cfg.Drive.RootFolder(), SoundEffectsRoot: cfg.Drive.SoundEffectsRootFolder, imagesFolder: cfg.Drive.ImagesFolder()}
}

// ── Orchestrator: NewComposition ─────────────────────────────────────────

// buildQdrantDeps constructs the tiny pre-phase QdrantDeps bundle used
// by BuildOutboxBundle (QDRANT-003 — IndexWriter as QdrantDeleter + the
// canonical ClipIndexerService).
//
// PR 4 (June 2026, refactor/single-qdrant-runtime): the body now
// delegates Client/Writer/Searcher/Manager/Health/Cleaner/Mapper/Store
// construction to qdrant.NewRuntime — there is exactly ONE qdrant.NewClient
// call per process (was previously called from BOTH buildQdrantDeps and
// BuildProcessBundle; the second invocation created a redundant Client
// that wire responses could drift apart on under future changes).
//
// The 3 fields returned here are exactly what BuildOutboxBundle reads.
// The remaining Qdrant-derived adapters (CollectionManager, VectorSvc,
// LocatorCleaner, QdrantHealthProbe, QdrantSearcher, QdrantClient
// itself) stay in BuildProcessBundle — none of them depend on
// outbox.Dispatcher, so they can be constructed inline after
// BuildOutboxBundle returns the canonical dispatcher.
//
// Wire-time invariant: buildQdrantDeps MUST NOT start goroutines
// (composition_test.go::TestComposition_NoGoroutinesSpawned_FrozenSiteCount
// pins the no-spawn shape of every builder name matching Build\w+Bundle in
// source). clipindexer.NewService + qdrant.NewRuntime all return struct
// values without spawning goroutines, so this body is in scope.
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed): replaces the
// PR-7 deferred-hydration strategy (hydrateMediaProcessor +
// ProcessBundle.MediaProcessor=nil) with this strict-DAG pre-phase.
// Composition order is now: qdrantDeps(no deps) -> outbox(reads qd) ->
// process(reads outbox+qd) -> domains(reads process+outbox).
func buildQdrantDeps(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger) (*QdrantDeps, error) {
	_ = ctx
	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		MaxConcurrentIndexing: cfg.ClipIndexer.MaxConcurrentIndexing,
		DBPath:                dbs.main.Path(),
	}, dbs.main, dbs.main.Path(), log)

	var runtime *qdrant.QdrantRuntime
	if cfg.Qdrant.Enabled {
		var rerr error
		runtime, rerr = qdrant.NewRuntime(qdrant.RuntimeConfig{
			QdrantCfg: &qdrant.Config{
				BaseURL: cfg.Qdrant.BaseURL,
				APIKey:  cfg.Qdrant.APIKey,
				Timeout: cfg.Qdrant.Timeout,
			},
			DB:     dbs.main.DB,
			Logger: log,
		})
		if rerr != nil {
			return nil, fmt.Errorf("buildQdrantDeps: qdrant.NewRuntime: %w", rerr)
		}
		// PR 3 fix/qdrant-outbox-fail-closed (#3 from verdict Qdrant):
		// IndexWriter is now constructed when Qdrant is enabled, regardless
		// of the ClipIndexer sidecar's IsEnabled bit. The previous
		// `cfg.Qdrant.Enabled && clipIndexerService.IsEnabled()` AND-gate
		// silently dropped the QdrantDeleter port whenever the ClipIndexer
		// service was disabled — the IndexDeleteHandler then dead-lettered
		// every asset.index.delete_requested event because both
		// QdrantDeleter and the paired AssetDeleter slot were nil at
		// registration time. Decoupled semantics: Qdrant-enabled →
		// Qdrant and IndexWriter always present. ClipIndexer is the sidecar
		// path (writes via the AI server) and stays independent of the
		// outbox deletion path.
		if clipIndexerService.IsEnabled() {
			clipIndexerService.SetVectorStore(runtime.Writer)
			log.Info("QDRANT-003 PR4: IndexWriter (from QdrantRuntime) wired as clipindexer VectorStoreIndexer",
				zap.String("runtime_alias", runtime.Schema.RuntimeAlias))
		} else {
			log.Info("QDRANT-003 PR4: Qdrant enabled, ClipIndexer disabled — QdrantRuntime constructed for IndexDeleteHandler path; VectorStore not wired into clipindexer service")
		}
	} else {
		log.Info("QDRANT-003: Qdrant disabled — no QdrantRuntime wired (buildQdrantDeps pre-phase)")
	}

	qd := &QdrantDeps{
		Runtime:            runtime,
		ClipIndexerService: clipIndexerService,
	}
	// PR 4: VectorPointDeleter port satisfied directly by runtime.Writer
	// (compile-time assertion in internal/infrastructure/qdrant/index_writer.go
	// pins the conformance: `_ jobsoutbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`).
	// No runtime `interface{}` cast needed.
	if runtime != nil {
		qd.QdrantDeleter = runtime.Writer
	}
	return qd, nil
}

// NewComposition composes all bundles in dependency order and returns the
// fully-assembled ComposeRoot. Cleanup is owned by shutdown.go.
//
// PG-028 (June 2026): each Build*Bundle now lives in its own
// `build_<bundle>.go` file. composition.go retains the bundle types,
// ComposeRoot, IOpaqueStartFunc, configOnlyDestinations, and the
// NewComposition orchestrator.
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

	// PR-Queue-Split-EXPAND / ADR-0003 (June 2026): the JobsBundle's DB
	// is the EXPAND-on jobs.db.sqlite when the gate is enabled + the DB
	// opened successfully at initDatabases; otherwise the canonical
	// single-DB shape on dbs.main. The BuildJobsBundle signature is
	// unchanged; composition-root does the pick. Documented here so a
	// future reader does not chase the dbs.main reference looking for
	// where the EXPAND gate surfaces.
	jobsDB := dbs.main
	if dbs.jobs != nil {
		jobsDB = dbs.jobs
	}
	jobs, err := BuildJobsBundle(jobsDB, log)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
	}

	ai, err := BuildAIBundle(ctx, cfg, dbs, log, repos, driveBundle)
	if err != nil {
		return nil, fmt.Errorf("compose ai: %w", err)
	}

	// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed) —
	// ring-break reorder: the canonical Qdrant adapters that
	// BuildOutboxBundle needs (ClipIndexerService + QdrantDeleter) are
	// constructed in a tiny buildQdrantDeps pre-phase so BuildOutboxBundle
	// can run BEFORE BuildProcessBundle. BuildProcessBundle then consumes
	// both qdrantDeps + outbox.OutboxBundle and constructs MediaProcessor
	// inline using the canonical outbox.Dispatcher SSOT (QDRANT-002
	// atomicity invariant). The composition graph is now a strict DAG:
	// qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd)
	// -> domains. The previous PR-7 deferred-hydration strategy
	// (hydrateMediaProcessor + MediaProcessor=nil) is gone.
	qdrantDeps, err := buildQdrantDeps(ctx, cfg, dbs, log)
	if err != nil {
		return nil, fmt.Errorf("compose qdrant deps: %w", err)
	}

	// PR-12d (June 2026): BuildOutboxBundle runs BEFORE BuildDomainBundle.
	// PR 8 (June 2026) takes this further and reorders BuildOutboxBundle
	// BEFORE BuildProcessBundle too: BuildProcessBundle now reads
	// qdrantDeps (the only Process-side deps BuildOutboxBundle needed,
	// exposed by the pre-phase) and outbox.OutboxBundle (for the
	// canonical mutations dispatcher used in MediaProcessor). The
	// dispatcher is wired into images.Service via constructor injection
	// in BuildDomainBundle.
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	process, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.DriveUploader, outbox, qdrantDeps)
	if err != nil {
		return nil, fmt.Errorf("compose process: %w", err)
	}

	domains, err := BuildDomainBundle(ctx, cfg, dbs, log, driveBundle, repos, search, process, ai, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose domains: %w", err)
	}

	sync, err := BuildSyncBundle(ctx, cfg, dbs, log, repos, search, process, driveBundle, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose sync: %w", err)
	}

	maint, err := BuildMaintBundle(ctx, cfg, dbs, log, driveBundle, repos, search, jobs, outbox)
	if err != nil {
		return nil, fmt.Errorf("compose maintenance: %w", err)
	}

	utility := BuildUtilityBundle(cfg, dbs.main, driveBundle.DriveClient)

	// Late-bindings: jobs.RegisterHandler for domain services that opt in.
	if sync.CatalogSync != nil && jobs.Service != nil {
		sync.CatalogSync.RegisterHandler(jobs.Service)
		sync.CatalogSync.RegisterDriveFolderSyncHandler(jobs.Service)
	}
	if domains.YoutubeClipService != nil && jobs.Service != nil {
		domains.YoutubeClipService.RegisterHandler(jobs.Service)
	}
	// Voiceover registration moved to the new GenerateJobHandler path
	// (P0.1, June 2026) — see buildVoiceoverService + composition.go::wireVoiceoverJobHandler
	// registered at the post-bundle binding block. The legacy Service.RegisterHandler
	// hook (which registered voiceover.batch + voiceover.promo) is intentionally
	// removed here; the legacy codes will be retired in the next refactor (P0.3).
	if domains.VoiceoverGenerateHandler != nil && jobs.Service != nil {
		// Catena A P0 (June 2026): the canonical `voiceover.generate`
		// job type is now backfilled with the typed-port GenerateJobHandler.
		// The boot smoke test at internal/app/voiceover_wiring_test.go
		// fails closed if this registration is absent — the failure mode
		// of HEAD pre-Catena-A was /api/voiceover/generate → 202 → job
		// queued → no consumer → silence.
		domains.VoiceoverGenerateHandler.Register(jobs.Service)
		log.Info("voiceover.generate handler registered (Catena A P0 wiring complete)")
	} else {
		log.Warn("voiceover.generate handler NOT registered (typed-port chain incomplete — Drive / destResolver / outbox / lifecycle / repo / audio / db must all be wired)",
			zap.Bool("generate_handler_built", domains.VoiceoverGenerateHandler != nil),
			zap.Bool("jobs_service_available", jobs.Service != nil))
	}
	if domains.ImageService != nil && jobs.Service != nil {
		domains.ImageService.RegisterHandler(jobs.Service)
	}
	if process.ClipIndexerService != nil && jobs.Service != nil {
		process.ClipIndexerService.RegisterJobHandler(jobs.Service)
	}
	// Capability Standard migration (June 2026): BooksService and
	// LessonsService worker handlers are NOT registered here.
	// The Generation capability owns the books.process and
	// lessons.process job types and publishes the handlers via
	// its Descriptor (api.DescriptorJobs), wired in registry.go
	// after generation.Build returns. Single source of truth.

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

		DriveStart:  driveStart,
		OutboxStart: outboxStart,
		// PR8 (June 2026): wire construction is performed in registry.go
		// and assigned back to root.IdempotencyMiddleware. The cleanup
		// goroutine lives only at the registry level (per reviewer fix A).
		// WireRegistry (after this fn returns) sets the field directly
		// via ComposeRoot.IdempotencyMiddleware assignment.
		Ctx: ctx,
	}

	return root, nil
}
