package sketchfab

import (
	"context"
	"database/sql"
	"time"
)

// Model3D represents a 3D model from Sketchfab
type Model3D struct {
	UID               string    `json:"uid"`
	Name              string    `json:"name"`
	UserName          string    `json:"user_name"`
	LicenseType       string    `json:"license_type"`
	ThumbURL          string    `json:"thumb_url"`
	ViewURL           string    `json:"view_url"`
	DownloadURL       string    `json:"download_url,omitempty"`
	DownloadExpiresAt *time.Time `json:"download_expires_at,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	MetadataJSON      string    `json:"metadata_json"`
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// SearchModels searches models in the local database
func (r *Repository) SearchModels(ctx context.Context, query string) ([]*Model3D, error) {
	sqlQuery := `
		SELECT uid, name, user_name, license_type, thumb_url, view_url, download_url, download_expires_at, created_at, metadata_json
		FROM sketchfab_models
		WHERE name LIKE ? OR uid LIKE ?
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, sqlQuery, "%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []*Model3D
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, nil
}

func scanModel(rows *sql.Rows) (*Model3D, error) {
	var m Model3D
	var downloadURL, expiresAtStr, createdAtStr sql.NullString
	
	err := rows.Scan(
		&m.UID, &m.Name, &m.UserName, &m.LicenseType, &m.ThumbURL, &m.ViewURL,
		&downloadURL, &expiresAtStr, &createdAtStr, &m.MetadataJSON,
	)
	if err != nil {
		return nil, err
	}

	m.DownloadURL = downloadURL.String
	if expiresAtStr.Valid {
		if t, err := time.Parse(time.RFC3339, expiresAtStr.String); err == nil {
			m.DownloadExpiresAt = &t
		}
	}
	if createdAtStr.Valid {
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr.String)
	}

	return &m, nil
}
