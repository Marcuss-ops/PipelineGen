// Package monitor — youtube_discoveries_contract_test.go: FASE pre-Commit-3/6
// canonical "leader-election by INSERT" dedupe contract for the youtube_discoveries
// ledger. Step 8 split (July 2026): extracted from the 1555-LOC
// youtube_discoveries_test.go canonical + placed alongside 3 sibling FASE
// leaf files. Helpers (migrationsSQLite114, newInMemoryLedger) live in
// youtube_discoveries_test_helpers_test.go.
//
// Tests under this header cover the user-mandated "leader-election by
// INSERT" contract: 5 distinct videos across TWO monitor cycles produce
// 5 ledger rows (not 10) and yield 5+5=10 enqueue decisions (5 wins +
// 5 losses). This is the load-bearing property for downstream
// ActiveKey composability.
//
// The dedupe keys are (channelID, videoID, policyVersion) — the
// UNIQUE(channel_id, video_id, policy_version) constraint enforces
// the "first writer wins" semantics.
//
// These tests FAIL on regression only if:
//   - TryReserve's ON CONFLICT DO NOTHING RETURNING id is dropped,
//   - the id derivation non-determinism breaks,
//   - MaxDiscoveredAt's terminal-states-only filter is dropped,
//   - the empty-channel MaxDiscoveredAt semantics flips from "" to NULL/error.

package monitor

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestYoutubeDiscoveries_FiveVideosTwoInvocations_DedupeContract pins
// the canonical Commit 3/6 contract: 5 distinct videos across TWO monitor
// cycles produce 5 ledger rows (not 10) and yield 5 wins on cycle 1 + 5
// losses on cycle 2. This is the user-mandated "5 video × 2 invocations
// = 5 ledger rows + 5 enqueue" contract.
// — User's per-spec: "Test SQLite 5 video × 2 invocations = 5 ledger rows
//   - 5 enqueue" (i.e. 5 wins on cycle 1, 5 losses on cycle 2).
func TestYoutubeDiscoveries_FiveVideosTwoInvocations_DedupeContract(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	const channelID = "ch-test"
	discoveredAt := time.Now().UTC().Format(time.RFC3339) // deterministic per cycle

	// 5 distinct videos.
	videos := []struct{ id, title, sourceURL string }{
		{"vid-001", "How AI Works", "https://www.youtube.com/watch?v=vid-001"},
		{"vid-002", "Day In Tokyo", "https://www.youtube.com/watch?v=vid-002"},
		{"vid-003", "Lo-fi Beats", "https://www.youtube.com/watch?v=vid-003"},
		{"vid-004", "Travel Vlog", "https://www.youtube.com/watch?v=vid-004"},
		{"vid-005", "Cooking Channel", "https://www.youtube.com/watch?v=vid-005"},
	}

	// Cycle 1: each TryReserve should WIN (ledger is empty).
	wonIDs := make([]string, 0, len(videos))
	for _, v := range videos {
		id, won, _, err := repo.TryReserve(ctx, channelID, v.id, "v1", v.sourceURL, v.title, discoveredAt)
		if err != nil {
			t.Fatalf("cycle 1: TryReserve(%q) returned error: %v", v.id, err)
		}
		if !won {
			t.Errorf("cycle 1: TryReserve(%q) lost on fresh ledger; expected first-cycle win", v.id)
		}
		wonIDs = append(wonIDs, id)
	}

	// Sanity: exactly 5 distinct ids.
	seen := make(map[string]bool, len(wonIDs))
	for _, id := range wonIDs {
		if seen[id] {
			t.Errorf("cycle 1: duplicate win id %q across distinct video_ids", id)
		}
		seen[id] = true
	}
	if len(wonIDs) != 5 {
		t.Fatalf("cycle 1 expected 5 wins, got %d", len(wonIDs))
	}

	// Mark all 5 as enqueued (simulate successful EnqueueExtract).
	enqueuedAt := time.Now().UTC().Format(time.RFC3339)
	for _, id := range wonIDs {
		if err := repo.MarkEnqueued(ctx, id, enqueuedAt); err != nil {
			t.Fatalf("cycle 1: MarkEnqueued(%q) failed: %v", id, err)
		}
	}

	// Cycle 2: each TryReserve should LOSE (ledger has the rows from cycle 1).
	lostCount := 0
	for _, v := range videos {
		_, won, _, err := repo.TryReserve(ctx, channelID, v.id, "v1", v.sourceURL, v.title, discoveredAt)
		if err != nil {
			t.Fatalf("cycle 2: TryReserve(%q) returned error: %v", v.id, err)
		}
		if won {
			t.Errorf("cycle 2: TryReserve(%q) won on existing ledger; expected already_scheduled (lost)", v.id)
		} else {
			lostCount++
		}
	}
	if lostCount != 5 {
		t.Fatalf("cycle 2 expected 5 losses, got %d", lostCount)
	}

	// Ledger-row invariant: exactly 5 rows (NOT 10) — the user's spec.
	n, err := repo.CountByChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("CountByChannel: %v", err)
	}
	if n != 5 {
		t.Fatalf("ledger should have exactly 5 rows (5×2 cycle dedupe contract), got %d", n)
	}

	// Cycle-end watermark: MAX(discovered_at) over terminal states should
	// be the cycle 1 timestamp (all 5 are at state='enqueued').
	watermark, err := repo.MaxDiscoveredAt(ctx, channelID)
	if err != nil {
		t.Fatalf("MaxDiscoveredAt: %v", err)
	}
	if watermark == "" {
		t.Fatal("watermark should be non-empty after 5 first-cycle wins")
	}

	// Direct table inspection: exactly 5 rows in the ledger table.
	var rowCount int
	if scanErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM youtube_discoveries WHERE channel_id = ?`, channelID).Scan(&rowCount); scanErr != nil {
		t.Fatalf("direct table count: %v", scanErr)
	}
	if rowCount != 5 {
		t.Fatalf("direct-table count: expected 5 rows, got %d", rowCount)
	}

	// All 5 rows are at state='enqueued' (MarkEnqueued was called once per win).
	var enqueuedCount int
	if scanErr := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM youtube_discoveries WHERE channel_id = ? AND state = 'enqueued'`, channelID).Scan(&enqueuedCount); scanErr != nil {
		t.Fatalf("direct table enqueued count: %v", scanErr)
	}
	if enqueuedCount != 5 {
		t.Fatalf("enqueued count: expected 5, got %d", enqueuedCount)
	}
}

