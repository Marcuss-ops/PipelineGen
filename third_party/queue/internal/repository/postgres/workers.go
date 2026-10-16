package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
)

// Compile-time check that the postgres repository satisfies the worker
// contract in addition to the job contract.
var _ repository.WorkerRepository = (*Repository)(nil)

// Register upserts a worker's identity and records its initial heartbeat.
func (r *Repository) Register(ctx context.Context, worker model.Worker) error {
	if worker.ID == "" {
		return fmt.Errorf("worker id is required")
	}
	status := string(worker.Status)
	if status == "" {
		status = string(model.WorkerStatusReady)
	}

	ctx, cancel := r.opContext(ctx)
	defer cancel()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO rendering_workers
		    (id, hostname, status, renderinggen_version, chronon_version,
		     overlay_schema_version, gpu_backend, gpu_device, gpu_driver,
		     started_at, last_heartbeat_at)
		VALUES
		    ($1,$2,$3,$4,$5,$6,$7,$8,$9, now(), now())
		ON CONFLICT (id) DO UPDATE SET
		    hostname = EXCLUDED.hostname,
		    status = EXCLUDED.status,
		    renderinggen_version = EXCLUDED.renderinggen_version,
		    chronon_version = EXCLUDED.chronon_version,
		    overlay_schema_version = EXCLUDED.overlay_schema_version,
		    gpu_backend = EXCLUDED.gpu_backend,
		    gpu_device = EXCLUDED.gpu_device,
		    gpu_driver = EXCLUDED.gpu_driver,
		    last_heartbeat_at = now()`,
		worker.ID, nullIfEmpty(worker.Hostname), status,
		nullIfEmpty(worker.RenderingGenVersion), nullIfEmpty(worker.ChrononVersion),
		nullIfZero(worker.OverlaySchemaVersion),
		nullIfEmpty(worker.GPUBackend), nullIfEmpty(worker.GPUDevice), nullIfEmpty(worker.GPUDriver))
	if err != nil {
		return fmt.Errorf("register worker %s: %w", worker.ID, err)
	}
	return nil
}

// Heartbeat records a heartbeat for a registered worker: it updates the
// current liveness and appends to the heartbeat ledger in one transaction.
// The ledger is pruned to a 7-day TTL to bound unbounded growth.
func (r *Repository) Heartbeat(ctx context.Context, workerID string) error {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE rendering_workers
		SET last_heartbeat_at = now()
		WHERE id = $1`, workerID)
	if err != nil {
		return err
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("worker %s is not registered", workerID)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO worker_heartbeats (worker_id) VALUES ($1)`, workerID); err != nil {
		return err
	}
	// Best-effort TTL prune: old rows are not correctness-critical, so a failure
	// must not fail the heartbeat — but it must not be invisible either, because
	// a prune that can never succeed turns worker_heartbeats into an unbounded
	// table that nothing else bounds.
	if _, pruneErr := tx.ExecContext(ctx, `DELETE FROM worker_heartbeats WHERE heartbeat_at < now() - interval '7 days'`); pruneErr != nil {
		log.Printf("WARN postgres: worker heartbeat prune failed: %v", pruneErr)
	}
	return tx.Commit()
}

// The heartbeat ledger is bounded by Heartbeat itself (a 7-day TTL prune in the
// same transaction), so there is deliberately no separate PruneWorkerHeartbeats
// entry point: a second way to bound the table would be a second source of truth
// for its retention, and the one that had no caller could not be the live one.
// TestEveryRepositoryOperationIsBounded fails if a bounded method appears here
// without a contract that declares it.

// List returns all registered workers sorted by ID.
func (r *Repository) List(ctx context.Context) ([]model.Worker, error) {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, hostname, status, renderinggen_version, chronon_version,
		       overlay_schema_version, gpu_backend, gpu_device, gpu_driver,
		       started_at, last_heartbeat_at
		FROM rendering_workers
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []model.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}

// There is deliberately no Health method here: the aggregate is DERIVED from
// List() by the single authority in internal/model (see the WorkerRepository
// contract). The FILTER-count that used to live here was a SECOND, SQL-side
// answer to "is this worker alive?" — it is gone, so a change to the heartbeat
// window can never leave the aggregate and the per-worker view disagreeing.

// scanWorker reads one worker row from a *sql.Rows (or Row) scanner.
func scanWorker(scanner interface{ Scan(...any) error }) (model.Worker, error) {
	var (
		w                model.Worker
		status           string
		hostname         sql.NullString
		rgVersion        sql.NullString
		chrononVersion   sql.NullString
		overlaySchemaVer sql.NullInt64
		gpuBackend       sql.NullString
		gpuDevice        sql.NullString
		gpuDriver        sql.NullString
		startedAt        time.Time
		lastHeartbeatAt  sql.NullTime
	)
	err := scanner.Scan(&w.ID, &hostname, &status, &rgVersion, &chrononVersion,
		&overlaySchemaVer, &gpuBackend, &gpuDevice, &gpuDriver,
		&startedAt, &lastHeartbeatAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Worker{}, err
	}
	if err != nil {
		return model.Worker{}, err
	}
	w.Status = model.WorkerStatus(status)
	w.Hostname = hostname.String
	w.RenderingGenVersion = rgVersion.String
	w.ChrononVersion = chrononVersion.String
	w.OverlaySchemaVersion = int(overlaySchemaVer.Int64)
	w.GPUBackend = gpuBackend.String
	w.GPUDevice = gpuDevice.String
	w.GPUDriver = gpuDriver.String
	w.StartedAt = startedAt
	if lastHeartbeatAt.Valid {
		w.LastHeartbeatAt = lastHeartbeatAt.Time
	}
	return w, nil
}

// nullIfZero converts a zero int to SQL NULL (overlay_schema_version is
// nullable; 0 means "not reported").
func nullIfZero(v int) any {
	if v == 0 {
		return nil
	}
	return v
}
