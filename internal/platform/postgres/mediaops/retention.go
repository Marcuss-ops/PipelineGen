package mediaops

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	// OutboxRetentionWindow bounds completed outbox history. The ledger keeps
	// every event until it is terminal; completed rows are history, not state,
	// and grew without bound before DB-05 (2026-11-27). Dead-letter rows are
	// deliberately NOT pruned: they require a review/replay decision.
	OutboxRetentionWindow = 30 * 24 * time.Hour
	// HeartbeatRetentionWindow bounds worker liveness history. Only the last
	// heartbeat per worker is operationally useful; older rows are append-only
	// noise.
	HeartbeatRetentionWindow = 7 * 24 * time.Hour
	// RetentionBatch bounds one DELETE so a sweep never holds a long
	// transaction on the live media database.
	RetentionBatch = 2000
	// RetentionSweepInterval is how often the windows run while the outbox
	// worker is up. The first sweep runs at startup.
	RetentionSweepInterval = 6 * time.Hour
)

// RetentionMetrics is the optional observability port for DB-05. A nil port
// disables emission.
type RetentionMetrics interface {
	// ObserveRetention projects one table sweep: rows deleted, rows still older
	// than the cutoff after the sweep (the true retention lag), and the wall
	// time the sweep took.
	ObserveRetention(table string, deleted, backlog int64, durationSeconds float64)
}

// Ports carries the optional metrics and logging hooks of one sweep. Both are
// best-effort: retention correctness never depends on them.
type Ports struct {
	Metrics RetentionMetrics
	Logf    func(msg string, err error)
	Infof   func(msg string, value int)
}

// SweepResult reports what one retention pass did, including the backlog left
// after pruning (the real lag: dead-letter history awaiting review).
type SweepResult struct {
	OutboxDeleted, OutboxBacklog         int64
	HeartbeatsDeleted, HeartbeatsBacklog int64
	OutboxDuration, HeartbeatDuration    time.Duration
}

// PruneCompletedOutboxBefore deletes terminal-success outbox events created
// before cutoff, in batches. Dead-letter events are never touched.
func PruneCompletedOutboxBefore(ctx context.Context, db *sql.DB, cutoff time.Time, batch int) (int64, error) {
	if batch <= 0 {
		batch = RetentionBatch
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res, err := db.ExecContext(ctx, `
			DELETE FROM outbox_events
			WHERE id IN (
				SELECT id FROM outbox_events
				WHERE status = 'completed' AND created_at_ts < $1
				ORDER BY created_at_ts
				LIMIT $2
			)`, cutoff, batch)
		if err != nil {
			return total, fmt.Errorf("media retention: prune outbox: %w", err)
		}
		deleted, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("media retention: outbox rows affected: %w", err)
		}
		total += deleted
		if deleted < int64(batch) {
			return total, nil
		}
	}
}

// PruneWorkerHeartbeatsBefore deletes heartbeat rows older than cutoff. The
// active worker keeps writing, so the cutoff—not the worker id—decides what is
// stale.
func PruneWorkerHeartbeatsBefore(ctx context.Context, db *sql.DB, cutoff time.Time, batch int) (int64, error) {
	if batch <= 0 {
		batch = RetentionBatch
	}
	// worker_heartbeats is a legacy media-plane table: the current writers use
	// the RenderingGen queue's table (which prunes itself). The live media DB
	// still carries the old table, while a freshly migrated one does not, so
	// the sweep is a no-op when the relation is absent.
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.worker_heartbeats') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("media retention: inspect worker_heartbeats: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		res, err := db.ExecContext(ctx, `
			DELETE FROM worker_heartbeats
			WHERE id IN (
				SELECT id FROM worker_heartbeats
				WHERE heartbeat_at < $1
				ORDER BY heartbeat_at
				LIMIT $2
			)`, cutoff, batch)
		if err != nil {
			return total, fmt.Errorf("media retention: prune heartbeats: %w", err)
		}
		deleted, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("media retention: heartbeat rows affected: %w", err)
		}
		total += deleted
		if deleted < int64(batch) {
			return total, nil
		}
	}
}

// CountOutboxBefore reports rows older than cutoff, regardless of status. A
// non-zero value after a sweep is either a dead-letter backlog awaiting review
// or a sweep that could not keep up.
func CountOutboxBefore(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	var n int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE created_at_ts < $1`, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("media retention: count outbox backlog: %w", err)
	}
	return n, nil
}

