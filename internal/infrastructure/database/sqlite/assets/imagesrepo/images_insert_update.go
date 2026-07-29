// Package assets — image insert/update operations.
//
// images_insert_update.go owns the write-path methods:
// AddImage, dualWriteImageDetails, UpsertGeneratedDetails,
// UpsertRetrievedDetails, UpdateOrigin, UpdateImageMetadata,
// UpdateEmbeddingStatus, UpdateEmbeddingData.
// Extracted from images_repository.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package imagesrepo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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
func (r *ImagesRepository) AddImage(ctx context.Context, img *asset.ImageAsset) (int64, error) {
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
		INSERT INTO media_assets (id, source, name, url, tags, tags_norm, media_type, width, height, file_hash, local_path, relative_path, drive_file_id, lifecycle_state, metadata_json, origin, provider, created_at, updated_at)
		VALUES (?, 'image', ?, ?, ?, ?, 'image', ?, ?, ?, ?, ?, ?, 'STAGING', ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			url=excluded.url,
			tags=excluded.tags,
			tags_norm=excluded.tags_norm,
			media_type=excluded.media_type,
			width=excluded.width,
			height=excluded.height,
			file_hash=excluded.file_hash,
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
func (r *ImagesRepository) dualWriteImageDetails(ctx context.Context, assetID string, img *asset.ImageAsset) error {
	if r == nil || img == nil {
		return nil
	}
	switch img.Origin {
	case asset.ImageOriginGenerated:
		return r.UpsertGeneratedDetails(ctx, &asset.GeneratedImageDetail{
			AssetID:    assetID,
			SourceHash: img.Hash,
			Model:      string(img.Provider),
		})
	case asset.ImageOriginRetrieved:
		return r.UpsertRetrievedDetails(ctx, &asset.RetrievedImageDetail{
			AssetID:        assetID,
			SourceImageURL: img.SourceURL,
			License:        img.License,
			Provider:       string(img.Provider),
		})
	}
	return nil
}

// UpsertGeneratedDetails writes per-asset provenance for an AI-generated image.
func (r *ImagesRepository) UpsertGeneratedDetails(ctx context.Context, d *asset.GeneratedImageDetail) error {
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
func (r *ImagesRepository) UpsertRetrievedDetails(ctx context.Context, d *asset.RetrievedImageDetail) error {
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

// UpdateOrigin updates media_assets.origin and media_assets.provider for
// the row keyed by file_hash. FASE 4 CUTOVER.
func (r *ImagesRepository) UpdateOrigin(ctx context.Context, hash, origin, provider string) error {
	if hash == "" {
		return fmt.Errorf("UpdateOrigin: hash is empty")
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET origin = ?, provider = ?, updated_at = CURRENT_TIMESTAMP
		WHERE source = 'image' AND file_hash = ?
	`, origin, provider, hash)
	return err
}

// UpdateImageMetadata aggiorna i metadati JSON di un'immagine esistente.
func (r *ImagesRepository) UpdateImageMetadata(ctx context.Context, hash, metadataJSON string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = ?
		WHERE source = 'image' AND file_hash = ?
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
		WHERE source = 'image' AND file_hash = ?
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
