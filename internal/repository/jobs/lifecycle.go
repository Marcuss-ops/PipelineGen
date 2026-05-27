package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"velox/go-master/internal/media/models"
)

func (r *Repository) SetProgress(ctx context.Context, jobID string, progress int, message string) error {
	query := `UPDATE jobs SET progress = ?, updated_at = ? WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, progress, time.Now().Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to set progress: %w", err)
	}

	if message != "" {
		_ = r.AddEvent(ctx, jobID, "progress", message, map[string]interface{}{"progress": progress})
	}

	return nil
}

func (r *Repository) Complete(ctx context.Context, jobID string, result map[string]any) error {
	resultJSON, _ := json.Marshal(result)
	if resultJSON == nil {
		resultJSON = []byte("{}")
	}

	now := time.Now()
	query := `UPDATE jobs SET status = 'completed', result_json = ?, progress = 100, completed_at = ?, updated_at = ?, active_key = '' WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, string(resultJSON), now.Format(time.RFC3339), now.Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to complete job: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "completed", "Job completed successfully", nil)
	return nil
}

func (r *Repository) Fail(ctx context.Context, jobID string, errMsg string) error {
	now := time.Now()
	query := `UPDATE jobs SET status = 'failed', error = ?, completed_at = ?, updated_at = ?, active_key = '' WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, errMsg, now.Format(time.RFC3339), now.Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to fail job: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "failed", errMsg, nil)
	return nil
}

func (r *Repository) Cancel(ctx context.Context, jobID string) error {
	now := time.Now()
	query := `UPDATE jobs SET status = 'cancelled', cancelled_at = ?, updated_at = ?, active_key = '' WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, now.Format(time.RFC3339), now.Format(time.RFC3339), jobID)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	_ = r.AddEvent(ctx, jobID, "cancelled", "Job cancelled by user", nil)
	return nil
}

func (r *Repository) Retry(ctx context.Context, jobID string) (*models.Job, error) {
	now := time.Now()
	query := `UPDATE jobs
		SET status = 'queued', retry_count = retry_count + 1, error = '', progress = 0,
			worker_id = '', lease_expiry = NULL, updated_at = ?
		WHERE id = ? AND status IN ('failed', 'cancelled') AND retry_count < max_retries`

	result, err := r.db.ExecContext(ctx, query, now.Format(time.RFC3339), jobID)
	if err != nil {
		return nil, fmt.Errorf("failed to retry job: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return nil, fmt.Errorf("job not eligible for retry")
	}

	return r.Get(ctx, jobID)
}
