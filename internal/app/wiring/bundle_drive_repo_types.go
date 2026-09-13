package wiring

import (
	"database/sql"

	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	imagestyles "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/styles"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	mwidem "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
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
	IdempotencyStore     mwidem.IdempotencyStore
	TextTrackRepo        detail.TextTrackRepository
	SubtitleArtifactRepo detail.SubtitleArtifactRepository
}

// MediaRepoBundle is the media SSOT bundle (PostgreSQL). It is the sole
// owner of media-authoritative reads/writes (P1-7). Operational SQLite
// surfaces stay in RepoBundle / other bundles.
//
// MEDIA-SSOT P1-7 (September 2026): the bundle exposes the canonical media
// READER and IDENTITY resolver up front so consumers have a single, complete
// place to look instead of reaching into the operational SQLite RepoBundle.
// Writer is attached once the canonical committer exists (it is built by
// BuildOutboxBundle, after this bundle).
type MediaRepoBundle struct {
	DB       *sql.DB
	Reader   *pgmedia.MediaSearcher
	Identity capregistry.CanonicalIdentityResolver
	// Writer is the canonical media committer (pgmedia.PostgresMediaCommitter),
	// attached after BuildOutboxBundle constructs it.
	Writer        assetspersistence.CanonicalAssetWriter
	TextTrackRepo detail.TextTrackRepository
}

// NewMediaRepoBundle constructs the media SSOT bundle from the PG handle.
// Returns nil when the PG handle is nil (degraded mode).
func NewMediaRepoBundle(pgDB *sql.DB, textTrackRepo detail.TextTrackRepository) *MediaRepoBundle {
	if pgDB == nil {
		return nil
	}
	bundle := &MediaRepoBundle{DB: pgDB, TextTrackRepo: textTrackRepo}
	// The reader and identity resolver are derived from the same handle the
	// committer writes, so reads and writes cannot drift onto two engines.
	bundle.Reader = pgmedia.NewMediaSearcher(pgDB)
	if identity, err := pgmedia.NewPostgresCanonicalIdentityResolver(pgDB); err == nil {
		bundle.Identity = identity
	}
	return bundle
}
