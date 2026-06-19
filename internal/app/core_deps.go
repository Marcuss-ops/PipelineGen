package app

import (
	"context"

	booksHandler "github.com/Marcuss-ops/PipelineGen/internal/api"
	common "github.com/Marcuss-ops/PipelineGen/internal/api"
	lessonsHandler "github.com/Marcuss-ops/PipelineGen/internal/api"
	handlers "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/core/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/core/processor"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/media"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/media/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/media/association"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/deliveries"
	booksService "github.com/Marcuss-ops/PipelineGen/internal/media/books"
	"github.com/Marcuss-ops/PipelineGen/internal/media/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/media/clipresolver"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/media/images"
	"github.com/Marcuss-ops/PipelineGen/internal/media/indexing"
	lessonsService "github.com/Marcuss-ops/PipelineGen/internal/media/lessons"
	"github.com/Marcuss-ops/PipelineGen/internal/media/monitor"
	"github.com/Marcuss-ops/PipelineGen/internal/media/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/images"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	"github.com/Marcuss-ops/PipelineGen/internal/scripts/gemmamemory"
	scriptcore "github.com/Marcuss-ops/PipelineGen/internal/scripts"

	gdrive "google.golang.org/api/drive/v3"
	mediastorage "github.com/Marcuss-ops/PipelineGen/internal/media/storage"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/media/voiceoversync"
	"github.com/Marcuss-ops/PipelineGen/internal/sources/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/database"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
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
	ScriptsRepo        *scriptcore.ScriptRepository
	ImageRepo          *images.Repository
	ImageService       *imgservice.Service
	ClipsRepo          *clips.Repository // canonical unified clips repository
	Assets             *assets.Service   // unified assets service authority (PR2)
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
	ArtifactService    *artifacts.Service
	DeliveryService    *deliveries.Service
	DeliveryRunner     *deliveries.Runner

	// startJobRunner is set by initCoreMinimal and invoked by WireServices
	// after WireRegistry completes, ensuring all job handlers are registered
	// before workers begin claiming jobs.
	startJobRunner func()
}
