package characters

import (
	"context"
	"database/sql"
	"time"
	"velox/go-master/internal/media/models"
)

// Repository handles persistence for AI characters/avatars
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new characters repository
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Upsert inserts or updates a character record
func (r *Repository) Upsert(ctx context.Context, char *models.Character) error {
	now := time.Now().Format(time.RFC3339)
	if char.CreatedAt.IsZero() {
		char.CreatedAt = time.Now()
	}
	char.UpdatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO characters (
			id, name, image_drive_id, image_drive_link, voice_id, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			image_drive_id = excluded.image_drive_id,
			image_drive_link = excluded.image_drive_link,
			voice_id = excluded.voice_id,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, char.ID, char.Name, char.ImageDriveID, char.ImageDriveLink, char.VoiceID, char.MetadataJSON(), char.CreatedAt.Format(time.RFC3339), now)

	return err
}

// GetByID retrieves a character by its slug ID
func (r *Repository) GetByID(ctx context.Context, id string) (*models.Character, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(image_drive_id, ''), COALESCE(image_drive_link, ''), COALESCE(voice_id, ''), COALESCE(metadata_json, '{}'), created_at, updated_at
		FROM characters WHERE id = ?`, id)

	var char models.Character
	var metaJSON, createdAt, updatedAt string
	err := row.Scan(&char.ID, &char.Name, &char.ImageDriveID, &char.ImageDriveLink, &char.VoiceID, &metaJSON, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	char.SetMetadataJSON(metaJSON)
	char.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	char.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return &char, nil
}

// List retrieves all characters
func (r *Repository) List(ctx context.Context) ([]*models.Character, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(image_drive_id, ''), COALESCE(image_drive_link, ''), COALESCE(voice_id, ''), COALESCE(metadata_json, '{}'), created_at, updated_at
		FROM characters ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chars []*models.Character
	for rows.Next() {
		var char models.Character
		var metaJSON, createdAt, updatedAt string
		if err := rows.Scan(&char.ID, &char.Name, &char.ImageDriveID, &char.ImageDriveLink, &char.VoiceID, &metaJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		char.SetMetadataJSON(metaJSON)
		char.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		char.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		chars = append(chars, &char)
	}
	return chars, nil
}

// Delete removes a character from the registry
func (r *Repository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM characters WHERE id = ?", id)
	return err
}
