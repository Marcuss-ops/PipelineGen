// Package assets — image insert/update operations.
//
// images_insert_update.go owns the write-path methods:
// AddImage, dualWriteImageDetails, UpsertGeneratedDetails,
// UpsertRetrievedDetails, UpdateImageMetadata,
// UpdateEmbeddingStatus, UpdateEmbeddingData.
// Extracted from images_repository.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package imagesrepo

import (
	"context"
	"encoding/json"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// AddImage aggiunge un record immagine nella tabella media_assets.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. Image INSERT/UPDATE on
// media_assets must be accompanied by an outbox_events INSERT so the
// vector index stays in sync. Callers should route through
// outbox.Dispatcher.EnqueueAndIndex or a future ImageDispatcher.
//
// FASE 1B (July 2026): origin and provider columns are now persisted
// as first-class columns (migration 115). They previously lived inside
// metadata_json (unreliable; see FASE 0 audit §1.4). Callers that
// populate ImageAsset.Origin / .Provider will see those values land
// in the dedicated columns; callers that don't populate them get the
// DEFAULT ” (unclassified) and are eligible for FASE 4 backfill to
// promote them to a canonical territory.
func (r *ImagesRepository) AddImage(ctx context.Context, img *detail.ImageAsset) (int64, error) {
	if r != nil && r.canonicalCommit != nil {
		return r.canonicalCommit(ctx, img)
	}
	id := img.Hash
	if id == "" {
		id = fmt.Sprintf("img_%d", img.CreatedAt.UnixNano())
	}

	tagsJSON, _ := json.Marshal(img.Tags)
	tagsNorm := normalizeTags(img.Tags)

	// Prepara metadata_json con campi extra non dedicati
	metaMap := make(map[string]any)
	if img.MetadataJSON != "" && img.MetadataJSON != "{}" {
		_ = json.Unmarshal([]byte(img.MetadataJSON), &metaMap)
	}
	metaMap["subject_id"] = img.SubjectID
	metaMap["description"] = img.Description
	if img.License != "" {
		metaMap["license"] = img.License
	}
	if img.QualityScore != 0 {
		metaMap["quality_score"] = img.QualityScore
	}
	if img.Error != "" {
		metaMap["error"] = img.Error
	}

	metaJSON, _ := json.Marshal(metaMap)

	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, url, tags, tags_norm, media_type, width, height, legacy_file_md5, local_path, relative_path, drive_file_id, lifecycle_state, metadata_json, origin, provider, created_at, updated_at)
		VALUES (?, 'image', ?, ?, ?, ?, 'image', ?, ?, ?, ?, ?, ?, 'STAGING', ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			url=excluded.url,
			tags=excluded.tags,
			tags_norm=excluded.tags_norm,
			media_type=excluded.media_type,
			width=excluded.width,
			height=excluded.height,
			legacy_file_md5=excluded.legacy_file_md5,
			local_path=excluded.local_path,
			relative_path=excluded.relative_path,
			drive_file_id=excluded.drive_file_id,
			lifecycle_state=excluded.lifecycle_state,
			metadata_json=excluded.metadata_json,
			origin=excluded.origin,
			provider=excluded.provider,
			updated_at=excluded.updated_at
	`, id, img.Description, img.SourceURL, string(tagsJSON), tagsNorm,
		img.Width, img.Height, img.Hash, img.PathRel, img.PathRel, img.DriveFileID,
		string(metaJSON), string(img.Origin), string(img.Provider), now, now)

	if err != nil {
		return 0, err
	}
	if err := r.dualWriteImageDetails(ctx, id, img); err != nil {
		return 0, fmt.Errorf("dual-write image details: %w", err)
	}
	return 0, nil
}

// dualWriteImageDetails reads img.Origin and routes the asset to the
// matching detail table with best-effort field mapping. Caller can call
// UpsertGeneratedDetails / UpsertRetrievedDetails subsequently to
// refine the row with full provenance.
func (r *ImagesRepository) dualWriteImageDetails(ctx context.Context, assetID string, img *detail.ImageAsset) error {
	if r == nil || img == nil {
		return nil
	}
	switch img.Origin {
	case detail.ImageOriginGenerated:
		return r.UpsertGeneratedDetails(ctx, &detail.GeneratedImageDetail{
			AssetID:    assetID,
			SourceHash: img.Hash,
			Model:      string(img.Provider),
		})
	case detail.ImageOriginRetrieved:
		return r.UpsertRetrievedDetails(ctx, &detail.RetrievedImageDetail{
			AssetID:        assetID,
			SourceImageURL: img.SourceURL,
			License:        img.License,
			Provider:       string(img.Provider),
		})
	}
	return nil
}

// UpsertGeneratedDetails writes per-asset provenance for an AI-generated image.
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

// UpsertRetrievedDetails writes per-asset provenance for a web-retrieved image.
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

// UpdateDriveDelivery records the post-commit Drive projection for an image.
// It is called only by the image delivery outbox worker; ingest ownership is
// established by AssetCommitter before this method can run.
func (r *ImagesRepository) UpdateDriveDelivery(ctx context.Context, hash, driveFileID, driveLink, downloadLink, status string) error {
	if hash == "" {
		return fmt.Errorf("UpdateDriveDelivery: hash is empty")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("UpdateDriveDelivery: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// A failed delivery must not erase a Drive identity that may have
	// been persisted by a previous successful attempt (for example, a
	// worker crash after Publish returned but before the outbox ACK).
	preserveIdentity := strings.HasPrefix(status, "delivery_failed:") && driveFileID == "" && driveLink == "" && downloadLink == ""
	var updateSQL string
	var args []any
	if preserveIdentity {
		updateSQL = `
			UPDATE media_assets
			SET metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.delivery_status', ?),
			    updated_at = CURRENT_TIMESTAMP
			WHERE source = 'image' AND legacy_file_md5 = ?`
		args = []any{status, hash}
	} else {
		updateSQL = `
			UPDATE media_assets
			SET drive_file_id = ?, drive_link = ?, download_link = ?,
			    metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.delivery_status', ?),
			    updated_at = CURRENT_TIMESTAMP
			WHERE source = 'image' AND legacy_file_md5 = ?`
		args = []any{driveFileID, driveLink, downloadLink, status, hash}
	}
	result, err := tx.ExecContext(ctx, updateSQL, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("UpdateDriveDelivery: inspect asset update: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("UpdateDriveDelivery: image with hash %q not found", hash)
	}

	if !preserveIdentity && driveFileID != "" {
		var assetID string
		if err := tx.QueryRowContext(ctx, `
			SELECT id FROM media_assets WHERE source = 'image' AND legacy_file_md5 = ?`, hash).Scan(&assetID); err != nil {
			return fmt.Errorf("UpdateDriveDelivery: read asset id: %w", err)
		}
		now := timeutil.FormatRFC3339(time.Now())
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO asset_locations
				(asset_id, location_kind, uri, external_id, web_view_link, download_url,
				 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
			VALUES (?, 'drive', ?, ?, ?, ?, '', 0, ?, 0, ?, ?)
			ON CONFLICT(asset_id, location_kind) DO UPDATE SET
				uri = excluded.uri,
				external_id = excluded.external_id,
				web_view_link = excluded.web_view_link,
				download_url = excluded.download_url,
				legacy_file_md5 = excluded.legacy_file_md5,
				updated_at = excluded.updated_at`,
			assetID, "drive://"+driveFileID, driveFileID, driveLink, downloadLink, hash, now, now); err != nil {
			return fmt.Errorf("UpdateDriveDelivery: upsert Drive location: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("UpdateDriveDelivery: commit: %w", err)
	}
	committed = true
	return nil
}

// UpdateImageMetadata aggiorna i metadati JSON di un'immagine esistente.
func (r *ImagesRepository) UpdateImageMetadata(ctx context.Context, hash, metadataJSON string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = ?
		WHERE source = 'image' AND legacy_file_md5 = ?
	`, metadataJSON, hash)
	return err
}

// UpdateEmbeddingStatus writes an embedding status marker to metadata_json.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. It uses json_set on
// metadata_json which is un-indexable and invisible to the canonical
// index_state machine. Callers should use clipindexer.setIndexState
// which writes the first-class index_state column.
func (r *ImagesRepository) UpdateEmbeddingStatus(ctx context.Context, hash, status string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = json_set(metadata_json, '$.embedding_status', ?)
		WHERE source = 'image' AND legacy_file_md5 = ?
	`, status, hash)
	return err
}

