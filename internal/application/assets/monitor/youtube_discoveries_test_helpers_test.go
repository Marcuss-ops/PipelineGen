// Package monitor — youtube_discoveries_test_helpers_test.go: shared
// test infrastructure for the 4 FASE-wave youtube-discoveries test files.
//
// Step 8 split (July 2026): the original 1555-LOC youtube_discoveries_test.go
// was decomposed into 4 leaf files by FASE wave boundary + this helpers
// file. This file is the SOLE canonical surface for the
// (migrationsSQLite114, newInMemoryLedger) pair that ALL four leaf
// files consume; per godlike/06 SSOT we keep the schema string + ledger
// factory in ONE place so the alloy of (schema, helper) stays byte-stable
// across the split.
//
//   - youtube_discoveries_contract_test.go         (FASE pre-Commit-3/6 — 4 dedupe contract tests)
//   - youtube_discoveries_retry_test.go             (Commit 3/6 P1 #5/#6/#7 — 6 retry-semantics tests)
//   - youtube_discoveries_concurrent_test.go        (FASE 1.1 atomic concurrent — 5 tests)
//   - youtube_discoveries_typed_transitions_test.go (FASE 1.3 typed-tests + FASE 3.7 sentinel-alias — 11 tests)
//
// Total: 4 + 6 + 5 + 11 = 26 tests (verified count preserved across the split).
//
// File header documentation covering the canonical "leader-election by
// INSERT" dedupe contract pins UNIQUE(channel_id, video_id,
// policy_version) and the TryReserve retry-eligibility rules lives in
// the per-section headers of each leaf file; this file documents the
// helpers only.
package monitor

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3" // stdlib-only driver lock per AGENTS.md

	// ARCH-ALLOWLIST: monitor-infra-import — owner=@monitor-team; deadline=2026-09-15; PR-CHECK-5-FOLLOWUP (2026-08-08); transitional hermetic-test seam (sqlassets.NewInMemoryRepo); forward-pointer PR-MONITOR-TEST-COMPOSITION
	sqlassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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
//
// godlike/06 SSOT (canonical hermetic-test seam): this is the ONLY
// path through which the 4 leaf files acquire a YoutubeDiscoveriesRepository
// handle. Splitting the helper into a per-file duplicate would re-introduce
// the drift risk the original single-file layout avoided.
func newInMemoryLedger(t *testing.T) (*sqlassets.YoutubeDiscoveriesRepository, *sql.DB, func()) {
	t.Helper()
	db, openErr := sql.Open("sqlite3", ":memory:")
	if openErr != nil {
		t.Fatalf("newInMemoryLedger: sqlite3.Open: %v", openErr)
	}
	if _, execErr := db.Exec(migrationsSQLite114); execErr != nil {
		t.Fatalf("newInMemoryLedger: apply migration 114 (create table + index): %v", execErr)
	}
	repo := sqlassets.NewYoutubeDiscoveriesRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, db, cleanup
}
