package bootstrap

import (
	"database/sql"

	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/monitors"
	"velox/go-master/internal/repository/scripts"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/association"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/clipresolver"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/clipindexer"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/monitor"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/voiceoversync"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
	gdrive "google.golang.org/api/drive/v3"
)

// CoreDeps holds the core dependencies of the system.
type CoreDeps struct {
	ScriptGen            *ollama.Generator
	DocClient            drive.DocClient
	DriveClient          *gdrive.Service
	Utility              *common.UtilityHandler
	DB                   *storage.SQLiteDB // Unified database
	ArtlistDB            *storage.SQLiteDB // Artlist database
	ImagesDB             *storage.SQLiteDB // Images database
	AssetsDB             *storage.SQLiteDB // Assets database
	ScriptsRepo          *scripts.ScriptRepository
	ImageRepo            *images.Repository
	ImageService         *imgservice.Service
	StockDriveRepo       *clips.Repository
	ArtlistRepo          *clips.Repository
	ClipsOnlyRepo        *clips.Repository
	MonitorsRepo         *monitors.Repository
	VoiceoverRepo        *voiceovers.Repository
	VoiceoverService     *voiceover.Service
	VoiceoverSync        *voiceoversync.Service
	IndexingService      *indexing.Service
	ClipIndexerService   *clipindexer.Service
	CatalogSyncService   *catalogsync.Service
	ChannelMonitor       *monitor.ChannelMonitor
	StockScheduler       *scheduler.StockScheduler
	CatalogRepo          *catalog.Repository
	AssocService         *association.Service
	JobsService          *jobservice.Service
	JobsDB               *sql.DB
	MediaProcessor       processor.Processor
	YoutubeClipService   *youtubeclip.Service
	AssetIndexService    *assetindex.Service
	AssetTreeService     *assettree.Service
	ClipResolver         *clipresolver.Service
}