// CountWorkerHeartbeatsBefore reports heartbeat rows older than cutoff. It is
// zero after a successful sweep and only non-zero when the table is absent
// (the legacy media plane) or the sweep failed.
func CountWorkerHeartbeatsBefore(ctx context.Context, db *sql.DB, cutoff time.Time) (int64, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.worker_heartbeats') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("media retention: inspect worker_heartbeats: %w", err)
	}
	if !exists {
		return 0, nil
	}
	var n int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM worker_heartbeats WHERE heartbeat_at < $1`, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("media retention: count heartbeat backlog: %w", err)
	}
	return n, nil
}

// Sweep runs both retention windows once, in order, and reports what it did.
// A failure on one window does not stop the other: the heartbeat sweep always
// runs, and counts are read after the delete so the backlog is the real lag.
func Sweep(ctx context.Context, db *sql.DB, ports Ports) SweepResult {
	var out SweepResult
	if db == nil {
		return out
	}
	now := time.Now().UTC()

	outboxCutoff := now.Add(-OutboxRetentionWindow)
	start := time.Now()
	deleted, err := PruneCompletedOutboxBefore(ctx, db, outboxCutoff, RetentionBatch)
	out.OutboxDuration = time.Since(start)
	if err != nil {
		logf(ports, "media retention: prune completed outbox failed", err)
	} else {
		out.OutboxDeleted = deleted
		backlog, countErr := CountOutboxBefore(ctx, db, outboxCutoff)
		if countErr != nil {
			logf(ports, "media retention: count outbox backlog failed", countErr)
		}
		out.OutboxBacklog = backlog
		if ports.Metrics != nil {
			ports.Metrics.ObserveRetention("outbox_events", deleted, backlog, out.OutboxDuration.Seconds())
		}
		if deleted > 0 || backlog > 0 {
			infof(ports, fmt.Sprintf("media retention: outbox sweep deleted=%d backlog=%d duration_ms=%d", deleted, backlog, out.OutboxDuration.Milliseconds()), int(deleted))
		}
	}

	heartbeatCutoff := now.Add(-HeartbeatRetentionWindow)
	start = time.Now()
	heartbeats, err := PruneWorkerHeartbeatsBefore(ctx, db, heartbeatCutoff, RetentionBatch)
	out.HeartbeatDuration = time.Since(start)
	if err != nil {
		logf(ports, "media retention: prune heartbeats failed", err)
		return out
	}
	out.HeartbeatsDeleted = heartbeats
	backlog, countErr := CountWorkerHeartbeatsBefore(ctx, db, heartbeatCutoff)
	if countErr != nil {
		logf(ports, "media retention: count heartbeat backlog failed", countErr)
	}
	out.HeartbeatsBacklog = backlog
	if ports.Metrics != nil {
		ports.Metrics.ObserveRetention("worker_heartbeats", heartbeats, backlog, out.HeartbeatDuration.Seconds())
	}
	if heartbeats > 0 || backlog > 0 {
		infof(ports, fmt.Sprintf("media retention: heartbeat sweep deleted=%d backlog=%d duration_ms=%d", heartbeats, backlog, out.HeartbeatDuration.Milliseconds()), int(heartbeats))
	}
	return out
}

func logf(ports Ports, msg string, err error) {
	if ports.Logf != nil {
		ports.Logf(msg, err)
	}
}

func infof(ports Ports, msg string, value int) {
	if ports.Infof != nil {
		ports.Infof(msg, value)
	}
}
