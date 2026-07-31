// youtube_discoveries_test_smoke.go — basic-shape tests for the
// youtube_discoveries ledger (Commit D + Commit 3/6 pulled out into
// per-family files, July 2026).
//
// File-header context — refer to CANONICAL.md / ARCHITECTURE.md for
// the canonical sources; this file is one of 4 sibling _test.go files
// in package monitor (smoke + scoring + indexing + recovery), each
// ≤ 1000 LoC per architecture/policy.yaml::max_lines_per_file.
//
// These tests cover the canonical "leader-election by INSERT" dedupe
// contract on the youtube_discoveries ledger. In-memory SQLite,
// migration 114 applied directly via newInMemoryLedger.
//
// The helpers (newInMemoryLedger + migrationsSQLite114 const) live
// in this file ONLY; the other 3 sibling files in the same package
// reuse them via Go's same-package scope (one canonical declaration,
// multiple consumers).

package monitor

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	// ARCH-ALLOWLIST: monitor-infra-import — owner=@monitor-team; deadline=2026-09-15; PR-CHECK-5-FOLLOWUP (2026-08-08); transitional hermetic-test seam (sqlassets.NewInMemoryRepo); forward-pointer PR-MONITOR-TEST-COMPOSITION
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/youtubediscoveries"
	_ "github.com/mattn/go-sqlite3" // stdlib-only driver lock per AGENTS.md
)

// migrationsSQLite114 is the canonical CREATE TABLE + INDEX for the
// youtube_discoveries v2 ledger. Inlined here as a const so the test
// doesn't depend on a migration runner; matches the SQL emitted by
// migrations/sqlite/114_youtube_discoveries_v2.sql byte-for-byte.
//
// Note: the v2 migration starts from an empty database (the v1→v2
// upgrade is dropped on the per-test fixture; tests focus on the v2
// REPOSITORY contract, not the migration shape itself).
const migrationsSQLite114 = `
CREATE TABLE IF NOT EXISTS youtube_discoveries (
    id                TEXT PRIMARY KEY,
    channel_id        TEXT NOT NULL,
    video_id          TEXT NOT NULL,
    policy_version    TEXT NOT NULL DEFAULT 'v1',
    state             TEXT NOT NULL DEFAULT 'pending',
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
    enqueued_at       TEXT,
    next_retry_at     TEXT,
    lease_owner       TEXT,
    lease_until       TEXT,
    job_id            TEXT,
    last_error        TEXT,
    source_url        TEXT,
    title             TEXT,
    outcome           TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, video_id, policy_version)
);
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_watermark
    ON youtube_discoveries(channel_id, discovered_at DESC);
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_retry
    ON youtube_discoveries(next_retry_at)
    WHERE state = 'rejected_retryable';
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_lease
    ON youtube_discoveries(lease_until)
    WHERE state IN ('pending', 'analyzing');
`

// newInMemoryLedger spins up an isolated *sql.DB on :memory:, applies
// migration 114, and constructs the canonical repository on top. The
// returned cleanup func tears the DB down; tests should `defer cleanup()`
// to free the connection under heavy parallel load.
func newInMemoryLedger(t *testing.T) (*youtubediscoveries.YoutubeDiscoveriesRepository, *sql.DB, func()) {
	t.Helper()
	db, openErr := sql.Open("sqlite3", ":memory:")
	if openErr != nil {
		t.Fatalf("newInMemoryLedger: sqlite3.Open: %v", openErr)
	}
	if _, execErr := db.Exec(migrationsSQLite114); execErr != nil {
		t.Fatalf("newInMemoryLedger: apply migration 114 (create table + index): %v", execErr)
	}
	repo := youtubediscoveries.NewYoutubeDiscoveriesRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, db, cleanup
}

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

func TestMonitorOutbox_DrainDispatchedReclaimsExpiredLease(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE monitor_enqueue_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			discovery_id TEXT NOT NULL,
			idempotency_key TEXT NOT NULL UNIQUE,
			payload_json TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			dispatched_at TEXT,
			job_id TEXT,
			error TEXT,
			retry_count INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT,
			lease_id TEXT NOT NULL DEFAULT '',
			lease_until TEXT
		)`); err != nil {
		t.Fatalf("create monitor outbox schema: %v", err)
	}

	expiredLease := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO monitor_enqueue_outbox
			(discovery_id, idempotency_key, payload_json, state, lease_id, lease_until)
		VALUES (?, ?, ?, 'dispatching', ?, ?)`,
		"disc-reclaim", "youtube-extract:disc-reclaim:v1", `{}`, "stale-drainer", expiredLease); err != nil {
		t.Fatalf("insert expired outbox entry: %v", err)
	}

	reclaimed, err := repo.DrainDispatched(ctx, 10, "reclaimer", time.Now().UTC().Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("DrainDispatched: %v", err)
	}
	if len(reclaimed) != 1 {
		t.Fatalf("reclaimed entries = %d, want 1", len(reclaimed))
	}
	if reclaimed[0].State != "pending" {
		t.Fatalf("reclaimed state = %q, want pending", reclaimed[0].State)
	}

	var state, leaseID string
	var leaseUntil sql.NullString
	if err := db.QueryRowContext(ctx, `
		SELECT state, lease_id, lease_until FROM monitor_enqueue_outbox WHERE id = ?`, reclaimed[0].ID).
		Scan(&state, &leaseID, &leaseUntil); err != nil {
		t.Fatalf("inspect reclaimed row: %v", err)
	}
	if state != "pending" || leaseID != "" || leaseUntil.Valid {
		t.Fatalf("reclaimed row = state=%q lease_id=%q lease_until=%v, want pending with cleared lease", state, leaseID, leaseUntil)
	}

	claimed, err := repo.DrainPendingOutbox(ctx, 10, "new-drainer", time.Now().UTC().Add(time.Minute).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("DrainPendingOutbox after reclaim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != reclaimed[0].ID || claimed[0].State != "dispatching" {
		t.Fatalf("reclaimed entry was not claimable again: %+v", claimed)
	}
}
