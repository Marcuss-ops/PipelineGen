package images

import (
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
		SELECT id, slug, display_name, wikidata_id, category, notes, created_at, updated_at
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
		SELECT id, slug, display_name, wikidata_id, category, notes, created_at, updated_at
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
		INSERT INTO images (hash, subject_id, path_rel, source_url, license, width, height, size_bytes, quality_score, description, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, img.Hash, img.SubjectID, img.PathRel, img.SourceURL, img.License, img.Width, img.Height, img.SizeBytes, img.QualityScore, img.Description, img.MetadataJSON)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetImageByHash recupera un'immagine tramite il suo hash
func (r *Repository) GetImageByHash(hash string) (*models.ImageAsset, error) {
	var img models.ImageAsset
	err := r.db.QueryRow(`
		SELECT id, hash, subject_id, path_rel, source_url, license, width, height, size_bytes, quality_score, description, metadata_json, created_at
		FROM images WHERE hash = ?
	`, hash).Scan(&img.ID, &img.Hash, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.License, &img.Width, &img.Height, &img.SizeBytes, &img.QualityScore, &img.Description, &img.MetadataJSON, &img.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &img, nil
}

// ListImagesBySubject elenca le immagini di un soggetto
func (r *Repository) ListImagesBySubject(subjectID int64) ([]models.ImageAsset, error) {
	rows, err := r.db.Query(`
		SELECT id, hash, subject_id, path_rel, source_url, license, width, height, size_bytes, quality_score, description, metadata_json, created_at
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
		if err := rows.Scan(&img.ID, &img.Hash, &img.SubjectID, &img.PathRel, &img.SourceURL, &img.License, &img.Width, &img.Height, &img.SizeBytes, &img.QualityScore, &img.Description, &img.MetadataJSON, &img.CreatedAt); err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	return images, nil
}
