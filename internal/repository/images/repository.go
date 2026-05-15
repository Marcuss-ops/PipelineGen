// Package images provides the repository for image assets.
package images

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/pkg/models"
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
func (r *Repository) GetSubjectBySlugOrAlias(id string) (*models.Subject, error) {
	var s models.Subject
	err := r.db.QueryRow(`
		SELECT id, name, COALESCE(description, ''), created_at, updated_at
		FROM subjects WHERE id = ?
	`, id).Scan(&s.Slug, &s.DisplayName, &s.Notes, &s.CreatedAt, &s.UpdatedAt)

	if err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateSubject crea un nuovo soggetto
func (r *Repository) CreateSubject(s *models.Subject) (int64, error) {
	_, err := r.db.Exec(`
		INSERT OR IGNORE INTO subjects (id, name, description, metadata_json)
		VALUES (?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.Notes, "{}")
	return 0, err
}

// AddImage aggiunge un record immagine nella tabella media_assets
func (r *Repository) AddImage(img *models.ImageAsset) (int64, error) {
	id := img.Hash
	if id == "" {
		id = fmt.Sprintf("img_%d", img.CreatedAt.UnixNano())
	}

	tagsJSON, _ := json.Marshal(img.Tags)
	
	// Prepara metadata_json con campi specifici per le immagini
	metaMap := make(map[string]any)
	if img.MetadataJSON != "" && img.MetadataJSON != "{}" {
		_ = json.Unmarshal([]byte(img.MetadataJSON), &metaMap)
	}
	metaMap["subject_id"] = img.SubjectID
	metaMap["local_path"] = img.PathRel
	metaMap["drive_file_id"] = img.DriveFileID
	metaMap["status"] = img.Status
	metaMap["description"] = img.Description
	metaMap["hash"] = img.Hash
	
	metaJSON, _ := json.Marshal(metaMap)

	_, err := r.db.Exec(`
		INSERT INTO media_assets (id, source, name, url, tags, metadata_json, created_at)
		VALUES (?, 'image', ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			url=excluded.url,
			tags=excluded.tags,
			metadata_json=excluded.metadata_json
	`, id, img.Description, img.SourceURL, string(tagsJSON), string(metaJSON), time.Now().Format(time.RFC3339))

	return 0, err
}

// GetImageByHash recupera un'immagine tramite il suo hash
func (r *Repository) GetImageByHash(hash string) (*models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at
		FROM media_assets 
		WHERE source = 'image' AND json_extract(metadata_json, '$.hash') = ?
		LIMIT 1
	`
	row := r.db.QueryRow(query, hash)
	return scanImageAsset(row)
}

// GetByID recupera un'immagine tramite il suo ID stringa
func (r *Repository) GetByID(ctx context.Context, id interface{}) (*models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at
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
		SELECT id, name, url, tags, metadata_json, created_at
		FROM media_assets 
		WHERE source = 'image' AND (json_extract(metadata_json, '$.drive_file_id') = ? OR url LIKE ?)
		LIMIT 1
	`
	row := r.db.QueryRowContext(ctx, query, fileID, "%"+fileID+"%")
	return scanImageAsset(row)
}

// ListImagesBySubject elenca le immagini di un soggetto
func (r *Repository) ListImagesBySubject(subjectID interface{}) ([]models.ImageAsset, error) {
	query := `
		SELECT id, name, url, tags, metadata_json, created_at
		FROM media_assets 
		WHERE source = 'image' AND json_extract(metadata_json, '$.subject_id') = ?
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(query, subjectID)
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
		SELECT id, name, url, tags, metadata_json, created_at
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

	err := row.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = url.String
	
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
		if v, ok := metaMap["local_path"].(string); ok {
			img.PathRel = v
		}
		if v, ok := metaMap["drive_file_id"].(string); ok {
			img.DriveFileID = v
		}
		if v, ok := metaMap["status"].(string); ok {
			img.Status = v
		}
		if v, ok := metaMap["hash"].(string); ok {
			img.Hash = v
		}
	}

	return &img, nil
}

func scanImageAssetRows(rows *sql.Rows) (*models.ImageAsset, error) {
	var img models.ImageAsset
	var tagsJSON, metaJSON, createdAtStr sql.NullString
	var name sql.NullString
	var url sql.NullString

	err := rows.Scan(&img.SlugID, &name, &url, &tagsJSON, &metaJSON, &createdAtStr)
	if err != nil {
		return nil, err
	}

	img.Description = name.String
	img.SourceURL = url.String
	
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
		if v, ok := metaMap["local_path"].(string); ok {
			img.PathRel = v
		}
		if v, ok := metaMap["drive_file_id"].(string); ok {
			img.DriveFileID = v
		}
		if v, ok := metaMap["status"].(string); ok {
			img.Status = v
		}
		if v, ok := metaMap["hash"].(string); ok {
			img.Hash = v
		}
	}

	return &img, nil
}

func (r *Repository) UpdateSubject(s *models.Subject) error {
	_, err := r.db.Exec("UPDATE subjects SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", s.DisplayName, s.Slug)
	return err
}
