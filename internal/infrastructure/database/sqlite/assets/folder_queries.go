// Package assets — clip_folders SQL queries (Wave C: moved from
// internal/kernel/asset/clips_core.go).
//
// ClipFolder/ClipManifest/ClipFolderStats/ClipManifestItem types stay
// in domain (canonical orchestration contracts). The 9 SQL receivers
// migrate to this infra file.
package assets

import (
	"context"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	sqlutil "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── SQL receivers (migrated from clips_core.go) ──────────────────────

// UpsertFolder inserts or updates a clip_folders row.
func (s *AssetStoreSQLite) UpsertFolder(ctx context.Context, folder *asset.ClipFolder) error {
	now := time.Now()
	// Compute search key: lowercase group + folder path, remove spaces
	searchKey := strings.ToLower(folder.Group + " " + folder.FolderPath)
	searchKey = strings.ReplaceAll(searchKey, " ", "")

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO clip_folders (id, source, source_url, video_id, folder_id, folder_path,
			local_folder_path, group_name, manifest_txt_path, manifest_json_path,
			clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at, search_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source, source_url=excluded.source_url, video_id=excluded.video_id,
			folder_id=excluded.folder_id, folder_path=excluded.folder_path,
			local_folder_path=excluded.local_folder_path, group_name=excluded.group_name,
			manifest_txt_path=excluded.manifest_txt_path, manifest_json_path=excluded.manifest_json_path,
			clip_count=excluded.clip_count, processed_count=excluded.processed_count,
			failed_count=excluded.failed_count, skipped_count=excluded.skipped_count,
			last_error=excluded.last_error, metadata=excluded.metadata, updated_at=excluded.updated_at,
			search_key=excluded.search_key
	`, folder.ID, folder.Source, folder.SourceURL, folder.VideoID, folder.FolderID, folder.FolderPath,
		folder.LocalFolderPath, folder.Group, folder.ManifestTXTPath, folder.ManifestJSONPath,
		folder.ClipCount, folder.ProcessedCount, folder.FailedCount, folder.SkippedCount, folder.LastError, folder.Metadata,
		timeutil.FormatRFC3339(folder.CreatedAt), timeutil.FormatRFC3339(now), searchKey)

	return err
}

// DeleteFolder deletes a clip folder by its ID.
func (s *AssetStoreSQLite) DeleteFolder(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errClipFolderIDRequired
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM clip_folders WHERE id = ?", id)
	return err
}

// errClipFolderIDRequired is the canonical sentinel returned by
// DeleteFolder when the id argument is empty (mirrors the legacy
// clips_core.go `fmt.Errorf` body verbatim, minus the alloc).
var errClipFolderIDRequired = stringError("clip folder id is required")

// stringError is a minimal error type used for sentinel errors that
// must be comparable with errors.Is. Equal-by-value semantics; backed
// by errors.New internally.
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// stringError is a small helper to keep the package's error sentinels
// as comparable values (Go's `errors.Is` works on equality, not
// pointer identity, so plain new(string) would not satisfy that
// contract across imports).
func stringError(s string) sentinelError { return sentinelError(s) }

// GetFolder retrieves a clip folder by ID.
func (s *AssetStoreSQLite) GetFolder(ctx context.Context, id string) (*asset.ClipFolder, error) {
	query := buildClipFolderQuery("") + " WHERE id = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, id)

	return s.scanClipFolder(row)
}

// GetFolderByVideoID retrieves a clip folder by video ID.
func (s *AssetStoreSQLite) GetFolderByVideoID(ctx context.Context, videoID string) (*asset.ClipFolder, error) {
	query := buildClipFolderQuery("") + " WHERE video_id = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, videoID)

	return s.scanClipFolder(row)
}

// scanClipFolder reads one ClipFolder from a single-row scanner.
func (s *AssetStoreSQLite) scanClipFolder(row interface {
	Scan(dest ...any) error
}) (*asset.ClipFolder, error) {
	var folder asset.ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
	folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
	return &folder, nil
}

// ListByFolderID returns all clips for a given folder ID (canonical
// column after migration 059).
func (s *AssetStoreSQLite) ListByFolderID(ctx context.Context, folderID string) ([]*asset.Asset, error) {
	query := buildMediaAssetQuery("") + " AND folder_id = ? ORDER BY created_at ASC"
	rows, err := s.db.QueryContext(ctx, query, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListByFolderPath returns all clips for a given folder path (canonical
// column).
func (s *AssetStoreSQLite) ListByFolderPath(ctx context.Context, folderPath string) ([]*asset.Asset, error) {
	query := buildMediaAssetQuery("") + " AND folder_path = ? ORDER BY created_at ASC"
	rows, err := s.db.QueryContext(ctx, query, folderPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*asset.Asset
	for rows.Next() {
		clip, err := ScanCanonicalAssetRowsPublic(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountByFolderID returns the number of clips in a folder (folder_id
// is a canonical column).
func (s *AssetStoreSQLite) CountByFolderID(ctx context.Context, folderID string) (int, error) {
	row := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_assets WHERE folder_id = ?", folderID)
	var count int
	err := row.Scan(&count)
	return count, err
}

// ListFolders returns all clip folders, optionally filtered by source.
func (s *AssetStoreSQLite) ListFolders(ctx context.Context, source string) ([]*asset.ClipFolder, error) {
	query := buildClipFolderQuery(source) + " ORDER BY updated_at DESC"
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*asset.ClipFolder
	for rows.Next() {
		f, err := s.scanClipFolder(rows)
		if err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// SearchFolders searches clip folders by keyword in source_url,
// video_id, group_name, or folder_path.
//
// Uses the canonical LIKE-fallback builder from pkg/sqlutil (FTS5
// banned, see ARCHITECTURE.md §6 persistence / AGENTS.md). The
// original `clips_core.go::SearchFolders` body was the template for
// this implementation — Phase 1 of Wave C preserves the contract
// verbatim so existing callers (folder_tree.go:46 etc.) keep the
// same observable behavior.
//
// Returns ([], nil) when the keyword-tokenizer yields zero usable
// tokens (mirrors the legacy `if conditionSQL == "" { return ...;
// nil }` short-circuit so a bare keyword like "  " matches nothing
// rather than returning the full table).
func (s *AssetStoreSQLite) SearchFolders(ctx context.Context, keyword string) ([]*asset.ClipFolder, error) {
	columns := []string{"source_url", "video_id", "group_name", "folder_path"}
	keywords := strings.Fields(keyword)
	if len(keywords) == 0 {
		keywords = []string{keyword}
	}

	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*asset.ClipFolder{}, nil
	}

	query := buildClipFolderQuery("") + " WHERE " + conditionSQL + " ORDER BY updated_at DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*asset.ClipFolder
	for rows.Next() {
		f, err := s.scanClipFolder(rows)
		if err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}
