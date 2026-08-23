// Package app — composition_types.go (split July 2026).
//
// This file owns the canonical bundle type definitions.
// Extracted from composition.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: each bundle type is the single canonical owner
// of its field set. NewComposition lives in composition.go.
package wiring

import (
	"context"

	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/api/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/api/transport"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	apiMw "github.com/Marcuss-ops/PipelineGen/internal/api/middleware"

	artifactfinalize "github.com/Marcuss-ops/PipelineGen/internal/application/artifact_finalize"

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
	texttracks "github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/application/books"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	lessonsSvc "github.com/Marcuss-ops/PipelineGen/internal/application/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	mwidem "github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	stagingsvc "github.com/Marcuss-ops/PipelineGen/internal/application/staging"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/application/system/health"
	translation "github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/jobs"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/maintenance"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	qdranttransport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/collections"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
)

// IOpaqueStartFunc is the opaque type for deferred initialisation closures
// returned by Build*Bundle constructors (PR9 series, June 2026).
type IOpaqueStartFunc func() error

// ── Bundle types ────────────────────────────────────────────────────────

// ComposeRoot is the assembled root tree. NewComposition returns this.
type ComposeRoot struct {
	// MediaExec is the single resolved media contract for this composition.
	// Platform config is mapped before downstream bundles are built.
	MediaExec mediaexec.ExecutionConfig

	DB              *storage.SQLiteDB
	ObservabilityDB *storage.SQLiteDB

	Drive      *DriveBundle
	Repos      *RepoBundle
	Search     *SearchBundle
	Process    *ProcessBundle
	TextTracks *TextTrackBundle

	AI      *AIBundle
	Domains *DomainBundle
	Jobs    *JobsBundle
	Outbox  *OutboxBundle
	Sync    *SyncBundle
	Maint   *MaintBundle
	Utility *UtilityBundle

	// Staging is the FASE 3 Spina Dorsale staging bundle
	// (Push 3.1b, July 2026). Exposes the application-layer
	// `staging.Store` port (the Stage service the publisher
	// worker pool will call) + the `artifact.Repository` port
	// (the canonical single-writer of the artifact_stages table;
	// the publisher worker + finalizer will consume it directly
	// for Mark* state transitions). Construction lives in
	// internal/app/build_bundles_staging.go::BuildStagingBundle.
	Staging *StagingBundle

	// Finalizer is the FASE 3 Spina Dorsale finalizer bundle
	// (Push 3.1d, July 2026). Exposes the application-layer
	// `artifact_finalize.Finalizer` port — the typed contract
	// that closes the publication saga's Finalize step by
	// scanning artifact_stages for a job_id (Repository.ListByJob)
	// and flipping every PUBLISHED row to SUCCEEDED via the
	// fenced Repository.MarkSucceeded primitive. The Finalizer
	// consumes the SAME artifact.Repository port that
	// Staging.Repository exposes (no second DB lookup; the typed
	// port is the canonical cursor to the same
	// *artifactstages.Repository concrete — godlike/06 SSOT).
	// Construction lives in
	// internal/app/build_bundles_artifact_finalize.go::
	// BuildArtifactFinalizeBundle (runs AFTER BuildStagingBundle).
	// Publisher worker pool integration (Push 3.1c, forward-pointer)
	// will drain the outbox + invoke root.Finalizer.Finalize
	// after each per-artifact MarkPublished to close the saga.
	Finalizer *FinalizerBundle

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
	DestResolver  asset.Resolver
	StyleRegistry *generation.StyleRegistry
	Publisher     delivery.Publisher
	Lifecycle     drive.FileLifecycle
	DriveUploader *drive.Uploader
}

