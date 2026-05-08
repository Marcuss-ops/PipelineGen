package clipcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/pkg/models"
	"velox/go-master/pkg/textutil"
)

// Repository handles database operations for clip metadata
type Repository struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewRepository creates a new clip catalog repository
func NewRepository(db *sql.DB, logger *zap.Logger) *Repository {
	return &Repository{db: db, logger: logger}
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

	// Build SQL query with OR conditions for each token
	conditions := make([]string, 0)
	args := make([]interface{}, 0)
	for _, token := range tokens {
		if len(token) < 3 {
			continue
		}
		likePattern := "%" + token + "%"
		conditions = append(conditions, "(search_text LIKE ? OR name LIKE ? OR tags LIKE ?)")
		args = append(args, likePattern, likePattern, likePattern)
	}

	if len(conditions) == 0 {
		return nil, nil
	}

	sqlQuery := fmt.Sprintf(`
		SELECT id, name, search_text, category, scene_type, tags, drive_link, local_path, quality_score, reuse_count, usable_for_json, avoid_for_json
		FROM clips
		WHERE %s
		ORDER BY quality_score DESC, reuse_count ASC
		LIMIT ?
	`, strings.Join(conditions, " OR "))

	args = append(args, limit)

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
		if err := rows.Scan(&c.ID, &c.Name, &c.SearchText, &c.Category, &c.SceneType, &tagsStr, &c.DriveLink, &c.LocalPath, &c.QualityScore, &c.ReuseCount, &usableForJSON, &avoidForJSON); err != nil {
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

// UpdateMetadata updates the metadata for a clip
func (r *Repository) UpdateMetadata(ctx context.Context, clipID string, meta ClipMetadata) error {
	embeddingJSON, _ := json.Marshal(meta.Embedding)
	usableForJSON, _ := json.Marshal(meta.UsableFor)
	avoidForJSON, _ := json.Marshal(meta.AvoidFor)

	sqlStmt := `
		UPDATE clips
		SET search_text = ?,
			embedding_json = ?,
			category = ?,
			scene_type = ?,
			usable_for_json = ?,
			avoid_for_json = ?,
			quality_score = ?,
			last_indexed_at = ?
		WHERE id = ?
	`

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx, sqlStmt,
		meta.SearchText,
		string(embeddingJSON),
		meta.Category,
		meta.SceneType,
		string(usableForJSON),
		string(avoidForJSON),
		meta.QualityScore,
		now,
		clipID,
	)

	if err != nil {
		return fmt.Errorf("failed to update metadata for clip %s: %w", clipID, err)
	}

	return nil
}

// MarkUsed marks a clip as used and updates reuse count
func (r *Repository) MarkUsed(ctx context.Context, clipID string, topic string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Increment reuse count
	_, err = tx.ExecContext(ctx, "UPDATE clips SET reuse_count = reuse_count + 1 WHERE id = ?", clipID)
	if err != nil {
		return fmt.Errorf("failed to increment reuse count: %w", err)
	}

	// Update last_used_at
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx, "UPDATE clips SET last_used_at = ? WHERE id = ?", now, clipID)
	if err != nil {
		return fmt.Errorf("failed to update last_used_at: %w", err)
	}

	return tx.Commit()
}

// GetEmbedding retrieves the embedding for a clip
func (r *Repository) GetEmbedding(ctx context.Context, clipID string) ([]float64, error) {
	var embeddingJSON string
	err := r.db.QueryRowContext(ctx, "SELECT embedding_json FROM clips WHERE id = ?", clipID).Scan(&embeddingJSON)
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
func (r *Repository) GetClip(ctx context.Context, clipID string) (*models.Clip, error) {
	// Delegate to existing clips repository or implement here
	// For now, return a basic clip
	var clip models.Clip
	err := r.db.QueryRowContext(ctx, `
		SELECT id, name, drive_link, local_path, category, search_text
		FROM clips WHERE id = ?
	`, clipID).Scan(&clip.ID, &clip.Name, &clip.DriveLink, &clip.LocalPath, &clip.Category, &clip.SearchTerms)

	if err != nil {
		return nil, err
	}

	return &clip, nil
}

// BuildSearchTextFromClip builds search text from clip metadata
func BuildSearchTextFromClip(clip *models.Clip) string {
	parts := make([]string, 0)

	// Add name tokens
	parts = append(parts, textutil.Tokenize(clip.Name)...)

	// Add search terms
	parts = append(parts, clip.SearchTerms...)

	// Add tags
	parts = append(parts, clip.Tags...)

	// Remove duplicates and join
	seen := make(map[string]bool)
	unique := make([]string, 0)
	for _, p := range parts {
		lower := strings.ToLower(p)
		if !seen[lower] && lower != "" {
			seen[lower] = true
			unique = append(unique, p)
		}
	}

	return strings.Join(unique, " ")
}
