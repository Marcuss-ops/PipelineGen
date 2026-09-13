package wiring

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providerassets"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assetindex"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	gdrive "google.golang.org/api/drive/v3"
)

// ArtlistBundle is the typed composition input for the Artlist module.
type ArtlistBundle struct {
	Committer assetspersistence.AssetCommitter
	MediaExec mediaexec.ExecutionConfig
	DB        *storage.SQLiteDB
	// MediaDB is the PostgreSQL media SSOT handle (root.MediaPostgres). It is
	// threaded into the Artlist finalizer so the persist transaction runs on
	// the same engine as the canonical committer (MEDIA-SSOT P0-3).
	MediaDB *sql.DB
	// NOTE: the legacy `Assets *detail.Service` field was DELETED (zero
	// production readers). The Artlist DB-only read surface runs on the
	// PostgreSQL media SSOT via artlistMediaSSOTAssetStore, so keeping a
	// second (SQLite) asset service in this bundle would only invite a
	// split-brain read.
	ClipsRepo          *assets.ClipsRepository
	DriveClient        *gdrive.Service
	DriveUploader      *driveup.Uploader
	Publisher          delivery.Publisher
	ClipResolver       *ClipResolverRecommendAdapter
	AssetIndexService  *assetindex.Service
	ClipIndexerService *clipindexer.Service
	MediaProcessor     detail.Processor
	Jobs               *JobsBundle
	CatalogSyncService *catalogsync.Service
	TextTrackRepo      detail.TextTrackRepository
}

// ArtlistWiring is the final Artlist module surface published by the registry.
type ArtlistWiring struct {
	Module            api.Module
	Service           *artlistPkg.Service
	ProviderAssets    *providerassets.Registry
	ArtlistDownloader artlistPkg.Downloader
	LicenseRepo       asset.LicenseRepository
	ReleaseRepo       asset.ReleaseRepository
	RenditionRepo     detail.RenditionRepository
}
