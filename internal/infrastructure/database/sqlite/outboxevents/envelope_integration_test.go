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

	// Minimal outbox_events — includes all columns read by scanEvent
	// so read-only queries (ListPending, ListByStatus) can be tested
	// against the same in-memory schema.
	_, err = db.Exec(`
		CREATE TABLE outbox_events (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type      TEXT    NOT NULL,
			aggregate_id    TEXT    NOT NULL,
			aggregate_type  TEXT    NOT NULL,
			payload_json    TEXT    NOT NULL,
			event_key       TEXT    NOT NULL UNIQUE,
			status          TEXT    NOT NULL DEFAULT 'pending',
			attempt_count   INTEGER NOT NULL DEFAULT 0,
			max_attempts    INTEGER NOT NULL DEFAULT 3,
			last_error      TEXT    NOT NULL DEFAULT '',
			worker_id       TEXT    NOT NULL DEFAULT '',
			lease_id        TEXT    NOT NULL DEFAULT '',
			lease_expiry    TEXT,
			completed_at    TEXT,
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
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p1, k1); err != nil {
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
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p2, k2); err != nil {
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
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p1, k1); err != nil {
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
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p2, k2); err != nil {
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
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p1, k1); err != nil {
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
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-1", "media_asset", p2, k2); err != nil {
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

// ── Card 7.1 (July 2026): force=true seam ────────────────────────
//
// Admin reindex emits asset.index.requested.v1 with force=true so the
// worker bypasses the source_version supersede gate. The event_key
// must carry the literal ":force" suffix so SQLite UNIQUE(event_key)
// does NOT collapse a force reindex with a prior non-force reindex
// for the same (assetID, schemaVersion, sourceVersion) tuple. The
// payload must carry "force": true so the worker reads the flag at
// consume time. These three tests pin both surfaces.

// TestBuildReindexEnvelopeV1Force_PayloadContainsForceField — the
// admin reindex payload must carry "force": true so the worker
// (IndexingHandler.Handle) reads the flag and bypasses the
// source_version supersede gate.
func TestBuildReindexEnvelopeV1Force_PayloadContainsForceField(t *testing.T) {
	t0 := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	_, payload, err := BuildReindexEnvelopeV1Force("asset-force-1", "media_assets_v3", "hash-ff1", t0)
	if err != nil {
		t.Fatalf("BuildReindexEnvelopeV1Force: %v", err)
	}
	if !strings.Contains(payload, `"force":true`) {
		t.Fatalf("force=true payload must carry \"force\":true, got %q", payload)
	}
	// Symmetry: the non-force variant must NOT carry force=true.
	_, payloadNonForce, err := BuildReindexEnvelopeV1("asset-force-1", "media_assets_v3", "hash-ff1", t0)
	if err != nil {
		t.Fatalf("BuildReindexEnvelopeV1: %v", err)
	}
	if strings.Contains(payloadNonForce, `"force":true`) {
		t.Fatalf("non-force payload must NOT carry \"force\":true, got %q", payloadNonForce)
	}
}

// TestBuildReindexEnvelopeV1Force_EventKeyHasForceSuffix — the
// force=true event_key appends the literal ":force" suffix so the
// SQLite UNIQUE(event_key) dedup does not collapse a force reindex
// with a prior non-force reindex for the same (assetID, schema,
// source) tuple. The non-force variant must produce the same
// event_key shape WITHOUT the suffix (regression pin — without the
// suffix the dedup would silently swallow operator force).
func TestBuildReindexEnvelopeV1Force_EventKeyHasForceSuffix(t *testing.T) {
	t0 := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	forceKey, _, err := BuildReindexEnvelopeV1Force("asset-force-2", "media_assets_v3", "hash-ff2", t0)
	if err != nil {
		t.Fatalf("BuildReindexEnvelopeV1Force: %v", err)
	}
	nonForceKey, _, err := BuildReindexEnvelopeV1("asset-force-2", "media_assets_v3", "hash-ff2", t0)
	if err != nil {
		t.Fatalf("BuildReindexEnvelopeV1: %v", err)
	}
	if !strings.HasSuffix(forceKey, ":force") {
		t.Fatalf("force=true event_key must end with literal \":force\" suffix, got %q", forceKey)
	}
	if forceKey == nonForceKey {
		t.Fatalf("force=true and force=false event_keys MUST differ for the same (assetID, schema, source) tuple; got identical %q", forceKey)
	}
}

// TestEnvelope_ForceNewRowOnTopOfNonForce — the admin reindex path
// must NOT be collapsed by a prior non-force enqueue for the same
// (assetID, schemaVersion, sourceVersion) tuple. SQLite UNIQUE
// (event_key) + ON CONFLICT DO NOTHING is the dedup vector: a
// non-force reindex row already exists (event_key without :force
// suffix), a subsequent force reindex (event_key WITH :force suffix)
// must produce a SECOND row. The worker's supersede gate bypass
// then runs IndexClip unconditionally on the force row.
func TestEnvelope_ForceNewRowOnTopOfNonForce(t *testing.T) {
	db := setupOutboxTable(t)
	repo := NewRepository(db)
	t0 := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)

	// 1. Non-force enqueue (the dedup-collapsible prior row).
	nonForceKey, nonForcePayload, err := BuildReindexEnvelopeV1("asset-force-3", "media_assets_v3", "hash-ff3", t0)
	if err != nil {
		t.Fatalf("build non-force: %v", err)
	}
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-force-3", "media_asset", nonForcePayload, nonForceKey); err != nil {
		t.Fatalf("enqueue non-force: %v", err)
	}
	if c := countRows(t, db, nonForceKey); c != 1 {
		t.Fatalf("non-force enqueue: want 1 row for event_key=%q, got %d", nonForceKey, c)
	}

	// 2. Force reindex (the admin reindex case) for the SAME
	// (assetID, schema, source) tuple. The :force suffix MUST
	// produce a distinct event_key, so the second Enqueue creates
	// a fresh outbox row (the operator's force is NOT collapsed).
	forceKey, forcePayload, err := BuildReindexEnvelopeV1Force("asset-force-3", "media_assets_v3", "hash-ff3", t0)
	if err != nil {
		t.Fatalf("build force: %v", err)
	}
	if forceKey == nonForceKey {
		t.Fatalf("force=true and force=false must produce distinct event_keys (regression: dedup would silently swallow operator force)")
	}
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-force-3", "media_asset", forcePayload, forceKey); err != nil {
		t.Fatalf("enqueue force: %v", err)
	}

	// 3. The non-force row still exists (the prior reconcile-repair
	// applied state is preserved for audit) and the force row was
	// inserted as a NEW row (not collapsed).
	if c := countRows(t, db, nonForceKey); c != 1 {
		t.Fatalf("non-force row should remain after force reindex; want 1, got %d", c)
	}
	if c := countRows(t, db, forceKey); c != 1 {
		t.Fatalf("force row should be inserted as a NEW row (not collapsed by non-force dedup); want 1, got %d", c)
	}

	// 4. Re-running the same force enqueue is dedup-collapsed at
	// the SQLite UNIQUE(event_key) level (idempotent re-runs of
	// the same admin command). Both event_keys stable across
	// retries; the operator can re-run safely.
	_, forcePayloadAgain, err := BuildReindexEnvelopeV1Force("asset-force-3", "media_assets_v3", "hash-ff3", t0)
	if err != nil {
		t.Fatalf("rebuild force: %v", err)
	}
	if _, err := repo.Enqueue(context.Background(), nil, EventAssetIndexRequested, "asset-force-3", "media_asset", forcePayloadAgain, forceKey); err != nil {
		t.Fatalf("re-enqueue force: %v", err)
	}
	if c := countRows(t, db, forceKey); c != 1 {
		t.Fatalf("re-enqueue of same force event_key must dedup to 1 row; got %d", c)
	}
}
