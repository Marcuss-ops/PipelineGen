package media_test

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/mediaops"
)

// DB-05: completed outbox history is pruned, dead-letter history is kept for
// review, and recent completed rows survive the window.
func TestPruneCompletedOutboxBefore(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	old := time.Now().Add(-31 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)

	insert := func(status string, at time.Time) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO outbox_events (event_type, status, created_at_ts) VALUES ('test.retention', $1, $2)`,
			status, at); err != nil {
			t.Fatalf("insert %s outbox event: %v", status, err)
		}
	}
	insert("completed", old)
	insert("completed", recent)
	insert("dead_letter", old)
	insert("pending", old)

	deleted, err := mediaops.PruneCompletedOutboxBefore(ctx, db, time.Now().Add(-mediaops.OutboxRetentionWindow), 2)
	if err != nil {
		t.Fatalf("prune completed outbox: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (only the old completed row)", deleted)
	}

	count := func(where string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox_events WHERE `+where).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", where, err)
		}
		return n
	}
	if got := count(`status='completed' AND created_at_ts < now() - interval '30 days'`); got != 0 {
		t.Errorf("old completed rows remaining = %d, want 0", got)
	}
	if got := count(`status='completed'`); got != 1 {
		t.Errorf("recent completed rows = %d, want 1", got)
	}
	if got := count(`status='dead_letter'`); got != 1 {
		t.Errorf("dead_letter rows = %d, want 1 (never pruned)", got)
	}
	if got := count(`status='pending'`); got != 1 {
		t.Errorf("pending rows = %d, want 1 (never pruned)", got)
	}
}

// DB-05: heartbeat history older than the window is removed; a fresh
// heartbeat survives.
func TestPruneWorkerHeartbeatsBefore(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()

	// Fresh test databases do not create the legacy media heartbeat table
	// (the queue owns the current one); create it here to exercise the sweep.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS worker_heartbeats (
		id BIGSERIAL PRIMARY KEY,
		worker_id TEXT NOT NULL,
		heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("create worker_heartbeats: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS worker_heartbeats`) })
	if _, err := db.ExecContext(ctx,
		`INSERT INTO worker_heartbeats (worker_id, heartbeat_at) VALUES ('stale', $1), ('fresh', $2)`,
		time.Now().Add(-8*24*time.Hour), time.Now()); err != nil {
		t.Fatalf("insert heartbeats: %v", err)
	}

	deleted, err := mediaops.PruneWorkerHeartbeatsBefore(ctx, db, time.Now().Add(-mediaops.HeartbeatRetentionWindow), 10)
	if err != nil {
		t.Fatalf("prune heartbeats: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	var workers []string
	rows, err := db.QueryContext(ctx, `SELECT worker_id FROM worker_heartbeats ORDER BY worker_id`)
	if err != nil {
		t.Fatalf("query heartbeats: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan heartbeat: %v", err)
		}
		workers = append(workers, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("heartbeat rows: %v", err)
	}
	if len(workers) != 1 || workers[0] != "fresh" {
		t.Fatalf("remaining heartbeats = %v, want [fresh]", workers)
	}
}
