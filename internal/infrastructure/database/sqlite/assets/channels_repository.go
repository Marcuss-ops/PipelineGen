package assets

import (
	"context"
	"database/sql"
	"errors"
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

// ErrLeaseLost is the sentinel error returned by MarkChecked when the
// SQL UPDATE `WHERE id=? AND lease_owner=?` matches zero rows because:
//   - the lease has expired and another worker re-claimed the channel
//     (lease_until < now), OR
//   - the lease_owner was reset to NULL by an admin op, OR
//   - this caller simply holds an outdated worker_id.
//
// Commit A (June 2026, P1 #8): callers detect this via errors.Is(err,
// ErrLeaseLost) and MUST treat it as "another worker owns this row
// now" — silently retrying on a different lease token would clobber
// that worker's state. The production caller (ChannelMonitor.record
// CheckOutcome) treats it as a transient bookkeeping race: the
// scheduled retry on the next scheduler tick (or manual resync) will
// reconcile via the ledger path added in Commit D. Test code asserts
// the sentinel directly via errors.Is.
var ErrLeaseLost = errors.New("channels: lease expired or stolen; row not updated")

// channelSelectColumns is the canonical SELECT projection for the
// category_channels table. ALL SELECT statements in this file MUST
// use this constant — never enumerate columns inline. Commit A
// (June 2026, P0 #1) closes the pre-fix bug where GetByID / ListAll /
// ListEnabled / ListByCategory each listed 27 columns while
// scanFields scanned 28 destinations (last_cursor was missing from
// every SELECT). `rows.Scan` rejects mismatched counts, so the bug
// was a runtime panic on every read.
//
// Order MUST match scanFields' Scan(…) destination list verbatim —
// it is the single source of truth for column-order ↔ struct-field
// binding. Future columns: append at the end (BEFORE created_at /
// updated_at) and update both this constant AND the CategoryChannel
// domain struct in the same commit so callers don't have to track
// subsecond column drift.
//
// The multi-line format is intentional: the same constant is used
// at SELECT sites (composed via `SELECT `+channelSelectColumns+`
// FROM …`) so formatting noise is bounded to ONE location. A
// condensed single-line version was considered and rejected — it
// would invite drift between the readable docblock shape and the
// SELECT-call shape.
const channelSelectColumns = `id, category, channel_url, channel_name, keywords, min_views, max_clip_duration, drive_folder_id, semantic_keywords, min_semantic_score, playlist_end, check_interval, max_videos_per_run, priority, lookback_days, max_segments, segment_prompt, enabled, next_check_at, last_checked_at, consecutive_failures, last_error, last_success_at, lease_owner, lease_until, last_cursor, created_at, updated_at`

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
			enabled, next_check_at, last_checked_at, consecutive_failures, last_error, last_success_at, lease_owner, lease_until, last_cursor, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			last_cursor = EXCLUDED.last_cursor,
			updated_at = EXCLUDED.updated_at
	`, ch.ID, ch.Category, ch.ChannelURL, ch.ChannelName, ch.Keywords,
		ch.MinViews, ch.MaxClipDuration, ch.DriveFolderID, ch.SemanticKeywords,
		ch.MinSemanticScore, ch.PlaylistEnd, ch.CheckInterval, ch.MaxVideosPerRun, ch.Priority,
		ch.LookbackDays, ch.MaxSegments, ch.SegmentPrompt, ch.Enabled, toNullString(ch.NextCheckAt), toNullString(ch.LastCheckedAt),
		ch.ConsecutiveFailures, toNullString(ch.LastError), toNullString(ch.LastSuccessAt), toNullString(ch.LeaseOwner), toNullString(ch.LeaseUntil), toNullString(ch.LastCursor),
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
		SELECT `+channelSelectColumns+`
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
		SELECT `+channelSelectColumns+`
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
		SELECT `+channelSelectColumns+`
		FROM category_channels
		WHERE id = ?
	`, id)
	return scanRow(row)
}

