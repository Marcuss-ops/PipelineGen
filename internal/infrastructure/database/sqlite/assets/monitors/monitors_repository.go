package monitors

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// MonitorsRepository owns the monitored_sources table. The schema
// (column names + table identity) lives on MonitoredSourceRow; callers
// exchange domain types via FromMonitoredSourceDomain / row.ToDomain so
// the domain layer never sees a `db:"..."` tag (PR4.B, June 2026).
type MonitorsRepository struct {
	db *sql.DB
}

func NewMonitorsRepository(db *sql.DB) *MonitorsRepository {
	return &MonitorsRepository{db: db}
}

func (r *MonitorsRepository) UpsertSource(ctx context.Context, source *asset.MonitoredSource) error {
	if source == nil {
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	if source.CreatedAt == "" {
		source.CreatedAt = now
	}
	source.UpdatedAt = now

	row := FromMonitoredSourceDomain(source)
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
	`, row.ID, row.Source, row.ExternalID, row.ExternalURL, row.Title,
		row.ChannelID, row.ChannelURL, row.Keyword, row.GroupName, row.Category,
		row.Status, row.LastSeenAt, row.LastCheckedAt, row.ProcessedCount,
		row.MetadataJSON, row.CreatedAt, row.UpdatedAt)
	return err
}

func (r *MonitorsRepository) GetByExternalURL(ctx context.Context, sourceType, externalURL string) (*asset.MonitoredSource, error) {
	var row MonitoredSourceRow
	err := r.db.QueryRowContext(ctx, `
		SELECT id, source, external_id, external_url, title, channel_id, channel_url,
			keyword, group_name, category, status, last_seen_at, last_checked_at,
			processed_count, metadata_json, created_at, updated_at
		FROM monitored_sources
		WHERE source = ? AND external_url = ?
	`, sourceType, externalURL).Scan(
		&row.ID, &row.Source, &row.ExternalID, &row.ExternalURL, &row.Title,
		&row.ChannelID, &row.ChannelURL, &row.Keyword, &row.GroupName, &row.Category,
		&row.Status, &row.LastSeenAt, &row.LastCheckedAt, &row.ProcessedCount,
		&row.MetadataJSON, &row.CreatedAt, &row.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row.ToDomain(), nil
}

func (r *MonitorsRepository) ListDue(ctx context.Context, sourceType string, limit int) ([]*asset.MonitoredSource, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, source, external_id, external_url, title, channel_id, channel_url,
			keyword, group_name, category, status, last_seen_at, last_checked_at,
			processed_count, metadata_json, created_at, updated_at
		FROM monitored_sources
		WHERE source = ? AND (last_checked_at IS NULL OR last_checked_at < ?)
		ORDER BY last_checked_at IS NOT NULL, last_checked_at ASC
		LIMIT ?
	`, sourceType, timeutil.FormatRFC3339(time.Now().UTC().Add(-24*time.Hour)), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var gathered []MonitoredSourceRow
	for rows.Next() {
		var row MonitoredSourceRow
		if err := rows.Scan(
			&row.ID, &row.Source, &row.ExternalID, &row.ExternalURL, &row.Title,
			&row.ChannelID, &row.ChannelURL, &row.Keyword, &row.GroupName, &row.Category,
			&row.Status, &row.LastSeenAt, &row.LastCheckedAt, &row.ProcessedCount,
			&row.MetadataJSON, &row.CreatedAt, &row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		gathered = append(gathered, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ToMonitoredSourceDomainList(gathered), nil
}

func (r *MonitorsRepository) MarkChecked(ctx context.Context, id string) error {
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		UPDATE monitored_sources
		SET last_checked_at = ?, updated_at = ?
		WHERE id = ?
	`, now, now, id)
	return err
}

func (r *MonitorsRepository) IncrementProcessed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE monitored_sources
		SET processed_count = processed_count + 1, updated_at = ?
		WHERE id = ?
	`, timeutil.FormatRFC3339(time.Now()), id)
	return err
}
