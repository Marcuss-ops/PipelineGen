// Package assets — image insert/update operations.
//
// Image-specific provenance/detail tables remain owned here. Every mutation of
// media_assets or asset_locations delegates to the canonical asset writer.
package imagesrepo

import (
	"context"
	"encoding/json"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// AddImage commits the canonical media asset through the composition-wired
// writer, then writes the image-specific provenance detail table. There is no
// repository-local media_assets fallback.
func (r *ImagesRepository) AddImage(ctx context.Context, img *detail.ImageAsset) (int64, error) {
	if r == nil || r.canonicalCommit == nil {
		return 0, fmt.Errorf("images.AddImage: canonical AssetCommitter is required; repository SQL fallback has been removed")
	}
	if img == nil {
		return 0, fmt.Errorf("images.AddImage: image is required")
	}
	// Preserve the legacy stable-ID fallback before entering the canonical
	// writer so the same identity is also available to detail-table writes.
	if strings.TrimSpace(img.Hash) == "" {
		img.Hash = fmt.Sprintf("img_%d", img.CreatedAt.UnixNano())
	}
	rows, err := r.canonicalCommit(ctx, img)
	if err != nil {
		return 0, err
	}
	if err := r.dualWriteImageDetails(ctx, img.Hash, img); err != nil {
		return 0, fmt.Errorf("dual-write image details: %w", err)
	}
	return rows, nil
}

// dualWriteImageDetails reads img.Origin and routes the asset to the matching
// detail table. These tables contain source-specific provenance and are not a
// second media catalog.
func (r *ImagesRepository) dualWriteImageDetails(ctx context.Context, assetID string, img *detail.ImageAsset) error {
	if r == nil || img == nil {
		return nil
	}
	switch img.Origin {
	case detail.ImageOriginGenerated:
		return r.UpsertGeneratedDetails(ctx, &detail.GeneratedImageDetail{
			AssetID: assetID, SourceHash: img.Hash, Model: string(img.Provider),
		})
	case detail.ImageOriginRetrieved:
		return r.UpsertRetrievedDetails(ctx, &detail.RetrievedImageDetail{
			AssetID: assetID, SourceImageURL: img.SourceURL, License: img.License,
			Provider: string(img.Provider),
		})
	}
	return nil
}

func (r *ImagesRepository) UpsertGeneratedDetails(ctx context.Context, d *detail.GeneratedImageDetail) error {
	if d == nil {
		return nil
	}
	if d.AssetID == "" {
		return fmt.Errorf("UpsertGeneratedDetails: AssetID is empty")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO generated_image_details
			(asset_id, prompt_original, prompt_resolved, style_id, style_version, model, seed, generation_job_id, source_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id) DO UPDATE SET
			prompt_original = excluded.prompt_original,
			prompt_resolved = excluded.prompt_resolved,
			style_id = excluded.style_id,
			style_version = excluded.style_version,
			model = excluded.model,
			seed = excluded.seed,
			generation_job_id = excluded.generation_job_id,
			source_hash = excluded.source_hash
	`, d.AssetID, d.PromptOriginal, d.PromptResolved, d.StyleID, d.StyleVersion,
		d.Model, d.Seed, d.GenerationJobID, d.SourceHash)
	return err
}

func (r *ImagesRepository) UpsertRetrievedDetails(ctx context.Context, d *detail.RetrievedImageDetail) error {
	if d == nil {
		return nil
	}
	if d.AssetID == "" {
		return fmt.Errorf("UpsertRetrievedDetails: AssetID is empty")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO retrieved_image_details
			(asset_id, source_image_url, source_page_url, license, author, search_query, retrieved_at, provider)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id) DO UPDATE SET
			source_image_url = excluded.source_image_url,
			source_page_url = excluded.source_page_url,
			license = excluded.license,
			author = excluded.author,
			search_query = excluded.search_query,
			retrieved_at = excluded.retrieved_at,
			provider = excluded.provider
	`, d.AssetID, d.SourceImageURL, d.SourcePageURL, d.License, d.Author,
		d.SearchQuery, d.RetrievedAt, d.Provider)
	return err
}

func (r *ImagesRepository) UpdateDriveDelivery(ctx context.Context, hash, driveFileID, driveLink, downloadLink, status string) error {
	if strings.TrimSpace(hash) == "" {
		return fmt.Errorf("UpdateDriveDelivery: hash is empty")
	}
	if r == nil || r.canonicalMutate == nil {
		return fmt.Errorf("UpdateDriveDelivery: canonical asset mutator is required")
	}
	assetID, metadataJSON, err := r.assetIdentityByHash(ctx, hash)
	if err != nil {
		return err
	}
	merged, err := mergeMetadataString(metadataJSON, "delivery_status", status)
	if err != nil {
		return fmt.Errorf("UpdateDriveDelivery: merge metadata: %w", err)
	}
	if strings.HasPrefix(status, "delivery_failed:") && driveFileID == "" && driveLink == "" && downloadLink == "" {
		return r.canonicalMutate.PatchAsset(ctx, persistence.AssetPatch{AssetID: assetID, MetadataJSON: &merged})
	}
	if err := r.canonicalMutate.ReconcileDriveLocations(ctx, []persistence.DriveLocationPatch{{
		AssetID: assetID, DriveFileID: driveFileID, DriveLink: driveLink, DownloadURL: downloadLink,
	}}); err != nil {
		return err
	}
	return r.canonicalMutate.PatchAsset(ctx, persistence.AssetPatch{AssetID: assetID, MetadataJSON: &merged})
}

func (r *ImagesRepository) UpdateImageMetadata(ctx context.Context, hash, metadataJSON string) error {
	if r == nil || r.canonicalMutate == nil {
		return fmt.Errorf("UpdateImageMetadata: canonical asset mutator is required")
	}
	assetID, _, err := r.assetIdentityByHash(ctx, hash)
	if err != nil {
		return err
	}
	return r.canonicalMutate.PatchAsset(ctx, persistence.AssetPatch{AssetID: assetID, MetadataJSON: &metadataJSON})
}

func (r *ImagesRepository) UpdateEmbeddingStatus(ctx context.Context, hash, status string) error {
	if r == nil || r.canonicalMutate == nil {
		return fmt.Errorf("UpdateEmbeddingStatus: canonical asset mutator is required")
	}
	assetID, metadataJSON, err := r.assetIdentityByHash(ctx, hash)
	if err != nil {
		return err
	}
	merged, err := mergeMetadataString(metadataJSON, "embedding_status", status)
	if err != nil {
		return err
	}
	return r.canonicalMutate.PatchAsset(ctx, persistence.AssetPatch{AssetID: assetID, MetadataJSON: &merged})
}

func (r *ImagesRepository) UpdateEmbeddingData(ctx context.Context, assetID, embeddingJSON, status string) error {
	if r == nil || r.canonicalMutate == nil {
		return fmt.Errorf("UpdateEmbeddingData: canonical asset mutator is required")
	}
	var metadataJSON string
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(metadata_json,'{}') FROM media_assets WHERE id = ?`, assetID).Scan(&metadataJSON); err != nil {
		return fmt.Errorf("UpdateEmbeddingData: read asset %q: %w", assetID, err)
	}
	merged, err := mergeMetadataString(metadataJSON, "embedding_status", status)
	if err != nil {
		return err
	}
	patch := persistence.AssetPatch{AssetID: assetID, MetadataJSON: &merged}
	if embeddingJSON != "" {
		patch.EmbeddingJSON = &embeddingJSON
	}
	return r.canonicalMutate.PatchAsset(ctx, patch)
}

func (r *ImagesRepository) assetIdentityByHash(ctx context.Context, hash string) (string, string, error) {
	var assetID, metadataJSON string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, COALESCE(metadata_json,'{}')
		FROM media_assets
		WHERE source='image' AND (legacy_file_md5=? OR id=?)
		ORDER BY CASE WHEN legacy_file_md5=? THEN 0 ELSE 1 END
		LIMIT 1`, hash, hash, hash).
		Scan(&assetID, &metadataJSON)
	if err != nil {
		return "", "", fmt.Errorf("image asset with identity %q not found: %w", hash, err)
	}
	return assetID, metadataJSON, nil
}

func mergeMetadataString(raw, key string, value any) (string, error) {
	metadata := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			return "", err
		}
	}
	metadata[key] = value
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (r *ImagesRepository) GetSubjectBySlugOrAlias(ctx context.Context, id string) (*asset.Subject, error) {
	var s asset.Subject
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(description, ''), created_at, updated_at
		FROM subjects WHERE id = ?
	`, id).Scan(&s.Slug, &s.DisplayName, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ImagesRepository) CreateSubject(ctx context.Context, s *asset.Subject) (int64, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO subjects (id, name, description, metadata_json)
		VALUES (?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.Notes, "{}")
	return 0, err
}
