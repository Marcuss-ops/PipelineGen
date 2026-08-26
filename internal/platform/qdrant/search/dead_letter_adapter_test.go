//go:build cgo

// QDRANT-003 (June 2026) closure — "Dead-letter reale" sub-task.
//
// This test requires CGO because it opens a real mattn/go-sqlite3
// connection (the in-memory :memory: round-trip). The build tag
// excludes the file from CGO_ENABLED=0 builds, where mattn/go-sqlite3
// is a no-op stub and sql.Open fails. Non-cgo local builds (Windows
// without a C toolchain, scratch CI runners) will skip these tests
// without compilation failure; CI runners with gcc (Linux/Mac) will
// compile and run them. The production code in dead_letter_adapter.go
// is NOT gated — it compiles under both CGO_ENABLED=0 and =1 because
// the mattn/sqlite3 import is transitive through outboxevents and only
// the driver registration is affected, not the type surface.
//
// The verifier integration path (schema.DeadLetterChecker wired from
// verification.ReindexVerifier) is covered separately by verifier_test.go with a
// stub adapter and is NOT cgo-gated — the cgo gate here is purely
// about the SQL filter round-trip.

package search

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// TestOutboxEventsDeadLetterAdapter_CountOpen spins up an in-memory SQLite
// with the minimum outbox_events schema needed by CountByEventTypeAndStatus,
// inserts events in various statuses, and asserts the adapter surfaces ONLY
// the dead_letter count for asset.index.requested.
//
// QDRANT-003 (June 2026) closure — "Dead-letter reale" sub-task: this
// test is the load-bearing round-trip behind the verification.ReindexVerifier
// integration in cmd/admin/reindex_qdrant.go. Without it the admin
// reindex-gate could silently pass verification with a falsely-zero
// report.DeadLetterOpen if a SQL typo (e.g. "deadletters") ever creeps in.
func TestOutboxEventsDeadLetterAdapter_CountOpen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := openDLTestDB(t)
	defer db.Close()

	seedDLRow(t, db, 1, outboxevents.EventAssetIndexRequested, "dead_letter")
	seedDLRow(t, db, 2, outboxevents.EventAssetIndexRequested, "dead_letter")
	seedDLRow(t, db, 3, outboxevents.EventAssetIndexRequested, "pending")
	seedDLRow(t, db, 4, outboxevents.EventAssetIndexRequested, "processing")
	seedDLRow(t, db, 5, outboxevents.EventAssetIndexRequested, "completed")
	seedDLRow(t, db, 6, outboxevents.EventAssetIndexRequested, "superseded")
	seedDLRow(t, db, 7, outboxevents.EventAssetDriveDeleteRequested, "dead_letter")

	repo := outboxevents.NewRepository(db)
	adapter := NewOutboxEventsDeadLetterAdapter(repo)

	got, err := adapter.CountOpen(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, got, "CountOpen must return ONLY asset.index.requested dead_letter rows")
}

// TestOutboxEventsDeadLetterAdapter_EmptyDB asserts the adapter returns 0
// (not an error) when the outbox_events table has zero rows for
// asset.index.requested in dead_letter. This is the canonical
// "first deployment" scenario where the Ready gate sees a fresh table.
func TestOutboxEventsDeadLetterAdapter_EmptyDB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := openDLTestDB(t)
	defer db.Close()

	repo := outboxevents.NewRepository(db)
	adapter := NewOutboxEventsDeadLetterAdapter(repo)

	got, err := adapter.CountOpen(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

// TestOutboxEventsDeadLetterAdapter_IgnoresDriveDeleteDeadLetters ensures
// unrelated deletion-lifecycle dead letters do not block the Qdrant reindex
// readiness gate.
func TestOutboxEventsDeadLetterAdapter_IgnoresDriveDeleteDeadLetters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := openDLTestDB(t)
	defer db.Close()

	seedDLRow(t, db, 1, outboxevents.EventAssetDriveDeleteRequested, "dead_letter")
	seedDLRow(t, db, 2, outboxevents.EventAssetDriveDeleteRequested, "dead_letter")

	repo := outboxevents.NewRepository(db)
	adapter := NewOutboxEventsDeadLetterAdapter(repo)

	got, err := adapter.CountOpen(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

// TestOutboxEventsDeadLetterAdapter_NilRepoPanic asserts the constructor
// refuses to silently build an unconfigured adapter — a partial wire-up
// cannot survive past NewOutboxEventsDeadLetterAdapter.
func TestOutboxEventsDeadLetterAdapter_NilRepoPanic(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		assert.NotNil(t, r, "nil repo must panic")
		if msg, ok := r.(string); ok {
			assert.Contains(t, msg, "repo is required")
		}
	}()
	_ = NewOutboxEventsDeadLetterAdapter(nil)
}

// ── helpers ─────────────────────────────────────────────────────────

// openDLTestDB opens an in-memory SQLite with the minimum outbox_events
// schema needed by CountByStatus. Mirrors the canonical migration 092
// (create_outbox_events.sql) — only the columns actually queried by
// CountByStatus are included.
func openDLTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)

	_, err = db.Exec(`
		CREATE TABLE outbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			aggregate_type TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			event_key TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			max_attempts INTEGER NOT NULL DEFAULT 5,
			next_attempt_at TEXT,
			last_error TEXT NOT NULL DEFAULT '',
			worker_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			lease_expiry TEXT,
			completed_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)
	`)
	require.NoError(t, err)
	return db
}

func seedDLRow(t *testing.T, db *sql.DB, id int64, eventType, status string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO outbox_events (id, event_type, aggregate_id, aggregate_type, payload_json, status, created_at, updated_at)
		VALUES (?, ?, 'agg-x', 'media_assets', '{}', ?, '2026-06-26T00:00:00Z', '2026-06-26T00:00:00Z')
	`, id, eventType, status)
	require.NoError(t, err)
}
