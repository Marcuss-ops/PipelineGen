package scripts

import (
	"context"
	"fmt"
)

func (r *ScriptRepository) SaveGenerationLog(ctx context.Context, logEntry ScriptGenerationLog) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO script_generation_logs (script_id, phase, prompt_hash, model, input_words, output_words, duration_ms, retry_count, cache_status, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, logEntry.ScriptID, logEntry.Phase, logEntry.PromptHash, logEntry.Model, logEntry.InputWords, logEntry.OutputWords, logEntry.DurationMs, logEntry.RetryCount, logEntry.CacheStatus, logEntry.Error)
	return err
}

func (r *ScriptRepository) GetGenerationLogs(ctx context.Context, scriptID int64) ([]ScriptGenerationLog, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, script_id, phase, prompt_hash, model, input_words, output_words, duration_ms, retry_count, cache_status, error, created_at
		 FROM script_generation_logs WHERE script_id = ? ORDER BY id`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query generation logs: %w", err)
	}
	defer rows.Close()

	var logs []ScriptGenerationLog
	for rows.Next() {
		var l ScriptGenerationLog
		if err := rows.Scan(&l.ID, &l.ScriptID, &l.Phase, &l.PromptHash, &l.Model, &l.InputWords, &l.OutputWords, &l.DurationMs, &l.RetryCount, &l.CacheStatus, &l.Error, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan generation log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, nil
}
