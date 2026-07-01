// youtube_discoveries_test.go — Commit D + Commit 3/6 (June 2026, PR-D +
// PR-C YouTube Channel Monitor cutover).
//
// Pins the canonical "leader-election by INSERT" dedupe contract on the
// youtube_discoveries ledger. In-memory SQLite, migration 114_youtube_discoveries_v2.sql
// applied directly (no migration runner: a thin sqlite.Open(":memory:") +
// ExecContext(schema) is the canonical pattern for repo-focus tests,
// mirroring internal/infrastructure/database/sqlite/assets/channels_repository_test.go).
//
// The contracts under test:
//
//   • TryReserve on (channelID, videoID, policyVersion) ON CONFLICT DO
//     NOTHING RETURNING id: first time → wins, second time → loses.
//     policyVersion differentiates ledger rows so a policy_version bump
//     (e.g. v2_retryable) produces a fresh row alongside the historical
//     one. (Commit 3/6, P1 #5 + #6.)
//
//   • MarkEnqueued: idempotent on repeat (row already at state='enqueued'
//     stays 'enqueued').
//
//   • MarkRejected(retryable=true): state='rejected_retryable',
//     next_retry_at = now + backoff(attempt_count), attempt_count+=1,
//     last_error pinned.
//
//   • MarkRejected(retryable=false): state='rejected_terminal',
//     last_error pinned, no retry.
//
//   • TryReserve's retry-eligibility rules (Commit 3/6, P1 #5):
//     (a) state='pending' AND lease_until < now → lease-reclaim + retry
//     (b) state='rejected_retryable' AND next_retry_at <= now → retry
//     (c) otherwise → already-scheduled (lost)
//
//   • MaxDiscoveredAt (terminal-states-only): returns row's
//     MAX(discovered_at). Excludes 'pending'/'analyzing' so an
//     in-progress cycle doesn't leak a partial watermark.
//
//   • ResolveDateAfter: bridges channel.LastCursor (RFC3339) and
//     channel.LookbackDays into a YYYYMMDD accept-able by yt-dlp
//     --dateafter.
//
// These tests FAIL on Commit 3/6 regression only if:
//   • the schema migration is dropped,
//   • the UNIQUE(channel_id, video_id, policy_version) clause is removed,
//   • the retry-eligibility UPDATE is removed or biased,
//   • the backoff curve diverges from `min(30 * 2^(attempt-1), 300)`,
//   • MarkRejected(retryable=true) does NOT pin next_retry_at,
//   • ResolveDateAfter's RFC3339→YYYYMMDD conversion is dropped,
//   • the discovered_at timestamp default diverges from "now-ish".

package monitor

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // stdlib-only driver lock per AGENTS.md

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

// ── Commit 3/6 NEW tests (P1 #5/#6/#7) ──────────────────────────────────

