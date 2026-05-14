package clipcatalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/pkg/models"
	"velox/go-master/pkg/sqlutil"
	"velox/go-master/pkg/textutil"
)

// Repository handles database operations for clip metadata
type Repository struct {
	db        *sql.DB
	logger    *zap.Logger
	serverURL string
	dbPath    string
}

// NewRepository creates a new clip catalog repository
func NewRepository(db *sql.DB, logger *zap.Logger) *Repository {
	return &Repository{db: db, logger: logger}
}

// SetServerInfo sets the semantic search server configuration
func (r *Repository) SetServerInfo(url, dbPath string) {
	r.serverURL = url
	r.dbPath = dbPath
}

// SearchSemantic performs semantic search using the embedding server
func (r *Repository) SearchSemantic(ctx context.Context, query string, limit int) ([]ClipCandidate, error) {
	if r.serverURL == "" || r.dbPath == "" {
		return nil, fmt.Errorf("semantic search not configured")
	}

	if limit <= 0 {
		limit = 10
	}

	payload := map[string]interface{}{
		"db_path": r.dbPath,
		"query":   query,
		"limit":   limit,
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/search", strings.TrimSuffix(r.serverURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("semantic search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("semantic search returned status %d", resp.StatusCode)
	}

	var searchResp struct {
		Clips []struct {
			ClipID string  `json:"clip_id"`
			Score  float64 `json:"score"`
		} `json:"clips"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	if len(searchResp.Clips) == 0 {
		return nil, nil
	}

	// Fetch full candidate details from DB for the IDs returned by semantic search
	ids := make([]string, 0, len(searchResp.Clips))
	scores := make(map[string]float64)
	for _, c := range searchResp.Clips {
		ids = append(ids, c.ClipID)
		scores[c.ClipID] = c.Score
	}

	// Use placeholders for IDs
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	sqlQuery := fmt.Sprintf(`
		SELECT id, name, search_text, category, scene_type, tags, drive_link, local_path, quality_score, reuse_count, usable_for_json, avoid_for_json
		FROM clips
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch semantic candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]ClipCandidate, 0)
	for rows.Next() {
		var c ClipCandidate
		var tagsStr string
		var usableForJSON string
		var avoidForJSON string
		if err := rows.Scan(&c.ID, &c.Name, &c.SearchText, &c.Category, &c.SceneType, &tagsStr, &c.DriveLink, &c.LocalPath, &c.QualityScore, &c.ReuseCount, &usableForJSON, &avoidForJSON); err != nil {
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

	// Sort candidates by the semantic score
	// (Optional: can also blend with quality_score)
	return candidates, nil
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
			c.id, c.name, c.search_text, c.category, c.scene_type, c.tags, 
			c.drive_link, c.local_path, c.quality_score, c.reuse_count, 
			c.usable_for_json, c.avoid_for_json, c.folder_id, c.folder_path,
			bm25(clips_fts, 5.0, 2.0, 1.0, 1.5, 1.0) as rank
		FROM clips_fts
		JOIN clips c ON c.id = clips_fts.id
		WHERE clips_fts MATCH ?
		ORDER BY rank ASC, c.quality_score DESC, c.reuse_count ASC
		LIMIT ?
	`

	rows, err := r.db.QueryContext(ctx, sqlQuery, ftsQuery, limit)
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
		SELECT id, name, search_text, category, scene_type, tags, drive_link, local_path, quality_score, reuse_count, usable_for_json, avoid_for_json, folder_id, folder_path
		FROM clips
		WHERE %s
		ORDER BY quality_score DESC, reuse_count ASC
		LIMIT ?
	`, conditionSQL)

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

// UpdateMetadata updates the metadata for a clip
func (r *Repository) UpdateMetadata(ctx context.Context, clipID string, meta ClipMetadata) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

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
	_, err = tx.ExecContext(ctx, sqlStmt,
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

	// Update FTS table
	tagsStr := ""
	if len(meta.Tags) > 0 {
		t, _ := json.Marshal(meta.Tags)
		tagsStr = string(t)
	}

	// Delete old FTS entry
	_, _ = tx.ExecContext(ctx, "DELETE FROM clips_fts WHERE clip_id = ?", clipID)

	// Insert new FTS entry (fetching name from clips table)
	ftsStmt := `
		INSERT INTO clips_fts(clip_id, name, search_text, tags, category, scene_type)
		SELECT id, name, ?, ?, ?, ? FROM clips WHERE id = ?
	`
	_, _ = tx.ExecContext(ctx, ftsStmt, meta.SearchText, tagsStr, meta.Category, meta.SceneType, clipID)

	return tx.Commit()
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
func (r *Repository) GetClip(ctx context.Context, clipID string) (*models.MediaAsset, error) {
	// Delegate to existing clips repository or implement here
	// For now, return a basic clip
	var clip models.MediaAsset
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
func BuildSearchTextFromClip(clip *models.MediaAsset) string {
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
