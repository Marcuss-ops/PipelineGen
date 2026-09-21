package wiring

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// catalogSyncSourceReader adapts the broad infrastructure Drive reader to the
// narrow application-owned SourceReader contract.
type catalogSyncDriveReader interface {
	GetFileMeta(ctx context.Context, fileID string) (*drive.FileMeta, error)
	ListFiles(ctx context.Context, parentID string) ([]drive.DriveFileInfo, error)
}

type catalogSyncSourceReader struct {
	reader catalogSyncDriveReader
}

func (a catalogSyncSourceReader) GetFileMeta(ctx context.Context, fileID string) (*catalogsync.RemoteFileMeta, error) {
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil || meta == nil {
		return nil, err
	}
	return &catalogsync.RemoteFileMeta{
		ID:          meta.ID,
		Name:        meta.Name,
		MimeType:    meta.MimeType,
		WebViewLink: meta.WebViewLink,
	}, nil
}

func (a catalogSyncSourceReader) ListFiles(ctx context.Context, parentID string) ([]catalogsync.RemoteFile, error) {
	files, err := a.reader.ListFiles(ctx, parentID)
	if err != nil {
		return nil, err
	}
	result := make([]catalogsync.RemoteFile, 0, len(files))
	for _, file := range files {
		result = append(result, catalogsync.RemoteFile{
			ID:             file.ID,
			Name:           file.Name,
			MimeType:       file.MimeType,
			Size:           file.Size,
			MD5Checksum:    file.MD5Checksum,
			WebViewLink:    file.WebViewLink,
			WebContentLink: file.WebContentLink,
			Parents:        file.Parents,
		})
	}
	return result, nil
}

var (
	_ catalogsync.SourceReader = catalogSyncSourceReader{}
	_ catalogSyncDriveReader   = (*drive.Uploader)(nil)
)

// The concrete SQLite repository implements the catalog storage ports that are
// still operational (folder reconciliation + the GetClip existence check).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21, sub-wave B): the
// catalogsync.AssetIndexer pin is REMOVED together with
// *assets.ClipsRepository.GetIndexState. An index state is a media fact, so it
// no longer has an operational-mirror implementation; the deployment without a
// media plane gets noMediaPlaneIndexState instead.
var (
	_ catalogsync.CatalogRepository    = (*assets.ClipsRepository)(nil)
	_ catalogsync.ProjectionDispatcher = (*outbox.Dispatcher)(nil)
)

// CatalogSyncPorts returns the catalog-sync repository + indexer pair for a
// caller that drives catalogsync outside the boot-time target set (the admin
// sync-drive-folder command).
//
// It exists so that command and BuildSyncBundle share ONE engine decision
// function and cannot drift: the PostgreSQL media SSOT when the media handle is
// wired, otherwise the operational folder repository with a fail-closed
// index-state slot. Before this helper the admin command handed the SQLite
// mirror in for both slots unconditionally, which is how a media READ stayed on
// the operational store in a command that composes the full stack.
func CatalogSyncPorts(mediaDB *sql.DB, legacy *assets.ClipsRepository) (catalogsync.CatalogRepository, catalogsync.AssetIndexer) {
	return newCatalogSyncRepository(mediaDB, legacy)
}

// errNoMediaPlaneIndexState is returned by noMediaPlaneIndexState: the
// deployment has no media plane, so no component can know an asset's index
// state.
var errNoMediaPlaneIndexState = errors.New("catalogsync index state: the media plane is not deployed (media_postgresql disabled) and the operational SQLite mirror holds no committed media rows — enable the media PostgreSQL SSOT to read index state")

// noMediaPlaneIndexState is the fail-closed catalogsync.AssetIndexer for a
// deployment that has no media plane at all (media_postgresql disabled).
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21, sub-wave B): this slot used to
// be answered by *assets.ClipsRepository.GetIndexState, i.e. by reading
// media_assets.index_state off the operational SQLite mirror. The media plane's
// only engine decision point (mediasub.RequireMediaPostgres) documents that
// there is NO SQLite/Qdrant fallback and that the operational mirror holds no
// committed media rows, so that read could only ever report the migration
// DEFAULT sentinel for a row PostgreSQL owns: an answer it cannot know, from a
// plane that is not deployed. The mirror read is deleted and this adapter
// answers the same slot explicitly.
//
// The slot cannot simply be nil: catalogsync.NewService rejects a target whose
// Indexer is nil (fail-closed boot validation), so an explicit adapter is the
// honest shape — not a mirror read, and not a fabricated availability.
type noMediaPlaneIndexState struct{}

var _ catalogsync.AssetIndexer = noMediaPlaneIndexState{}

// GetIndexState reports that the index state is unknown because no media plane
// is deployed. asset.StateDiscovered is the canonical "not proven indexed"
// value the port contract requires (an index-state read is diagnostic and must
// never suppress a new index intent); the error is the authoritative answer for
// any caller that branches on it.
func (noMediaPlaneIndexState) GetIndexState(context.Context, string) (asset.IndexState, error) {
	return asset.StateDiscovered, errNoMediaPlaneIndexState
}
