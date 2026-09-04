package wiring

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	mwidem "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/monitors"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/catalog"
	sqlitescripts "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/scripts"
)

// DriveBundle owns the composed Google Drive ports and delivery surfaces.
type DriveBundle struct {
	Admin         drive.Admin
	Reader        drive.Reader
	DocClient     drive.DocClient
	DocPublisher  delivery.DocPublisher
	DriveDests    *DriveDestinations
	DestResolver  asset.Resolver
	StyleRegistry *generation.StyleRegistry
	Publisher     delivery.Publisher
	Lifecycle     drive.FileLifecycle
	DriveUploader *drive.Uploader
}

// RepoBundle owns the repository instances shared by the composition root.
type RepoBundle struct {
	ScriptsRepo        *sqlitescripts.ScriptRepository
	ImageRepo          *imagesrepo.ImagesRepository
	AssetsStore        *imagesregistry.AssetStoreSQLite
	ClipsRepo          *assets.ClipsRepository
	Assets             *detail.Service
	MonitorsRepo       *monitors.MonitorsRepository
	VoiceoverRepo      *assets.VoiceoversRepository
	CatalogRepo        *catalog.Repository
	EntityImageCatalog entitycatalog.Repository
	SQRepo             *imagesregistry.SearchQueriesRepository
	IdempotencyStore   mwidem.IdempotencyStore
	TextTrackRepo        detail.TextTrackRepository
	SubtitleArtifactRepo detail.SubtitleArtifactRepository
}
