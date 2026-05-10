package images

import (
	"context"
	"database/sql"
	"fmt"
	"velox/go-master/pkg/models"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
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
	s.ID = 0
	return &s, nil
}

// CreateSubject crea un nuovo soggetto
func (r *Repository) CreateSubject(s *models.Subject) (int64, error) {
	_, err := r.db.Exec(`
		INSERT INTO subjects (id, name, description, metadata_json)
		VALUES (?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.Notes, "{}")
	if err != nil {
		return 0, err
	}
	return 0, nil
}

// AddImage aggiunge un record immagine
func (r *Repository) AddImage(img *models.ImageAsset) (int64, error) {
	id := img.Hash
	if id == "" {
		id = fmt.Sprintf("img_%d", img.CreatedAt.UnixNano())
	}

	_, err := r.db.Exec(`
		INSERT INTO images (id, subject_id, source_url, hash, path_rel, description, status, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, id, img.SubjectID, img.SourceURL, img.Hash, img.PathRel, img.Description, "ready", "{}")
	
	return 0, err
}

// GetImageByHash recupera un'immagine tramite il suo hash
func (r *Repository) GetImageByHash(hash string) (*models.ImageAsset, error) {
	var img models.ImageAsset
	err := r.db.QueryRow(`
		SELECT id, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(description, ''), hash, created_at
		FROM images WHERE hash = ?
	`, hash).Scan(&img.SlugID, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.Description, &img.Hash, &img.CreatedAt)
	
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// GetByID recupera un'immagine tramite il suo ID stringa
func (r *Repository) GetByID(ctx context.Context, id interface{}) (*models.ImageAsset, error) {
	var img models.ImageAsset
	// Supportiamo sia string che int64 (per compatibilità col vecchio codice)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(description, ''), hash, created_at
		FROM images WHERE id = ?
	`, id).Scan(&img.SlugID, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.Description, &img.Hash, &img.CreatedAt)
	
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// Delete elimina un'immagine
func (r *Repository) Delete(ctx context.Context, id interface{}) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM images WHERE id = ?", id)
	return err
}

// GetByDriveFileID recupera un'immagine tramite Drive file ID
func (r *Repository) GetByDriveFileID(ctx context.Context, fileID string) (*models.ImageAsset, error) {
	var img models.ImageAsset
	err := r.db.QueryRowContext(ctx, `
		SELECT id, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(description, ''), hash, created_at
		FROM images WHERE drive_file_id = ? OR source_url LIKE ?
	`, fileID, "%"+fileID+"%").Scan(&img.SlugID, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.Description, &img.Hash, &img.CreatedAt)
	
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// ListImagesBySubject elenca le immagini di un soggetto
func (r *Repository) ListImagesBySubject(subjectID interface{}) ([]models.ImageAsset, error) {
	rows, err := r.db.Query(`
		SELECT id, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(description, ''), hash, created_at
		FROM images WHERE subject_id = ?
	`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []models.ImageAsset
	for rows.Next() {
		var img models.ImageAsset
		if err := rows.Scan(&img.SlugID, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.Description, &img.Hash, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, nil
}

// ListAll lists all image assets
func (r *Repository) ListAll(ctx context.Context) ([]*models.ImageAsset, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(description, ''), hash, created_at
		FROM images ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []*models.ImageAsset
	for rows.Next() {
		var img models.ImageAsset
		if err := rows.Scan(&img.SlugID, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.Description, &img.Hash, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, &img)
	}
	return images, rows.Err()
}

func (r *Repository) UpdateSubject(s *models.Subject) error {
	_, err := r.db.Exec("UPDATE subjects SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", s.DisplayName, s.Slug)
	return err
}
