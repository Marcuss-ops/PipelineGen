// package sqlite provides the ImagesRepository for image assets.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

type ImagesRepository struct {
	db *sql.DB
}

func NewImagesRepository(db *sql.DB) *ImagesRepository {
	return &ImagesRepository{db: db}
}

// DB returns the underlying database connection
func (r *ImagesRepository) DB() *sql.DB {
	return r.db
}

// GetSubjectBySlugOrAlias recupera un soggetto tramite ID (slug)
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

// CreateSubject crea un nuovo soggetto
func (r *ImagesRepository) CreateSubject(ctx context.Context, s *asset.Subject) (int64, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO subjects (id, name, description, metadata_json)
		VALUES (?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.Notes, "{}")
	return 0, err
}

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
// DEFAULT '' (unclassified) and are eligible for FASE 4 backfill to
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

	return 0, err
}

// normalizeTags converte una lista di tag in una stringa normalizzata per ricerca full-text.
func normalizeTags(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		low := strings.ToLower(t)
		// rimuovi accenti/base
		low = strings.NewReplacer(
			"Ã ", "a", "Ã¨", "e", "Ã©", "e", "Ã¬", "i", "Ã²", "o", "Ã¹", "u",
		).Replace(low)
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(low)
	}
	return b.String()
}

// GetImageByHash recupera un'immagine tramite il suo hash.
// FASE 1B: reads origin + provider first-class columns (migration 115).
func (r *ImagesRepository) GetImageByHash(ctx context.Context, hash string) (*asset.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image' AND file_hash = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, hash)
	return scanImageAsset(row)
}

// GetByID recupera un'immagine tramite il suo ID stringa.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) GetByID(ctx context.Context, id any) (*asset.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image' AND id = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	return scanImageAsset(row)
}

// Delete elimina un'immagine
func (r *ImagesRepository) Delete(ctx context.Context, id any) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM media_assets WHERE source = 'image' AND id = ?", id)
	return err
}

// GetByDriveFileID recupera un'immagine tramite Drive file ID. drive_file_id
// Ã¨ una colonna canonica (migration 059): lettura diretta invece di
// json_extract(metadata_json, '$.drive_file_id').
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) GetByDriveFileID(ctx context.Context, fileID string) (*asset.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image' AND (drive_file_id = ? OR url LIKE ?)
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, fileID, "%"+fileID+"%")
	return scanImageAsset(row)
}

// ListImagesBySubject recupera tutte le immagini per un soggetto.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) ListImagesBySubject(ctx context.Context, subjectID string) ([]asset.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image' AND json_extract(metadata_json, '$.subject_id') = ?
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, subjectID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []asset.ImageAsset
	for rows.Next() {
		img, err := scanImageAssetRows(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, *img)
	}
	return images, nil
}

