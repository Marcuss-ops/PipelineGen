package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// FolderRepository is the PostgreSQL clip_folders projection backing the
// catalog-sync folder reconciliation (schema: migrations/postgres/
// 007_media_clip_folders.sql).
//
// Writes arrive through the imagesregistry.FolderProjection port (dual-write
// from the operational SQLite table); reads serve ListFolders/GetFolder* so
// the synchroniser no longer needs the SQLite media plane. The concrete type
// is structurally asserted against the port at the composition root, which is
// the only place both packages are imported.
type FolderRepository struct {
	db *sql.DB
}

// NewFolderRepository constructs the projection reader/writer. db is required.
func NewFolderRepository(db *sql.DB) *FolderRepository {
	if db == nil {
		panic("media.NewFolderRepository: db is required")
	}
	return &FolderRepository{db: db}
}

// clipFolderColumns is the canonical projection shared by every read.
const clipFolderColumns = `
	id, source, source_url, video_id, folder_id, folder_path,
	local_folder_path, group_name, manifest_txt_path, manifest_json_path,
	clip_count, processed_count, failed_count, skipped_count,
	last_error, metadata, created_at, updated_at`

// UpsertFolder implements the read-modify-write shape of the port.
func (r *FolderRepository) UpsertFolder(ctx context.Context, folder *detail.ClipFolder) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clip_folders (
			id, source, source_url, video_id, folder_id, folder_path,
			local_folder_path, group_name, manifest_txt_path, manifest_json_path,
			clip_count, processed_count, failed_count, skipped_count,
			last_error, metadata, created_at, updated_at, search_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (id) DO UPDATE SET
			source = EXCLUDED.source,
			source_url = EXCLUDED.source_url,
			video_id = EXCLUDED.video_id,
			folder_id = EXCLUDED.folder_id,
			folder_path = EXCLUDED.folder_path,
			local_folder_path = EXCLUDED.local_folder_path,
			group_name = EXCLUDED.group_name,
			manifest_txt_path = EXCLUDED.manifest_txt_path,
			manifest_json_path = EXCLUDED.manifest_json_path,
			clip_count = EXCLUDED.clip_count,
			processed_count = EXCLUDED.processed_count,
			failed_count = EXCLUDED.failed_count,
			skipped_count = EXCLUDED.skipped_count,
			last_error = EXCLUDED.last_error,
			metadata = EXCLUDED.metadata,
			updated_at = EXCLUDED.updated_at,
			updated_at_ts = NULLIF(EXCLUDED.updated_at, '')::timestamptz,
			search_key = EXCLUDED.search_key
	`, folder.ID, folder.Source, folder.SourceURL, folder.VideoID, folder.FolderID, folder.FolderPath,
		folder.LocalFolderPath, folder.Group, folder.ManifestTXTPath, folder.ManifestJSONPath,
		folder.ClipCount, folder.ProcessedCount, folder.FailedCount, folder.SkippedCount,
		folder.LastError, folder.Metadata,
		timeutil.FormatRFC3339(folder.CreatedAt), timeutil.FormatRFC3339(folder.UpdatedAt),
		folderSearchKey(folder))
	if err != nil {
		return fmt.Errorf("postgres media: upsert clip folder %q: %w", folder.ID, err)
	}
	return nil
}

// InsertFolderIfAbsent preserves the legacy INSERT OR IGNORE semantics of the
// Drive folder resolver: an already-known folder is a no-op so aggregate
// counters and manifest pointers are never clobbered.
func (r *FolderRepository) InsertFolderIfAbsent(ctx context.Context, folder *detail.ClipFolder) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO clip_folders (
			id, source, source_url, video_id, folder_id, folder_path,
			local_folder_path, group_name, manifest_txt_path, manifest_json_path,
			clip_count, processed_count, failed_count, skipped_count,
			last_error, metadata, created_at, updated_at, search_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (id) DO NOTHING
	`, folder.ID, folder.Source, folder.SourceURL, folder.VideoID, folder.FolderID, folder.FolderPath,
		folder.LocalFolderPath, folder.Group, folder.ManifestTXTPath, folder.ManifestJSONPath,
		folder.ClipCount, folder.ProcessedCount, folder.FailedCount, folder.SkippedCount,
		folder.LastError, folder.Metadata,
		timeutil.FormatRFC3339(folder.CreatedAt), timeutil.FormatRFC3339(folder.UpdatedAt),
		folderSearchKey(folder))
	if err != nil {
		return fmt.Errorf("postgres media: insert clip folder %q: %w", folder.ID, err)
	}
	return nil
}