// RepoBundle owns all SQLite-backed repositories.
type RepoBundle struct {
	ScriptsRepo        *sqlitescripts.ScriptRepository
	ImageRepo          *imagesrepo.ImagesRepository
	ClipsRepo          *assets.ClipsRepository
	Assets             *asset.Service
	MonitorsRepo       *monitors.MonitorsRepository
	VoiceoverRepo      *assets.VoiceoversRepository
	CatalogRepo        *catalog.Repository
	EntityImageCatalog entitycatalog.Repository
	SQRepo             *assets.SearchQueriesRepository
	IdempotencyStore   mwidem.IdempotencyStore
	// TextTrackRepo is the canonical Fase 2.a / Fase 4
	// TextTrackRepository used by the video pipeline
	// (ClipSourceBuilder.ConfigureTextTrackReader) and the
	// TextTrackResolver (Fase 1.b). The composition root
	// wires the *texttracks.TextTrackRepositorySQLite here so
	// both consumers can share one canonical surface.
	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): added
	// so the cutover wiring
	// (`root.Repos.TextTrackRepo`) compiles.
	TextTrackRepo        asset.TextTrackRepository
	SubtitleArtifactRepo asset.SubtitleArtifactRepository
}

// SearchBundle holds the asset metadata search/index pair and resolver.
type SearchBundle struct {
	AssetIndexService *assetindex.Service
	AssetTreeService  *assettree.Service
	AssetResolver     *assetindex.Resolver
	ProviderRegistry  *providers.Registry
	SearchFanOut      search.SearchFanOut
}

// ProcessQdrantBundle groups all Qdrant-related adapter fields.
// Embedded anonymously in ProcessBundle so consumer code
// (process.QdrantClient, process.VectorSvc, etc.) continues to
// compile via Go's field promotion — zero refactoring in callers.
//
// Extracted from ProcessBundle per CODE-QUALITY-AUDIT-2026-07-05 P0-1
// embedded-struct-promotion topology.
type ProcessQdrantBundle struct {
	CollectionManager *collections.CollectionManager
	QdrantDeleter     jobsoutbox.VectorPointDeleter
	QdrantRuntime     *qdrant.QdrantRuntime
	VectorSvc         assetsearch.VectorStorePort
	QdrantClient      *qdranttransport.Client
	QdrantHealthProbe *disasterrecovery.HealthProbe
	LocatorCleaner    *qdrantmaintenance.LocatorCleaner
	QdrantSearcher    *qdrantsearch.Searcher
}

// ProcessBundle holds the heavy media-processing adapters.
//
// ProcessQdrantBundle is embedded anonymously so process.QdrantClient etc.
// continue to resolve via Go field promotion (zero refactoring in callers).
type ProcessBundle struct {
	ProcessQdrantBundle
	MediaProcessor     asset.Processor
	ClipIndexerService *clipindexer.Service
	VLMClient          *vlm.Client
}

// QdrantDeps is the tiny pre-phase bundle for BuildOutboxBundle.
type QdrantDeps struct {
	Runtime            *qdrant.QdrantRuntime
	ClipIndexerService *clipindexer.Service
	QdrantDeleter      jobsoutbox.VectorPointDeleter
}

// AIBundle owns script generation, engine, and gemmamemory Repository.
type AIBundle struct {
	OllamaClient      *client.Client
	OllamaEmbedClient *client.Client // dedicated embedding client (separate model from chat)
	Reranker          *reranker.Client
	ScriptGen         *ollama.Generator
	OllamaTranslator  *translation.OllamaTranslator
	MemoryRepo        scriptports.MemoryGate
	MemorySvc         *adapters.Service
	ScriptEngine      *scriptcore.Engine
	// WhisperTranscriber is the concrete Whisper adapter
	// (Fase 5: wired into the AcquireService for the backfill
	// CLI's 5-priority chain — priority 5: Whisper fallback).
	// The narrow texttracks.WhisperPort interface is a
	// STRUCTURAL subset of youtubeports.WhisperTranscriberPort
	// (single method TranscribeAudioWithDetection); the type
	// assertion in BuildTextTrackBundle is a no-op at runtime.
	// nil when Whisper is not configured (the chain silently
	// skips priority 5).
	WhisperTranscriber youtubeports.WhisperTranscriberPort

	// SceneTextGenerator is the canonical TextGenerator adapter
	// that implements scriptgeneration.TextGenerator by wrapping
	// ScriptEngine. Produces AI-generated scene text (scene-by-scene)
	// separate from the Translator, per the P1 verdetto.
	// nil when ScriptEngine is not available (pre-wiring guard).
	SceneTextGenerator *SceneTextGenerator

	// ScriptVoiceoverGenerator is the canonical VoiceoverGenerator
	// adapter that implements scriptgeneration.VoiceoverGenerator by
	// wrapping the TTS audio processor. Produces real audio assets from
	// scene text (not just copying existing voiceover_paths), per the
	// P1 verdetto.
	// nil when audio processor is not available (pre-wiring guard).
	ScriptVoiceoverGenerator *ScriptVoiceoverGenerator
}

