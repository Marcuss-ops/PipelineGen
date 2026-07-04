// package sqlite provides the ImagesRepository for image assets.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
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
	// FASE 4 CUTOVER (July 2026, image-territories action plan): dual-write
	// to the matching detail table when origin is set. Per godlike/07
	// fail-closed, an UPSERT error after a successful media_assets INSERT
	// is propagated to the caller; the operator audit-gazette surfaces
	// the asymmetry and the caller can re-run UpsertGeneratedDetails /
	// UpsertRetrievedDetails idempotently.
	if err := r.dualWriteImageDetails(ctx, id, img); err != nil {
		return 0, fmt.Errorf("dual-write image details: %w", err)
	}
	return 0, nil
}

// dualWriteImageDetails reads img.Origin and routes the asset to the
// matching detail table with best-effort field mapping. Caller can call
// UpsertGeneratedDetails / UpsertRetrievedDetails subsequently to
// refine the row with full provenance (style_id, prompt_resolved, seed,
// generation_job_id for generated; license, author, search_query for
// retrieved).
//
// FASE 4 CUTOVER: when origin=” or origin=ImageOriginUploaded the
// function returns nil silently (unclassified rows are eligible for the
// FASE 4 step-4D backfill later).
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
	// origin='' (unclassified) OR origin=ImageOriginUploaded → skip silently.
	return nil
}

// UpsertGeneratedDetails writes per-asset provenance for an AI-generated
// image. ON CONFLICT(asset_id) DO UPDATE so re-running for the same
// asset is idempotent. FASE 4 CUTOVER.
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

