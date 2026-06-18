package gemmamemory

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Repository provides data access for the gemma memory tables.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a new memory repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// ─── Level 1: Exact cache ───

// FindExactOutput looks up a previously generated output by channel + mode + hash.
func (r *Repository) FindExactOutput(ctx context.Context, channelID, mode, inputHash string) (*GenerationOutput, error) {
	var out GenerationOutput
	err := r.db.QueryRowContext(ctx,
		`SELECT id, channel_id, mode, language, title, prompt, normalized_input, input_hash,
		        output_text, output_json, model, job_id, word_count, created_at
		 FROM gemma_script_outputs
		 WHERE channel_id = ? AND mode = ? AND input_hash = ?`,
		channelID, mode, inputHash,
	).Scan(&out.ID, &out.ChannelID, &out.Mode, &out.Language, &out.Title,
		&out.Prompt, &out.NormalizedInput, &out.InputHash,
		&out.OutputText, &out.OutputJSON, &out.Model, &out.JobID,
		&out.WordCount, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SaveGeneration saves a completed generation output and returns the ID.
func (r *Repository) SaveGeneration(ctx context.Context, input SaveGenerationInput, normalizedInput, inputHash string) (string, error) {
	id := "gen_" + uuid.New().String()[:12]
	_, err := r.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO gemma_script_outputs
		 (id, channel_id, mode, language, title, prompt, normalized_input, input_hash,
		  output_text, output_json, model, job_id, word_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.ChannelID, input.Mode, input.Language, input.Title, input.Prompt,
		normalizedInput, inputHash, input.OutputText, input.OutputJSON,
		input.Model, input.JobID, input.WordCount,
	)
	if err != nil {
		return "", fmt.Errorf("save generation: %w", err)
	}
	return id, nil
}

// ─── Level 2: Memory entries ───

// FindMemoryByChannel returns memory entries for a channel, optionally filtered by type.
func (r *Repository) FindMemoryByChannel(ctx context.Context, channelID, memoryType string, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	var query string
	var args []any
	if memoryType != "" {
		query = `SELECT id, channel_id, memory_type, topic_key, title, summary, content_text,
		                content_json, source_generation_id, source_job_id, usefulness_score, created_at
		         FROM gemma_memory_entries
		         WHERE channel_id = ? AND memory_type = ?
		         ORDER BY usefulness_score DESC, created_at DESC
		         LIMIT ?`
		args = []any{channelID, memoryType, limit}
	} else {
		query = `SELECT id, channel_id, memory_type, topic_key, title, summary, content_text,
		                content_json, source_generation_id, source_job_id, usefulness_score, created_at
		         FROM gemma_memory_entries
		         WHERE channel_id = ?
		         ORDER BY usefulness_score DESC, created_at DESC
		         LIMIT ?`
		args = []any{channelID, limit}
	}
	return r.scanMemories(r.db.QueryContext(ctx, query, args...))
}

// FindMemoryByTopicKey returns memories matching a specific topic key.
func (r *Repository) FindMemoryByTopicKey(ctx context.Context, channelID, topicKey string, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, memory_type, topic_key, title, summary, content_text,
		        content_json, source_generation_id, source_job_id, usefulness_score, created_at
		 FROM gemma_memory_entries
		 WHERE channel_id = ? AND topic_key = ?
		 ORDER BY usefulness_score DESC, created_at DESC
		 LIMIT ?`,
		channelID, topicKey, limit,
	)
	if err != nil {
		return nil, err
	}
	return r.scanMemories(rows, err)
}

// SaveMemory stores a reusable memory entry.
func (r *Repository) SaveMemory(ctx context.Context, input SaveMemoryInput) (string, error) {
	id := "mem_" + uuid.New().String()[:12]
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO gemma_memory_entries
		 (id, channel_id, memory_type, topic_key, title, summary, content_text, content_json,
		  source_generation_id, source_job_id, last_used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		id, input.ChannelID, input.MemoryType, input.TopicKey, input.Title,
		input.Summary, input.ContentText, input.ContentJSON,
		input.SourceGenerationID, input.SourceJobID,
	)
	if err != nil {
		return "", fmt.Errorf("save memory: %w", err)
	}
	return id, nil
}

// TouchMemory bumps last_used_at on a memory entry and increments its usefulness_score.
// Returns rows affected (0 if the id is missing).
func (r *Repository) TouchMemory(ctx context.Context, id string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"UPDATE gemma_memory_entries SET last_used_at = datetime('now'), usefulness_score = usefulness_score + 1.0 WHERE id = ?", id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SweepStaleMemories decays the usefulness_score of memories not touched in the last 7 days,
// and deletes memory rows whose last_used_at is older than maxAgeDays.
// Returns the number of rows deleted.
func (r *Repository) SweepStaleMemories(ctx context.Context, maxAgeDays int) (int64, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}
	// Decay usefulness_score of memories not used in the last 7 days (reducing priority by 10% per sweep cycle)
	_, _ = r.db.ExecContext(ctx, "UPDATE gemma_memory_entries SET usefulness_score = usefulness_score * 0.9 WHERE last_used_at < datetime('now', ?)", "-7 days")

	res, err := r.db.ExecContext(ctx,
		"DELETE FROM gemma_memory_entries WHERE last_used_at < datetime('now', ?)",
		fmt.Sprintf("-%d days", maxAgeDays),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SweepStaleChunks deletes chunk rows older than maxAgeDays. Chunks are
// only used for LIKE-based similarity search, so 60 days is plenty: a topic
// the user has not touched in 2 months is almost certainly not relevant
// for the next run.
func (r *Repository) SweepStaleChunks(ctx context.Context, maxAgeDays int) (int64, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 60
	}
	res, err := r.db.ExecContext(ctx,
		"DELETE FROM gemma_script_chunks WHERE created_at < datetime('now', ?)",
		fmt.Sprintf("-%d days", maxAgeDays),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CapMemoriesPerChannel trims gemma_memory_entries per channel so no single
// channel accumulates more than maxPerChannel rows. The least useful and
// oldest rows are dropped first. Returns the number of rows deleted.
func (r *Repository) CapMemoriesPerChannel(ctx context.Context, maxPerChannel int) (int64, error) {
	if maxPerChannel <= 0 {
		maxPerChannel = 500
	}
	// For each channel, keep the top N by usefulness+recency and delete the rest.
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM gemma_memory_entries
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY channel_id
					ORDER BY usefulness_score DESC, last_used_at DESC
				) AS rn
				FROM gemma_memory_entries
			) WHERE rn > ?
		)`, maxPerChannel)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FindMemoryCrossChannel searches for memories across ALL channels when the specific
// channel has no relevant results. Useful for channels that share topics.
// Returns at most limit entries, ordered by usefulness_score DESC.
func (r *Repository) FindMemoryCrossChannel(ctx context.Context, excludeChannelID, memoryType string, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	var query string
	var args []any
	if memoryType != "" {
		query = `SELECT id, channel_id, memory_type, topic_key, title, summary, content_text,
	                content_json, source_generation_id, source_job_id, usefulness_score, created_at
	         FROM gemma_memory_entries
	         WHERE channel_id != ? AND memory_type = ?
	         ORDER BY usefulness_score DESC, created_at DESC
	         LIMIT ?`
		args = []any{excludeChannelID, memoryType, limit}
	} else {
		query = `SELECT id, channel_id, memory_type, topic_key, title, summary, content_text,
	                content_json, source_generation_id, source_job_id, usefulness_score, created_at
	         FROM gemma_memory_entries
	         WHERE channel_id != ?
	         ORDER BY usefulness_score DESC, created_at DESC
	         LIMIT ?`
		args = []any{excludeChannelID, limit}
	}
	return r.scanMemories(r.db.QueryContext(ctx, query, args...))
}

// SweepAll runs all maintenance tasks in one call: decays stale memories,
// deletes expired entries, caps per-channel growth, and cleans old chunks.
// Returns total rows deleted across all sweeps.
func (r *Repository) SweepAll(ctx context.Context) (totalDeleted int64, err error) {
	// 1. Decay + delete expired memories (90 days)
	n, sweepErr := r.SweepStaleMemories(ctx, 90)
	if sweepErr != nil {
		err = sweepErr
	}
	totalDeleted += n

	// 2. Cap per-channel memories at 500
	n, capErr := r.CapMemoriesPerChannel(ctx, 500)
	if capErr != nil {
		if err == nil {
			err = capErr
		}
	}
	totalDeleted += n

	// 3. Clean old chunks (60 days)
	n, chunkErr := r.SweepStaleChunks(ctx, 60)
	if chunkErr != nil {
		if err == nil {
			err = chunkErr
		}
	}
	totalDeleted += n

	return totalDeleted, err
}

// CountMemories returns the number of rows in the given table, used to
// populate the script_memory_entries Prometheus gauge.
func (r *Repository) CountMemories(ctx context.Context, table string) (int64, error) {
	if !isAllowedMemoryTable(table) {
		return 0, fmt.Errorf("table %q is not a known gemma memory table", table)
	}
	var n int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func isAllowedMemoryTable(t string) bool {
	switch t {
	case "gemma_memory_entries", "gemma_script_chunks", "gemma_script_outputs":
		return true
	}
	return false
}

// ─── Level 2: Script chunks ───

// FindSimilarChunksBySearchText uses LIKE to find chunks whose search_text contains the tokens.
func (r *Repository) FindSimilarChunksBySearchText(ctx context.Context, channelID string, tokens []string, limit int) ([]ScriptChunk, error) {
	if limit <= 0 {
		limit = 10
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	// Build LIKE conditions: search_text LIKE '%token1%' AND search_text LIKE '%token2%' ...
	var conditions []string
	var args []any
	for _, tok := range tokens {
		if len(tok) < 3 {
			continue
		}
		conditions = append(conditions, "search_text LIKE ?")
		args = append(args, "%"+strings.ToLower(tok)+"%")
	}
	if len(conditions) == 0 {
		return nil, nil
	}
	args = append(args, limit)
	query := fmt.Sprintf(
		`SELECT id, generation_id, channel_id, chunk_index, chunk_type, topic_key, title,
		        text, search_text, embedding_json, created_at
		 FROM gemma_script_chunks
		 WHERE channel_id = ? AND %s
		 ORDER BY created_at DESC
		 LIMIT ?`,
		strings.Join(conditions, " AND "),
	)
	// Prepend channelID
	fullArgs := append([]any{channelID}, args...)
	rows, err := r.db.QueryContext(ctx, query, fullArgs...)
	if err != nil {
		return nil, err
	}
	return r.scanChunks(rows, err)
}

// SaveChunks stores script chunks for a generation.
func (r *Repository) SaveChunks(ctx context.Context, generationID, channelID, title, topicKey string, chunks []string) error {
	for i, chunk := range chunks {
		id := "chk_" + uuid.New().String()[:12]
		searchText := NormalizeSearchText(chunk)
		_, err := r.db.ExecContext(ctx,
			`INSERT INTO gemma_script_chunks
			 (id, generation_id, channel_id, chunk_index, chunk_type, topic_key, title, text, search_text)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, generationID, channelID, i, "paragraph", topicKey, title, chunk, searchText,
		)
		if err != nil {
			return fmt.Errorf("save chunk %d: %w", i, err)
		}
	}
	return nil
}

// ─── Helpers ───

func (r *Repository) scanMemories(rows *sql.Rows, err error) ([]MemoryEntry, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		if err := rows.Scan(&e.ID, &e.ChannelID, &e.MemoryType, &e.TopicKey, &e.Title,
			&e.Summary, &e.ContentText, &e.ContentJSON, &e.SourceGenerationID,
			&e.SourceJobID, &e.UsefulnessScore, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (r *Repository) scanChunks(rows *sql.Rows, err error) ([]ScriptChunk, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var chunks []ScriptChunk
	for rows.Next() {
		var c ScriptChunk
		if err := rows.Scan(&c.ID, &c.GenerationID, &c.ChannelID, &c.ChunkIndex,
			&c.ChunkType, &c.TopicKey, &c.Title, &c.Text, &c.SearchText,
			&c.EmbeddingJSON, &c.CreatedAt); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// CountExactOutputsByTitle returns the number of exact cache entries from the
// last 24 hours whose title contains the given pattern (LIKE %title%) for the
// specified channel and mode. The 24-hour window prevents permanent cache
// bypass: once CacheHitLimit is exceeded and a fresh generation is saved,
// old entries age out and the cache can become useful again for the topic.
func (r *Repository) CountExactOutputsByTitle(ctx context.Context, channelID, mode, title string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM gemma_script_outputs
		 WHERE channel_id = ? AND mode = ? AND title LIKE ?
		 AND created_at > datetime('now', '-24 hours')`,
		channelID, mode, "%"+title+"%",
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count exact outputs by title: %w", err)
	}
	return count, nil
}

// DeleteExactOutputsByTitles removes outputs from gemma_script_outputs matching any of the specified titles.
func (r *Repository) DeleteExactOutputsByTitles(ctx context.Context, titles []string) (int64, error) {
	if len(titles) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(titles))
	args := make([]any, len(titles))
	for i, t := range titles {
		placeholders[i] = "?"
		args[i] = t
	}
	// IN (?, ?, ...) is built from a constant number of placeholders; titles are
	// still bound via ? so this is safe from injection.
	query := "DELETE FROM gemma_script_outputs WHERE title IN (" + strings.Join(placeholders, ",") + ")"
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
