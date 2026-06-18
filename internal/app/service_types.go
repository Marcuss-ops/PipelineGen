package app

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api/handlers/common"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	"github.com/Marcuss-ops/PipelineGen/internal/media/autotag"
	"github.com/Marcuss-ops/PipelineGen/internal/media/books"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/media/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/indexing"
	lessonsService "github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/media/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/ml/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/ml/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/images"
	jobrepo "github.com/Marcuss-ops/PipelineGen/internal/repository/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/scripts"
	gdrive "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	assetlocations "github.com/Marcuss-ops/PipelineGen/internal/repository/assetlocations"
	assetprocessing "github.com/Marcuss-ops/PipelineGen/internal/repository/assetprocessing"
	assetrelations "github.com/Marcuss-ops/PipelineGen/internal/repository/assetrelations"
	assettags "github.com/Marcuss-ops/PipelineGen/internal/repository/assettags"
	assetversions "github.com/Marcuss-ops/PipelineGen/internal/repository/assetversions"
	"github.com/Marcuss-ops/PipelineGen/internal/service/gemmamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/storage/scheduler"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/vlm"
)

type services struct {
	scriptGen          *ollama.Generator
	docClient          drive.DocClient
	driveUploader      *drive.Uploader
	driveClient        *gdrive.Service
	utility            *common.UtilityHandler
	scriptsRepo        *scripts.ScriptRepository
	imageRepo          *images.Repository
	imageService       *imgservice.Service
	stockDriveRepo     *clips.Repository
	clipsOnlyRepo      *clips.Repository
	driveDests         *DriveDestinations // resolved Drive folder IDs (immutable Config)
	monitorsRepo       *monitors.Repository
	voiceoverService   *voiceover.Service
	voiceoverSync      *voiceoversync.Service
	indexingService    *indexing.Service
	clipIndexerService *clipindexer.Service
	catalogRepo        *catalog.Repository
	catalogSync        *catalogsync.Service
	assocService       *association.Service
	jobsRepo           *jobrepo.Repository
	jobsService        *jobservice.Service
	jobsDispatcher     *jobservice.Dispatcher
	memoryRepo         *gemmamemory.Repository
	mediaProcessor     processor.Processor
	ollamaClient       *client.Client
	youtubeClipService *youtube.Service
	assetIndexService  *assetindex.Service
	assetTreeService   *assettree.Service
	assetResolver      *assetindex.Resolver
	lifecycleScheduler *scheduler.LifecycleScheduler
	maintenanceSvc     *maintenance.Service
	styleRegistry      *generation.StyleRegistry
	vectorSvc          *vectorstore.Service
	realtimeSvc        *realtime.Service
	vlmClient          *vlm.Client
	autotagService     *autotag.Service
	booksService       *books.Service
	lessonsService     *lessonsService.Service

	mediaStore *storage.Store

	// outboxDispatcher is the canonical ingestion entry point. Injected
	// into ingestion flows (catalogsync, voiceover, artlist orchestrator,
	// stock upload, youtube registration, manual upload, …). Admin reindex
	// uses outbox.DirectIndexer instead — the dispatcher is for production
	// writes only.
	outboxDispatcher *outbox.Dispatcher

	// Outbox events (PR5) — reliable outbox for asset.index.requested,
	// delivery, metadata_export, provider_sync, workflow.step.* handlers.
	// Replaces the legacy media_index_outbox Worker pool.
	outboxEventsRepo     *outboxevents.Repository
	outboxEventsPool     *outboxevents.Pool
	outboxEventsRegistry *outboxevents.HandlerRegistry

	// Asset satellite tables (canonical model completion, PR0)
	assetLocationsRepo  *assetlocations.Repository
	assetProcessingRepo asset.ProcessingRepository
	assetRelationsRepo  *assetrelations.Repository
	assetTagsRepo       *assettags.Repository
	assetVersionsRepo   *assetversions.Repository
}
