package app

import (
	gdrive "google.golang.org/api/drive/v3"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	jobrepo "velox/go-master/internal/repository/jobs"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/clipindexer"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/indexing"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/storage/scheduler"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/internal/repository/sketchfab"
	sketchfabservice "velox/go-master/internal/media/sketchfab"
)

type services struct {
	scriptGen          *ollama.Generator
	docClient          drive.DocClient
	driveClient        *gdrive.Service
	utility            *common.UtilityHandler
	scriptsRepo        *scripts.ScriptRepository
	imageRepo          *images.Repository
	imageService       *imgservice.Service
	stockDriveRepo     *clips.Repository
	artlistRepo        *clips.Repository
	clipsOnlyRepo      *clips.Repository
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
	mediaProcessor     processor.Processor
	youtubeClipService *youtube.Service
	assetIndexService  *assetindex.Service
	assetTreeService   *assettree.Service
	assetResolver      *assetindex.Resolver
	lifecycleScheduler *scheduler.LifecycleScheduler
	maintenanceSvc     *maintenance.Service
	sketchfabRepo      *sketchfab.Repository
	sketchfabService   *sketchfabservice.Service
}
