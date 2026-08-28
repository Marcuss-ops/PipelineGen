package performance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// ConcurrencyStore persists the batch lifecycle facts (migration 237) on the
// PRIMARY database and derives the deterministic batch concurrency report
// from those facts plus the runs' canonical observability reports
// (run_observability on the OBSERVABILITY database).
type ConcurrencyStore struct {
	db  *sql.DB // primary: benchmark_batches / benchmark_batch_jobs
	obs *sql.DB // observability: run_observability (canonical RunReport JSON)
}

func NewConcurrencyStore(db, obsDB *sql.DB) (*ConcurrencyStore, error) {
	if db == nil {
		return nil, errors.New("concurrency store: nil database")
	}
	return &ConcurrencyStore{db: db, obs: obsDB}, nil
}

var _ capperformance.BatchConcurrencyStore = (*ConcurrencyStore)(nil)
var _ capperformance.BatchConcurrencyReportReader = (*ConcurrencyStore)(nil)

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

// ConcurrencyReport derives the deterministic batch concurrency report for a
// persisted batch: worker_slot_count from benchmark_batches, the batch's run
// reports (report_json with operations) from run_observability, then the
// canonical kernel derivation (end-before-start tie-breaker, per-phase
// peak/avg, slot_utilization, parallelism_factor, scaling_efficiency).
// Runs without a stored report are skipped (never fabricated); an unknown
// batch yields an all-zero report.
func (s *ConcurrencyStore) ConcurrencyReport(ctx context.Context, batchID string) (kernobs.BatchReport, error) {
	if s == nil || s.db == nil {
		return kernobs.BatchReport{}, errors.New("concurrency store: not configured")
	}
	if strings.TrimSpace(batchID) == "" {
		return kernobs.BatchReport{}, errors.New("concurrency store: batch id is required")
	}
	if s.obs == nil {
		return kernobs.BatchReport{}, errors.New("concurrency store: observability database is not wired — run reports unavailable")
	}

	var slots int
	if err := s.db.QueryRowContext(ctx, `SELECT worker_slot_count FROM benchmark_batches WHERE batch_id=?`, batchID).Scan(&slots); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kernobs.BatchReport{}, nil // unknown batch: all-zero report, no fabrication
		}
		return kernobs.BatchReport{}, fmt.Errorf("concurrency store: read batch %q: %w", batchID, err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT run_id FROM benchmark_batch_jobs WHERE batch_id=? AND run_id<>'' ORDER BY run_id`, batchID)
	if err != nil {
		return kernobs.BatchReport{}, fmt.Errorf("concurrency store: read batch jobs %q: %w", batchID, err)
	}
	defer rows.Close()
	reports := make([]kernobs.RunReport, 0, 8)
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			return kernobs.BatchReport{}, fmt.Errorf("concurrency store: scan batch job %q: %w", batchID, err)
		}
		report, err := s.loadRunReport(ctx, runID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // job without a stored observability report: skip, never fabricate
			}
			return kernobs.BatchReport{}, err
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return kernobs.BatchReport{}, fmt.Errorf("concurrency store: iterate batch jobs %q: %w", batchID, err)
	}

	return kernobs.DeriveBatchReport(reports, slots), nil
}

// loadRunReport reads one canonical RunReport from the observability database.
func (s *ConcurrencyStore) loadRunReport(ctx context.Context, runID string) (kernobs.RunReport, error) {
	var reportJSON string
	err := s.obs.QueryRowContext(ctx, `SELECT report_json FROM run_observability WHERE run_id=?`, runID).Scan(&reportJSON)
	if err != nil {
		return kernobs.RunReport{}, err
	}
	var report kernobs.RunReport
	if err := json.Unmarshal([]byte(reportJSON), &report); err != nil {
		return kernobs.RunReport{}, fmt.Errorf("concurrency store: decode run report %q: %w", runID, err)
	}
	return report, nil
}
