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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
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
//
// FASE 9 (June 2026, P0.1 / DRIVE-005): the canonical Pattern 0 ports
// (Admin + Reader) are the public surface. The unexported driveUploader
// field is for internal wiring only (BuildDomainBundle, BuildProcessBundle,
// etc.) within package app. New code MUST use Admin / Reader / Publisher
// per Pattern 0.
type DriveBundle struct {
	// Admin is the canonical Pattern 0 port for administrative Drive
	// ops (folder management, file lifecycle, raw uploads, liveness
	// probe via Ping). *drive.Uploader satisfies Admin structurally —
	// see internal/infrastructure/drive/ports.go compile-time assert.
	Admin drive.Admin
	// Reader is the canonical Pattern 0 port for read-only Drive ops
	// (download, metadata, listing, existence checks).
	Reader        drive.Reader
	DocClient     drive.DocClient
	DriveDests    *DriveDestinations
	MediaStore    *drive.Store
	DestResolver  asset.Resolver
	StyleRegistry *generation.StyleRegistry
	// Publisher is the canonical Drive upload canal (FASE 3, June 2026).
	// All new upload callers MUST go through Publisher; ServiceDeps concrete
	// handle was retired (P0.1 closure).
	Publisher delivery.Publisher
	// CARD-3 (June 2026): Lifecycle is the canonical Pattern 0 port for
	// file-lifecycle Drive ops (Trash/Move/Rename/Cleanup). Owned by
	// *drive.FileLifecycleAdapter (file_lifecycle.go); the previous
	// DriveFolderManagerAdapter.Trash method was retired per godlike/06
	// "one owner per fact": folder-management and file-lifecycle are
	// distinct seams. Reuses driveUploader.Service so credentials load
	// exactly once at composition.
	Lifecycle drive.FileLifecycle

	// Wave B (June 2026): DriveBundle.DriveUploader deprecated field removed.
	// The 5 cleanup.go callers + 1 list_drive_folder.go caller now reach
	// drive.Admin / drive.Reader via root.Drive.Admin and root.Drive.Reader
	// directly (the Pattern 0 canonical ports).
	//
	// Wave C (June 2026 — partial): DriveBundle.DriveClient deprecated
	// field STILL PRESENT for back-compat with internal/app/ and
	// internal/application/assets/providers/artlist/ raw SDK reach-through
	// sites that haven't migrated to Pattern 0 ports yet. The 8 cmd/admin/
	// raw callers (cleanup.go, list_drive_folder.go, stock_reset.go,
	// stock_subfolders_reset.go, summarize_book.go, sync_outros.go,
	// reset_video_ai.go, backfill_hash.go::runBackfillHashV2) DO NOT
	// touch root.Drive.DriveClient anymore — they reach drive.Admin /
	// drive.Reader / drive.DocClient via Pattern 0 ports.
	//
	// Wave D Commit 1 (June 2026 — mechanical migration): the residual
	// 13 sites now consume the DriveFolderManager port instead of the
	// raw SDK — registry_internal_modules.go::registerArtlist still
	// threads root.Drive.DriveClient into ArtlistBundle as the
	// plumbing channel that feeds the *drive.DriveFolderManagerAdapter
	// in WireArtlist, but the diagnostic API field has been renamed
	// (HasDriveClient → HasDriveFolderManager, see artlist/types.go)
	// and every comment that referenced the raw SDK reach-through now
	// points at the port. DriveClient REMAINS on the bundle per the
	// Wave D Commit 1 back-compat mandate (operator signal will
	// determine Wave D Commit 2/3 retirement).
	DriveClient *gdrive.Service

	// driveUploader is unexported for internal wiring only. *drive.Uploader
	// is the SINGLE source-of-truth concrete exposed via Admin / Reader ports.
	driveUploader *drive.Uploader
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
// that the outbox used. There is exactly ONE Qdrant client construction call
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

// AIBundle owns script generation, engine, and gemmamemory Repository.
//
// Commit H Phase 2 (June 2026): the gemmamemory gate service field (gemma
// memory gate service) is dropped from the bundle — gemmamemory.Service
// + its MemoryCacheAdapter wrapper are no longer wired at composition.
// MemoryRepo (canonical *adapters.Repository) stays for the
// gemma-memory-sweeper background job (lifecycle.go:393).
//
// StyleRegistry lives on DriveBundle (PR4.A). ScriptFlowHandler lives
// in registry.go::WireRegistry.
//
// Fase 9 step 2 (July 2026, Spina Dorsale): OllamaTranslator is the
// canonical application-layer port-surface concrete (translation.TranslationPort +
// 3 legacy ports). Composition root instantiates ONE *OllamaTranslator
// per process and routes every consumer (svc.Translation, svc.Translator,
// svc.TranslationPort, and any future metadata-translator dependency)
// through this instance. ScriptGen stays as the direct concrete consumed
// by ScriptEngine (scriptcore.NewEngine requires *ollama.Generator at
// compile time); OllamaTranslator wraps the same *ollama.Generator so
// the two fields share the canonical translation logic without
// duplicating it. Per godlike/06 "one owner per fact", the
// *ollama.Generator translation logic is owned by ONE canonical
// Pyt-path (translation.ollama_translator.go) reachable via all 4 ports.
type AIBundle struct {
	OllamaClient     *client.Client
	ScriptGen        *ollama.Generator
	OllamaTranslator *translation.OllamaTranslator
	MemoryRepo       *adapters.Repository
	ScriptEngine     *scriptcore.Engine
}

// DomainBundle is everything media-specific that lives at the application layer.
//
// P0.1 (June 2026): ArtifactService added — the concrete content-addressed
// artifact blob service (CreateAndVerify + LocalPath) constructed by
// BuildDomainBundle from db + cfg.Storage.DataDir.
type DomainBundle struct {
	YoutubeClipService *youtube.Service
	VoiceoverService   *voiceover.Service
	VoiceoverSync      *voiceoverreconcile.Service
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
	// BLOC5.3 commit-2-child-canonical (June 2026): the per-language
	// child use case (ProcessVoiceoverItemUseCase) is held as the
	// narrow VoiceoverItemExecutor port (Pattern 0, AGENTS.md) so the
	// composition layer survives a future BACKFILL that swaps the
	// concrete use case for a different implementation without
	// touching the late-bindings block. The concrete
	// *ProcessVoiceoverItemUseCase is constructed in BuildDomainBundle
	// (build_bundles_domain.go) via buildVoiceoverService's 3rd return
	// value (processItemUseCase) and assigned here. The
	// GenerateItemJobHandler (per-job-type child handler for
	// job.TypeVoiceoverGenerateItem) is constructed and registered at
	// the late-bindings block below.
	VoiceoverProcessItem         voiceover.VoiceoverItemExecutor
	VoiceoverGenerateItemHandler *voiceoverjobs.GenerateItemJobHandler
	// P0.1 (June 2026): the content-addressed artifact blob service.
	// Constructed in BuildDomainBundle from dbs.main.DB +
	// cfg.Storage.DataDir. Wired into AssetsModuleDeps.Core and
	// consumed by the upload UseCase via artifactServiceAdapter.
	ArtifactService *artifacts.Service
	// FASE 7 (July 2026, image-territories action plan): the routing-
	// layer ImageSearchResolver (routing.ImageSearchResolver) wired in
	// composition.BuildDomainBundle from imageSvc.RetrievalRegistry() +
	// repos.ImageRepo. Canonical singleton surface for downstream
	// handlers + lifecycle Stop consumers. Reaches the HTTP layer
	// via api.ServerDeps.ImageSearchResolver (composition → server).
	ImageSearchResolver routing.ImageSearchResolver
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
	//
	// P0.7 Wave 21 Step 10/12 (June 2026): construct the voiceover
	// cleanup driver inline BEFORE BuildOutboxBundle because the
	// outbox bundle now expects a jobsoutbox.VoiceoverCleanupDriver
	// arg (the canonical narrow port for orphan Drive file delete).
	// The same *voiceoverDriveAdapter instance (defined in
	// adapters_voiceover_use_case.go, package app) is shared between
	// voiceover.DriveUploaderPort (legacy port; satisfied by the
	// same DeleteFile method) and the new jobsoutbox.VoiceoverCleanupDriver
	// surface — Go's implicit-interface rule pins conformance at
	// compile time. nil Drive admin → nil cleanup driver → handler
	// registered but skips the Drive delete branch (local-remove
	// branch still runs via stdlib; logs operator-visible warning).
	driveAdmin := driveBundle.Admin
	var voiceoverDriver jobsoutbox.VoiceoverCleanupDriver
	if driveAdmin != nil {
		voiceoverDriver = &voiceoverDriveAdapter{drive: driveAdmin}
	}
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, qdrantDeps, jobs, voiceoverDriver)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
	}

	// F2.8 (June 2026): thread driveBundle.Publisher (the canonical
	// delivery.Publisher port) instead of the unexported driveUploader.
	// The pre-F2.8 raw-uploader bypass is closed. Publisher is
	// fail-populated in BuildDriveBundle (build_bundles_drive.go) — a
	// nil publisher surfaces in processor.NewProcessor as a typed
	// panic at composition time (loud in operator log) rather than
	// silent nil-deref on first upload.
	process, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.Publisher, outbox, qdrantDeps)
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

	// Wave A (June 2026): BuildUtilityBundle now takes the canonical
	// drive.Admin port. The legacy `rawDriveSvc *gdrive.Service`
	// extraction from `driveBundle.driveUploader.Service` is gone —
	// pass `driveBundle.Admin` (the Pattern 0 port) directly so the
	// health probe can call driveAdmin.Ping() which wraps the raw
	// About.Get internally.
	utility := BuildUtilityBundle(cfg, dbs.main, driveBundle.Admin)

	// Late-bindings: jobs.RegisterHandler for domain services that opt in.
	// Audit P0 #2 cont. — PR-VALIDATOR-LITERAL-REGISTER (July 2026):
	// every silent-Warn inline Register call is now fail-closed
	// (errors surface as wrapped composition errors; NewComposition
	// aborts instead of silently dropping jobs onto an unsigned
	// dispatcher). The validator below then re-invokes the literal
	// Register method verbatim, closing the silent-success class on
	// every critical handler.
	if sync.CatalogSync != nil && jobs.Service != nil {
		if err := sync.CatalogSync.RegisterHandler(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose catalogsync.catalog_sync binding: %w", err)
		}
		if err := sync.CatalogSync.RegisterDriveFolderSyncHandler(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose catalogsync.drive_folder_sync binding: %w", err)
		}
	}
	if domains.YoutubeClipService != nil && jobs.Service != nil {
		if err := domains.YoutubeClipService.RegisterHandler(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose youtube.clip_extract binding: %w", err)
		}
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
		//
		// Audit P0 #2 (July 2026): Register now returns error so this
		// wiring step fails loud at boot instead of silently dropping
		// jobs onto an unsigned dispatcher (the pre-P0 #2 silent-Warn
		// pattern was the audit-mandated fix surface).
		if err := domains.VoiceoverGenerateHandler.Register(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose voiceover.generate handler wiring (Catena A P0): %w", err)
		}
		log.Info("voiceover.generate handler registered (Catena A P0 wiring complete)")
	} else {
		log.Warn("voiceover.generate handler NOT registered (typed-port chain incomplete — Drive / destResolver / outbox / lifecycle / repo / audio / db must all be wired)",
			zap.Bool("generate_handler_built", domains.VoiceoverGenerateHandler != nil),
			zap.Bool("jobs_service_available", jobs.Service != nil))
	}
	// PR-VOICEOVER-PARENT-CHILD-FANOUT (P0.3, June 2026): construct the
	// parent GenerateJobHandler (Fanout-bound) and the child
	// GenerateItemJobHandler (per-language) at composition time, where
	// jobs.Service is available for both FanoutUseCase construction AND
	// the late-binding Register calls. Idempotent — second pass logs the
	// `already registered` warning via the existing Dispatcher
	// double-Register protection.
	//
	// Audit P0 #2 (July 2026): both Register calls now return error;
	// NewComposition aborts if either fails (fail-closed at boot).
	// Pre-P0 #2 a silent-Warn here would lose the parent-child wiring
	// and the parent fan-out would dead-letter every N children.
	if jobs.Service != nil && domains.VoiceoverProcessItem != nil {
		fanout := voiceoverjobs.NewFanoutVoiceoversUseCase(voiceoverjobs.FanoutDeps{
			Enqueuer: jobs.Service,
			Logger:   log,
		})
		parentHandler := voiceoverjobs.NewGenerateJobHandler(fanout, log)
		// Audit P0 #2 (July 2026): the dispatcher's duplicate-
		// Register contract is not part of its surface (verify either
		// errors on a second Register call or silently overwrites).
		// Block A above may have already bound a handler for
		// TypeVoiceoverGenerate when BuildDomainBundle succeeded.
		// The pre-P0 #2 silent-Warn path masked this; Post-P0 #2
		// must explicitly preserve idempotency via dispatcher's
		// HasHandler probe (canonical per internal/app/
		// voiceover_wiring_test.go::TestVoiceoverGenerateHandler_RequiresRegistration).
		// If already bound, skip the re-Register — the domains field
		// is still overwritten with the BLOC5.3 fanout-bound handler
		// for downstream state-tracking consumers.
		if !jobs.Service.HasHandler(appjobs.TypeVoiceoverGenerate) {
			if err := parentHandler.Register(jobs.Service); err != nil {
				return nil, fmt.Errorf("compose voiceover.generate parent handler Register (BLOC5.3 commit-2): %w", err)
			}
		} else {
			log.Info("voiceover.generate handler already bound (Catena A P0 wiring succeeded) — preserving dispatcher binding; BLOC5.3 fanout-bound handler canonicals the domains.VoiceoverGenerateHandler field reference for downstream state-tracking",
				zap.String("job_type", appjobs.TypeVoiceoverGenerate))
		}
		domains.VoiceoverGenerateHandler = parentHandler

		// TypeVoiceoverGenerateItem is NOT pre-registered by Block A
		// (Block A only touches TypeVoiceoverGenerate). Per-language
		// child handler registration is uniquely owned by this block;
		// any failure surfaces as a typed error and aborts composition
		// (fail-closed at boot, audit P0.2).
		childHandler := voiceoverjobs.NewGenerateItemJobHandler(domains.VoiceoverProcessItem, log)
		if err := childHandler.Register(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose voiceover.generate_item child handler Register (BLOC5.3 commit-2): %w", err)
		}
		domains.VoiceoverGenerateItemHandler = childHandler
		log.Info("BLOC5.3 commit-2 voiceover handlers wired: parent voiceover.generate + child voiceover.generate_item")
	}
	if domains.ImageService != nil && jobs.Service != nil {
		if err := domains.ImageService.RegisterHandler(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose images.image_generate_google binding: %w", err)
		}
	}
	if process.ClipIndexerService != nil && jobs.Service != nil {
		if err := process.ClipIndexerService.RegisterJobHandler(jobs.Service); err != nil {
			return nil, fmt.Errorf("compose clipindexer.media_reindex binding: %w", err)
		}
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

	// ── Critical-handler validator (Audit P0 #2 cont., PR-VALIDATOR-LITERAL-REGISTER, July 2026) ──
	// Each CriticalHandler.Bind closure now re-invokes the
	// corresponding handler.Register(svc) method verbatim (not a
	// HasHandler post-call confirmation). The inline late-bindings
	// calls above are duplicated + fail-closed (errors propagate as
	// wrapped composition errors); the validator is the canonical
	// re-bind surface that catches the silent-success class on
	// every critical handler (audit-P0.2 cont. closes the gap that
	// audit-P0.2 only partially closed: P0.2 converted voiceover to
	// error-return but left the OTHER 7 silent-Warn handlers
	// uncovered).
	//
	// godlike/05 fail-closed posture: any non-nil error from
	// ValidateCriticalHandlers aborts NewComposition; the server
	// never boots with a half-registered dispatcher.
	//
	// stockpipeline.media_stock is NOT in this slice — it's wired via
	// registerInternalModules::WireStockPipeline AFTER NewComposition
	// returns. The canonical stockpipeline validator pass lives in
	// lifecycle.go (post-WireStockPipeline + pre-ListenAndServe).
	var criticalHandlerValidators []CriticalHandler
	if sync.CatalogSync != nil && jobs.Service != nil && jobs != nil {
		catSync := sync.CatalogSync
		criticalHandlerValidators = append(criticalHandlerValidators,
			CriticalHandler{
				Name: "catalogsync.catalog_sync",
				Bind: func(svc *appjobs.Service) error {
					return catSync.RegisterHandler(svc)
				},
			},
			CriticalHandler{
				Name: "catalogsync.drive_folder_sync",
				Bind: func(svc *appjobs.Service) error {
					return catSync.RegisterDriveFolderSyncHandler(svc)
				},
			},
		)
	}
	if domains.YoutubeClipService != nil && jobs.Service != nil {
		yt := domains.YoutubeClipService
		criticalHandlerValidators = append(criticalHandlerValidators,
			CriticalHandler{
				Name: "youtube.clip_extract",
				Bind: func(svc *appjobs.Service) error {
					return yt.RegisterHandler(svc)
				},
			},
		)
	}
	if domains.ImageService != nil && jobs.Service != nil {
		img := domains.ImageService
		criticalHandlerValidators = append(criticalHandlerValidators,
			CriticalHandler{
				Name: "images.image_generate_google",
				Bind: func(svc *appjobs.Service) error {
					return img.RegisterHandler(svc)
				},
			},
		)
	}
	if process.ClipIndexerService != nil && jobs.Service != nil {
		ci := process.ClipIndexerService
		criticalHandlerValidators = append(criticalHandlerValidators,
			CriticalHandler{
				Name: "clipindexer.media_reindex",
				Bind: func(svc *appjobs.Service) error {
					return ci.RegisterJobHandler(svc)
				},
			},
		)
	}
	// voiceover.generate: literal Register re-call gated by
	// HasHandler check to preserve BLOC5.3 + Catena A P0 idempotency
	// (parent gate at late-bindings time). If the dispatcher already
	// holds a Catena A P0 binding, the validator no-ops so we don't
	// overwrite it with the BLOC5.3 caller-reference handler.
	if jobs.Service != nil {
		vh := domains.VoiceoverGenerateHandler
		if vh != nil {
			criticalHandlerValidators = append(criticalHandlerValidators,
				CriticalHandler{
					Name: "voiceover.generate",
					Bind: func(svc *appjobs.Service) error {
						if svc.HasHandler(appjobs.TypeVoiceoverGenerate) {
							return nil // idempotent: Catena A P0 bind preserved
						}
						return vh.Register(svc)
					},
				},
			)
		}
	}
	if gih := domains.VoiceoverGenerateItemHandler; gih != nil && jobs.Service != nil {
		criticalHandlerValidators = append(criticalHandlerValidators,
			CriticalHandler{
				Name: "voiceover.generate_item",
				Bind: func(svc *appjobs.Service) error {
					return gih.Register(svc)
				},
			},
		)
	}
	if err := ValidateCriticalHandlers(jobs.Service, log, criticalHandlerValidators); err != nil {
		return nil, fmt.Errorf("compose critical-handler validation (audit-P0.2 cont., PR-VALIDATOR-LITERAL-REGISTER): %w", err)
	}

	return root, nil
}
