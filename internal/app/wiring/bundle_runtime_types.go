package wiring

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/maintenance"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	assetstorage "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	systemhealth "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system/health"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)
import (
	artifactfinalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifact_finalize"
	stagingsvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/staging"
	artifact "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)
import (
	vowiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	assetsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase"
	scriptgencore "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/usecase/gencore"
	translation "github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	voiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	voiceoverjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/jobs"
	voicesync "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/sync"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/platform/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// OutboxBundle aggregates the canonical outbox dispatcher and event workers.
type OutboxBundle struct {
	CanonicalWriter assetspersistence.CanonicalAssetWriter
	Dispatcher      *outbox.Dispatcher
	EventsRepo      *outboxevents.Repository
	EventsRegistry  *outboxevents.HandlerRegistry
	EventsPool      *outboxevents.Pool
	// JobsEventsRegistry is the split jobs plane's outbox registry — the one
	// JobsEventsPool drains jobs.db with. Execution-plane completion events
	// (job.completed, finalize delivery intents) live in that outbox, so their
	// consumers MUST register here, never on EventsRegistry. Without a split
	// plane this is the same instance as EventsRegistry.
	JobsEventsRegistry *outboxevents.HandlerRegistry
	JobsEventsPool     *outboxevents.Pool
	MediaIndexWorker   *pgmedia.PostgresIndexWorker
	Publisher          outboxevents.Handler
	DriveUploader      outboxevents.Handler

	// MediaIndexRequester is the canonical PostgreSQL media index-plane
	// request port. The TextTrackMaterializer emits its post-translation
	// reindex through it (asset.index.requested into the PostgreSQL outbox)
	// instead of the operational SQLite outbox, which has no media handler
	// in any mode. nil when the PostgreSQL media plane is not deployed.
	MediaIndexRequester texttracks.IndexRequester
	// MediaSearchTextRebuilder recomposes media_assets.search_text from the
	// asset metadata + READY transcripts (original + translations) before
	// the reindex is requested, so the translations enter the E5 embedding.
	// nil when the PostgreSQL media plane is not deployed.
	MediaSearchTextRebuilder texttracks.SearchTextRebuilder
}

// SyncBundle owns only catalog-to-Drive synchronization.
type SyncBundle struct {
	CatalogSync *catalogsync.Service
}

// MaintBundle owns periodic maintenance and deletion services.
type MaintBundle struct {
	MaintenanceSvc             *maintenance.Service
	DeletionSvc                *deletion.DeletionService
	EntityImageRecertification *entitycatalog.RecertificationService
}

// UtilityBundle owns lightweight system utility services.
type UtilityBundle struct {
	HealthService *systemhealth.Service
	ReadyChecker  *systemhealth.ReadyChecker
}

// AssetsWiring holds the externally exposed assets module wiring.
type AssetsWiring struct {
	Module               module.Module
	DeletionSvc          *deletion.DeletionService
	InternalMediaHandler *assetstorage.Handler
	SearchAggregator     *assetsearch.Aggregator
	SearchFanOut         assetsearch.SearchFanOut
}

// StagingBundle owns the canonical staging store, repository and resolved
// workspace used by the publication saga.
type StagingBundle struct {
	Store      stagingsvc.Store
	Repository artifact.ArtifactStageRepository
	Workspace  string
}

// FinalizerBundle owns the typed finalization surface for the publication saga.
type FinalizerBundle struct {
	Finalizer artifactfinalize.Finalizer
}

// AIBundle owns script generation and AI composition surfaces.
type AIBundle struct {
	OllamaClient             *client.Client
	OllamaEmbedClient        *client.Client
	Reranker                 *reranker.Client
	ScriptGen                *ollama.Generator
	OllamaTranslator         *translation.OllamaTranslator
	MemoryRepo               scriptports.MemoryGate
	MemorySvc                *processor.Service
	ScriptEngine             *scriptgencore.Engine
	WhisperTranscriber       youtubeports.WhisperTranscriberPort
	SceneTextGenerator       *SceneTextGenerator
	ScriptVoiceoverGenerator *vowiring.ScriptVoiceoverGenerator
}

// DomainBundle is the remaining application-level domain dependency bag. New
// wiring should prefer capability-owned Deps structs instead of adding fields.
type DomainBundle struct {
	CueWriter          texttracks.TimedCueWriter
	FolderPathWriter   texttracks.FolderPathWriter
	YoutubeClipService *youtube.Service
	SubtitleFetcher    youtubeports.SubtitleFetcherPort

	VoiceoverSync                *voicesync.Service
	ImageService                 *images.Service
	IngestService                *ingest.Service
	MetaWriter                   semantic.MetadataWriterPort
	RealtimeMatcher              assetsapi.RealtimeMatcher
	RealtimeSearch               scriptcore.RealtimeSearchService
	AutotagService               *autotag.Service
	AssocService                 scriptcore.AssocSearchService
	VoiceoverGenerateHandler     *voiceoverjobs.GenerateJobHandler
	VoiceoverProcessItem         voiceover.VoiceoverItemExecutor
	VoiceoverPublishPool         interface{ Wait() }
	VoiceoverGenerateItemHandler *voiceoverjobs.GenerateItemJobHandler
	ArtifactService              *artifacts.Service
	ImageSearchResolver          images.ImageSearchResolver
	AudioProcessor               *audioasset.Processor
}
