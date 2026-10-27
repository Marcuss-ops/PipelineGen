// Package assets — image search/lookup operations.
//
// images_search.go owns the read-path methods:
// GetImageByHash, GetByID, Delete, GetByDriveFileID,
// ListImagesBySubject, ListAll.
// Extracted from images_repository.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package imagesrepo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// GetImageByHash recupera un'immagine tramite il suo hash.
// FASE 1B: reads origin + provider first-class columns (migration 115).
func (r *ImagesRepository) GetImageByHash(ctx context.Context, hash string) (*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, content_sha256, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND content_sha256 = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, hash)
	return scanImageAssetFromRow(row)
}

// GetByID recupera un'immagine tramite il suo ID stringa.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) GetByID(ctx context.Context, id any) (*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, content_sha256, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND id = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	return scanImageAssetFromRow(row)
}

// ListImagesByAssetIDs returns canonical list projections for semantic-index
// hits, including their typed provider rights and authorized Drive link.
func (r *ImagesRepository) ListImagesByAssetIDs(ctx context.Context, assetIDs []string) ([]detail.RepositoryImageRow, error) {
	if r == nil || r.db == nil || len(assetIDs) == 0 {
		return nil, nil
	}
	if len(assetIDs) > 50 {
		assetIDs = assetIDs[:50]
	}
	pl := make([]string, len(assetIDs))
	args := make([]any, len(assetIDs))
	for i, id := range assetIDs {
		pl[i], args[i] = "?", id
	}
	query := `SELECT ma.id, ma.origin, ma.provider, json_extract(ma.metadata_json, '$.subject_id'), COALESCE(NULLIF(ma.url, ''), NULLIF(ma.thumbnail_url, ''), NULLIF(ma.thumb_url, ''), rid.source_image_url), ma.drive_link, ma.content_sha256, ma.width, ma.height, rid.source_page_url, rid.license, rid.author, gid.prompt_resolved, gid.style_id, gid.style_version
		FROM media_assets ma
		LEFT JOIN retrieved_image_details rid ON ma.id = rid.asset_id
		LEFT JOIN generated_image_details gid ON ma.id = gid.asset_id
		WHERE ma.source = 'image' AND ma.id IN (` + strings.Join(pl, ",") + `)`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]detail.RepositoryImageRow, 0, len(assetIDs))
	for rows.Next() {
		var id, origin, provider, subjectID, previewURL, driveLink, hash sql.NullString
		var width, height sql.NullInt64
		var sourcePage, license, author, prompt, styleID, styleVersion sql.NullString
		if err := rows.Scan(&id, &origin, &provider, &subjectID, &previewURL, &driveLink, &hash, &width, &height, &sourcePage, &license, &author, &prompt, &styleID, &styleVersion); err != nil {
			return nil, err
		}
		out = append(out, detail.RepositoryImageRow{
			AssetID: id.String, Origin: detail.ImageOrigin(origin.String), Provider: provider.String,
			Name: prompt.String, PreviewURL: previewURL.String, DriveLink: driveLink.String,
			ContentSHA256: hash.String, Width: int(width.Int64), Height: int(height.Int64),
			SourcePageURL: sourcePage.String, License: license.String, Author: author.String,
			StyleID: styleID.String, StyleVersion: styleVersion.String,
		})
	}
	return out, rows.Err()
}

// Delete elimina un'immagine
func (r *ImagesRepository) Delete(ctx context.Context, id any) error {
	return fmt.Errorf("images.Delete: canonical Dispatcher deletion path is required")
}

// GetByDriveFileID recupera un'immagine tramite Drive file ID.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) GetByDriveFileID(ctx context.Context, fileID string) (*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, content_sha256, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND (drive_file_id = ? OR drive_link LIKE ? OR url LIKE ?)
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, fileID, "%"+fileID+"%", "%"+fileID+"%")
	return scanImageAssetFromRow(row)
}

// DEPRECATED (FASE 6, July 2026, image-territories action plan).
// Canonical replacement: ListImages(ctx, routing.RepositoryListFilter).
// Forward-to-ListImages conversion queued at CONTRACT phase
// (deprecation record PR-IMAGE-LISTIMAGESBYSUBJECT in
// architecture/deprecations.yaml).
//
// ListImagesBySubject recupera tutte le immagini per un soggetto.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) ListImagesBySubject(ctx context.Context, subjectID string) ([]detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, content_sha256, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND json_extract(metadata_json, '$.subject_id') = ?
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, subjectID)

	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	images := make([]detail.ImageAsset, 0)
	for rows.Next() {
		img, err := scanImageAssetFromRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, *img)
	}
	return images, nil
}

// ListAll lists all image assets.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) ListAll(ctx context.Context) ([]*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, content_sha256, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image'
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	images := make([]*detail.ImageAsset, 0)
	for rows.Next() {
		img, err := scanImageAssetFromRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}
