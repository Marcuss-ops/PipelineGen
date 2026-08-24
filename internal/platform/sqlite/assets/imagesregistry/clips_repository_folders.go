package imagesregistry

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

func (r *ClipsRepository) UpsertFolder(ctx context.Context, folder *asset.ClipFolder) error {
	mBytes, err := json.Marshal(folder)
	if err != nil {
		return err
	}
	var assetsFolder asset.ClipFolder
	if err := json.Unmarshal(mBytes, &assetsFolder); err != nil {
		return err
	}
	return r.AssetStoreSQLite.UpsertFolder(ctx, &assetsFolder)
}

func (r *ClipsRepository) GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error) {
	folder, err := r.AssetStoreSQLite.GetFolder(ctx, folderID)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, nil
	}
	mBytes, _ := json.Marshal(folder)
	var mFolder asset.ClipFolder
	_ = json.Unmarshal(mBytes, &mFolder)
	return &mFolder, nil
}

func (r *ClipsRepository) GetFolderByVideoID(ctx context.Context, videoID string) (*asset.ClipFolder, error) {
	folder, err := r.AssetStoreSQLite.GetFolderByVideoID(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if folder == nil {
		return nil, nil
	}
	mBytes, _ := json.Marshal(folder)
	var mFolder asset.ClipFolder
	_ = json.Unmarshal(mBytes, &mFolder)
	return &mFolder, nil
}

func (r *ClipsRepository) GetFolderByPath(ctx context.Context, folderPath string) (*asset.ClipFolder, error) {
	query := "SELECT id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at FROM clip_folders WHERE folder_path = ? LIMIT 1"
	row := r.db.QueryRowContext(ctx, query, folderPath)
	var folder asset.ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
	folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &folder, nil
}

func (r *ClipsRepository) ListFolders(ctx context.Context, source string) ([]*asset.ClipFolder, error) {
	folders, err := r.AssetStoreSQLite.ListFolders(ctx, source)
	if err != nil {
		return nil, err
	}
	out := make([]*asset.ClipFolder, len(folders))
	for i, f := range folders {
		mBytes, _ := json.Marshal(f)
		var mFolder asset.ClipFolder
		_ = json.Unmarshal(mBytes, &mFolder)
		out[i] = &mFolder
	}
	return out, nil
}

// DriveFolderAttrs captures the columns of the clip_folders table needed by
// the Drive folder resolver. The struct is exported so the composition
// glue can populate it without the caller holding raw *sql.DB.
type DriveFolderAttrs struct {
	Source     string
	SourceURL  string
	FolderID   string
	FolderPath string
	GroupName  string
	CreatedAt  string
	UpdatedAt  string
}

// LookupDriveFolderIDBySourcePath returns the folder_id stored in
// clip_folders for the given (source, folder_path) tuple, or empty
// string when no row exists. Raw sql.ErrNoRows is rewritten to "" so
// callers can use the result as a "is present?" predicate.
func (r *ClipsRepository) LookupDriveFolderIDBySourcePath(ctx context.Context, source, folderPath string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx,
		`SELECT folder_id FROM clip_folders WHERE source = ? AND folder_path = ? LIMIT 1`,
		source, folderPath,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// UpsertDriveFolder writes the (source, folder_id) row into clip_folders.
// INSERT OR IGNORE semantics match the legacy bootstrap.go behavior so
// re-running the resolver on an already-known folder is a no-op.
func (r *ClipsRepository) UpsertDriveFolder(ctx context.Context, attrs DriveFolderAttrs) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO clip_folders
		(id, source, source_url, folder_id, folder_path, group_name, created_at, updated_at, search_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		attrs.FolderID, attrs.Source, attrs.SourceURL, attrs.FolderID,
		attrs.FolderPath, attrs.GroupName, attrs.CreatedAt, attrs.UpdatedAt,
		strings.ToLower(strings.ReplaceAll(attrs.Source+attrs.FolderPath, " ", "")),
	)
	return err
}

// StreamAssetIDs pages through `SELECT id FROM media_assets LIMIT ? OFFSET ?`
// rows calling onPage once per non-empty page. The callback can abort
// iteration by returning a non-nil error; that error is propagated
// verbatim. ctx.Err() is honored between pages.