// DeleteFolder removes a folder projection row.
func (r *FolderRepository) DeleteFolder(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("postgres media: clip folder id is required")
	}
	if _, err := r.db.ExecContext(ctx, `DELETE FROM clip_folders WHERE id = $1`, id); err != nil {
		return fmt.Errorf("postgres media: delete clip folder %q: %w", id, err)
	}
	return nil
}

// ListFolders returns the folder projection, optionally filtered by source.
// Empty / "all" / "unified" preserve the legacy unfiltered semantics.
func (r *FolderRepository) ListFolders(ctx context.Context, source string) ([]*detail.ClipFolder, error) {
	query := `SELECT ` + clipFolderColumns + ` FROM clip_folders`
	args := []any{}
	if isFilteredFolderSource(source) {
		query += ` WHERE source = $1`
		args = append(args, source)
	}
	query += " ORDER BY " + TimestampExpr("updated_at") + " DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres media: list clip folders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var folders []*detail.ClipFolder
	for rows.Next() {
		folder, err := scanClipFolder(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres media: scan clip folder: %w", err)
		}
		folders = append(folders, folder)
	}
	return folders, rows.Err()
}

// GetFolder returns the folder with the given id, or (nil, nil) when absent.
func (r *FolderRepository) GetFolder(ctx context.Context, id string) (*detail.ClipFolder, error) {
	return r.folderBy(ctx, `id = $1`, id)
}

// GetFolderByPath returns the folder with the given Drive folder path.
func (r *FolderRepository) GetFolderByPath(ctx context.Context, folderPath string) (*detail.ClipFolder, error) {
	return r.folderBy(ctx, `folder_path = $1`, folderPath)
}

// GetFolderByVideoID returns the folder bound to a source video id.
func (r *FolderRepository) GetFolderByVideoID(ctx context.Context, videoID string) (*detail.ClipFolder, error) {
	return r.folderBy(ctx, `video_id = $1`, videoID)
}

func (r *FolderRepository) folderBy(ctx context.Context, predicate string, arg string) (*detail.ClipFolder, error) {
	query := `SELECT ` + clipFolderColumns + ` FROM clip_folders WHERE ` + predicate + ` LIMIT 1`
	row := r.db.QueryRowContext(ctx, query, arg)
	folder, err := scanClipFolder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres media: get clip folder: %w", err)
	}
	return folder, nil
}

// LookupDriveFolderIDBySourcePath returns the Drive folder id for a
// (source, folder_path) tuple, or "" when the folder is unknown.
func (r *FolderRepository) LookupDriveFolderIDBySourcePath(ctx context.Context, source, folderPath string) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx,
		`SELECT folder_id FROM clip_folders WHERE source = $1 AND folder_path = $2 LIMIT 1`,
		source, folderPath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres media: lookup clip folder id: %w", err)
	}
	return id, nil
}

