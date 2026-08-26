// package sqlite provides persistence for scheduled YouTube topic searches.
package imagesregistry

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SearchQueriesRepository handles persistence for search_queries and their results.
type SearchQueriesRepository struct {
	db *sql.DB
}

// NewSearchQueriesRepository creates a new search queries SearchQueriesRepository.
func NewSearchQueriesRepository(db *sql.DB) *SearchQueriesRepository {
	return &SearchQueriesRepository{db: db}
}

// DB returns the underlying database connection.
func (r *SearchQueriesRepository) DB() *sql.DB {
	return r.db
}

// â”€â”€ Search Queries â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// Upsert creates or updates a search query.
func (r *SearchQueriesRepository) Upsert(ctx context.Context, q *detail.SearchQuery) error {
	now := timeutil.FormatRFC3339(time.Now())
	if q.CreatedAt == "" {
		q.CreatedAt = now
	}
	q.UpdatedAt = now

	if q.CheckInterval == "" {
		q.CheckInterval = "7d"
	}
	if q.MaxResults <= 0 {
		q.MaxResults = 5
	}
	if q.MinScore <= 0 {
		q.MinScore = 60
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO search_queries (id, query, category, drive_folder_id, min_score, max_results,
			check_interval, last_run_at, last_video_published_at, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			query = EXCLUDED.query,
			category = EXCLUDED.category,
			drive_folder_id = EXCLUDED.drive_folder_id,
			min_score = EXCLUDED.min_score,
			max_results = EXCLUDED.max_results,
			check_interval = EXCLUDED.check_interval,
			last_run_at = EXCLUDED.last_run_at,
			last_video_published_at = EXCLUDED.last_video_published_at,
			is_active = EXCLUDED.is_active,
			updated_at = EXCLUDED.updated_at
	`, q.ID, q.Query, q.Category, q.DriveFolderID, q.MinScore, q.MaxResults,
		q.CheckInterval, q.LastRunAt, q.LastVideoPublishedAt, q.IsActive,
		q.CreatedAt, q.UpdatedAt)
	return err
}

// ListAll returns all search queries, active first.
func (r *SearchQueriesRepository) ListAll(ctx context.Context) ([]*detail.SearchQuery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, query, category, drive_folder_id, min_score, max_results,
			check_interval, last_run_at, last_video_published_at, is_active,
			created_at, updated_at
		FROM search_queries
		ORDER BY is_active DESC, created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQueries(rows)
}

// ListActive returns all active search queries.
func (r *SearchQueriesRepository) ListActive(ctx context.Context) ([]*detail.SearchQuery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, query, category, drive_folder_id, min_score, max_results,
			check_interval, last_run_at, last_video_published_at, is_active,
			created_at, updated_at
		FROM search_queries
		WHERE is_active = 1
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQueries(rows)
}

// GetByID retrieves a single search query by ID.
func (r *SearchQueriesRepository) GetByID(ctx context.Context, id string) (*detail.SearchQuery, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, query, category, drive_folder_id, min_score, max_results,
			check_interval, last_run_at, last_video_published_at, is_active,
			created_at, updated_at
		FROM search_queries
		WHERE id = ?
	`, id)
	return scanQuery(row)
}

// Delete removes a search query by ID.
func (r *SearchQueriesRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM search_queries WHERE id = ?", id)
	return err
}

// UpdateLastRun updates the last_run_at and last_video_published_at for a query.
func (r *SearchQueriesRepository) UpdateLastRun(ctx context.Context, id string, lastRun, lastPublishedAt string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE search_queries SET last_run_at = ?, last_video_published_at = ?, updated_at = datetime('now')
		WHERE id = ?
	`, lastRun, lastPublishedAt, id)
	return err
}

// â”€â”€ Search Query Results â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

// InsertResult records a processed video for a search query.
func (r *SearchQueriesRepository) InsertResult(ctx context.Context, res *detail.SearchQueryResult) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO search_query_results (query_id, video_id, video_title, channel_name, published_at, processed_at, score)
		VALUES (?, ?, ?, ?, ?, datetime('now'), ?)
	`, res.QueryID, res.VideoID, res.VideoTitle, res.ChannelName, res.PublishedAt, res.Score)
	return err
}

// IsVideoProcessed checks if a video was already processed by any search query.
// This provides cross-dedup: the same video won't be processed twice even if
// it matches multiple queries or also comes from a channel.
func (r *SearchQueriesRepository) IsVideoProcessed(ctx context.Context, videoID string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM search_query_results WHERE video_id = ?
	`, videoID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListResultsByQuery returns all results for a specific query.
func (r *SearchQueriesRepository) ListResultsByQuery(ctx context.Context, queryID string) ([]*detail.SearchQueryResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT query_id, video_id, video_title, channel_name, published_at, processed_at, score
		FROM search_query_results
		WHERE query_id = ?
		ORDER BY processed_at DESC
	`, queryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanResults(rows)
}

// â”€â”€ Scanners â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

func scanQueries(rows *sql.Rows) ([]*detail.SearchQuery, error) {
	var results []*detail.SearchQuery
	for rows.Next() {
		q, err := scanQueryFields(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

func scanQuery(row *sql.Row) (*detail.SearchQuery, error) {
	return scanQueryFields(row)
}

func scanQueryFields(scanner interface {
	Scan(dest ...any) error
}) (*detail.SearchQuery, error) {
	q := &detail.SearchQuery{}
	var lastRunAt, lastVideoPubAt, createdAt, updatedAt sql.NullString
	err := scanner.Scan(&q.ID, &q.Query, &q.Category, &q.DriveFolderID,
		&q.MinScore, &q.MaxResults, &q.CheckInterval,
		&lastRunAt, &lastVideoPubAt, &q.IsActive,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	q.LastRunAt = lastRunAt.String
	q.LastVideoPublishedAt = lastVideoPubAt.String
	q.CreatedAt = createdAt.String
	q.UpdatedAt = updatedAt.String
	return q, nil
}

func scanResults(rows *sql.Rows) ([]*detail.SearchQueryResult, error) {
	var results []*detail.SearchQueryResult
	for rows.Next() {
		r, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func scanResult(scanner interface {
	Scan(dest ...any) error
}) (*detail.SearchQueryResult, error) {
	r := &detail.SearchQueryResult{}
	var publishedAt, processedAt sql.NullString
	err := scanner.Scan(&r.QueryID, &r.VideoID, &r.VideoTitle, &r.ChannelName,
		&publishedAt, &processedAt, &r.Score)
	if err != nil {
		return nil, err
	}
	r.PublishedAt = publishedAt.String
	r.ProcessedAt = processedAt.String
	return r, nil
}
