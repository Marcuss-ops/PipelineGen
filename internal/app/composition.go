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
	common "github.com/Marcuss-ops/PipelineGen/internal/api/common"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/gemmamemory"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoversync "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/sync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube"

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
	// QDRANT-003 (June 2026): canonical Qdrant adapters.
	CollectionManager *qdrant.CollectionManager
	QdrantDeleter     qdrant.QdrantDeleter
	// QDRANT-004 (June 2026): search.VectorStorePort for the mediasearch API.
	// Populated by BuildProcessBundle when Qdrant is enabled.
	// Wave 15 (June 2026): typed port per AGENTS.md Pattern 0 — replaces
	// `interface{}` carrier. Compile-time assertion at
	// internal/infrastructure/qdrant/search_adapter.go catches drift.
	VectorSvc assetsearch.VectorStorePort
	// QDRANT-005 Fase 1 (June 2026): direct *qdrant.Client for diagnostics
	// (CountPoints). Populated by BuildProcessBundle when Qdrant is enabled.
	QdrantClient *qdrant.Client
	// QDRANT-005 Fase 2 (June 2026): optional QdrantHealthProbe interface{}
	// that satisfies the lifecycle readiness probe contract
	// (`Probe(context.Context) error`). The concrete runtime binding is
	// constructed by BuildProcessBundle from the same *qdrant.Client
	// when Qdrant is enabled; exposed as `any` here so the wire step in
	// wire_services.go can type-assert without forcing every composition
	// path to import qdrant (see PR for layering).
	QdrantHealthProbe any
}

// AIBundle owns script generation, engine, and memory.
// StyleRegistry lives on DriveBundle (PR4.A). MemoryRepo is constructed
// in BuildAIBundle. ScriptFlowHandler lives in registry.go::WireRegistry.
type AIBundle struct {
	OllamaClient  *client.Client
	ScriptGen     *ollama.Generator
	MemoryRepo    *gemmamemory.Repository
	MemoryService *gemmamemory.Service
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
	Utility       *common.UtilityHandler
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

	process, err := BuildProcessBundle(ctx, cfg, dbs, log, repos, driveBundle.DriveUploader)
	if err != nil {
		return nil, fmt.Errorf("compose process: %w", err)
	}

	jobs, err := BuildJobsBundle(dbs.main, log)
	if err != nil {
		return nil, fmt.Errorf("compose jobs: %w", err)
	}

	ai, err := BuildAIBundle(ctx, cfg, dbs, log, repos, driveBundle)
	if err != nil {
		return nil, fmt.Errorf("compose ai: %w", err)
	}

	// PR-12d (June 2026): BuildOutboxBundle now runs BEFORE
	// BuildDomainBundle. Previously BuildDomainBundle ran first and
	// ImageService.SetDispatcher was called AFTER BuildOutboxBundle
	// returned; that ordering hazard meant any ImageService call
	// between the two landed on the raw stockRepo.Upsert path. The
	// dispatcher is now passed through BuildDomainBundle's signature
	// and consumed by images.Service via constructor injection.
	outbox, outboxStart, err := BuildOutboxBundle(ctx, cfg, dbs, log, repos, process, jobs)
	if err != nil {
		return nil, fmt.Errorf("compose outbox: %w", err)
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
