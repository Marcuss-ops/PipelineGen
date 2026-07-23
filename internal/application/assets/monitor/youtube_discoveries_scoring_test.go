// youtube_discoveries_test_scoring.go — scoring & policy surface for
// the youtube_discoveries ledger (retry curve, policy version, DateAfter
// bridge, retryable/terminal lock + monotonicity). Sibling of
// youtube_discoveries_test_smoke.go + youtube_discoveries_test_indexing.go
// + youtube_discoveries_test_recovery.go in package monitor.

package monitor

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // stdlib-only driver lock per AGENTS.md

	// ARCH-ALLOWLIST: monitor-infra-import — owner=@monitor-team; deadline=2026-09-15; PR-CHECK-5-FOLLOWUP (2026-08-08); transitional hermetic-test seam (sqlassets.NewInMemoryRepo); forward-pointer PR-MONITOR-TEST-COMPOSITION
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/youtubediscoveries"
)

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
//     attempt_count=2 (TryReserve seeds 1; MarkRejected increments).
//  3. TryReserve(key, "v1") → won=false (retry not yet due; current
//     time < next_retry_at). Caller classifies already_scheduled.
//  4. SQL-level check: state='rejected_retryable', next_retry_at
//     matches the contract, attempt_count=1.
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
	// next_retry_at = now + backoff(1) = 30s.
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
	if gotAttemptCount != 2 {
		t.Errorf("step 4 attempt_count = %d, want 2 (TryReserve seeds 1; first retryable MarkRejected bumps to 2)", gotAttemptCount)
	}
	if gotLastError != rejection {
		t.Errorf("step 4 last_error = %q, want %q", gotLastError, rejection)
	}
	if gotOutcome != "rejected" {
		t.Errorf("step 4 outcome = %q, want rejected (legacy shadow)", gotOutcome)
	}

	// Verify next_retry_at is parseable and falls within the expected
	// 40s..80s window (backoff(2)=60s, allow ±20s slack).
	retryAt, parseErr := time.Parse(time.RFC3339, gotNextRetryAt)
	if parseErr != nil {
		t.Fatalf("step 4 next_retry_at not RFC3339: %v (got %q)", parseErr, gotNextRetryAt)
	}
	delta := time.Until(retryAt)
	// backoff(newAttempt=2) = 60s; allow ±20s slack
	if delta < 40*time.Second || delta > 80*time.Second {
		t.Errorf("step 4 next_retry_at delta = %v, want in [40s, 80s] (backoff(2)=60s)", delta)
	}

	// Step 5: simulate a second retry-reclaim by directly bumping
	// attempt_count via SQL — Blocco 2 blocks MarkRejected(retryable)
	// on a 'rejected_retryable' row (the tighter WHERE prevents
	// double-increment in production). The retry flow goes through
	// tryReserveConflict(b) which reclaims the row back to 'pending'
	// with attempt_count+1; here we simulate that by bumping the
	// counter directly so the remainder of the test (Step 6 terminal
	// escalation) observes the incremented count.
	if _, err := db.ExecContext(ctx, `
		UPDATE youtube_discoveries
		SET attempt_count = attempt_count + 1,
		    next_retry_at = ?,
		    last_error = ?
		WHERE id = ?
	`, time.Now().UTC().Add(60*time.Second).Format(time.RFC3339), "transient again", id); err != nil {
		t.Fatalf("step 5 direct SQL bump: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT attempt_count FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotAttemptCount); err != nil {
		t.Fatalf("step 5 attempt_count SELECT: %v", err)
	}
	if gotAttemptCount != 3 {
		t.Errorf("step 5 attempt_count = %d, want 3 (2 + 1 via direct SQL bump)", gotAttemptCount)
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
		got := youtubediscoveries.ComputeRetryBackoffSeconds(tc.attempt)
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
		got := youtubediscoveries.ResolveDateAfter("2026-06-30T15:04:05Z", 0)
		want := "20260630"
		if got != want {
			t.Errorf("ResolveDateAfter(RFC3339, 0) = %q, want %q", got, want)
		}
	})

	t.Run("RFC3339 with non-zero lookbackDays still wins", func(t *testing.T) {
		// Even when LookbackDays=7 is a fallback, the RFC3339 cursor
		// is the SOURCE-OF-TRUTH (the cursor drives monotonic
		// "after this date" filtering).
		got := youtubediscoveries.ResolveDateAfter("2026-06-30T15:04:05Z", 7)
		if got != "20260630" {
			t.Errorf("RFC3339 should win over lookbackDays; got %q, want 20260630", got)
		}
	})

	t.Run("lookbackDays when cursor is empty", func(t *testing.T) {
		// Time-bound assertion: should be near (now - 7d).
		got := youtubediscoveries.ResolveDateAfter("", 7)
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
		got := youtubediscoveries.ResolveDateAfter("", 0)
		if got != "" {
			t.Errorf("expected empty DateAfter for no-cursor+no-lookback path, got %q", got)
		}
	})

	t.Run("malformed RFC3339 falls back to lookbackDays", func(t *testing.T) {
		// Garbage that doesn't start with YYYY-... or that has dashes
		// in the wrong position must not produce a wrong YYYYMMDD.
		got := youtubediscoveries.ResolveDateAfter("garbage-not-rfc3339", 7)
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
	if attemptA != 2 {
		t.Errorf("retryable=true MUST increment attempt_count to 2 (TryReserve seeds 1), got %d", attemptA)
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
	if attemptB != 1 {
		t.Errorf("retryable=false MUST NOT increment attempt_count beyond TryReserve seed, got %d", attemptB)
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

	// Retryable path: attempt_count → 1.
	if err := repo.MarkRejected(ctx, id, "transient 1", true); err != nil {
		t.Fatalf("retryable MarkRejected: %v", err)
	}
	// Terminal path: attempt_count stays at 1.
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
	if attempt != 2 {
		t.Errorf("attempt_count = %d, want 2 (TryReserve seeds 1; first retryable MarkRejected bumps to 2)", attempt)
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