// DomainBundle is everything media-specific that lives at the application layer.
type DomainBundle struct {
	CueWriter          texttracks.TimedCueWriter
	FolderPathWriter   texttracks.FolderPathWriter
	YoutubeClipService *youtube.Service

	// SubtitleFetcher is the canonical YouTube subtitle-fetcher
	// adapter (Fase 5: wired into the AcquireService for the
	// backfill CLI's 5-priority chain — priorities 3+4). The
	// composition root constructs it in
	// build_bundles_domain_media.go and exposes it here so
	// composition.go can pass it to BuildTextTrackBundle as
	// part of AcquirePorts. The narrow texttracks.SubtitlesPort
	// interface is a STRUCTURAL subset of
	// youtubeports.SubtitleFetcherPort — the type assertion
	// in BuildTextTrackBundle is a no-op at runtime.
	SubtitleFetcher youtubeports.SubtitleFetcherPort

	VoiceoverSync                *voiceoverreconcile.Service
	ImageService                 *imgservice.Service
	IngestService                *ingest.Service
	BooksService                 *books.Service
	LessonsService               *lessonsSvc.Service
	MetaWriter                   semantic.MetadataWriterPort
	RealtimeMatcher              assetsapi.RealtimeMatcher
	RealtimeSearch               scriptcore.RealtimeSearchService
	AutotagService               *autotag.Service
	AssocService                 scriptcore.AssocSearchService
	VoiceoverGenerateHandler     *voiceoverjobs.GenerateJobHandler
	VoiceoverProcessItem         voiceover.VoiceoverItemExecutor
	VoiceoverPublishPool         interface{ Wait() } // P0.4 async publish drainer for the runner
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
	// Publisher is the canonical outboxevents.Handler that drains
	// `artifact.publish_requested.v1` events into staging.Store.Stage
	// (FASE 3 / Push 3.1c, July 2026). The concrete is registered
	// inside BuildOutboxBundle after the canonical Qdrant if/else
	// block (so it runs regardless of cfg.Qdrant state); the field
	// is typed as the interface so this file does not need to
	// import the publish_outbox package directly (separation of
	// concerns — composition_types.go only describes WHAT is
	// wired, not HOW). Construction lives in
	// build_bundles_process.go::BuildOutboxBundle and consumes
	// StagingBundle.Store as its outbox→staging port (pre-condition:
	// the composition root must build StagingBundle before
	// BuildOutboxBundle so the wiring is fail-closed at
	// NewComposition time).
	Publisher outboxevents.Handler
	// DriveUploader is the canonical outboxevents.Handler that
	// drains `artifact.staged.v1` events into delivery.Publisher
	// .Publish + Repository.MarkPublished (FASE 3 / Push 3.1e,
	// July 2026). Closes the FASE 3 Publish step end-to-end:
	// stores a STAGED artifact on Disk → atomically emits
	// `artifact.staged.v1` (Push 3.1c InsertWithOutbox) → this
	// consumer drives delivery.Publisher.Publish → calls
	// Repository.MarkPublished with a canonical JSON
	// PublishedLocation payload. The concrete is registered in
	// BuildOutboxBundle after the publish_outbox Publisher
	// handler (sequenced for fail-closed reasoning); the field is
	// typed as the interface so this file does not need to
	// import the publish_drive package directly. Construction
	// lives in build_bundles_process.go::BuildOutboxBundle and
	// consumes DriveBundle.Publisher (the canonical delivery
	// gateway) + StagingBundle.Repository (the canonical
	// artifact_stages single-writer).
	DriveUploader outboxevents.Handler
}