// UpdateEmbeddingData updates the embedding_json column AND embedding_status in metadata_json.
// If embeddingJSON is empty, only the status is updated.
// This is the unified method for persisting embedding data to survive Qdrant wipes.
// Works for ALL media types (image, artlist, youtube, stock, voiceover) — not just images.
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX. It writes embedding_json
// directly without an outbox event. The canonical path is through
// clipindexer.setIndexedAt which writes the column AND the sidecar
// metadata_json in a single atomic UPDATE alongside the index_state
// transition. Callers using this method are responsible for ensuring
// the outbox event is emitted separately.
func (r *ImagesRepository) UpdateEmbeddingData(ctx context.Context, assetID, embeddingJSON, status string) error {
	if embeddingJSON != "" {
		_, err := r.db.ExecContext(ctx, `
			UPDATE media_assets
			SET embedding_json = ?,
			    metadata_json = json_set(metadata_json, '$.embedding_status', ?)
			WHERE id = ?
		`, embeddingJSON, status, assetID)
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = json_set(metadata_json, '$.embedding_status', ?)
		WHERE id = ?
	`, status, assetID)
	return err
}

// ── Subject helpers ─────────────────────────────────────────────────────────

// GetSubjectBySlugOrAlias recupera un soggetto tramite ID (slug).
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

// CreateSubject crea un nuovo soggetto.
func (r *ImagesRepository) CreateSubject(ctx context.Context, s *asset.Subject) (int64, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO subjects (id, name, description, metadata_json)
		VALUES (?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.Notes, "{}")
	return 0, err
}
