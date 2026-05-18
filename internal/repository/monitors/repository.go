package monitors

import (
	"context"
	"database/sql"
	"time"

	"velox/go-master/internal/media/models"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) UpsertSource(ctx context.Context, source *models.MonitoredSource) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if source.CreatedAt == "" {
		source.CreatedAt = now
	}
	source.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO monitored_sources (
			id, source, external_id, external_url, title, channel_id, channel_url,
			keyword, group_name, category, status, last_seen_at, last_checked_at,
			processed_count, metadata_json, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		) ON CONFLICT(id) DO UPDATE SET
			source = EXCLUDED.source,
			external_id = EXCLUDED.external_id,
			external_url = EXCLUDED.external_url,
			title = EXCLUDED.title,
			channel_id = EXCLUDED.channel_id,
			channel_url = EXCLUDED.channel_url,
			keyword = EXCLUDED.keyword,
			group_name = EXCLUDED.group_name,
			category = EXCLUDED.category,
			status = EXCLUDED.status,
			last_seen_at = EXCLUDED.last_seen_at,
			last_checked_at = EXCLUDED.last_checked_at,
			processed_count = EXCLUDED.processed_count,
			metadata_json = EXCLUDED.metadata_json,
			updated_at = EXCLUDED.updated_at
	`, source.ID, source.Source, source.ExternalID, source.ExternalURL, source.Title,
		source.ChannelID, source.ChannelURL, source.Keyword, source.GroupName, source.Category,
		source.Status, source.LastSeenAt, source.LastCheckedAt, source.ProcessedCount,
		source.MetadataJSON, source.CreatedAt, source.UpdatedAt)
	return err
}

func (r *Repository) GetByExternalURL(ctx context.Context, sourceType, externalURL string) (*models.MonitoredSource, error) {
	var s models.MonitoredSource
	err := r.db.QueryRowContext(ctx, `
		SELECT id, source, external_id, external_url, title, channel_id, channel_url,
			keyword, group_name, category, status, last_seen_at, last_checked_at,
			processed_count, metadata_json, created_at, updated_at
		FROM monitored_sources
		WHERE source = ? AND external_url = ?
	`, sourceType, externalURL).Scan(
		&s.ID, &s.Source, &s.ExternalID, &s.ExternalURL, &s.Title,
		&s.ChannelID, &s.ChannelURL, &s.Keyword, &s.GroupName, &s.Category,
		&s.Status, &s.LastSeenAt, &s.LastCheckedAt, &s.ProcessedCount,
		&s.MetadataJSON, &s.CreatedAt, &s.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &s, err
}

func (r *Repository) ListDue(ctx context.Context, sourceType string, limit int) ([]*models.MonitoredSource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, source, external_id, external_url, title, channel_id, channel_url,
			keyword, group_name, category, status, last_seen_at, last_checked_at,
			processed_count, metadata_json, created_at, updated_at
		FROM monitored_sources
		WHERE source = ? AND (last_checked_at IS NULL OR last_checked_at < ?)
		ORDER BY last_checked_at IS NOT NULL, last_checked_at ASC
		LIMIT ?
	`, sourceType, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []*models.MonitoredSource
	for rows.Next() {
		var s models.MonitoredSource
		err := rows.Scan(
			&s.ID, &s.Source, &s.ExternalID, &s.ExternalURL, &s.Title,
			&s.ChannelID, &s.ChannelURL, &s.Keyword, &s.GroupName, &s.Category,
			&s.Status, &s.LastSeenAt, &s.LastCheckedAt, &s.ProcessedCount,
			&s.MetadataJSON, &s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		sources = append(sources, &s)
	}
	return sources, rows.Err()
}

func (r *Repository) MarkChecked(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, `
		UPDATE monitored_sources
		SET last_checked_at = ?, updated_at = ?
		WHERE id = ?
	`, now, now, id)
	return err
}

func (r *Repository) IncrementProcessed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE monitored_sources
		SET processed_count = processed_count + 1, updated_at = ?
		WHERE id = ?
	`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}
