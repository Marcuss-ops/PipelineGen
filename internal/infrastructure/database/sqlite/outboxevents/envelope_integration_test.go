// envelope_integration_test.go — PR 11 (June 2026) SQL-backed
// dedup invariant test.
//
// User spec:
//   - due apply identici → una riga outbox
//   - content hash cambiato → nuova riga
//   - target schema cambiato → nuova riga
//
// These tests construct a real in-memory SQLite outbox_events table
// with the same UNIQUE(event_key) constraint the production schema
// carries, then exercise the canonical builder + Repository.Enqueue
// to prove the dedup holds end-to-end — not just that the builder
// itself is deterministic (covered by envelope_test.go).
//
// The test does NOT need a full outbox_events production schema:
// it only needs the parts that participate in dedup
// (event_type, aggregate_id, aggregate_type, payload_json, event_key,
// created_at, updated_at + status default 'pending' + a UNIQUE
// index on event_key with ON CONFLICT DO NOTHING).

package outboxevents

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// setupOutboxTable spins up a fresh in-memory SQLite database with
// an outbox_events table minimally compatible with the production
// dedup shape (UNIQUE(event_key) is the dedup vector). Returns the
// *sql.DB. Each test gets a fresh DB via ":memory:" so cross-test
// state pollution is impossible. The schema includes status (default
// 'pending') so production callers reading outbox_events rows see
// the same shape — the dedup assertion only needs the unique
// constraint, but this stays structurally aligned with the real
// migrations.
func setupOutboxTable(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Minimal outbox_events — only the dedup-relevant columns +
	// the structural columns needed by Repository.Enqueue.
	_, err = db.Exec(`
		CREATE TABLE outbox_events (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type      TEXT    NOT NULL,
			aggregate_id    TEXT    NOT NULL,
			aggregate_type  TEXT    NOT NULL,
			payload_json    TEXT    NOT NULL,
			event_key       TEXT    NOT NULL UNIQUE,
			status          TEXT    NOT NULL DEFAULT 'pending',
			created_at      TEXT    NOT NULL,
			updated_at      TEXT    NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("CREATE TABLE outbox_events: %v", err)
	}
	return db
}

// countRows returns the number of outbox_events rows with a given
// event_key. Returns 0 when no rows match. Used by the dedup tests
// to assert the SQL invariant directly.
func countRows(t *testing.T, db *sql.DB, eventKey string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE event_key = ?`, eventKey).Scan(&n)
	if err != nil {
		t.Fatalf("count rows event_key=%q: %v", eventKey, err)
	}
	return n
}

// TestEnvelope_DedupAcrossTwoAppliesOpenMock — the spec's headline
// invariant: two consecutive EnqueueReindex calls on the same
// (assetID, targetSchemaVersion, sourceVersion) tuple must produce
// exactly one outbox_events row. The first apply writes, the second
// is collapsed by ON CONFLICT (event_key) DO NOTHING.
//
// We build the envelope twice (proving the canonical builder is
// idempotent) then call Repository.Enqueue twice. The dedup is
// enforced by the SQL UNIQUE constraint on event_key + ON CONFLICT
// clause in Enqueue's INSERT statement.
func TestEnvelope_DedupAcrossTwoAppliesOpenMock(t *testing.T) {
	db := setupOutboxTable(t)
	repo := NewRepository(db)
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	// Apply 1.
	k1, p1, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	if err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p1, k1); err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	if c := countRows(t, db, k1); c != 1 {
		t.Fatalf("after apply 1: want 1 row for event_key=%q, got %d", k1, c)
	}

	// Apply 2 — same (assetID, schema, hash). Builder returns the
	// SAME event_key (proven deterministically), and Repository.Enqueue
	// must collapse it via ON CONFLICT.
	k2, p2, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}
	if k2 != k1 {
		t.Fatalf("builder must produce identical event_key across applies: k1=%q k2=%q", k1, k2)
	}
	if err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p2, k2); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	if c := countRows(t, db, k1); c != 1 {
		t.Fatalf("after apply 2: want 1 row (dedup invariant), got %d event_key=%q", c, k1)
	}
}

// TestEnvelope_NewRowOnHashChange — spec: "content hash cambiato →
// nuova riga". A change in sourceVersion produces a different
// event_key so the prior enqueued event is NOT collapsed — the new
// tuple gets its own row.
func TestEnvelope_NewRowOnHashChange(t *testing.T) {
	db := setupOutboxTable(t)
	repo := NewRepository(db)
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	// First enqueue with hash-aaaa.
	k1, p1, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	if err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p1, k1); err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}

	// Re-ingest detected new content (asset mutated, new content_hash
	// arrived via different ingest path). hash-bbbb is a new key.
	k2, p2, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-bbbb", t0)
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}
	if k1 == k2 {
		t.Fatalf("hash change must produce a different event_key")
	}
	if err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p2, k2); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}

	if c := countRows(t, db, k1); c != 1 {
		t.Fatalf("after hash change: old event_key row should remain, got count=%d", c)
	}
	if c := countRows(t, db, k2); c != 1 {
		t.Fatalf("after hash change: new event_key row should be inserted, got count=%d", c)
	}
}

// TestEnvelope_NewRowOnSchemaChange — spec: "target schema cambiato
// → nuova riga". A schema-version upgrade (e.g. media_assets_v3 →
// media_assets_v4) produces a different event_key, so the prior row
// remains and the new tuple gets its own row.
func TestEnvelope_NewRowOnSchemaChange(t *testing.T) {
	db := setupOutboxTable(t)
	repo := NewRepository(db)
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	k1, p1, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	if err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p1, k1); err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}

	// Schema upgrade — same asset + same content hash, new physical
	// target. Different event_key.
	k2, p2, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v4", "hash-aaaa", t0)
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}
	if k1 == k2 {
		t.Fatalf("schema change must produce a different event_key")
	}
	if err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p2, k2); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}

	if c := countRows(t, db, k1); c != 1 {
		t.Fatalf("old schema's row should remain: count=%d", c)
	}
	if c := countRows(t, db, k2); c != 1 {
		t.Fatalf("new schema's row should be inserted: count=%d", c)
	}
}

// TestEnvelope_DedupPreservesPayloadCollisions — the builder's
// per-call UUID (audit token) MUST differ across calls even when the
// event_key is identical, so log-searchability survives dedup. This
// pins the "split" between payload event_id (audit) and DB event_key
// (dedup).
func TestEnvelope_DedupPreservesPayloadCollisions(t *testing.T) {
	// Already covered by envelope_test.go::TestBuildReindexEnvelopeV1_EventIDUniqueAcrossCalls
	// — fold into the integration narrative here for spec-traceability.
	// (No SQL needed for this purely-payload property.)
	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)
	_, p1, _ := BuildReindexEnvelopeV1("a", "media_assets_v3", "h", t0)
	_, p2, _ := BuildReindexEnvelopeV1("a", "media_assets_v3", "h", t0)
	if !strings.Contains(p1, "event_id\":") || !strings.Contains(p2, "event_id\":") {
		t.Fatalf("payload must contain event_id field, got p1=%q p2=%q", p1, p2)
	}
	if p1 == p2 {
		t.Fatalf("payload bytes must vary across calls (event_id is per-call UUID)")
	}
}
