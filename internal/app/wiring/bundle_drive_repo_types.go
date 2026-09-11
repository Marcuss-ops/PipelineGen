package wiring

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
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
	StyleRegistry *imagestyles.StyleRegistry
	Publisher     delivery.Publisher
	Lifecycle     drive.FileLifecycle
	DriveUploader *drive.Uploader
}

// RepoBundle owns the repository instances shared by the composition root.
//
// MEDIA-SSOT boundary (P1-7, September 2026): media-authoritative surfaces
// are duplicated into MediaRepoBundle (PostgreSQL SSOT). RepoBundle retains
// operational SQLite surfaces only. New media reads/writes MUST target
// MediaRepoBundle; reusing RepoBundle for media risks reintroducing the
// SQLite read/mutation bypass.
type RepoBundle struct {
	ScriptsRepo          *sqlitescripts.ScriptRepository
	ImageRepo            *imagesrepo.ImagesRepository
	AssetsStore          *imagesregistry.AssetStoreSQLite // operational-only legacy; media path uses PG
	ClipsRepo            *assets.ClipsRepository          // legacy operational facade; media lifecycle via PG saga
	Assets               *detail.Service                  // legacy detail.Service (SQLite); media hydration via pg MediaSearcher
	MonitorsRepo         *monitors.MonitorsRepository
	VoiceoverRepo        *assets.VoiceoversRepository
	CatalogRepo          *catalog.Repository
	EntityImageCatalog   entitycatalog.Repository
	SQRepo               *imagesregistry.SearchQueriesRepository
	IdempotencyStore     mwidem.IdempotencyStore
	TextTrackRepo        detail.TextTrackRepository
	SubtitleArtifactRepo detail.SubtitleArtifactRepository
}

// MediaRepoBundle is the media SSOT bundle (PostgreSQL). It is the sole
// owner of media-authoritative reads/writes (P1-7). Operational SQLite
// surfaces stay in RepoBundle / other bundles.
type MediaRepoBundle struct {
	DB            *sql.DB
	TextTrackRepo detail.TextTrackRepository
}

// NewMediaRepoBundle constructs the media SSOT bundle from the PG handle.
// Returns nil when the PG handle is nil (degraded mode).
func NewMediaRepoBundle(pgDB *sql.DB, textTrackRepo detail.TextTrackRepository) *MediaRepoBundle {
	if pgDB == nil {
		return nil
	}
	return &MediaRepoBundle{DB: pgDB, TextTrackRepo: textTrackRepo}
}
