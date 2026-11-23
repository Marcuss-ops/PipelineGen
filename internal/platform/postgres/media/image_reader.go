package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
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
		return urlutil.DriveFileURL(trimmed)
	}
	return ""
}

// ── Subject-scoped image reuse read ─────────────────────────────────
//
// The lookup that turns a materialized image back into a reusable row lives
// here, under the single media read authority. Implementing it in the
// composition root would reintroduce the Postgres-writer/SQLite-reader
// split-brain the media cutover removed.

// SubjectImageRow is the identity + delivery projection the reuse lookup needs.
// It is deliberately not detail.ImageAsset: the caller only needs the fields
// that make an image reusable (content address, Drive link, provenance), and a
// narrow row keeps the query cheap.
type SubjectImageRow struct {
	AssetID       string
	ContentSHA256 string
	Name          string
	Provider      string
	Origin        string
	SourcePageURL string
	SubjectID     string
	Width         int
	Height        int
	// Location is kept as its own value: the content address identifies the
	// bytes, the location says where a copy lives on a provider, and no single
	// struct is allowed to own both facts.
	Location SubjectImageLocation
}

// SubjectImageLocation is the provider-location projection of a subject image.
// It deliberately carries no content address.
type SubjectImageLocation struct {
	DriveLink  string
	PreviewURL string
}

const subjectImageSelect = `
	SELECT id, COALESCE(name, ''), COALESCE(url, ''), COALESCE(thumbnail_url, ''),
	       COALESCE(drive_link, ''), COALESCE(content_sha256, ''), COALESCE(provider, ''),
	       COALESCE(origin, ''), COALESCE(width, 0), COALESCE(height, 0),
	       COALESCE(NULLIF(metadata_json, ''), '{}')
	FROM media_assets`

// ListImagesBySubject resolves retrieved images whose durable subject projection
// matches the supplied subject slug.
//
// subject_id is the primary key of the projection. search_text/search_terms
// cover a lookup keyed by the raw entity or the provider query: the reuse path
// has the provider query ("Corrado Augias portrait") while the row is keyed by
// the entity slug ("corrado-augias"), and both must find the same image.
// Only rows that can actually be reused (Drive link + content address) are
// returned; a row without bytes is not a reuse candidate.
func (r *ImageReader) ListImagesBySubject(ctx context.Context, subject string, limit int) ([]SubjectImageRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres subject image reader: not wired")
	}
	subject = strings.ToLower(strings.TrimSpace(subject))
	if subject == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.db.QueryContext(ctx, subjectImageSelect+`
		WHERE source = 'image'
		  AND COALESCE(drive_link, '') <> ''
		  AND COALESCE(content_sha256, '') <> ''
		  AND COALESCE(deleted_at, '') = ''
		  AND (
			COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'subject_id' = $1
			OR COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'canonical_entity_id' = $1
			OR lower(COALESCE(search_text, '')) LIKE '%' || $1 || '%'
			OR lower(COALESCE(search_terms, '')) LIKE '%' || $1 || '%'
		  )
		ORDER BY COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'subject_id' = $1 DESC, id
		LIMIT $2`, subject, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres subject image reader: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSubjectImageRows(rows)
}

// ListImagesByAssetIDs hydrates canonical asset ids (the semantic-index path)
// from the same SSOT.
func (r *ImageReader) ListImagesByAssetIDs(ctx context.Context, assetIDs []string) ([]SubjectImageRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres subject image reader: not wired")
	}
	placeholders := make([]string, 0, len(assetIDs))
	args := make([]any, 0, len(assetIDs))
	for _, id := range assetIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		args = append(args, id)
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
	}
	if len(args) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, subjectImageSelect+`
		WHERE source = 'image' AND id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres subject image reader: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanSubjectImageRows(rows)
}

func scanSubjectImageRows(rows *sql.Rows) ([]SubjectImageRow, error) {
	out := make([]SubjectImageRow, 0)
	for rows.Next() {
		var (
			row       SubjectImageRow
			url       string
			thumbnail string
			metadata  string
		)
		if err := rows.Scan(&row.AssetID, &row.Name, &url, &thumbnail,
			&row.Location.DriveLink, &row.ContentSHA256, &row.Provider, &row.Origin,
			&row.Width, &row.Height, &metadata); err != nil {
			return nil, fmt.Errorf("postgres subject image reader: scan: %w", err)
		}
		if strings.TrimSpace(url) != "" {
			row.Location.PreviewURL = url
		} else {
			row.Location.PreviewURL = thumbnail
		}
		out = append(out, finishSubjectImageRow(row, metadata))
	}
	return out, rows.Err()
}

func finishSubjectImageRow(row SubjectImageRow, metadata string) SubjectImageRow {
	var decoded map[string]any
	if strings.TrimSpace(metadata) == "" || json.Unmarshal([]byte(metadata), &decoded) != nil {
		return row
	}
	if value, ok := decoded["subject_id"].(string); ok {
		row.SubjectID = value
	}
	if value, ok := decoded["source_page_url"].(string); ok {
		row.SourcePageURL = value
	}
	if row.Name == "" {
		if value, ok := decoded["entity"].(string); ok {
			row.Name = value
		}
	}
	return row
}