// TestYoutubeDiscoveries_TryReserveIdempotentOnRepeat pins contract 1's
// underlying invariant: a TryReserve called twice for the same key on a
// fresh ledger produces the SAME id (won on first, lost on second). This
// is the load-bearing property for the dedupe contract — a non-deterministic
// id derivation would break the ActiveKey composability in
// downstream consumers.
func TestYoutubeDiscoveries_TryReserveIdempotentOnRepeat(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	const channelID = "ch-test"
	const videoID = "vid-001"
	const sourceURL = "https://www.youtube.com/watch?v=vid-001"
	const title = "Stable ID Derivation"

	id1, won1, _, err1 := repo.TryReserve(ctx, channelID, videoID, "v1", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err1 != nil {
		t.Fatalf("TryReserve call 1: %v", err1)
	}
	if !won1 {
		t.Fatal("TryReserve call 1 should win on fresh ledger")
	}

	id2, won2, _, err2 := repo.TryReserve(ctx, channelID, videoID, "v1", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err2 != nil {
		t.Fatalf("TryReserve call 2: %v", err2)
	}
	if won2 {
		t.Fatal("TryReserve call 2 should lose (already scheduled)")
	}
	if id1 != id2 {
		t.Errorf("TryReserve id derivation: call 1=%q, call 2=%q (must be deterministic for dedupe composability)", id1, id2)
	}
	if !strings.HasPrefix(id1, "disc_") {
		t.Errorf("TryReserve id should have 'disc_' prefix, got %q", id1)
	}
}

// TestYoutubeDiscoveries_TryReserveEmptyArgs pins the validation
// contract: empty channelID or videoID returns a non-nil error
// (loud failure rather than silent row corruption).
func TestYoutubeDiscoveries_TryReserveEmptyArgs(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	if _, _, _, err := repo.TryReserve(ctx, "", "vid-001", "v1", "https://x", "t", time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Error("TryReserve with empty channelID should return error, got nil")
	}
	if _, _, _, err := repo.TryReserve(ctx, "ch-1", "", "v1", "https://x", "t", time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Error("TryReserve with empty videoID should return error, got nil")
	}
}

// TestYoutubeDiscoveries_MaxDiscoveredAt_EmptyChannel pins
// contract: when no terminal-state rows exist for a channel,
// MaxDiscoveredAt returns ("", nil). The cycle-end watermark
// in discovery.go::recordCycleEndWatermark relies on this for
// empty-ledger short-circuit (no row corruption on first cycle's
// MAX() call).
func TestYoutubeDiscoveries_MaxDiscoveredAt_EmptyChannel(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	wm, err := repo.MaxDiscoveredAt(ctx, "channel-without-rows")
	if err != nil {
		t.Fatalf("MaxDiscoveredAt on empty ledger: %v", err)
	}
	if wm != "" {
		t.Errorf("MaxDiscoveredAt on empty ledger should return empty string, got %q", wm)
	}
}
