package clipcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	sqlutil "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Repository handles database operations for clip metadata
type Repository struct {
	db     *sql.DB
	logger *zap.Logger
	source string
}

// NewRepository creates a new clip catalog repository
func NewRepository(db *sql.DB, logger *zap.Logger) *Repository {
	return &Repository{db: db, logger: logger}
}

// SetSource sets the repository's source filter (e.g. 'stock', 'youtube', 'artlist')
func (r *Repository) SetSource(source string) {
	r.source = source
}

// FindCandidatesFTS searches for clip candidates using FTS5 for better ranking
func (r *Repository) FindCandidatesFTS(ctx context.Context, query string, limit int) ([]ClipCandidate, error) {
	if limit <= 0 {
		limit = 10
	}

	// Tokenize query and prepare for FTS MATCH
	tokens := textutil.Tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	// Join tokens with OR or just as a phrase. For FTS, simple space works as AND.
	// We use "term*" for prefix matching.
	ftsQuery := ""
	for _, t := range tokens {
		if len(t) < 2 {
			continue
		}
		if ftsQuery != "" {
			ftsQuery += " "
		}
		ftsQuery += t + "*"
	}

	if ftsQuery == "" {
		return nil, nil
	}

	sqlQuery := `
		SELECT
			c.id, c.name,
			COALESCE(c.search_text, '') as search_text,
			COALESCE(c.category, '') as category,
			COALESCE(c.scene_type, '') as scene_type,
			COALESCE(c.tags, '[]') as tags,
			COALESCE(c.drive_link, '') as drive_link,
			COALESCE(c.local_path, '') as local_path,
			COALESCE(c.quality_score, 0.0) as quality_score,
			COALESCE(c.reuse_count, 0) as reuse_count,
			COALESCE(json_extract(c.metadata_json, '$.usable_for'), '[]') as usable_for,
			COALESCE(json_extract(c.metadata_json, '$.avoid_for'), '[]') as avoid_for,
			COALESCE(c.folder_id, '') as folder_id,
			COALESCE(c.folder_path, '') as folder_path,
			bm25(clips_fts, 5.0, 2.0, 1.0, 1.5, 1.0) as rank
		FROM clips_fts
		JOIN media_assets c ON c.id = clips_fts.id
		WHERE clips_fts MATCH ? AND (? = '' OR c.source = ?)
		ORDER BY rank ASC, c.quality_score DESC, c.reuse_count ASC
		LIMIT ?
	`

	rows, err := r.db.QueryContext(ctx, sqlQuery, ftsQuery, r.source, r.source, limit)
	if err != nil {
		// If FTS table doesn't exist or other error, fallback to legacy FindCandidates
		r.logger.Warn("FTS search failed, falling back", zap.Error(err))
		return r.FindCandidates(ctx, query, limit)
	}
	defer rows.Close()

	candidates := make([]ClipCandidate, 0)
	for rows.Next() {
		var c ClipCandidate
		var tagsStr string
		var usableForJSON string
		var avoidForJSON string
		var rank float64
		if err := rows.Scan(&c.ID, &c.Name, &c.SearchText, &c.Category, &c.SceneType, &tagsStr, &c.DriveLink, &c.LocalPath, &c.QualityScore, &c.ReuseCount, &usableForJSON, &avoidForJSON, &c.FolderID, &c.FolderPath, &rank); err != nil {
			continue
		}

		// Parse tags
		if tagsStr != "" {
			json.Unmarshal([]byte(tagsStr), &c.Tags)
		}
		if usableForJSON != "" {
			json.Unmarshal([]byte(usableForJSON), &c.UsableFor)
		}
		if avoidForJSON != "" {
			json.Unmarshal([]byte(avoidForJSON), &c.AvoidFor)
		}

		candidates = append(candidates, c)
	}

	return candidates, nil
}

