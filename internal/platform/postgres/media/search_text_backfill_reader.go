package media

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SearchTextBackfillCandidate is one asset whose search_text is empty and that
// is therefore a repair candidate: the composing inputs come from the row
// itself plus its current transcript track.
type SearchTextBackfillCandidate struct {
	ID          string
	Source      string
	Name        string
	Category    string
	TagsJSON    string
	SourceURL   string
	Description string
	Summary     string
	Title       string
	Transcript  string
}

// SearchTextBackfillReader answers the search_text repair's candidate scan from
// the PostgreSQL media SSOT.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): this replaces the retired
// `SELECT … FROM media_assets m … asset_text_tracks` statement that
// cmd/admin's repair-stock-metadata ran on the operational SQLite handle. Both
// media_assets and asset_text_tracks exist on PostgreSQL column-for-column
// (migration 001), so the query is a dialect translation rather than a split:
// the operational handle held no committed media rows, so the repair could only
// ever find zero candidates while the SSOT held them.
//
// The writes stay where they were — every repaired row goes through the
// canonical asset mutator (persistence.AssetMutator), never through this
// reader. This file is read-only.
type SearchTextBackfillReader struct {
	db *sql.DB
}

// NewSearchTextBackfillReader binds the reader to the media SSOT handle. A nil
// handle returns nil so callers fail closed rather than reading a second engine
// (godlike/07 no-fake-availability).
func NewSearchTextBackfillReader(db *sql.DB) *SearchTextBackfillReader {
	if db == nil {
		return nil
	}
	return &SearchTextBackfillReader{db: db}
}

// ListSearchTextBackfillCandidates returns every non-repaired asset of the
// given sources, ordered by id, with its current transcript resolved in the
// same query so the composer never has to issue per-asset reads.
//
// A non-positive limit means "no bound". At least one source is required: an
// unbounded IN () is a syntax error, and an empty source set would silently
// widen the repair to the whole catalog.
func (r *SearchTextBackfillReader) ListSearchTextBackfillCandidates(ctx context.Context, sources []string, limit int) ([]SearchTextBackfillCandidate, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("postgres search_text backfill: media reader unavailable")
	}
	cleaned := make([]string, 0, len(sources))
	for _, source := range sources {
		if trimmed := strings.TrimSpace(source); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("postgres search_text backfill: at least one source is required")
	}

	placeholders := make([]string, len(cleaned))
	args := make([]any, 0, len(cleaned)+1)
	for i, source := range cleaned {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args = append(args, source)
	}

	query := `
		SELECT m.id, COALESCE(m.source, ''), COALESCE(m.name, ''), COALESCE(m.category, ''),
		       COALESCE(m.tags, '[]'), COALESCE(m.source_url, ''),
		       COALESCE(m.metadata_json::jsonb->>'description', ''),
		       COALESCE(m.metadata_json::jsonb->>'summary', ''),
		       COALESCE(m.metadata_json::jsonb->>'title', ''),
		       COALESCE((SELECT t.text_content FROM asset_text_tracks t
		                 WHERE t.asset_id = m.id AND t.text_kind = 'transcript' AND t.is_current = 1
		                 ORDER BY t.id LIMIT 1), '')
		FROM media_assets m
		WHERE (m.search_text IS NULL OR TRIM(m.search_text) = '')
		  AND m.source IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY m.id`
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres search_text backfill: query candidates: %w", err)
	}
	defer rows.Close()

	var out []SearchTextBackfillCandidate
	for rows.Next() {
		var rec SearchTextBackfillCandidate
		if err := rows.Scan(&rec.ID, &rec.Source, &rec.Name, &rec.Category, &rec.TagsJSON,
			&rec.SourceURL, &rec.Description, &rec.Summary, &rec.Title, &rec.Transcript); err != nil {
			return nil, fmt.Errorf("postgres search_text backfill: scan candidate: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres search_text backfill: iterate candidates: %w", err)
	}
	return out, nil
}