// CountFolders returns the number of projected folder rows. The boot-time
// backfill uses it to verify the projection was populated.
func (r *FolderRepository) CountFolders(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clip_folders`).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres media: count clip folders: %w", err)
	}
	return n, nil
}

// scanClipFolder reads one folder from a single-row scanner.
func scanClipFolder(row interface{ Scan(dest ...any) error }) (*detail.ClipFolder, error) {
	var folder detail.ClipFolder
	var createdAt, updatedAt string
	if err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
	folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &folder, nil
}

// isFilteredFolderSource mirrors the legacy SQLite ListFolders filter: the
// pseudo-sources "all"/"unified" mean "no source predicate".
func isFilteredFolderSource(source string) bool {
	return source != "" && source != "all" && source != "unified"
}

// folderSearchKey mirrors the SQLite search_key derivation (lowercased
// group + folder path with spaces removed).
func folderSearchKey(folder *detail.ClipFolder) string {
	return strings.ToLower(strings.ReplaceAll(folder.Group+" "+folder.FolderPath, " ", ""))
}

// ── absorbed from media_folder_resolver.go ──

// MediaFolderResolver resolves the canonical Drive folder id of a media asset.
// It satisfies finalization.ArtifactFolderResolver structurally so this package
// does not import the capability.
type MediaFolderResolver struct {
	db *sql.DB
}

// NewMediaFolderResolver returns a resolver bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path.
func NewMediaFolderResolver(db *sql.DB) *MediaFolderResolver {
	if db == nil {
		panic("media.NewMediaFolderResolver: db is required")
	}
	return &MediaFolderResolver{db: db}
}

// DB exposes the underlying handle so callers can assert the resolver lives on
// the media SSOT.
func (r *MediaFolderResolver) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.db
}

// ResolveArtifactFolder returns the Drive folder id of the parent video.
//
// An empty return means "not resolved" and the caller keeps the legacy path
// (see the finalization.ArtifactFolderResolver contract) — that includes the
// unknown-asset case, which the retired SQLite statement also mapped to an empty
// string rather than an error.
func (r *MediaFolderResolver) ResolveArtifactFolder(ctx context.Context, parentVideoID string) (string, error) {
	if r == nil || r.db == nil {
		return "", fmt.Errorf("media folder resolver: media SSOT handle is not configured")
	}
	var folderID string
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(folder_id, '') FROM media_assets WHERE id = $1`,
		parentVideoID,
	).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve artifact folder %q: %w", parentVideoID, err)
	}
	return folderID, nil
}

// ── absorbed from drive_file_lister.go ──

// MediaDriveFileLister lists the asset ids of media-SSOT rows that still carry
// a non-empty Drive file id and are not soft-deleted.
type MediaDriveFileLister struct {
	db *sql.DB
}

// NewMediaDriveFileLister returns a lister bound to the PostgreSQL media
// database. The handle is required: a nil database is a programming error, not
// a degrade path, so this panics in the same way NewMediaSearcher does.
func NewMediaDriveFileLister(db *sql.DB) *MediaDriveFileLister {
	if db == nil {
		panic("media.NewMediaDriveFileLister: db is required")
	}
	return &MediaDriveFileLister{db: db}
}

// DB exposes the underlying handle so the composition root can assert it lives
// on the media SSOT (see the media write/read bridge probes).
func (l *MediaDriveFileLister) DB() *sql.DB {
	if l == nil {
		return nil
	}
	return l.db
}

// ListAssetIDsWithDriveFileID returns the media asset ids with a Drive file id.
//
// The predicate mirrors the retired SQLite statement exactly; `<>` is used
// rather than `IS DISTINCT FROM` on purpose, because `lifecycle_state <> 'DELETED'`
// is NULL — and therefore excludes the row — for a NULL lifecycle_state under
// both engines. Restating it as `IS DISTINCT FROM` would silently start
// including NULL-lifecycle rows and change the result set.
func (l *MediaDriveFileLister) ListAssetIDsWithDriveFileID(ctx context.Context) ([]string, error) {
	if l == nil || l.db == nil {
		return nil, fmt.Errorf("media drive file lister: media SSOT handle is not configured")
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE drive_file_id IS NOT NULL AND drive_file_id <> ''
		  AND lifecycle_state <> 'DELETED'
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("media drive file lister: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("media drive file lister: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("media drive file lister: iterate: %w", err)
	}
	return ids, nil
}
