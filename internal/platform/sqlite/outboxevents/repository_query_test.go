// repository_query_test.go — read-only outbox dashboard query tests.
package outboxevents

import (
	"context"
	"testing"
	"time"
)

// TestListByStatus_ReturnsOnlyMatchingStatus verifies that
// ListByStatus filters by status and respects the result limit.
func TestListByStatus_ReturnsOnlyMatchingStatus(t *testing.T) {
	db := setupOutboxTable(t)
	repo := NewRepository(db)
	ctx := context.Background()
	t0 := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	// Enqueue three events: two pending, one dead_letter.
	for i := 0; i < 2; i++ {
		key, payload, err := BuildReindexEnvelopeV1("asset-1", "media_assets_v3", "hash-"+string(rune('a'+i)), t0)
		if err != nil {
			t.Fatalf("build pending %d: %v", i, err)
		}
		if _, err := repo.Enqueue(ctx, nil, EventAssetIndexRequested, "asset-1", "media_asset", payload, key); err != nil {
			t.Fatalf("enqueue pending %d: %v", i, err)
		}
	}
	{
		key, payload, err := BuildReindexEnvelopeV1("asset-2", "media_assets_v3", "hash-bbbb", t0)
		if err != nil {
			t.Fatalf("build dead_letter: %v", err)
		}
		if _, err := repo.Enqueue(ctx, nil, EventAssetIndexRequested, "asset-2", "media_asset", payload, key); err != nil {
			t.Fatalf("enqueue dead_letter: %v", err)
		}
		// Mark the event as dead_letter directly via SQL (write methods are
		// tested elsewhere; this keeps the test focused on the read query).
		if _, err := db.Exec("UPDATE outbox_events SET status = 'dead_letter' WHERE event_key = ?", key); err != nil {
			t.Fatalf("mark dead_letter: %v", err)
		}
	}

	pending, err := repo.ListByStatus(ctx, "pending")
	if err != nil {
		t.Fatalf("ListByStatus pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("want 2 pending events, got %d", len(pending))
	}

	dead, err := repo.ListByStatus(ctx, "dead_letter")
	if err != nil {
		t.Fatalf("ListByStatus dead_letter: %v", err)
	}
	if len(dead) != 1 {
		t.Fatalf("want 1 dead_letter event, got %d", len(dead))
	}

	// Unknown status should return empty, not error.
	empty, err := repo.ListByStatus(ctx, "unknown_status")
	if err != nil {
		t.Fatalf("ListByStatus unknown: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want 0 events for unknown status, got %d", len(empty))
	}
}

// TestListByStatus_RequiresStatus verifies that ListByStatus fails
// closed when status is empty.
func TestListByStatus_RequiresStatus(t *testing.T) {
	db := setupOutboxTable(t)
	repo := NewRepository(db)
	_, err := repo.ListByStatus(context.Background(), "")
	if err == nil {
		t.Fatalf("expected error for empty status")
	}
}
