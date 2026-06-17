package app

import (
	"context"

	booksHandler "velox/go-master/internal/api/handlers/books"
	"velox/go-master/internal/api/handlers/common"
	lessonsHandler "velox/go-master/internal/api/handlers/lessons"
	"velox/go-master/internal/api/handlers/script/handlers"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/core/processor"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/association"
	booksService "velox/go-master/internal/media/books"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/clipresolver"
	"velox/go-master/internal/media/generation"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/indexing"
	lessonsService "velox/go-master/internal/media/lessons"
	"velox/go-master/internal/media/monitor"
	"velox/go-master/internal/media/realtime"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/gemmamemory"
	"velox/go-master/internal/service/scriptcore"

	gdrive "google.golang.org/api/drive/v3"
	mediastorage "velox/go-master/internal/media/storage"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
)

// CoreDeps holds the core dependencies of the system.
type CoreDeps struct {
	Context            context.Context
	ScriptGen          *ollama.Generator
	DocClient          drive.DocClient
	DriveUploader      *drive.Uploader
	DriveClient        *gdrive.Service
	Utility            *common.UtilityHandler
	DB                 *storage.SQLiteDB // Unified database (scripts, jobs, asset index, media assets)
	ScriptsRepo        *scripts.ScriptRepository
	ImageRepo          *images.Repository
	ImageService       *imgservice.Service
	StockDriveRepo     *clips.Repository
	ArtlistRepo        *clips.Repository
	ClipsOnlyRepo      *clips.Repository
	MonitorsRepo       *monitors.Repository
	VoiceoverRepo      *voiceovers.Repository
	VoiceoverService   *voiceover.Service
	VoiceoverSync      *voiceoversync.Service
	IndexingService    *indexing.Service
	ClipIndexerService *clipindexer.Service
	CatalogSyncService *catalogsync.Service
	ChannelMonitor     *monitor.ChannelMonitor
	CatalogRepo        *catalog.Repository
	AssocService       *association.Service
	JobsService        *jobservice.Service
	MediaProcessor     processor.Processor
	YoutubeClipService *youtube.Service
	AssetIndexService  *assetindex.Service
	AssetTreeService   *assettree.Service
	StyleRegistry      *generation.StyleRegistry
	ClipResolver       *clipresolver.Service
	VectorStore        *vectorstore.Service
	RealtimeService    *realtime.Service
	DeletionService    *media.DeletionService
	MaintenanceService *maintenance.Service
	MemoryService      *gemmamemory.Service
	ScriptEngine       *scriptcore.Engine
	ScriptFlowHandler  *handlers.ScriptFlowHandler
	BooksService       *booksService.Service
	BooksHandler       *booksHandler.Handler
	LessonsService     *lessonsService.Service
	LessonsHandler     *lessonsHandler.Handler
	MediaStore         *mediastorage.Store

	// startJobRunner is set by initCoreMinimal and invoked by WireServices
	// after WireRegistry completes, ensuring all job handlers are registered
	// before workers begin claiming jobs.
	startJobRunner func()
}