// UpsertRetrievedDetails writes per-asset provenance for a web-retrieved
// image. ON CONFLICT(asset_id) DO UPDATE so re-running for the same
// asset is idempotent. FASE 4 CUTOVER.
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
// the row keyed by file_hash. Used by the FASE 4 step-4D backfill admin
// command to promote unclassified rows (origin=”) to a canonical
// territory. FASE 4 CUTOVER.
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
	return scanImageAssetFromRow(row)
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
	return scanImageAssetFromRow(row)
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
		img, err := scanImageAssetFromRow(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// scanImageAssetFromRow is the canonical (godlike/06 SSOT) helper that
// scans a single image row into *asset.ImageAsset. Replaces the
// pre-B6 byte-equivalent duplication between scanImageAsset
// (*sql.Row-shaped) and scanImageAssetRows (Rows-shaped). Both old
// helpers are gone; this single typed-(structural-interface) helper
// covers every caller because both *sql.Row.Scan(...) and
// *sql.Rows.Scan(...) satisfy `interface{ Scan(dest ...any) error }`.
//
// FASE 1B reads origin and provider as first-class columns (added by
// migration 115), surfacing them on ImageAsset.Origin / .Provider for
// downstream ImageSearchResolver routing (FASE 6).
//
// Column projection MUST match the SELECT in:
//   - GetImageByHash, GetByID, GetByDriveFileID (Row path, this file)
//   - ListImagesBySubject, ListAll (Rows path, this file)
//
// B6 SSOT refactor (PR-IMAGES-AI-VS-NORMAL-PLAN, July 2026). Property
// tests in images_repository_test.go assert byte-equivalence across
// *sql.Row and *sql.Rows paths.
func scanImageAssetFromRow(s interface {
	Scan(dest ...any) error
}) (*asset.ImageAsset, error) {
	var img asset.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name, origin, provider sql.NullString
	var url sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := s.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID, &origin, &provider)
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

// FASE 6 (July 2026, image-territories action plan): ListImages is the
// FASE 6 canonical replacement for ListImagesBySubject. Takes a
// routing.RepositoryListFilter and returns routing.RepositoryImageRow
// rows (FASE 8: renamed from routing.ImageFilter/routing.ImageSearchResult
// to disambiguate from the canonical routing-layer DTOs that the
// ImageSearcher interface returns — the SQLite adapter needs the
// underlying Subject/Slug/Description columns to populate the join
// projection, so the read-model shape carries extra fields).
// See deprecation record PR-IMAGE-LISTIMAGESBYSUBJECT for the migration
// narrative; the physical removal of ListImagesBySubject is queued at
// the Wave 14 mega-package split gate, NOT in this commit.
//
// Implementation: routes against media_assets LEFT OUTER JOIN
// generated_image_details (gid) so StyleID/StyleVersion are populated
// for generated rows. Filter.Origins filtering hard-filters by
// media_assets.origin — when called from the generated-territory
// searcher (searcher_generated.go), Origins is pre-narrowed to
// [OriginGenerated], so no retrieved rows leak into the result.
func (r *ImagesRepository) ListImages(ctx context.Context, filter routing.RepositoryListFilter) ([]routing.RepositoryImageRow, error) {
	if r == nil {
		return nil, nil
	}
	limit := filter.Limit
	if limit <= 0 || limit > routing.MaxListImagesLimit {
		if limit > routing.MaxListImagesLimit {
			limit = routing.MaxListImagesLimit
		} else {
			limit = routing.DefaultResolvedLimit
		}
	}

	var sb strings.Builder
	sb.WriteString(`SELECT ma.id, ma.origin, ma.provider_id, ma.subject_id, ma.preview_url, ma.width, ma.height, gid.prompt_resolved, gid.style_id, gid.style_version FROM media_assets ma LEFT JOIN generated_image_details gid ON ma.id = gid.asset_id WHERE 1=1`)
	args := []any{}

	if filter.SubjectID != "" {
		sb.WriteString(" AND ma.subject_id = ?")
		args = append(args, filter.SubjectID)
	}

	if len(filter.Origins) > 0 {
		ph := make([]string, len(filter.Origins))
		for i, o := range filter.Origins {
			ph[i] = "?"
			args = append(args, string(o))
		}
		sb.WriteString(" AND ma.origin IN (" + strings.Join(ph, ",") + ")")
	}

	if len(filter.Providers) > 0 {
		ph := make([]string, len(filter.Providers))
		for i, p := range filter.Providers {
			ph[i] = "?"
			args = append(args, p)
		}
		sb.WriteString(" AND ma.provider_id IN (" + strings.Join(ph, ",") + ")")
	}

	if len(filter.StyleIDs) > 0 {
		ph := make([]string, len(filter.StyleIDs))
		for i, s := range filter.StyleIDs {
			ph[i] = "?"
			args = append(args, s)
		}
		sb.WriteString(" AND gid.style_id IN (" + strings.Join(ph, ",") + ")")
	}

	if len(filter.Tags) > 0 {
		for _, tag := range filter.Tags {
			sb.WriteString(" AND ma.tags LIKE ?")
			args = append(args, "%"+tag+"%")
		}
	}

	sb.WriteString(" ORDER BY ma.id DESC LIMIT ?")
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]routing.RepositoryImageRow, 0, limit)
	for rows.Next() {
		var (
			id, originStr, providerID, subjectID, previewURL sql.NullString
			width, height                                    sql.NullInt64
			promptResolved, styleID, styleVersion            sql.NullString
		)
		err := rows.Scan(&id, &originStr, &providerID, &subjectID, &previewURL, &width, &height, &promptResolved, &styleID, &styleVersion)
		if err != nil {
			return nil, err
		}
		name := ""
		if promptResolved.Valid {
			runes := []rune(promptResolved.String)
			if len(runes) > 80 {
				name = string(runes[:80])
			} else {
				name = promptResolved.String
			}
		}
		var w, h int
		if width.Valid {
			w = int(width.Int64)
		}
		if height.Valid {
			h = int(height.Int64)
		}
		out = append(out, routing.RepositoryImageRow{
			AssetID:      id.String,
			Origin:       asset.ImageOrigin(originStr.String),
			Provider:     providerID.String,
			Name:         name,
			PreviewURL:   previewURL.String,
			Width:        w,
			Height:       h,
			Score:        1.0,
			StyleID:      styleID.String,
			StyleVersion: styleVersion.String,
		})
	}
	return out, rows.Err()
}
