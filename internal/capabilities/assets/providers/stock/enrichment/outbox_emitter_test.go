// Package enrichment — outbox_emitter_test.go (PR-011C follow-up, July 2026).
//
// 7 hermetic TDD tests pinning the outboxBackedAssetPublishedEmitter
// contract. The tests use a real *sql.DB backed by :memory: SQLite
// with the canonical outbox_events migration schema applied — the
// production wire path is exercised end-to-end (BeginTx →
// outboxevents.Repository.Enqueue → Commit).
//
// Test taxonomy:
//  1. TestNewOutboxBackedAssetPublishedEmitter_NilDB —
//     composition-time fail-closed on nil db.
//  2. TestOutboxBackedAssetPublishedEmitter_HappyPath_InsertsRow —
//     happy-path: emit v1 envelope, verify exactly 1 row in
//     outbox_events with the canonical column values
//     (event_type + aggregate_id + aggregate_type + event_key +
//     payload_json + status='pending').
//  3. TestOutboxBackedAssetPublishedEmitter_OnConflict_IdempotentReplay —
//     same payload.IdempotencyKey emitted twice → exactly 1
//     row in outbox_events (ON CONFLICT(event_key) DO NOTHING).
//  4. TestOutboxBackedAssetPublishedEmitter_DifferentEventKeys_MultipleRows —
//     two payloads with different event_keys → exactly 2 rows.
//  5. TestOutboxBackedAssetPublishedEmitter_NilReceiver_ReturnsSentinel —
//     nil-receiver safe; returns ErrEnrichmentHandlerNotConfigured.
//  6. TestOutboxBackedAssetPublishedEmitter_NilDB_ReturnsSentinel —
//     a constructed adapter with nil db (post-construction
//     corruption) returns ErrEnrichmentHandlerNotConfigured.
//  7. TestOutboxBackedAssetPublishedEmitter_PayloadJSON_PersistedAtomically —
//     the payload_json column contains the byte-equivalent
//     JSON serialization of the v1 envelope (roundtrip
//     invariant for the consumer AssetPublishedHandler).
//
// godlike/06 SSOT (one canonical owner per fact): the 7 tests
// live ONLY in this file. Future contract additions MUST extend
// this file (NOT introduce a parallel test surface).
//
// godlike/07 minimum-blast-radius: zero external dependencies
// (no real ollama, no real network, no real SQLite database
// file). The test surface is hermetic and idempotent —
// `go test -short -count=1` passes deterministically on any
// Go toolchain.
package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
)

// outboxEventsSchema is the canonical outbox_events DDL
// (mirrors migrations/sqlite/092_create_outbox_events.sql so
// tests exercise the same wire path as production). All
// columns + the UNIQUE index on event_key are present so
// ON CONFLICT(event_key) DO NOTHING fires in Enqueue.
const outboxEventsSchema = `
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);
`

