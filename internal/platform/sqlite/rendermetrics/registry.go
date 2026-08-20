// Package rendermetrics adapts the render-attempt analytics recorder port to
// SQLite. It is the concrete implementation of
// scriptgeneration.RenderAttemptRecorder: one upsert per attempt, keyed by
// attempt_id, so re-recording the same attempt converges on one row.
package rendermetrics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// Registry is the SQLite-backed render-attempt analytics recorder.
type Registry struct {
	db *sql.DB
	// now is the recorded_at timestamp source (defaults to time.Now().UTC).
	now func() time.Time
}

// New builds a recorder over the primary SQLite database.
func New(db *sql.DB) (*Registry, error) {
	if db == nil {
		return nil, errors.New("render metrics registry: nil database")
	}
	return &Registry{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

var _ scriptgen.RenderAttemptRecorder = (*Registry)(nil)

// RecordAttempt upserts one render-attempt analytics row. It fails closed on a
// missing attempt identity; it never silently succeeds as a no-op.
func (r *Registry) RecordAttempt(ctx context.Context, attempt scriptgen.RenderAttemptAnalytics) error {
	if r == nil || r.db == nil {
		return errors.New("render metrics registry: not configured")
	}
	if attempt.AttemptID == "" {
		return errors.New("render metrics registry: attempt_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO render_attempt_analytics (
			attempt_id, job_id,
			phrase_count, word_count, image_count, leak_count,
			render_ms, encode_ms,
			completion_wait_ms, polling_sleep_ms, polling_interval_ms, poll_count,
			width, height, fps_num, fps_den, frame_count, duration_us, size_bytes,
			sha256, drive_file_id, drive_link, recorded_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(attempt_id) DO UPDATE SET
			job_id = excluded.job_id,
			phrase_count = excluded.phrase_count,
			word_count = excluded.word_count,
			image_count = excluded.image_count,
			leak_count = excluded.leak_count,
			render_ms = excluded.render_ms,
			encode_ms = excluded.encode_ms,
			completion_wait_ms = excluded.completion_wait_ms,
			polling_sleep_ms = excluded.polling_sleep_ms,
			polling_interval_ms = excluded.polling_interval_ms,
			poll_count = excluded.poll_count,
			width = excluded.width,
			height = excluded.height,
			fps_num = excluded.fps_num,
			fps_den = excluded.fps_den,
			frame_count = excluded.frame_count,
			duration_us = excluded.duration_us,
			size_bytes = excluded.size_bytes,
			sha256 = excluded.sha256,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			recorded_at = excluded.recorded_at`,
		attempt.AttemptID,
		attempt.JobID,
		attempt.Content.Phrases,
		attempt.Content.Words,
		attempt.Content.Images,
		attempt.Content.Leaks,
		attempt.RenderMS,
		attempt.EncodeMS,
		attempt.CompletionWaitMS,
		attempt.PollingSleepMS,
		attempt.PollingIntervalMS,
		attempt.PollCount,
		attempt.Width,
		attempt.Height,
		attempt.FPSNum,
		attempt.FPSDen,
		attempt.FrameCount,
		attempt.DurationUS,
		attempt.SizeBytes,
		attempt.SHA256,
		attempt.DriveFileID,
		attempt.DriveLink,
		r.now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("record render attempt %q: %w", attempt.AttemptID, err)
	}
	return nil
}
