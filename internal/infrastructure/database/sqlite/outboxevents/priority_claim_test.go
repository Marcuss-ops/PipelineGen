package outboxevents

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// setupPriorityOutboxTable spins up an in-memory outbox_events table that
// mirrors the production schema AFTER migration 186 (priority column +
// next_attempt_at) so ClaimNext's priority-aware candidate CTE can be
// exercised against the real column layout.
func setupPriorityOutboxTable(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE outbox_events (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type      TEXT    NOT NULL,
			aggregate_id    TEXT    NOT NULL,
			aggregate_type  TEXT    NOT NULL DEFAULT '',
			payload_json    TEXT    NOT NULL DEFAULT '',
			event_key       TEXT    NOT NULL UNIQUE,
			status          TEXT    NOT NULL DEFAULT 'pending',
			attempt_count   INTEGER NOT NULL DEFAULT 0,
			max_attempts    INTEGER NOT NULL DEFAULT 3,
			priority        INTEGER NOT NULL DEFAULT 5,
			last_error      TEXT    NOT NULL DEFAULT '',
			next_attempt_at TEXT,
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

// TestClaimNext_PriorityClaimsHighFirst locks in the migration-186
// scheduling contract: a high-priority (script-required) event enqueued
// AFTER a normal-priority (bulk-folder-sync) event is claimed FIRST.
// Without priority ordering, ClaimNext would return the older
// normal-priority row (next_attempt_at ASC, id ASC).
func TestClaimNext_PriorityClaimsHighFirst(t *testing.T) {
	db := setupPriorityOutboxTable(t)
	repo := NewRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	// 1. Normal-priority event enqueued first (older row, id smaller).
	normalKey, normalPayload, err := BuildReindexEnvelopeV1("asset-normal", "media_assets_v3", "hash-normal", t0)
	if err != nil {
		t.Fatalf("build normal envelope: %v", err)
	}
	if _, err := repo.Enqueue(ctx, nil, EventAssetIndexRequested, "asset-normal", "media_asset", normalPayload, normalKey); err != nil {
		t.Fatalf("enqueue normal: %v", err)
	}

	// 2. High-priority event enqueued second (newer row, id larger).
	highKey, highPayload, err := BuildReindexEnvelopeV1("asset-high", "media_assets_v3", "hash-high", t0)
	if err != nil {
		t.Fatalf("build high envelope: %v", err)
	}
	if _, err := repo.EnqueueWithPriority(ctx, nil, EventAssetIndexRequested, "asset-high", "media_asset", highPayload, highKey, PriorityHigh); err != nil {
		t.Fatalf("enqueue high: %v", err)
	}

	// 3. First claim MUST be the high-priority row, despite being newer.
	claim, err := repo.ClaimNext(ctx, "worker-priority", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext 1: %v", err)
	}
	if claim == nil {
		t.Fatal("ClaimNext 1 returned nil claim")
	}
	if claim.Event.AggregateID != "asset-high" {
		t.Fatalf("first claim aggregate_id=%q, want %q (high priority must jump the queue)", claim.Event.AggregateID, "asset-high")
	}
	if claim.Event.Priority != PriorityHigh {
		t.Errorf("first claim priority=%d, want %d", claim.Event.Priority, PriorityHigh)
	}
	if err := repo.MarkCompleted(ctx, claim.Event.ID, claim.LeaseID); err != nil {
		t.Fatalf("mark first completed: %v", err)
	}

	// 4. Second claim is the normal-priority row.
	claim2, err := repo.ClaimNext(ctx, "worker-priority", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext 2: %v", err)
	}
	if claim2 == nil {
		t.Fatal("ClaimNext 2 returned nil claim")
	}
	if claim2.Event.AggregateID != "asset-normal" {
		t.Fatalf("second claim aggregate_id=%q, want %q", claim2.Event.AggregateID, "asset-normal")
	}
	if claim2.Event.Priority != PriorityNormal {
		t.Errorf("second claim priority=%d, want default %d", claim2.Event.Priority, PriorityNormal)
	}
}

// TestEnqueueWithPriority_PersistsPriority verifies that EnqueueWithPriority
// stamps the column and scanEvent round-trips it back into Event.Priority.
func TestEnqueueWithPriority_PersistsPriority(t *testing.T) {
	db := setupPriorityOutboxTable(t)
	repo := NewRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	key, payload, err := BuildReindexEnvelopeV1("asset-prio", "media_assets_v3", "hash-prio", t0)
	if err != nil {
		t.Fatalf("build envelope: %v", err)
	}
	res, err := repo.EnqueueWithPriority(ctx, nil, EventAssetIndexRequested, "asset-prio", "media_asset", payload, key, PriorityHigh)
	if err != nil {
		t.Fatalf("EnqueueWithPriority: %v", err)
	}
	if !res.Inserted {
		t.Fatalf("expected fresh insert, got Inserted=false (status=%q)", res.ExistingStatus)
	}

	claim, err := repo.ClaimNext(ctx, "worker-prio", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if claim == nil {
		t.Fatal("ClaimNext returned nil")
	}
	if claim.Event.Priority != PriorityHigh {
		t.Fatalf("Event.Priority=%d, want %d", claim.Event.Priority, PriorityHigh)
	}
}
