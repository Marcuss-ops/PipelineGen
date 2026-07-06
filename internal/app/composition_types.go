// Package app — composition_types.go (split July 2026).
//
// This file owns the canonical bundle type definitions.
// Extracted from composition.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: each bundle type is the single canonical owner
// of its field set. NewComposition lives in composition.go.
package app

import (
	"context"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	apiMw "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	voiceoverreconcile "github.com/Marcuss-ops/PipelineGen/internal/application/assets/reconciliation/voiceover"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	mwidem "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
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
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/maintenance"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	qdranttransport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
)

// IOpaqueStartFunc is the opaque type for deferred initialisation closures
// returned by Build*Bundle constructors (PR9 series, June 2026).
type IOpaqueStartFunc func() error

// ── Bundle types ────────────────────────────────────────────────────────

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

	DriveStart            IOpaqueStartFunc
	OutboxStart           IOpaqueStartFunc
	IdempotencyMiddleware *apiMw.Idempotency
	Ctx                   context.Context
}

// DriveBundle owns all Google Drive adapters.
type DriveBundle struct {
	Admin         drive.Admin
	Reader        drive.Reader
	DocClient     drive.DocClient
	DocPublisher  delivery.DocPublisher
	DriveDests    *DriveDestinations
	MediaStore    *drive.Store
	DestResolver  asset.Resolver
	StyleRegistry *generation.StyleRegistry
	Publisher     delivery.Publisher
	Lifecycle     drive.FileLifecycle
	driveUploader *drive.Uploader
}

// RepoBundle owns all SQLite-backed repositories.
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
type SearchBundle struct {
	AssetIndexService *assetindex.Service
	AssetTreeService  *assettree.Service
	AssetResolver     *assetindex.Resolver
	ProviderRegistry  *providers.Registry
}

// ProcessBundle holds the heavy media-processing adapters.
type ProcessBundle struct {
	MediaProcessor     asset.Processor
	ClipIndexerService *clipindexer.Service
	VLMClient          *vlm.Client
	CollectionManager  *collections.CollectionManager
	QdrantDeleter      jobsoutbox.VectorPointDeleter
	QdrantRuntime      *qdrant.QdrantRuntime
	VectorSvc          assetsearch.VectorStorePort
	QdrantClient       *qdranttransport.Client
	QdrantHealthProbe  *disasterrecovery.HealthProbe
	LocatorCleaner     *qdrantmaintenance.LocatorCleaner
	QdrantSearcher     *qdrantsearch.Searcher
}

// QdrantDeps is the tiny pre-phase bundle for BuildOutboxBundle.
type QdrantDeps struct {
	Runtime            *qdrant.QdrantRuntime
	ClipIndexerService *clipindexer.Service
	QdrantDeleter      jobsoutbox.VectorPointDeleter
}

// AIBundle owns script generation, engine, and gemmamemory Repository.
type AIBundle struct {
	OllamaClient     *client.Client
	ScriptGen        *ollama.Generator
	OllamaTranslator *translation.OllamaTranslator
	MemoryRepo       *adapters.Repository
	ScriptEngine     *scriptcore.Engine
}

// DomainBundle is everything media-specific that lives at the application layer.
type DomainBundle struct {
	YoutubeClipService           *youtube.Service
	VoiceoverService             *voiceover.Service
	VoiceoverSync                *voiceoverreconcile.Service
	ImageService                 *imgservice.Service
	IngestService                *ingest.Service
	BooksService                 *books.Service
	LessonsService               *lessonsSvc.Service
	MetaWriter                   *semantic.MetadataWriter
	RealtimeMatcher              assetsapi.RealtimeMatcher
	RealtimeSearch               scriptcore.RealtimeSearchService
	AutotagService               *autotag.Service
	AssocService                 scriptcore.AssocSearchService
	VoiceoverGenerateHandler     *voiceoverjobs.GenerateJobHandler
	VoiceoverProcessItem         voiceover.VoiceoverItemExecutor
	VoiceoverGenerateItemHandler *voiceoverjobs.GenerateItemJobHandler
	ArtifactService              *artifacts.Service
	ImageSearchResolver          routing.ImageSearchResolver
	AudioProcessor               *audioasset.Processor
}

// OutboxBundle aggregates the canonical outbox dispatcher and events pool.
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

// AssetsWiring holds the Assets module wiring.
type AssetsWiring struct {
	Module               module.Module
	DeletionSvc          *deletion.DeletionService
	InternalMediaHandler *assetstorage.Handler
	SearchAggregator     *search.Aggregator
	SearchFanOut         search.SearchFanOut
}