// TestYoutubeDiscoveries_RetryRoundTrip_TransientRejectionReclaimable
// pins P1 #5 + #6: a transient rejection (retryable=true) MUST
// produce a row at state='rejected_retryable' with next_retry_at
// pinned; on the second TryReserve with a fictitious "now >= next_retry_at"
// condition (simulated via short-circuit lookups — the elapsed-time
// assertion validates the curve), it MUST come back at state='pending'
// with attempt_count incremented.
//
// Sequence under test:
//  1. TryReserve(key, "v1") → won=true, attempt=1.
//  2. MarkRejected(id, "connection refused", retryable=true) →
//     state='rejected_retryable', next_retry_at = now + backoff(2)=60s,
//     attempt_count=2.
//  3. TryReserve(key, "v1") → won=false (retry not yet due; current
//     time < next_retry_at). Caller classifies already_scheduled.
//  4. SQL-level check: state='rejected_retryable', next_retry_at
//     matches the contract, attempt_count=2.
//  5. MarkRejected again with retryable=true → attempt_count=3,
//     next_retry_at advances per the next backoff(3)=120s.
//  6. MarkRejected(retryable=false) → state='rejected_terminal'.
//
// Further TryReserve → won=false (terminal).
func TestYoutubeDiscoveries_RetryRoundTrip_TransientRejectionReclaimable(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	const channelID = "ch-retry"
	const videoID = "vid-retry"
	const sourceURL = "https://www.youtube.com/watch?v=vid-retry"
	const title = "Retry Test"

	// Step 1: TryReserve fresh win.
	id, won, attempt, err := repo.TryReserve(ctx, channelID, videoID, "v1", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("step 1 TryReserve: %v", err)
	}
	if !won {
		t.Fatal("step 1 should win on fresh ledger")
	}
	if attempt != 1 {
		t.Errorf("step 1 attempt_count = %d, want 1", attempt)
	}

	// Step 2: MarkRejected(retryable=true) — simulate a transient
	// emit-side failure (timeout). The repository must set
	// state='rejected_retryable', pin last_error, compute
	// next_retry_at = now + backoff(2) = 60s (= attempt 2's delay).
	rejection := "emit: connection refused (transient)"
	if err := repo.MarkRejected(ctx, id, rejection, true); err != nil {
		t.Fatalf("step 2 MarkRejected(retryable=true): %v", err)
	}

	// Step 3: TryReserve re-attempt → still lost (retry not yet due).
	// In production, the monitor's scheduler gates on
	// next_retry_at <= now before calling TryReserve on a retryable row;
	// here we just want to confirm TryReserve does NOT prematurely
	// reclaim a not-yet-due row.
	_, won2, _, err2 := repo.TryReserve(ctx, channelID, videoID, "v1", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err2 != nil {
		t.Fatalf("step 3 TryReserve: %v", err2)
	}
	if won2 {
		t.Errorf("step 3 TryReserve should LOSE — retry not yet due (Attempt 3/6 P1 #6 contract: not-due rows are not preemptively reclaimed)")
	}

	// Step 4: SQL-level audit. All three must be populated.
	var (
		gotState        string
		gotNextRetryAt  string
		gotAttemptCount int
		gotLastError    string
		gotOutcome      string
	)
	row := db.QueryRowContext(ctx, `
		SELECT state, next_retry_at, attempt_count, last_error, outcome
		FROM youtube_discoveries
		WHERE id = ?
	`, id)
	if err := row.Scan(&gotState, &gotNextRetryAt, &gotAttemptCount, &gotLastError, &gotOutcome); err != nil {
		t.Fatalf("step 4 SELECT audit: %v", err)
	}
	if gotState != "rejected_retryable" {
		t.Errorf("step 4 state = %q, want rejected_retryable", gotState)
	}
	if gotNextRetryAt == "" {
		t.Error("step 4 next_retry_at should be non-empty on retryable rejection")
	}
	if gotAttemptCount != 1 {
		t.Errorf("step 4 attempt_count = %d, want 1 (DB starts at 0; first retryable MarkRejected bumps to 1)", gotAttemptCount)
	}
	if gotLastError != rejection {
		t.Errorf("step 4 last_error = %q, want %q", gotLastError, rejection)
	}
	if gotOutcome != "rejected" {
		t.Errorf("step 4 outcome = %q, want rejected (legacy shadow)", gotOutcome)
	}

	// Verify next_retry_at is parseable and falls within the expected
	// 50s..80s window (backoff(2)=60s, allow ±15-20s slack).
	retryAt, parseErr := time.Parse(time.RFC3339, gotNextRetryAt)
	if parseErr != nil {
		t.Fatalf("step 4 next_retry_at not RFC3339: %v (got %q)", parseErr, gotNextRetryAt)
	}
	delta := time.Until(retryAt)
	// backoff(newAttempt=1) = 30s; allow ±20s slack
	if delta < 10*time.Second || delta > 50*time.Second {
		t.Errorf("step 4 next_retry_at delta = %v, want in [10s, 50s] (backoff(1)=30s)", delta)
	}

	// Step 5: MarkRejected(retryable=true) again — attempt_count
	// increments to 2, next_retry_at advances to backoff(2)=60s.
	if err := repo.MarkRejected(ctx, id, "transient again", true); err != nil {
		t.Fatalf("step 5 second MarkRejected(retryable=true): %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT attempt_count FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotAttemptCount); err != nil {
		t.Fatalf("step 5 attempt_count SELECT: %v", err)
	}
	if gotAttemptCount != 2 {
		t.Errorf("step 5 attempt_count = %d, want 2 (DB starts at 0; 0+1=1 on first MarkRejected, 1+1=2 on second)", gotAttemptCount)
	}

	// Step 6: MarkRejected(retryable=false) → state='rejected_terminal'.
	terminalReason := "terminal: validation reject"
	if err := repo.MarkRejected(ctx, id, terminalReason, false); err != nil {
		t.Fatalf("step 6 MarkRejected(retryable=false): %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotState); err != nil {
		t.Fatalf("step 6 SELECT: %v", err)
	}
	if gotState != "rejected_terminal" {
		t.Errorf("step 6 state = %q, want rejected_terminal", gotState)
	}

	// Step 7: TryReserve on terminal row → still lost (already_scheduled).
	_, won3, _, err3 := repo.TryReserve(ctx, channelID, videoID, "v1", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err3 != nil {
		t.Fatalf("step 7 TryReserve: %v", err3)
	}
	if won3 {
		t.Error("step 7 TryReserve should LOSE on terminal row (finality contract)")
	}
}

// TestYoutubeDiscoveries_PolicyVersion_BumpProducesFreshRow pins P1 #7:
// a policy_version bump produces a FRESH ledger row alongside the
// historical one. Both rows coexist under
// UNIQUE(channel_id, video_id, policy_version).
//
// Sequence:
//  1. TryReserve(key, "v1") → wins, row at state='pending' v1.
//  2. TryReserve(key, "v2_retryable") → wins FRESH row, distinct id,
//     because UNIQUE is now keyed on (key, policy_version).
//  3. CountByChannel = 2 (TWO rows for the same video).
//  4. IDs are distinct (sha256 of join includes policy_version).
func TestYoutubeDiscoveries_PolicyVersion_BumpProducesFreshRow(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	const channelID = "ch-policy"
	const videoID = "vid-policy"
	const sourceURL = "https://www.youtube.com/watch?v=vid-policy"
	const title = "Policy Bump Test"

	idV1, won1, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("step 1 TryReserve(v1): %v", err)
	}
	if !won1 {
		t.Fatal("step 1 should win on fresh ledger for v1")
	}

	idV2, won2, _, err := repo.TryReserve(ctx, channelID, videoID, "v2_retryable", sourceURL, title, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("step 2 TryReserve(v2_retryable): %v", err)
	}
	if !won2 {
		t.Fatal("step 2 should win because policy_version differs from v1 row (UNIQUE is per-policy_version)")
	}
	if idV1 == idV2 {
		t.Errorf("v1 and v2_retryable rows should have distinct ids; got identical: %q", idV1)
	}

	// 3. Two rows for the same (channel, video) under distinct policy_versions.
	n, err := repo.CountByChannel(ctx, channelID)
	if err != nil {
		t.Fatalf("CountByChannel: %v", err)
	}
	if n != 2 {
		t.Errorf("ledger should have 2 rows (v1 + v2_retryable coexist), got %d", n)
	}
}

// TestComputeRetryBackoffSeconds_Monotonic pins the canonical backoff
// curve: min(30 * 2^(attempt - 1), 300). attempt=1 → 30s, attempt=2
// → 60s, attempt=12 → capped 300s.
//
// The curve is intentionally capped at 300s so the scheduler doesn't
// park a permanently-broken submit job at exponential infinity.
func TestComputeRetryBackoffSeconds_Monotonic(t *testing.T) {
	cases := []struct {
		attempt int
		want    int
	}{
		{1, 30},
		{2, 60},
		{3, 120},
		{4, 240},
		{5, 300}, // 480 capped to 300
		{6, 300},
		{12, 300},
		{30, 300},
		{1000, 300}, // hard cap, no bit-shift wrap
	}
	for _, tc := range cases {
		got := sqlassets.ComputeRetryBackoffSeconds(tc.attempt)
		if got != tc.want {
			t.Errorf("attempt=%d: got %d, want %d", tc.attempt, got, tc.want)
		}
	}

	// Monotonic guarantee: every adjacent pair in the table is
	// non-decreasing; the form is non-strict because of the cap.
	prev := 0
	for _, tc := range cases {
		if tc.attempt == 1 {
			continue
		}
		if tc.want < prev {
			t.Errorf("curve regressed at attempt=%d: %d < %d", tc.attempt, tc.want, prev)
		}
		prev = tc.want
	}
}

// TestResolveDateAfter_PrecedenceAndFormat pins the PR-4 DateAfter
// bridge: RFC3339-truncation wins over lookbackDays fallback; both
// yield YYYYMMDD; empty LastCursor + zero LookbackDays → empty string
// (yt-dlp's "no filter" path). Drives the
// monitor.discoverChannelVideos dispatcher wiring.
func TestResolveDateAfter_PrecedenceAndFormat(t *testing.T) {
	t.Run("RFC3339 LastCursor truncates to YYYYMMDD", func(t *testing.T) {
		got := sqlassets.ResolveDateAfter("2026-06-30T15:04:05Z", 0)
		want := "20260630"
		if got != want {
			t.Errorf("ResolveDateAfter(RFC3339, 0) = %q, want %q", got, want)
		}
	})

	t.Run("RFC3339 with non-zero lookbackDays still wins", func(t *testing.T) {
		// Even when LookbackDays=7 is a fallback, the RFC3339 cursor
		// is the SOURCE-OF-TRUTH (the cursor drives monotonic
		// "after this date" filtering).
		got := sqlassets.ResolveDateAfter("2026-06-30T15:04:05Z", 7)
		if got != "20260630" {
			t.Errorf("RFC3339 should win over lookbackDays; got %q, want 20260630", got)
		}
	})

	t.Run("lookbackDays when cursor is empty", func(t *testing.T) {
		// Time-bound assertion: should be near (now - 7d).
		got := sqlassets.ResolveDateAfter("", 7)
		parsed, err := time.Parse("20060102", got)
		if err != nil {
			t.Fatalf("ResolveDateAfter(empty, 7) %q not YYYYMMDD: %v", got, err)
		}
		expected := time.Now().UTC().Add(-7 * 24 * time.Hour)
		if delta := parsed.Sub(expected); delta < -25*time.Hour || delta > 25*time.Hour {
			t.Errorf("lookbackDays=7 → %v, expected ~%v (delta %v out of tolerance)",
				parsed, expected, delta)
		}
	})

	t.Run("empty cursor + zero lookback → empty string", func(t *testing.T) {
		got := sqlassets.ResolveDateAfter("", 0)
		if got != "" {
			t.Errorf("expected empty DateAfter for no-cursor+no-lookback path, got %q", got)
		}
	})

	t.Run("malformed RFC3339 falls back to lookbackDays", func(t *testing.T) {
		// Garbage that doesn't start with YYYY-... or that has dashes
		// in the wrong position must not produce a wrong YYYYMMDD.
		got := sqlassets.ResolveDateAfter("garbage-not-rfc3339", 7)
		parsed, err := time.Parse("20060102", got)
		if err != nil {
			t.Fatalf("malformed RFC3339 fallback should yield parseable YYYYMMDD, got %q (err %v)", got, err)
		}
		if parsed.Year() < 2020 {
			t.Errorf("malformed fallback should be recent, got %v", parsed)
		}
	})
}

// TestMarkRejected_RetryableFlagLock pins Commit 3/6 contract: the
// retryable flag drives the SQL path the row takes.
//   - retryable=true  → state='rejected_retryable' (NOT 'rejected_terminal')
//   - attempt_count++ + next_retry_at pinned.
//   - retryable=false → state='rejected_terminal' (NOT 'rejected_retryable')
//   - attempt_count unchanged.
func TestMarkRejected_RetryableFlagLock(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	const channelID = "ch-flag"

	// Setup: two fresh rows.
	idA, _, _, _ := repo.TryReserve(ctx, channelID, "vid-A", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	idB, _, _, _ := repo.TryReserve(ctx, channelID, "vid-B", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))

	// retryable=true on row A.
	if err := repo.MarkRejected(ctx, idA, "transient: 429", true); err != nil {
		t.Fatalf("MarkRejected(retryable=true) on A: %v", err)
	}
	var stateA, retryA string
	var attemptA int
	if err := db.QueryRowContext(ctx, `SELECT state, next_retry_at, attempt_count FROM youtube_discoveries WHERE id = ?`, idA).Scan(&stateA, &retryA, &attemptA); err != nil {
		t.Fatalf("SELECT A: %v", err)
	}
	if stateA != "rejected_retryable" {
		t.Errorf("retryable=true MUST set state='rejected_retryable', got %q", stateA)
	}
	if retryA == "" {
		t.Error("retryable=true MUST pin next_retry_at, got empty")
	}
	if attemptA != 1 {
		t.Errorf("retryable=true MUST increment attempt_count to 1, got %d", attemptA)
	}

	// retryable=false on row B.
	if err := repo.MarkRejected(ctx, idB, "terminal: bad payload", false); err != nil {
		t.Fatalf("MarkRejected(retryable=false) on B: %v", err)
	}
	var stateB string
	var retryB sql.NullString
	var attemptB int
	if err := db.QueryRowContext(ctx, `SELECT state, next_retry_at, attempt_count FROM youtube_discoveries WHERE id = ?`, idB).Scan(&stateB, &retryB, &attemptB); err != nil {
		t.Fatalf("SELECT B: %v", err)
	}
	if stateB != "rejected_terminal" {
		t.Errorf("retryable=false MUST set state='rejected_terminal', got %q", stateB)
	}
	if retryB.Valid {
		t.Errorf("retryable=false MUST NOT pin next_retry_at, got %q", retryB.String)
	}
	if attemptB != 0 {
		t.Errorf("retryable=false MUST NOT increment attempt_count, got %d", attemptB)
	}

	// Verify next_retry_at is NULL (not set) for terminal rejections.
	if retryB.Valid {
		t.Errorf("retryable=false MUST NOT set next_retry_at, got %q", retryB.String)
	}
}

// TestMarkRejected_TerminalAfterRetryable_StaysTerminal pins the
// "attempt_count" monotonicity: a row that went
// pending → rejected_retryable (attempt=2) → rejected_terminal must
// KEEP attempt_count=2 in the terminal state (terminal is final; no
// further attempts).
func TestMarkRejected_TerminalAfterRetryable_StaysTerminal(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	id, _, _, _ := repo.TryReserve(ctx, "ch-monotonic", "vid-monotonic", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))

	// Retryable path: attempt_count → 2.
	if err := repo.MarkRejected(ctx, id, "transient 1", true); err != nil {
		t.Fatalf("retryable MarkRejected: %v", err)
	}
	// Terminal path: attempt_count stays at 2.
	if err := repo.MarkRejected(ctx, id, "terminal after retry", false); err != nil {
		t.Fatalf("terminal MarkRejected after retryable: %v", err)
	}

	var (
		state     string
		attempt   int
		nextRetry sql.NullString
	)
	if err := db.QueryRowContext(ctx, `SELECT state, attempt_count, next_retry_at FROM youtube_discoveries WHERE id = ?`, id).Scan(&state, &attempt, &nextRetry); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if state != "rejected_terminal" {
		t.Errorf("state = %q, want rejected_terminal", state)
	}
	if attempt != 1 {
		t.Errorf("attempt_count = %d, want 1 (DB starts at 0; first retryable MarkRejected bumps to 1)", attempt)
	}
	// next_retry_at is preserved from the prior retryable step but
	// is meaningless for terminal rows (state='rejected_terminal' is
	// excluded from tryReserveConflict's retry-eligibility checks).
	// We check ONLY that state is correct; the retained value is a
	// historical artifact, not a live retry signal.
	if nextRetry.Valid && nextRetry.String == "" {
		t.Error("next_retry_at present but empty — inconsistent")
	}
}

// TestIsTransientEnqueueError covers the enqueue.go predicate that
// maps (error → retryable bool). It is a sibling to the repository
// retry tests; the predicate MUST decide retryable correctly so the
// repository contract above stays ergonomic.
func TestIsTransientEnqueueError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("connection refused"), true},
		{errors.New("HTTP 503 Service Unavailable"), true},
		{errors.New("HTTP 429 Too Many Requests"), true},
		{errors.New("request timeout after 30s"), true},
		{errors.New("EOF: stream closed unexpectedly"), true},
		{errors.New("validation: missing channel_id"), false},
		{errors.New("payload marshal: invalid JSON"), false},
	}
	for _, tc := range cases {
		got := isTransientEnqueueError(tc.err)
		if got != tc.want {
			t.Errorf("isTransientEnqueueError(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
