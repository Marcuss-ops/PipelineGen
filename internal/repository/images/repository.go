// Package images provides the repository for image assets.
package images

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"velox/go-master/internal/media/models"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// DB returns the underlying database connection
func (r *Repository) DB() *sql.DB {
	return r.db
}

// GetSubjectBySlugOrAlias recupera un soggetto tramite ID (slug)
func (r *Repository) GetSubjectBySlugOrAlias(ctx context.Context, id string) (*models.Subject, error) {
	var s models.Subject
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
func (r *Repository) CreateSubject(ctx context.Context, s *models.Subject) (int64, error) {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO subjects (id, name, description, metadata_json)
		VALUES (?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.Notes, "{}")
	return 0, err
}

// AddImage aggiunge un record immagine nella tabella media_assets
func (r *Repository) AddImage(ctx context.Context, img *models.ImageAsset) (int64, error) {
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

	now := time.Now().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO media_assets (id, source, name, url, tags, tags_norm, media_type, width, height, file_hash, local_path, relative_path, drive_file_id, status, metadata_json, created_at, updated_at)
		VALUES (?, 'image', ?, ?, ?, ?, 'image', ?, ?, ?, ?, ?, ?, 'ready', ?, ?, ?)
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
			status=excluded.status,
			metadata_json=excluded.metadata_json,
			updated_at=excluded.updated_at
	`, id, img.Description, img.SourceURL, string(tagsJSON), tagsNorm,
		img.Width, img.Height, img.Hash, img.PathRel, img.PathRel, img.DriveFileID,
		string(metaJSON), now, now)

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
			"à", "a", "è", "e", "é", "e", "ì", "i", "ò", "o", "ù", "u",
		).Replace(low)
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(low)
	}
	return b.String()
}

// GetImageByHash recupera un'immagine tramite il suo hash
func (r *Repository) GetImageByHash(ctx context.Context, hash string) (*models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
		FROM media_assets 
		WHERE source = 'image' AND file_hash = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, hash)
	return scanImageAsset(row)
}

// GetByID recupera un'immagine tramite il suo ID stringa
func (r *Repository) GetByID(ctx context.Context, id interface{}) (*models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
		FROM media_assets 
		WHERE source = 'image' AND id = ?
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, id)
	return scanImageAsset(row)
}

// Delete elimina un'immagine
func (r *Repository) Delete(ctx context.Context, id interface{}) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM media_assets WHERE source = 'image' AND id = ?", id)
	return err
}

// GetByDriveFileID recupera un'immagine tramite Drive file ID
func (r *Repository) GetByDriveFileID(ctx context.Context, fileID string) (*models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
		FROM media_assets 
		WHERE source = 'image' AND (json_extract(metadata_json, '$.drive_file_id') = ? OR url LIKE ?)
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, fileID, "%"+fileID+"%")
	return scanImageAsset(row)
}

// ListImagesBySubject recupera tutte le immagini per un soggetto
func (r *Repository) ListImagesBySubject(ctx context.Context, subjectID string) ([]models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
		FROM media_assets 
		WHERE source = 'image' AND json_extract(metadata_json, '$.subject_id') = ?
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, subjectID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []models.ImageAsset
	for rows.Next() {
		img, err := scanImageAssetRows(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, *img)
	}
	return images, nil
}

// ListAll lists all image assets
func (r *Repository) ListAll(ctx context.Context) ([]*models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at, file_hash, local_path, drive_file_id
		FROM media_assets 
		WHERE source = 'image'
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []*models.ImageAsset
	for rows.Next() {
		img, err := scanImageAssetRows(rows)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

func scanImageAsset(row interface {
	Scan(dest ...any) error
}) (*models.ImageAsset, error) {
	var img models.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name sql.NullString
	var url sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := row.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = url.String
	img.Hash = fileHash.String
	img.PathRel = localPath.String
	img.DriveFileID = driveFileID.String
	
	if createdAtStr.Valid {
		img.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr.String)
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

func scanImageAssetRows(rows *sql.Rows) (*models.ImageAsset, error) {
	var img models.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name sql.NullString
	var url sql.NullString
	var fileHash, localPath, driveFileID sql.NullString

	err := rows.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr, &fileHash, &localPath, &driveFileID)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = url.String
	img.Hash = fileHash.String
	img.PathRel = localPath.String
	img.DriveFileID = driveFileID.String
	
	if createdAtStr.Valid {
		img.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr.String)
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

func (r *Repository) UpdateSubject(ctx context.Context, s *models.Subject) error {
	_, err := r.db.ExecContext(ctx, "UPDATE subjects SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", s.DisplayName, s.Slug)
	return err
}

// UpdateImageMetadata aggiorna i metadati JSON di un'immagine esistente.
func (r *Repository) UpdateImageMetadata(ctx context.Context, hash, metadataJSON string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = ?
		WHERE source = 'image' AND json_extract(metadata_json, '$.hash') = ?
	`, metadataJSON, hash)
	return err
}
