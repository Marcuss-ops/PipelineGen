package assets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	legacyassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
)

// NewCatalogSyncRepository returns the catalog-sync media repository +
// indexer. PostgreSQL is the media SSOT, so the GetClip existence check and
// GetIndexState read it whenever the handle is wired (MEDIA-SSOT read
// split-brain fix); folder operations stay on the operational SQLite
// repository until the clip_folders writers migrate.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21, sub-wave B): the degrade
// branch keeps serving the folder/GetClip half from `legacy`, but it no longer
// serves the INDEX-STATE half from the mirror. An index state is a media fact,
// the engine decision point (mediasub.RequireMediaPostgres) declares that there
// is no SQLite fallback and that the operational mirror holds no committed
// media rows, so the slot is answered by noMediaPlaneIndexState, which fails
// closed. The former *assets.ClipsRepository.GetIndexState implementation is
// deleted with it.
func NewCatalogSyncRepository(mediaDB *sql.DB, legacy *legacyassets.ClipsRepository) (catalogsync.CatalogRepository, catalogsync.AssetIndexer) {
	if mediaDB != nil && legacy != nil {
		router := &postgresCatalogRepository{media: pgmedia.NewMediaSearcher(mediaDB), legacy: legacy}
		// The folder list is read from PostgreSQL only once the projection is
		// actually wired (dual-write + boot backfill). An unpopulated
		// projection would under-report folders, so pruneMissingFolders would
		// silently skip pruning (stale rows survive) instead of reconciling;
		// the read therefore stays on SQLite until the projection owns the data.
		if legacy.FolderProjectionWired() {
			router.folders = pgmedia.NewFolderRepository(mediaDB)
		}
		return router, router
	}
	// No media plane: the folder/GetClip degrade stays on the operational store,
	// the index-state slot fails closed (never the mirror — see
	// noMediaPlaneIndexState). A nil legacy is returned as a true nil interface
	// so the catalogsync target validation sees the missing port instead of a
	// typed-nil it cannot detect.
	if legacy == nil {
		return nil, nil
	}
	return legacy, noMediaPlaneIndexState{}
}

// postgresCatalogRepository routes media reads (GetClip/GetIndexState) to the
// PostgreSQL media SSOT while folder reconciliation remains on the SQLite
// clip_folders repository.
//
// clip_folders is deliberately NOT read from PostgreSQL yet: every writer still
// persists it in SQLite, so a PG-only folder read would observe an empty
// projection. Migrating the folder write path is a prerequisite for moving the
// folder reads.
type postgresCatalogRepository struct {
	media   catalogMediaReader
	folders catalogFolderReader
	legacy  *legacyassets.ClipsRepository
}

// catalogMediaReader is the narrow PostgreSQL read surface the catalog router
// consumes. *pgmedia.MediaSearcher implements it.
type catalogMediaReader interface {
	GetAsset(ctx context.Context, assetID string) (*pgmedia.MediaAssetRecord, error)
}

// catalogFolderReader is the narrow PostgreSQL folder projection the catalog
// router consumes. *pgmedia.FolderRepository implements it.
type catalogFolderReader interface {
	ListFolders(ctx context.Context, source string) ([]*detail.ClipFolder, error)
}

var (
	_ catalogsync.CatalogRepository = (*postgresCatalogRepository)(nil)
	_ catalogsync.AssetIndexer      = (*postgresCatalogRepository)(nil)
)

func (r *postgresCatalogRepository) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	rec, err := r.media.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, pgmedia.ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return catalogAssetFromRecord(rec), nil
}

func (r *postgresCatalogRepository) GetIndexState(ctx context.Context, id string) (asset.IndexState, error) {
	rec, err := r.media.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, pgmedia.ErrMediaAssetNotFound) {
			// Preserve the legacy SQLite contract: a missing row is sql.ErrNoRows.
			return "", sql.ErrNoRows
		}
		return "", err
	}
	return asset.IndexState(rec.IndexState), nil
}

func (r *postgresCatalogRepository) ListFolders(ctx context.Context, source string) ([]*detail.ClipFolder, error) {
	if r.folders != nil {
		return r.folders.ListFolders(ctx, source)
	}
	return r.legacy.ListFolders(ctx, source)
}

// DeleteFolder writes through the operational repository, whose attached
// PostgreSQL projection mirrors the delete. Routing the delete here (rather
// than straight at the projection) keeps the two stores converged: a
// projection-only delete would leave a stale SQLite row that the next sync
// would resurrect via the folder list.
func (r *postgresCatalogRepository) DeleteFolder(ctx context.Context, id string) error {
	return r.legacy.DeleteFolder(ctx, id)
}

// catalogAssetFromRecord reconstructs the kernel asset the catalog-sync
// preservation logic consumes. Typed columns are merged over the metadata_json
// object so the legacy accessors (LocalPath/ContentSHA256/FolderID/…) resolve.
func catalogAssetFromRecord(rec *pgmedia.MediaAssetRecord) *asset.Asset {
	if rec == nil {
		return nil
	}
	meta := rec.MetadataMap()
	meta["local_path"] = rec.LocalPath
	meta["content_hash"] = rec.SHA256
	meta["drive_file_id"] = rec.DriveFileID
	meta["folder_id"] = rec.FolderID
	meta["parent_folder_id"] = rec.ParentFolderID
	meta["folder_path"] = rec.FolderPath
	return &asset.Asset{
		ID:             rec.ID,
		Source:         asset.Source(rec.Source),
		Name:           rec.Name,
		Filename:       rec.Filename,
		MediaType:      asset.MediaType(rec.MediaType),
		Category:       rec.Category,
		SourceURL:      rec.SourceURL,
		ThumbnailURL:   rec.ThumbnailURL,
		Duration:       time.Duration(rec.DurationMS) * time.Millisecond,
		Tags:           append([]string(nil), rec.Tags...),
		LifecycleState: asset.LifecycleState(rec.LifecycleState),
		Metadata:       meta,
		CreatedAt:      rec.CreatedAtTime(),
	}
}