// ListEnabled returns all enabled channels (enabled=1). Used by the
// channel monitor to discover which channels to check.
func (r *ChannelsRepository) ListEnabled(ctx context.Context) ([]*asset.CategoryChannel, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT `+channelSelectColumns+`
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

// MarkChecked updates the scheduling columns after a channel-sync check
// completes. Commit A (June 2026, P1 #8): the UPDATE is fenced on
// `lease_owner = ?` — only the worker that holds the lease can update
// the row. RowsAffected==0 (lease lost, lease expired, lease_owner
// NULL) returns the sentinel ErrLeaseLost so callers can
// programmatically decide not to retry with a different token.
//
// On a fenced UPDATE, the SET clause also nukes lease_owner +
// lease_until (the holder transitions to idle), so the next
// ClaimDue can re-claim the row on a future tick. LeaseToken is
// required for production callers (ChannelMonitor passes
// ch.LeaseOwner); empty LeaseToken bypasses the fence and matches
// any current lease_owner (or no lease_owner) — that's the
// back-compat path for any future admin CLI that doesn't hold a
// lease.
func (r *ChannelsRepository) MarkChecked(ctx context.Context, id, leaseToken, nextCheckAt, lastError string, success bool) error {
	now := timeutil.FormatRFC3339(time.Now())

	var (
		res sql.Result
		err error
	)

	if leaseToken == "" {
		// Back-compat / admin path: fence is opt-in. Empty leaseToken
		// intentionally matches any lease_owner (including NULL) so a
		// one-shot admin tool can update scheduling state without
		// claiming a lease first.
		res, err = r.db.ExecContext(ctx, `
			UPDATE category_channels
			SET next_check_at = ?,
			    last_checked_at = ?,
			    last_error = CASE WHEN ? = '' THEN last_error ELSE ? END,
			    last_success_at = CASE WHEN ? THEN ? ELSE last_success_at END,
			    consecutive_failures = CASE WHEN ? THEN 0 ELSE consecutive_failures + 1 END,
			    updated_at = ?
			WHERE id = ?
		`, nextCheckAt, now,
			lastError, lastError,
			success, now,
			success,
			now, id)
	} else {
		// Production / monitor path: fenced UPDATE. Lease_owner is
		// reset to NULL so the next ClaimDue tick can re-claim cleanly.
		// last_error is preserved as the new error string when non-empty
		// (per the pre-fix semantics) and consecutive_failures
		// increments on failure per the pre-fix semantics.
		res, err = r.db.ExecContext(ctx, `
			UPDATE category_channels
			SET next_check_at = ?,
			    last_checked_at = ?,
			    last_error = CASE WHEN ? = '' THEN last_error ELSE ? END,
			    last_success_at = CASE WHEN ? THEN ? ELSE last_success_at END,
			    consecutive_failures = CASE WHEN ? THEN 0 ELSE consecutive_failures + 1 END,
			    lease_owner = NULL,
			    lease_until = NULL,
			    updated_at = ?
			WHERE id = ? AND lease_owner = ?
		`, nextCheckAt, now,
			lastError, lastError,
			success, now,
			success,
			now, id, leaseToken)
	}
	if err != nil {
		return err
	}
	rowsAffected, raErr := res.RowsAffected()
	if raErr != nil {
		return raErr
	}
	if rowsAffected == 0 && leaseToken != "" {
		// Fenced UPDATE matched zero rows — the worker lost its lease
		// (expired / stolen / lease_owner was reset). DO NOT mutate
		// state; surface the sentinel so the caller can react.
		return ErrLeaseLost
	}
	return nil
}

// ClaimDue atomically claims channels that are due for checking.
// Commit A (June 2026, P1 #10): ORDER BY now includes `priority ASC`
// so hot-priority channels (Priority=1) are claimed before normal
// (Priority=2) before cold (Priority=3) within the same tick. The
// secondary sort on next_check_at ASC is preserved so within each
// priority bucket the scheduler still prefers the most-overdue
// channel.
func (r *ChannelsRepository) ClaimDue(ctx context.Context, nowStr, workerID, leaseUntil string, limit int) ([]*asset.CategoryChannel, error) {
	if limit <= 0 {
		limit = 10
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM category_channels
		WHERE enabled = 1
		  AND (next_check_at IS NULL OR next_check_at = '' OR next_check_at <= ?)
		  AND (lease_until IS NULL OR lease_until = '' OR lease_until < ?)
		ORDER BY priority ASC, next_check_at ASC
		LIMIT ?
	`, nowStr, nowStr, limit)
	if err != nil {
		return nil, err
	}

	var claimedIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		claimedIDs = append(claimedIDs, id)
	}
	rows.Close()

	nowFormatted := timeutil.FormatRFC3339(time.Now())
	for _, id := range claimedIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE category_channels SET lease_owner = ?, lease_until = ?, updated_at = ? WHERE id = ?
		`, workerID, leaseUntil, nowFormatted, id); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	var result []*asset.CategoryChannel
	for _, id := range claimedIDs {
		ch, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if ch != nil {
			result = append(result, ch)
		}
	}
	return result, nil
}

// UpdateCursor updates the incremental sync cursor for a channel.
// PR 5 (June 2026): tracks the last video ID processed. Takes individual
// params to avoid importing application-layer command types.
func (r *ChannelsRepository) UpdateCursor(ctx context.Context, id, cursor string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE category_channels SET last_cursor = ?, updated_at = ? WHERE id = ?
	`, cursor, timeutil.FormatRFC3339(time.Now()), id)
	return err
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

// scanFields scans a row scanner into a CategoryChannel. The
// destination order MUST match channelSelectColumns verbatim — the
// single source of truth is the constant in this file. Adding a column
// means: ALTER TABLE migration + add field to CategoryChannel
// (domain/asset/types_media.go) + append column here and in the
// const + extend scanFields dest list in the same commit.
func scanFields(scanner interface{ Scan(dest ...any) error }) (*asset.CategoryChannel, error) {
	ch := &asset.CategoryChannel{}
	var createdAt, updatedAt sql.NullString
	var nextCheckAt, lastCheckedAt sql.NullString
	var lastError, lastSuccessAt, leaseOwner, leaseUntil, lastCursor sql.NullString
	err := scanner.Scan(&ch.ID, &ch.Category, &ch.ChannelURL, &ch.ChannelName,
		&ch.Keywords, &ch.MinViews, &ch.MaxClipDuration, &ch.DriveFolderID,
		&ch.SemanticKeywords, &ch.MinSemanticScore, &ch.PlaylistEnd,
		&ch.CheckInterval, &ch.MaxVideosPerRun, &ch.Priority,
		&ch.LookbackDays, &ch.MaxSegments, &ch.SegmentPrompt,
		&ch.Enabled, &nextCheckAt, &lastCheckedAt,
		&ch.ConsecutiveFailures, &lastError, &lastSuccessAt, &leaseOwner, &leaseUntil, &lastCursor,
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
	ch.LastCursor = lastCursor.String
	ch.CreatedAt = createdAt.String
	ch.UpdatedAt = updatedAt.String
	return ch, nil
}