// ListAll lists all image assets.
// FASE 1B: reads origin + provider columns (migration 115).
func (r *ImagesRepository) ListAll(ctx context.Context) ([]*asset.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id, origin, provider
		FROM media_assets
		WHERE source = 'image'
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []*asset.ImageAsset
	for rows.Next() {
		img, err := scanImageAssetRows(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// scanImageAsset scans one *sql.Row into an ImageAsset. FASE 1B reads
// origin and provider as first-class columns (added by migration 115),
// surfacing them on ImageAsset.Origin / ImageProvider for downstream
// ImageSearchResolver routing (FASE 6).
func scanImageAsset(row interface {
	Scan(dest ...any) error
}) (*asset.ImageAsset, error) {
	var img asset.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name, origin, provider sql.NullString
	var url sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := row.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID, &origin, &provider)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = url.String
	img.Hash = fileHash.String
	img.PathRel = localPath.String
	img.DriveFileID = driveFileID.String
	img.Origin = asset.ImageOrigin(origin.String)
	img.Provider = asset.ImageProvider(provider.String)

	if createdAtStr.Valid {
		img.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	}

	if tagsJSON.Valid && tagsJSON.String != "" {
		_ = json.Unmarshal([]byte(tagsJSON.String), &img.Tags)
	}

	if metaJSON.Valid && metaJSON.String != "" {
		img.MetadataJSON = metaJSON.String
		var metaMap map[string]any
		_ = json.Unmarshal([]byte(metaJSON.String), &metaMap)

		if v, ok := metaMap["subject_id"].(string); ok {
			img.SubjectID = v
		}
		if v, ok := metaMap["status"].(string); ok {
			img.Status = v
		}
	}

	return &img, nil
}

func scanImageAssetRows(rows *sql.Rows) (*asset.ImageAsset, error) {
	var img asset.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name, origin, provider sql.NullString
	var url sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := rows.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID, &origin, &provider)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = url.String
	img.Hash = fileHash.String
	img.PathRel = localPath.String
	img.DriveFileID = driveFileID.String
	img.Origin = asset.ImageOrigin(origin.String)
	img.Provider = asset.ImageProvider(provider.String)

	if createdAtStr.Valid {
		img.CreatedAt = timeutil.ParseRFC3339(createdAtStr.String)
	}

	if tagsJSON.Valid && tagsJSON.String != "" {
		_ = json.Unmarshal([]byte(tagsJSON.String), &img.Tags)
	}

	if metaJSON.Valid && metaJSON.String != "" {
		img.MetadataJSON = metaJSON.String
		var metaMap map[string]any
		_ = json.Unmarshal([]byte(metaJSON.String), &metaMap)

		if v, ok := metaMap["subject_id"].(string); ok {
			img.SubjectID = v
		}
		if v, ok := metaMap["status"].(string); ok {
			img.Status = v
		}
	}

	return &img, nil
}

func (r *ImagesRepository) UpdateSubject(ctx context.Context, s *asset.Subject) error {
	_, err := r.db.ExecContext(ctx, "UPDATE subjects SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", s.DisplayName, s.Slug)
	return err
}

// GetGeneratedDetails returns the per-asset generated_image_details row
// for the given asset_id, or (nil, nil) iff no row exists. The (nil, nil)
// branch mirrors the LEFT-OUTER-JOIN semantics that FASE 4B BACKFILL
// relies on for pre-FASE-4 legacy rows.
//
// FASE 4A EXPAND (July 2026, image-territories action plan): the read
// path is separate from the GetImage* methods. BACKFILL (4B) will JOIN
// this row in via LEFT OUTER; CUTOVER (4C) will invert precedence.
// CONTRACT (4D, deferred) will drop this standalone method once JOIN
// reads become canonical.
func (r *ImagesRepository) GetGeneratedDetails(ctx context.Context, assetID string) (*asset.GeneratedImageDetail, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, prompt_original, prompt_resolved, style_id, style_version,
		       model, seed, generation_job_id, source_hash
		FROM generated_image_details
		WHERE asset_id = ?
	`, assetID)
	var d asset.GeneratedImageDetail
	err := row.Scan(&d.AssetID, &d.PromptOriginal, &d.PromptResolved,
		&d.StyleID, &d.StyleVersion, &d.Model, &d.Seed,
		&d.GenerationJobID, &d.SourceHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetRetrievedDetails mirrors GetGeneratedDetails for the
// retrieved_image_details row. Same (nil, nil) semantics for pre-FASE-4
// legacy rows.
func (r *ImagesRepository) GetRetrievedDetails(ctx context.Context, assetID string) (*asset.RetrievedImageDetail, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT asset_id, source_image_url, source_page_url, license, author,
		       search_query, retrieved_at, provider
		FROM retrieved_image_details
		WHERE asset_id = ?
	`, assetID)
	var d asset.RetrievedImageDetail
	err := row.Scan(&d.AssetID, &d.SourceImageURL, &d.SourcePageURL,
		&d.License, &d.Author, &d.SearchQuery, &d.RetrievedAt,
		&d.Provider)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
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
// Works for ALL media types (image, artlist, youtube, stock, voiceover) â€” not just images.
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
