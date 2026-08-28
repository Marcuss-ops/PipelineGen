package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

type ConcurrencyStore struct{ db *sql.DB }

func NewConcurrencyStore(db *sql.DB) (*ConcurrencyStore, error) {
	if db == nil {
		return nil, errors.New("concurrency store: nil database")
	}
	return &ConcurrencyStore{db: db}, nil
}

var _ capperformance.BatchConcurrencyStore = (*ConcurrencyStore)(nil)

func (s *ConcurrencyStore) RecordBatch(ctx context.Context, b capperformance.BatchConcurrency) error {
	if s == nil || s.db == nil {
		return errors.New("concurrency store: not configured")
	}
	if strings.TrimSpace(b.BatchID) == "" || b.WorkerSlotCount < 1 || strings.TrimSpace(b.StartedAt) == "" {
		return errors.New("concurrency store: batch id, positive worker slots and start time are required")
	}
	if b.CreatedAt == "" {
		b.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO benchmark_batches (batch_id,run_id,worker_slot_count,started_at,completed_at,created_at) VALUES (?,?,?,?,?,?) ON CONFLICT(batch_id) DO UPDATE SET run_id=excluded.run_id,worker_slot_count=excluded.worker_slot_count,started_at=excluded.started_at,completed_at=excluded.completed_at`, b.BatchID, b.RunID, b.WorkerSlotCount, b.StartedAt, nullableConcurrencyTime(b.CompletedAt), b.CreatedAt)
	if err != nil {
		return fmt.Errorf("record batch %q: %w", b.BatchID, err)
	}
	return nil
}

func (s *ConcurrencyStore) RecordBatchJob(ctx context.Context, j capperformance.BatchJobInterval) error {
	if s == nil || s.db == nil {
		return errors.New("concurrency store: not configured")
	}
	if strings.TrimSpace(j.BatchID) == "" || strings.TrimSpace(j.JobID) == "" || strings.TrimSpace(j.StartedAt) == "" {
		return errors.New("concurrency store: batch id, job id and start time are required")
	}
	if j.CreatedAt == "" {
		j.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO benchmark_batch_jobs (batch_id,job_id,run_id,worker_slot,queued_at,started_at,completed_at,status,created_at) VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(batch_id,job_id) DO UPDATE SET run_id=excluded.run_id,worker_slot=excluded.worker_slot,queued_at=excluded.queued_at,started_at=excluded.started_at,completed_at=excluded.completed_at,status=excluded.status`, j.BatchID, j.JobID, j.RunID, j.WorkerSlot, nullableConcurrencyTime(j.QueuedAt), j.StartedAt, nullableConcurrencyTime(j.CompletedAt), j.Status, j.CreatedAt)
	if err != nil {
		return fmt.Errorf("record batch job %q/%q: %w", j.BatchID, j.JobID, err)
	}
	return nil
}

func nullableConcurrencyTime(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
