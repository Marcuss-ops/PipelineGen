// Package assets — image search/lookup operations.
//
// images_search.go owns the read-path methods:
// GetImageByHash, GetByID, Delete, GetByDriveFileID,
// ListImagesBySubject, ListAll.
// Extracted from images_repository.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package imagesrepo

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// GetImageByHash recupera un'immagine tramite il suo hash.
// FASE 1B: reads origin + provider first-class columns (migration 115).
func (r *ImagesRepository) GetImageByHash(ctx context.Context, hash string) (*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND legacy_file_md5 = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, hash)
	return scanImageAssetFromRow(row)
}

// GetByID recupera un'immagine tramite il suo ID stringa.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) GetByID(ctx context.Context, id any) (*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND id = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	return scanImageAssetFromRow(row)
}

// Delete elimina un'immagine
func (r *ImagesRepository) Delete(ctx context.Context, id any) error {
	return fmt.Errorf("images.Delete: canonical Dispatcher deletion path is required")
}

// GetByDriveFileID recupera un'immagine tramite Drive file ID.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) GetByDriveFileID(ctx context.Context, fileID string) (*detail.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider
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
		SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image' AND json_extract(metadata_json, '$.subject_id') = ?
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, subjectID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
		SELECT id, name, url, tags, metadata_json, created_at, legacy_file_md5, local_path, drive_file_id, drive_link, origin, provider
		FROM media_assets
		WHERE source = 'image'
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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