// FindCandidates searches for clip candidates based on query
func (r *Repository) FindCandidates(ctx context.Context, query string, limit int) ([]ClipCandidate, error) {
	if limit <= 0 {
		limit = 10
	}

	// Tokenize query for better matching
	tokens := textutil.Tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	// Build SQL query enforcing AND across tokens
	columns := []string{"search_text", "name", "tags"}
	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(tokens, columns)
	if conditionSQL == "" {
		return nil, nil
	}

	sqlQuery := fmt.Sprintf(`
		SELECT id, name,
			COALESCE(search_text, ''),
			COALESCE(category, ''),
			COALESCE(scene_type, ''),
			COALESCE(tags, '[]'),
			COALESCE(drive_link, ''),
			COALESCE(local_path, ''),
			COALESCE(quality_score, 0.0),
			COALESCE(reuse_count, 0),
			COALESCE(json_extract(metadata_json, '$.usable_for'), '[]'),
			COALESCE(json_extract(metadata_json, '$.avoid_for'), '[]'),
			COALESCE(folder_id, ''),
			COALESCE(folder_path, '')
		FROM media_assets
		WHERE (%s) AND (? = '' OR source = ?)
		ORDER BY quality_score DESC, reuse_count ASC
		LIMIT ?
	`, conditionSQL)

	args = append(args, r.source, r.source, limit)

	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]ClipCandidate, 0)
	for rows.Next() {
		var c ClipCandidate
		var tagsStr string
		var usableForJSON string
		var avoidForJSON string
		if err := rows.Scan(&c.ID, &c.Name, &c.SearchText, &c.Category, &c.SceneType, &tagsStr, &c.DriveLink, &c.LocalPath, &c.QualityScore, &c.ReuseCount, &usableForJSON, &avoidForJSON, &c.FolderID, &c.FolderPath); err != nil {
			r.logger.Warn("failed to scan candidate", zap.Error(err))
			continue
		}

		// Parse tags
		if tagsStr != "" {
			json.Unmarshal([]byte(tagsStr), &c.Tags)
		}

		// Parse usable_for
		if usableForJSON != "" {
			json.Unmarshal([]byte(usableForJSON), &c.UsableFor)
		}

		// Parse avoid_for
		if avoidForJSON != "" {
			json.Unmarshal([]byte(avoidForJSON), &c.AvoidFor)
		}

		candidates = append(candidates, c)
	}

	return candidates, nil
}

// MarkUsed marks a clip as used and updates reuse count
func (r *Repository) MarkUsed(ctx context.Context, clipID string, topic string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Increment reuse count on the canonical column (migration 059).
	_, err = tx.ExecContext(ctx, `
		UPDATE media_assets
		SET reuse_count = COALESCE(reuse_count, 0) + 1
		WHERE id = ?`, clipID)
	if err != nil {
		return fmt.Errorf("failed to increment reuse count: %w", err)
	}

	// Update last_used_at on the canonical column (migration 059).
	now := timeutil.FormatRFC3339(time.Now())
	_, err = tx.ExecContext(ctx, "UPDATE media_assets SET last_used_at = ? WHERE id = ?", now, clipID)
	if err != nil {
		return fmt.Errorf("failed to update last_used_at: %w", err)
	}

	return tx.Commit()
}

// GetEmbedding retrieves the embedding for a clip
func (r *Repository) GetEmbedding(ctx context.Context, clipID string) ([]float64, error) {
	var embeddingJSON string
	err := r.db.QueryRowContext(ctx, "SELECT embedding_json FROM media_assets WHERE id = ?", clipID).Scan(&embeddingJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get embedding: %w", err)
	}

	var embedding []float64
	if err := json.Unmarshal([]byte(embeddingJSON), &embedding); err != nil {
		return nil, fmt.Errorf("failed to unmarshal embedding: %w", err)
	}

	return embedding, nil
}

// GetClip retrieves a full clip by ID
func (r *Repository) GetClip(ctx context.Context, clipID string) (*asset.Asset, error) {
	var clip asset.Asset
	var driveLinkNull, localPathNull, categoryNull, searchTextNull sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name,
			COALESCE(drive_link, ''),
			COALESCE(local_path, ''),
			COALESCE(category, ''),
			COALESCE(search_text, '')
		FROM media_assets WHERE id = ?
	`, clipID).Scan(&clip.ID, &clip.Name, &driveLinkNull, &localPathNull, &categoryNull, &searchTextNull)

	if err != nil {
		return nil, err
	}

	clip.SetDriveLink(driveLinkNull.String)
	clip.SetLocalPath(localPathNull.String)
	clip.Category = categoryNull.String
	if searchTextNull.String != "" {
		clip.SearchTerms = []string{searchTextNull.String}
	}

	return &clip, nil
}
