package wiring

import (
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
	Committer          assetspersistence.AssetCommitter
	MediaExec          mediaexec.ExecutionConfig
	DB                 *storage.SQLiteDB
	Assets             *detail.Service
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