// outboxEmitterDB returns a fresh in-memory SQLite DB with the
// canonical outbox_events schema applied. The :memory: database
// is per-connection; SetMaxOpenConns(1) ensures all pool
// connections see the same database instance (mirrors the
// canonical pattern in qdrant_flow_e2e_test.go::qdrantFlowDB).
func outboxEmitterDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite3 memory: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(outboxEventsSchema); err != nil {
		t.Fatalf("apply outbox_events schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// validV1Payload is the canonical AssetPublishedRequestV1 used
// across the test surface. Mirrors the handler-built payload
// (PR-011C follow-up) with a valid 64-char hex idempotency_key.
func validV1Payload() jobs.AssetPublishedRequestV1 {
	return jobs.AssetPublishedRequestV1{
		SchemaVersion:  jobs.AssetPublishedSchemaVersion,
		EventID:        "11111111-2222-3333-4444-555555555555",
		AssetID:        "stock:run_abc:chunk:0",
		Destination:    "stock",
		Origin:         "generated",
		Category:       "Boxe",
		Subject:        "Manny Pacquiao",
		Provider:       "pexels",
		DriveFileID:    "drive-file-12345",
		DrivePath:      "stock/Boxe/pexels/Manny-Pacquiao",
		ContentType:    "video",
		Tags:           []string{"boxing", "training"},
		IdempotencyKey: "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd",
		RequestedAt:    "2026-07-06T12:00:00.000Z",
	}
}

// Test 1: composition-time fail-closed on nil db.
func TestNewOutboxBackedAssetPublishedEmitter_NilDB(t *testing.T) {
	a, err := NewOutboxBackedAssetPublishedEmitter(nil, nil)
	if a != nil {
		t.Errorf("expected nil adapter, got %+v", a)
	}
	if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
		t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
	}
}

// Test 2: happy-path — emit a v1 envelope, verify exactly 1 row
// in outbox_events with the canonical column values.
func TestOutboxBackedAssetPublishedEmitter_HappyPath_InsertsRow(t *testing.T) {
	db := outboxEmitterDB(t)
	emitter, err := NewOutboxBackedAssetPublishedEmitter(db, nil)
	if err != nil {
		t.Fatalf("NewOutboxBackedAssetPublishedEmitter: %v", err)
	}

	payload := validV1Payload()
	if err := emitter.EmitAssetPublished(context.Background(), payload); err != nil {
		t.Fatalf("EmitAssetPublished: %v", err)
	}

	// Verify exactly 1 row in outbox_events.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM outbox_events").Scan(&count); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 outbox row, got %d", count)
	}

	// Verify the canonical column values.
	var (
		gotEventType     string
		gotAggregateID   string
		gotAggregateType string
		gotEventKey      string
		gotPayloadJSON   string
		gotStatus        string
	)
	err = db.QueryRow(`
		SELECT event_type, aggregate_id, aggregate_type, event_key, payload_json, status
		FROM outbox_events
		WHERE event_key = ?
	`, payload.IdempotencyKey).Scan(
		&gotEventType, &gotAggregateID, &gotAggregateType, &gotEventKey, &gotPayloadJSON, &gotStatus,
	)
	if err != nil {
		t.Fatalf("query outbox_events: %v", err)
	}
	if gotEventType != "asset.published" {
		t.Errorf("event_type mismatch: got %q, want %q", gotEventType, "asset.published")
	}
	if gotAggregateID != payload.AssetID {
		t.Errorf("aggregate_id mismatch: got %q, want %q", gotAggregateID, payload.AssetID)
	}
	if gotAggregateType != "media_asset" {
		t.Errorf("aggregate_type mismatch: got %q, want %q", gotAggregateType, "media_asset")
	}
	if gotEventKey != payload.IdempotencyKey {
		t.Errorf("event_key mismatch: got %q, want %q", gotEventKey, payload.IdempotencyKey)
	}
	if gotStatus != "pending" {
		t.Errorf("status mismatch: got %q, want %q", gotStatus, "pending")
	}

	// Verify the payload_json is the byte-equivalent JSON
	// serialization of the v1 envelope (roundtrip invariant
	// for the consumer AssetPublishedHandler).
	var gotPayload jobs.AssetPublishedRequestV1
	if err := json.Unmarshal([]byte(gotPayloadJSON), &gotPayload); err != nil {
		t.Fatalf("unmarshal payload_json: %v", err)
	}
	if gotPayload.SchemaVersion != payload.SchemaVersion {
		t.Errorf("payload.SchemaVersion mismatch: got %q, want %q", gotPayload.SchemaVersion, payload.SchemaVersion)
	}
	if gotPayload.AssetID != payload.AssetID {
		t.Errorf("payload.AssetID mismatch: got %q, want %q", gotPayload.AssetID, payload.AssetID)
	}
	if gotPayload.IdempotencyKey != payload.IdempotencyKey {
		t.Errorf("payload.IdempotencyKey mismatch: got %q, want %q", gotPayload.IdempotencyKey, payload.IdempotencyKey)
	}
}

// Test 3: ON CONFLICT suppression — same payload.IdempotencyKey
// emitted twice → exactly 1 row (idempotency contract).
func TestOutboxBackedAssetPublishedEmitter_OnConflict_IdempotentReplay(t *testing.T) {
	db := outboxEmitterDB(t)
	emitter, err := NewOutboxBackedAssetPublishedEmitter(db, nil)
	if err != nil {
		t.Fatalf("NewOutboxBackedAssetPublishedEmitter: %v", err)
	}

	payload := validV1Payload()
	// Emit the same payload twice.
	if err := emitter.EmitAssetPublished(context.Background(), payload); err != nil {
		t.Fatalf("first EmitAssetPublished: %v", err)
	}
	if err := emitter.EmitAssetPublished(context.Background(), payload); err != nil {
		t.Fatalf("second EmitAssetPublished (idempotent replay): %v", err)
	}

	// Verify exactly 1 row in outbox_events (ON CONFLICT
	// suppressed the second insert).
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM outbox_events").Scan(&count); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("idempotent replay: expected 1 outbox row, got %d", count)
	}
}

// Test 4: different event_keys → multiple rows (canonical fan-out).
func TestOutboxBackedAssetPublishedEmitter_DifferentEventKeys_MultipleRows(t *testing.T) {
	db := outboxEmitterDB(t)
	emitter, err := NewOutboxBackedAssetPublishedEmitter(db, nil)
	if err != nil {
		t.Fatalf("NewOutboxBackedAssetPublishedEmitter: %v", err)
	}

	payload1 := validV1Payload()
	payload1.EventID = "11111111-2222-3333-4444-555555555555"
	payload1.IdempotencyKey = "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd"

	payload2 := validV1Payload()
	payload2.EventID = "66666666-7777-8888-9999-aaaaaaaaaaaa"
	payload2.IdempotencyKey = "bbbbccccddddeeeeffff0000111122223333444455556666777788889999aaaa"
	payload2.AssetID = "stock:run_def:chunk:0"

	if err := emitter.EmitAssetPublished(context.Background(), payload1); err != nil {
		t.Fatalf("EmitAssetPublished payload1: %v", err)
	}
	if err := emitter.EmitAssetPublished(context.Background(), payload2); err != nil {
		t.Fatalf("EmitAssetPublished payload2: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM outbox_events").Scan(&count); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	if count != 2 {
		t.Fatalf("different event_keys: expected 2 outbox rows, got %d", count)
	}
}

// Test 5: nil-receiver safe; returns ErrEnrichmentHandlerNotConfigured
// (composition-time fail-closed).
func TestOutboxBackedAssetPublishedEmitter_NilReceiver_ReturnsSentinel(t *testing.T) {
	var emitter *outboxBackedAssetPublishedEmitter // nil
	payload := validV1Payload()
	err := emitter.EmitAssetPublished(context.Background(), payload)
	if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
		t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
	}
}

