package scripts

import (
	"context"
	"database/sql"
	"fmt"
)

func (r *ScriptRepository) GetResearchCache(ctx context.Context, key string) (string, error) {
	var sourceText string
	err := r.db.QueryRowContext(ctx, `
		SELECT source_text FROM research_cache
		WHERE key = ? AND last_used > datetime('now', '-7 days')
	`, key).Scan(&sourceText)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	_, _ = r.db.ExecContext(ctx, "UPDATE research_cache SET last_used = datetime('now') WHERE key = ?", key)
	return sourceText, nil
}

func (r *ScriptRepository) SaveResearchCache(ctx context.Context, key, topic, language string, maxSteps int, sourceText string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO research_cache (key, topic, language, max_steps, source_text, created_at, last_used)
		VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))
	`, key, topic, language, maxSteps, sourceText)
	return err
}

func (r *ScriptRepository) TouchResearchCache(ctx context.Context, key string) (int64, error) {
	result, err := r.db.ExecContext(ctx, "UPDATE research_cache SET last_used = datetime('now') WHERE key = ?", key)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *ScriptRepository) SweepStaleResearchCache(ctx context.Context, maxAgeDays int) (int64, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 30
	}
	result, err := r.db.ExecContext(ctx,
		"DELETE FROM research_cache WHERE last_used < datetime('now', ?)",
		fmt.Sprintf("-%d days", maxAgeDays),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
