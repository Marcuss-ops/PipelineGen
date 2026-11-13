package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// ImageReader is the PostgreSQL media-SSOT reader for image identity lookups.
// It replaces the legacy SQLite media_assets reads that the images repository
// used to resolve a hash, an asset id or a Drive file id. The canonical image
// ingestion commits AssetID == content hash, so a PG-only row must be visible
// to these lookups; otherwise the image cache/dedup path re-downloads an asset
// that is already in the canonical store.
type ImageReader struct {
	db *sql.DB
}

// NewImageReader constructs the canonical image identity reader.
func NewImageReader(db *sql.DB) *ImageReader {
	if db == nil {
		panic("media.NewImageReader: db is required")
	}
	return &ImageReader{db: db}
}

const imageAssetSelect = `
	SELECT id, name, COALESCE(NULLIF(url, ''), NULLIF(thumbnail_url, '')), tags,
	       metadata_json, created_at, content_sha256, local_path, relative_path,
	       drive_file_id, drive_link, origin, provider, width, height
	FROM media_assets`

// GetImageByHash resolves an image by its canonical content hash. Both the raw
// hex digest and the `sha256:`-prefixed form are accepted, matching the media
// SSOT hash contract.
func (r *ImageReader) GetImageByHash(ctx context.Context, hash string) (*detail.ImageAsset, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres image reader: not wired")
	}
	trimmed := strings.TrimSpace(hash)
	if trimmed == "" {
		return nil, fmt.Errorf("postgres image reader: content hash is required")
	}
	raw := strings.TrimPrefix(trimmed, "sha256:")
	row := r.db.QueryRowContext(ctx, imageAssetSelect+`
		WHERE source = 'image'
		  AND (content_sha256 IN ($1, $2) OR binary_sha256 IN ($1, $2))
		ORDER BY CASE WHEN content_sha256 = $2 THEN 0 ELSE 1 END
		LIMIT 1`, trimmed, raw)
	return scanPGImageAsset(row)
}

// GetImageByID resolves an image by its canonical media_assets id.
func (r *ImageReader) GetImageByID(ctx context.Context, id string) (*detail.ImageAsset, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres image reader: not wired")
	}
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return nil, fmt.Errorf("postgres image reader: asset id is required")
	}
	row := r.db.QueryRowContext(ctx, imageAssetSelect+`
		WHERE source = 'image' AND id = $1
		LIMIT 1`, trimmed)
	return scanPGImageAsset(row)
}

// GetImageByDriveFileID resolves an image by its Drive identity, preserving the
// legacy exact-file-id + link/url substring matching contract.
func (r *ImageReader) GetImageByDriveFileID(ctx context.Context, fileID string) (*detail.ImageAsset, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres image reader: not wired")
	}
	trimmed := strings.TrimSpace(fileID)
	if trimmed == "" {
		return nil, fmt.Errorf("postgres image reader: drive file id is required")
	}
	pattern := "%" + trimmed + "%"
	row := r.db.QueryRowContext(ctx, imageAssetSelect+`
		WHERE source = 'image'
		  AND (drive_file_id = $1 OR drive_link LIKE $2 OR url LIKE $2)
		LIMIT 1`, trimmed, pattern)
	return scanPGImageAsset(row)
}

// scanPGImageAsset decodes the canonical image projection. A missing row is
// (nil, nil), the same not-found shape the images repository has always
// returned to its callers.
func scanPGImageAsset(row mediaAssetScanner) (*detail.ImageAsset, error) {
	var (
		id, name, url, tags, metadata, createdAt sql.NullString
		hash, localPath, relativePath            sql.NullString
		driveFileID, driveLink, origin, provider sql.NullString
		width, height                            sql.NullInt64
	)
	err := row.Scan(&id, &name, &url, &tags, &metadata, &createdAt, &hash,
		&localPath, &relativePath, &driveFileID, &driveLink, &origin, &provider,
		&width, &height)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres image reader: scan asset: %w", err)
	}

	img := &detail.ImageAsset{
		SlugID:       id.String,
		Description:  name.String,
		SourceURL:    canonicalPGImageURL(url.String, driveLink.String, driveFileID.String),
		Hash:         hash.String,
		PathRel:      firstNonEmpty(relativePath.String, localPath.String),
		LocalPath:    localPath.String,
		DriveFileID:  driveFileID.String,
		Origin:       detail.ImageOrigin(origin.String),
		Provider:     detail.ImageProvider(provider.String),
		Width:        int(width.Int64),
		Height:       int(height.Int64),
		MetadataJSON: metadata.String,
	}
	if createdAt.Valid && strings.TrimSpace(createdAt.String) != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(createdAt.String)); parseErr == nil {
			img.CreatedAt = parsed
		}
	}
	if tags.Valid && tags.String != "" {
		_ = json.Unmarshal([]byte(tags.String), &img.Tags)
	}
	if metadata.Valid && metadata.String != "" {
		var decoded map[string]any
		if json.Unmarshal([]byte(metadata.String), &decoded) == nil {
			if v, ok := decoded["subject_id"].(string); ok {
				img.SubjectID = v
			}
			if v, ok := decoded["status"].(string); ok {
				img.Status = v
			}
		}
	}
	return img, nil
}

func canonicalPGImageURL(url, driveLink, driveFileID string) string {
	if trimmed := strings.TrimSpace(url); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(driveLink); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(driveFileID); trimmed != "" {
		return fmt.Sprintf("https://drive.google.com/file/d/%s/view", trimmed)
	}
	return ""
}