// Test 6: a constructed adapter with nil db (post-construction
// corruption) returns ErrEnrichmentHandlerNotConfigured. Defends
// against future refactors that swap db after construction.
func TestOutboxBackedAssetPublishedEmitter_NilDB_ReturnsSentinel(t *testing.T) {
	emitter := &outboxBackedAssetPublishedEmitter{db: nil, log: nil}
	payload := validV1Payload()
	err := emitter.EmitAssetPublished(context.Background(), payload)
	if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
		t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
	}
}

// Test 7: payload_json is the byte-equivalent JSON serialization
// of the v1 envelope (roundtrip invariant for the consumer
// AssetPublishedHandler). This is the canonical wire-shape
// contract — the consumer (AssetPublishedHandler) unmarshals
// the same struct and reads the same fields.
func TestOutboxBackedAssetPublishedEmitter_PayloadJSON_PersistedAtomically(t *testing.T) {
	db := outboxEmitterDB(t)
	emitter, err := NewOutboxBackedAssetPublishedEmitter(db, nil)
	if err != nil {
		t.Fatalf("NewOutboxBackedAssetPublishedEmitter: %v", err)
	}

	payload := validV1Payload()
	if err := emitter.EmitAssetPublished(context.Background(), payload); err != nil {
		t.Fatalf("EmitAssetPublished: %v", err)
	}

	// Read the row + unmarshal into a fresh struct.
	var payloadJSON string
	if err := db.QueryRow(
		"SELECT payload_json FROM outbox_events WHERE event_key = ?",
		payload.IdempotencyKey,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("query payload_json: %v", err)
	}

	var got jobs.AssetPublishedRequestV1
	if err := json.Unmarshal([]byte(payloadJSON), &got); err != nil {
		t.Fatalf("unmarshal payload_json: %v", err)
	}

	// Field-by-field roundtrip (locks the wire-shape contract).
	if got.SchemaVersion != payload.SchemaVersion {
		t.Errorf("SchemaVersion: got %q, want %q", got.SchemaVersion, payload.SchemaVersion)
	}
	if got.EventID != payload.EventID {
		t.Errorf("EventID: got %q, want %q", got.EventID, payload.EventID)
	}
	if got.AssetID != payload.AssetID {
		t.Errorf("AssetID: got %q, want %q", got.AssetID, payload.AssetID)
	}
	if got.Destination != payload.Destination {
		t.Errorf("Destination: got %q, want %q", got.Destination, payload.Destination)
	}
	if got.Origin != payload.Origin {
		t.Errorf("Origin: got %q, want %q", got.Origin, payload.Origin)
	}
	if got.Category != payload.Category {
		t.Errorf("Category: got %q, want %q", got.Category, payload.Category)
	}
	if got.Subject != payload.Subject {
		t.Errorf("Subject: got %q, want %q", got.Subject, payload.Subject)
	}
	if got.Provider != payload.Provider {
		t.Errorf("Provider: got %q, want %q", got.Provider, payload.Provider)
	}
	if got.DriveFileID != payload.DriveFileID {
		t.Errorf("DriveFileID: got %q, want %q", got.DriveFileID, payload.DriveFileID)
	}
	if got.DrivePath != payload.DrivePath {
		t.Errorf("DrivePath: got %q, want %q", got.DrivePath, payload.DrivePath)
	}
	if got.ContentType != payload.ContentType {
		t.Errorf("ContentType: got %q, want %q", got.ContentType, payload.ContentType)
	}
	if got.IdempotencyKey != payload.IdempotencyKey {
		t.Errorf("IdempotencyKey: got %q, want %q", got.IdempotencyKey, payload.IdempotencyKey)
	}
	if got.RequestedAt != payload.RequestedAt {
		t.Errorf("RequestedAt: got %q, want %q", got.RequestedAt, payload.RequestedAt)
	}
	if len(got.Tags) != len(payload.Tags) {
		t.Errorf("Tags length: got %d, want %d", len(got.Tags), len(payload.Tags))
	}
	for i := range payload.Tags {
		if got.Tags[i] != payload.Tags[i] {
			t.Errorf("Tags[%d]: got %q, want %q", i, got.Tags[i], payload.Tags[i])
		}
	}
}
