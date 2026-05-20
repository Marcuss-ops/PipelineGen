package app

import (
	"database/sql"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/core/processor"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media"
	"velox/go-master/internal/media/assetindex"
	"velox/go-master/internal/media/assettree"
	"velox/go-master/internal/media/association"
	"velox/go-master/internal/media/catalogsync"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/clipresolver"
	imgservice "velox/go-master/internal/media/images"
	"velox/go-master/internal/media/indexing"
	"velox/go-master/internal/media/monitor"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"

	gdrive "google.golang.org/api/drive/v3"
	"velox/go-master/internal/media/voiceover"
	"velox/go-master/internal/media/voiceoversync"
	"velox/go-master/internal/sources/youtube"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/storage/scheduler"
	"velox/go-master/internal/upload/drive"
)

// CoreDeps holds the core dependencies of the system.
type CoreDeps struct {
	ScriptGen          *ollama.Generator
	DocClient          drive.DocClient
	DriveClient        *gdrive.Service
	Utility            *common.UtilityHandler
	DB                 *storage.SQLiteDB // Unified database
	MediaDB            *storage.SQLiteDB // Media database
	AssetsDB           *storage.SQLiteDB // Assets database
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
	StockScheduler     *scheduler.StockScheduler
	CatalogRepo        *catalog.Repository
	AssocService       *association.Service
	JobsService        *jobservice.Service
	JobsDB             *sql.DB
	MediaProcessor     processor.Processor
	YoutubeClipService *youtube.Service
	AssetIndexService  *assetindex.Service
	AssetTreeService   *assettree.Service
	ClipResolver       *clipresolver.Service
	DeletionService    *media.DeletionService
	MaintenanceService *maintenance.Service
}
