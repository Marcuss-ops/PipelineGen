package images

import (
	"context"
	"database/sql"
	"velox/go-master/pkg/models"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// GetSubjectBySlugOrAlias recupera un soggetto tramite slug o cercando tra gli alias
func (r *Repository) GetSubjectBySlugOrAlias(query string) (*models.Subject, error) {
	var s models.Subject
	// Cerchiamo prima per slug esatto o wikidata_id
	err := r.db.QueryRow(`
		SELECT id, slug, COALESCE(display_name, ''), COALESCE(wikidata_id, ''), COALESCE(category, ''), COALESCE(notes, ''), created_at, updated_at
		FROM subjects WHERE slug = ? OR wikidata_id = ? OR aliases LIKE ?
	`, query, query, "%"+query+"%").Scan(&s.ID, &s.Slug, &s.DisplayName, &s.WikidataID, &s.Category, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SearchSubjectsFTS esegue una ricerca full-text sui soggetti
func (r *Repository) SearchSubjectsFTS(searchTerm string) ([]models.Subject, error) {
	rows, err := r.db.Query(`
		SELECT id, slug, COALESCE(display_name, ''), COALESCE(wikidata_id, ''), COALESCE(category, ''), COALESCE(notes, ''), created_at, updated_at
		FROM subjects
		WHERE id IN (SELECT rowid FROM subjects_fts WHERE subjects_fts MATCH ?)
	`, searchTerm)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subjects []models.Subject
	for rows.Next() {
		var s models.Subject
		if err := rows.Scan(&s.ID, &s.Slug, &s.DisplayName, &s.WikidataID, &s.Category, &s.Notes, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		subjects = append(subjects, s)
	}
	return subjects, nil
}

// AddAlias aggiunge un alias a un soggetto esistente
func (r *Repository) AddAlias(subjectID int64, newAlias string) error {
	var aliases string
	err := r.db.QueryRow("SELECT aliases FROM subjects WHERE id = ?", subjectID).Scan(&aliases)
	if err != nil {
		return err
	}

	// Semplice gestione stringa per ora (JSON sarebbe meglio ma per alias va bene)
	if aliases != "" {
		aliases += "," + newAlias
	} else {
		aliases = newAlias
	}

	_, err = r.db.Exec("UPDATE subjects SET aliases = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", aliases, subjectID)
	return err
}

// CreateSubject crea un nuovo soggetto
func (r *Repository) CreateSubject(s *models.Subject) (int64, error) {
	result, err := r.db.Exec(`
		INSERT INTO subjects (slug, display_name, wikidata_id, category, notes)
		VALUES (?, ?, ?, ?, ?)
	`, s.Slug, s.DisplayName, s.WikidataID, s.Category, s.Notes)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// AddImage aggiunge un record immagine
func (r *Repository) AddImage(img *models.ImageAsset) (int64, error) {
	result, err := r.db.Exec(`
		INSERT INTO images (hash, subject_id, path_rel, source_url, license, width, height, size_bytes, quality_score, description, drive_file_id, status, error, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, img.Hash, img.SubjectID, img.PathRel, img.SourceURL, img.License, img.Width, img.Height, img.SizeBytes, img.QualityScore, img.Description, img.DriveFileID, img.Status, img.Error, img.MetadataJSON)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetImageByHash recupera un'immagine tramite il suo hash
func (r *Repository) GetImageByHash(hash string) (*models.ImageAsset, error) {
	var img models.ImageAsset
	err := r.db.QueryRow(`
		SELECT id, hash, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(license, ''), width, height, size_bytes, quality_score, COALESCE(description, ''), COALESCE(drive_file_id, ''), COALESCE(status, ''), COALESCE(error, ''), COALESCE(metadata_json, '{}'), created_at
		FROM images WHERE hash = ?
	`, hash).Scan(&img.ID, &img.Hash, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.License, &img.Width, &img.Height, &img.SizeBytes, &img.QualityScore, &img.Description, &img.DriveFileID, &img.Status, &img.Error, &img.MetadataJSON, &img.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// ListImagesBySubject elenca le immagini di un soggetto
func (r *Repository) ListImagesBySubject(subjectID int64) ([]models.ImageAsset, error) {
	rows, err := r.db.Query(`
		SELECT id, hash, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(license, ''), width, height, size_bytes, quality_score, COALESCE(description, ''), COALESCE(drive_file_id, ''), COALESCE(status, ''), COALESCE(error, ''), COALESCE(metadata_json, '{}'), created_at
		FROM images WHERE subject_id = ?
		ORDER BY quality_score DESC, created_at DESC
	`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []models.ImageAsset
	for rows.Next() {
		var img models.ImageAsset
		if err := rows.Scan(&img.ID, &img.Hash, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.License, &img.Width, &img.Height, &img.SizeBytes, &img.QualityScore, &img.Description, &img.DriveFileID, &img.Status, &img.Error, &img.MetadataJSON, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, nil
}

// ListAll lists all image assets
func (r *Repository) ListAll(ctx context.Context) ([]*models.ImageAsset, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, hash, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(license, ''), width, height, size_bytes, quality_score, COALESCE(description, ''), COALESCE(drive_file_id, ''), COALESCE(status, ''), COALESCE(error, ''), COALESCE(metadata_json, '{}'), created_at
		FROM images ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var images []*models.ImageAsset
	for rows.Next() {
		var img models.ImageAsset
		if err := rows.Scan(&img.ID, &img.Hash, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.License, &img.Width, &img.Height, &img.SizeBytes, &img.QualityScore, &img.Description, &img.DriveFileID, &img.Status, &img.Error, &img.MetadataJSON, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, &img)
	}
	return images, rows.Err()
}

// Delete deletes an image by ID
func (r *Repository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM images WHERE id = ?`, id)
	return err
}

// GetByDriveFileID retrieves an image by Drive file ID (checks source_url)
func (r *Repository) GetByDriveFileID(ctx context.Context, fileID string) (*models.ImageAsset, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, hash, subject_id, COALESCE(path_rel, ''), COALESCE(source_url, ''), COALESCE(license, ''), width, height, size_bytes, quality_score, COALESCE(description, ''), COALESCE(drive_file_id, ''), COALESCE(status, ''), COALESCE(error, ''), COALESCE(metadata_json, '{}'), created_at
		FROM images WHERE source_url LIKE ? OR source_url LIKE ?
	`, "%"+fileID+"%", "%drive.google.com%"+fileID+"%")

	var img models.ImageAsset
	err := row.Scan(&img.ID, &img.Hash, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.License, &img.Width, &img.Height, &img.SizeBytes, &img.QualityScore, &img.Description, &img.DriveFileID, &img.Status, &img.Error, &img.MetadataJSON, &img.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &img, nil
}
