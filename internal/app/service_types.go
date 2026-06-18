package app

import (
	gdrive "google.golang.org/api/drive/v3"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/core/processor"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/media/autotag"
	"velox/go-master/internal/media/books"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/generation"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/indexing"
	lessonsService "velox/go-master/internal/media/lessons"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/outbox"
	"velox/go-master/internal/repository/outboxevents"
	"velox/go-master/internal/repository/scripts"

	assetprocessing "velox/go-master/internal/repository/assetprocessing"
	assetrelations "velox/go-master/internal/repository/assetrelations"
	assettags "velox/go-master/internal/repository/assettags"
	assetversions "velox/go-master/internal/repository/assetversions"
	"velox/go-master/internal/service/gemmamemory"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/storage/scheduler"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/vlm"
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
	assetProcessingRepo *assetprocessing.Repository
	assetRelationsRepo  *assetrelations.Repository
	assetTagsRepo       *assettags.Repository
	assetVersionsRepo   *assetversions.Repository
}
