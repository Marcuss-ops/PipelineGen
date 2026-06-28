package assets

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ChannelsRepository handles persistence for category↔channel associations.
type ChannelsRepository struct {
	db *sql.DB
}

// NewChannelsRepository creates a new category channels ChannelsRepository.
func NewChannelsRepository(db *sql.DB) *ChannelsRepository {
	return &ChannelsRepository{db: db}
}

// Upsert creates or updates a category↔channel association.
func (r *ChannelsRepository) Upsert(ctx context.Context, ch *asset.CategoryChannel) error {
	now := timeutil.FormatRFC3339(time.Now())
	if ch.CreatedAt == "" {
		ch.CreatedAt = now
	}
	ch.UpdatedAt = now

	// Capability Standard (June 2026): default application lives in
	// channels.Service.Default, not here. This function is mechanical;
	// it writes the row and trusts that every field is the canonical
	// value the application layer chose. Service.toDomain is the single
	// source of defaults (see internal/application/channels/service.go).
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO category_channels (id, category, channel_url, channel_name, keywords, min_views, max_clip_duration, drive_folder_id,
			semantic_keywords, min_semantic_score, playlist_end, check_interval, max_videos_per_run, priority, lookback_days, max_segments, segment_prompt,
			enabled, next_check_at, last_checked_at, consecutive_failures, last_error, last_success_at, lease_owner, lease_until, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			category = EXCLUDED.category,
			channel_url = EXCLUDED.channel_url,
			channel_name = EXCLUDED.channel_name,
			keywords = EXCLUDED.keywords,
			min_views = EXCLUDED.min_views,
			max_clip_duration = EXCLUDED.max_clip_duration,
			drive_folder_id = EXCLUDED.drive_folder_id,
			semantic_keywords = EXCLUDED.semantic_keywords,
			min_semantic_score = EXCLUDED.min_semantic_score,
			playlist_end = EXCLUDED.playlist_end,
			check_interval = EXCLUDED.check_interval,
			max_videos_per_run = EXCLUDED.max_videos_per_run,
			priority = EXCLUDED.priority,
			lookback_days = EXCLUDED.lookback_days,
			max_segments = EXCLUDED.max_segments,
			segment_prompt = EXCLUDED.segment_prompt,
			enabled = EXCLUDED.enabled,
			next_check_at = EXCLUDED.next_check_at,
			last_checked_at = EXCLUDED.last_checked_at,
			consecutive_failures = EXCLUDED.consecutive_failures,
			last_error = EXCLUDED.last_error,
			last_success_at = EXCLUDED.last_success_at,
			lease_owner = EXCLUDED.lease_owner,
			lease_until = EXCLUDED.lease_until,
			updated_at = EXCLUDED.updated_at
	`, ch.ID, ch.Category, ch.ChannelURL, ch.ChannelName, ch.Keywords,
		ch.MinViews, ch.MaxClipDuration, ch.DriveFolderID, ch.SemanticKeywords,
		ch.MinSemanticScore, ch.PlaylistEnd, ch.CheckInterval, ch.MaxVideosPerRun, ch.Priority,
		ch.LookbackDays, ch.MaxSegments, ch.SegmentPrompt, ch.Enabled, toNullString(ch.NextCheckAt), toNullString(ch.LastCheckedAt),
		ch.ConsecutiveFailures, toNullString(ch.LastError), toNullString(ch.LastSuccessAt), toNullString(ch.LeaseOwner), toNullString(ch.LeaseUntil),
		ch.CreatedAt, ch.UpdatedAt)
	return err
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// ListByCategory returns all channels for a given category.
func (r *ChannelsRepository) ListByCategory(ctx context.Context, category string) ([]*asset.CategoryChannel, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category, channel_url, channel_name, keywords, min_views, max_clip_duration, drive_folder_id,
			semantic_keywords, min_semantic_score, playlist_end, check_interval, max_videos_per_run, priority,
			lookback_days, max_segments, segment_prompt, enabled, next_check_at, last_checked_at,
			consecutive_failures, last_error, last_success_at, lease_owner, lease_until, created_at, updated_at
		FROM category_channels
		WHERE category = ?
		ORDER BY channel_name ASC, channel_url ASC
	`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRows(rows)
}

// ListAll returns all categoryâ†”channel associations, grouped by category order.
func (r *ChannelsRepository) ListAll(ctx context.Context) ([]*asset.CategoryChannel, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category, channel_url, channel_name, keywords, min_views, max_clip_duration, drive_folder_id,
			semantic_keywords, min_semantic_score, playlist_end, check_interval, max_videos_per_run, priority,
			lookback_days, max_segments, segment_prompt, enabled, next_check_at, last_checked_at,
			consecutive_failures, last_error, last_success_at, lease_owner, lease_until, created_at, updated_at
		FROM category_channels
		ORDER BY category ASC, channel_name ASC, channel_url ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRows(rows)
}

// ListCategories returns all distinct categories that have channels assigned.
func (r *ChannelsRepository) ListCategories(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT category
		FROM category_channels
		ORDER BY category ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var categories []string
	for rows.Next() {
		var cat string
		if err := rows.Scan(&cat); err != nil {
			return nil, err
		}
		categories = append(categories, cat)
	}
	return categories, rows.Err()
}

// GetByID retrieves a single channel association by ID.
func (r *ChannelsRepository) GetByID(ctx context.Context, id string) (*asset.CategoryChannel, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, category, channel_url, channel_name, keywords, min_views, max_clip_duration, drive_folder_id,
			semantic_keywords, min_semantic_score, playlist_end, check_interval, max_videos_per_run, priority,
			lookback_days, max_segments, segment_prompt, enabled, next_check_at, last_checked_at,
			consecutive_failures, last_error, last_success_at, lease_owner, lease_until, created_at, updated_at
		FROM category_channels
		WHERE id = ?
	`, id)
	return scanRow(row)
}

// ListEnabled returns all enabled channels (enabled=1). Used by the
// channel monitor to discover which channels to check.
func (r *ChannelsRepository) ListEnabled(ctx context.Context) ([]*asset.CategoryChannel, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, category, channel_url, channel_name, keywords, min_views, max_clip_duration, drive_folder_id,
			semantic_keywords, min_semantic_score, playlist_end, check_interval, max_videos_per_run, priority,
			lookback_days, max_segments, segment_prompt, enabled, next_check_at, last_checked_at,
			consecutive_failures, last_error, last_success_at, lease_owner, lease_until, created_at, updated_at
		FROM category_channels
		WHERE enabled = 1
		ORDER BY category ASC, channel_name ASC, channel_url ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanRows(rows)
}

// Delete removes a channel association by ID.
func (r *ChannelsRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM category_channels WHERE id = ?", id)
	return err
}

// DeleteByCategory removes all channels for a given category.
func (r *ChannelsRepository) DeleteByCategory(ctx context.Context, category string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM category_channels WHERE category = ?", category)
	return err
}

// CountByCategory returns the number of channels assigned to a category.
func (r *ChannelsRepository) CountByCategory(ctx context.Context, category string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM category_channels WHERE category = ?", category).Scan(&count)
	return count, err
}

// scanRows scans multiple rows into CategoryChannel slices.
func scanRows(rows *sql.Rows) ([]*asset.CategoryChannel, error) {
	var results []*asset.CategoryChannel
	for rows.Next() {
		ch, err := scanFields(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, ch)
	}
	return results, rows.Err()
}

// scanRow scans a single row into a CategoryChannel.
func scanRow(row *sql.Row) (*asset.CategoryChannel, error) {
	return scanFields(row)
}

// scanFields scans a row scanner into a CategoryChannel.
func scanFields(scanner interface{ Scan(dest ...any) error }) (*asset.CategoryChannel, error) {
	ch := &asset.CategoryChannel{}
	var createdAt, updatedAt sql.NullString
	var nextCheckAt, lastCheckedAt sql.NullString
	var lastError, lastSuccessAt, leaseOwner, leaseUntil sql.NullString
	err := scanner.Scan(&ch.ID, &ch.Category, &ch.ChannelURL, &ch.ChannelName,
		&ch.Keywords, &ch.MinViews, &ch.MaxClipDuration, &ch.DriveFolderID,
		&ch.SemanticKeywords, &ch.MinSemanticScore, &ch.PlaylistEnd,
		&ch.CheckInterval, &ch.MaxVideosPerRun, &ch.Priority,
		&ch.LookbackDays, &ch.MaxSegments, &ch.SegmentPrompt,
		&ch.Enabled, &nextCheckAt, &lastCheckedAt,
		&ch.ConsecutiveFailures, &lastError, &lastSuccessAt, &leaseOwner, &leaseUntil,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	ch.NextCheckAt = nextCheckAt.String
	ch.LastCheckedAt = lastCheckedAt.String
	ch.LastError = lastError.String
	ch.LastSuccessAt = lastSuccessAt.String
	ch.LeaseOwner = leaseOwner.String
	ch.LeaseUntil = leaseUntil.String
	ch.CreatedAt = createdAt.String
	ch.UpdatedAt = updatedAt.String
	return ch, nil
}