// SyncBundle owns ONLY the catalog→Drive sync.
type SyncBundle struct {
	CatalogSync *catalogsync.Service
}

// MaintBundle owns the periodic maintenance + deletion services.
type MaintBundle struct {
	MaintenanceSvc             *maintenance.Service
	DeletionSvc                *deletion.DeletionService
	EntityImageRecertification *entitycatalog.RecertificationService
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

// StagingBundle owns the FASE 3 Spina Dorsale staging step
// (Push 3.1b, July 2026). The Store is the application-layer port
// the publisher worker pool drains the outbox into (forward-pointer
// to Push 3.1c); the Repository is the canonical single-writer of
// the artifact_stages table (the publisher worker + finalizer will
// call Mark* on this port to record PUBLISHED → SUCCEEDED /
// FAILED_PERMANENT transitions). The Workspace field is the
// resolved absolute path so downstream consumers (admin tools,
// observability dashboards) can surface the canonical root without
// re-reading config.
//
// godlike/06 SSOT: this is the SINGLE canonical staging bundle;
// the composition root instantiates it ONCE in NewComposition and
// hands the typed ports to downstream consumers via the ComposeRoot
// field. No caller (worker, finalizer, admin tool) should construct
// a second instance or read the workspace dir independently.
type StagingBundle struct {
	// Store is the application-layer FASE 3 staging port. The
	// publisher worker pool drains the outbox event into Store.Stage
	// to persist a new STAGED row + local file. The concrete is the
	// canonical *staging.StoreService; the field is typed as the
	// port so future mock-injection (test stubs) is type-safe.
	Store stagingsvc.Store

	// Repository is the canonical single-writer of the
	// artifact_stages table (the artifact.Repository port).
	// Exposed here for the publisher worker (MarkPublished) +
	// finalizer (MarkSucceeded / MarkFailedPermanent) to call
	// Mark* state transitions without round-tripping through the
	// application layer.
	Repository artifact.Repository

	// Workspace is the resolved absolute path of the staging
	// workspace (the input to StoreService.Stage). Exposed for
	// observability + admin tooling; consumers MUST NOT use this
	// to construct files (the Store is the SOLE writer).
	Workspace string
}

// FinalizerBundle owns the FASE 3 Spina Dorsale finalization
// step (Push 3.1d, July 2026). The Finalizer is the
// application-layer port that closes the publication saga's
// Finalize step — a read scan via Repository.ListByJob for a
// given job_id + a fenced-CAS write loop via Repository.
// MarkSucceeded, gated on "all REQUIRED artifacts for the job
// are in PUBLISHED state" (FASE 3 (b) fail-closed: any missing
// required artifact trips ErrArtifactRequiredMissing).
//
// godlike/06 SSOT: this is the SINGLE canonical finalizer
// bundle; the composition root instantiates it ONCE in
// NewComposition and hands the typed port to downstream
// consumers via the ComposeRoot.Finalizer field. No caller
// (publisher worker pool, admin tool, test stub) should
// construct a second instance or call the artifact.Repository
// Mark* primitives directly — every consumer reaches the typed
// port via this bundle.
//
// The Finalizer field is typed as the artifact_finalize.Finalizer
// INTERFACE (not the concrete pointer) so future mock-injection
// (test stubs in 3.1c / 3.1d extensions) is type-safe without
// re-wiring the bundle. The compile-time conformance anchor is
// `var _ Finalizer = (*finalizerService)(nil)` in
// internal/application/artifact_finalize/service.go.
type FinalizerBundle struct {
	Finalizer artifactfinalize.Finalizer
}
