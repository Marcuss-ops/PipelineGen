package asset

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ListClips returns clips for a source (or all sources when source is empty
// / "all" / "unified"). No pagination — callers needing paged reads should
// use ListClipsPaged instead.
func (s *AssetStoreSQLite) ListClips(ctx context.Context, source string) ([]*Asset, error) {
	query := buildMediaAssetQuery(source)
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListClipsPaged returns clips with pagination and optional search.
// If q is non-empty, performs a search via SearchClips and ignores the
// pagination input (result still capped at the configured limit).
func (s *AssetStoreSQLite) ListClipsPaged(ctx context.Context, source string, limit, offset int, q string) ([]*Asset, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}

	if strings.TrimSpace(q) != "" {
		return s.SearchClips(ctx, source, q)
	}

	query := buildMediaAssetQuery(source) + " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args := []any{}
	if source != "" && source != "all" && source != "unified" {
		args = append(args, source)
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountClips returns the total number of clips (excluding soft-deleted).
func (s *AssetStoreSQLite) CountClips(ctx context.Context) (int, error) {
	query := "SELECT COUNT(*) FROM media_assets WHERE " + SoftDeleteFilter()
	row := s.db.QueryRowContext(ctx, query)
	var count int
	err := row.Scan(&count)
	return count, err
}

// LastUpdatedAtForTerm returns the most recent created_at value for clips
// matching a term. Uses LIKE search on tags (kept narrow — only the
// artlist source tag pattern is searched).
func (s *AssetStoreSQLite) LastUpdatedAtForTerm(ctx context.Context, term string) (*string, error) {
	term = strings.TrimSpace(term)

	var lastUpdated sql.NullString
	query := `
		SELECT MAX(created_at)
		FROM media_assets
		WHERE source = 'artlist' AND tags LIKE ?
	`
	row := s.db.QueryRowContext(ctx, query, "%"+term+"%")

	if err := row.Scan(&lastUpdated); err != nil {
		return nil, err
	}
	if !lastUpdated.Valid || strings.TrimSpace(lastUpdated.String) == "" {
		return nil, nil
	}
	return &lastUpdated.String, nil
}

// MediaFile represents a scanned media file
type MediaFile struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	URL     string    `json:"url,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// ScanDirectory scans a directory for media files
func ScanDirectory(root string, urlPrefix string) ([]MediaFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var files []MediaFile
	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d == nil || d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}

		var url string
		if urlPrefix != "" {
			url = strings.TrimRight(urlPrefix, "/") + "/" + filepath.ToSlash(rel)
		}

		files = append(files, MediaFile{
			Name:    d.Name(),
			Path:    path,
			URL:     url,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC(),
		})
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by modification time descending (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	return files, nil
}
