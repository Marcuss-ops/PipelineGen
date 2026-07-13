// youtube_discoveries_test_indexing.go — concurrent TryReserve-leasing
// tests for the youtube_discoveries ledger (FASE 1.1: atomic UPDATE ...
// WHERE ... RETURNING contract). Sibling of youtube_discoveries_test_smoke.go
// + youtube_discoveries_test_scoring.go + youtube_discoveries_test_recovery.go
// in package monitor.

package monitor

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // stdlib-only driver lock per AGENTS.md
	// ARCH-ALLOWLIST: monitor-infra-import — owner=@monitor-team; deadline=2026-09-15; PR-CHECK-5-FOLLOWUP (2026-08-08); transitional hermetic-test seam (sqlassets.NewInMemoryRepo); forward-pointer PR-MONITOR-TEST-COMPOSITION
)

// ── FASE 1.1: Atomic TryReserve concurrent tests ────────────────────────────
//
// Blocco 1 (July 2026) — the canonical 5-test suite that locks the atomic
// UPDATE ... WHERE ... RETURNING compare-and-swap contract in tryReserveConflict.
// The pre-fix shape (SELECT state/lease → decide in Go → UPDATE) was a TOCTOU
// race: two goroutines could both read an expired lease, both UPDATE, and both
// return won=true. The fix gates on the WHERE clause; only one row matches.
//
// These tests use db.SetMaxOpenConns(1) so goroutines share the same in-memory
// SQLite world. Each concurrent test runs 50 goroutines behind a barrier.

// TestTryReserve_FreshRow_ExactlyOneWinner verifies that 50 goroutines racing
// on a fresh (channelID, videoID) pair produce exactly 1 winner, 1 row, and
// attempt_count=1 with a non-null future lease_until. Runs 100 iterations
// to amplify race detection.
func TestTryReserve_FreshRow_ExactlyOneWinner(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	const channelID = "ch-fresh"
	const videoID = "vid-fresh"
	discoveredAt := time.Now().UTC().Format(time.RFC3339)

	for iter := 0; iter < 100; iter++ {
		// Each iteration uses a fresh policy_version so rows don't collide.
		pv := fmt.Sprintf("v1-iter-%d", iter)

		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan bool, 50)

		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, won, _, err := repo.TryReserve(ctx, channelID, videoID, pv, "https://x", "t", discoveredAt)
				if err != nil {
					t.Errorf("iter %d: TryReserve error: %v", iter, err)
				}
				results <- won
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		wins := 0
		for w := range results {
			if w {
				wins++
			}
		}
		if wins != 1 {
			t.Errorf("iter %d: fresh row race: exactly 1 goroutine should win, got %d", iter, wins)
		}

		// Verify: 1 row, attempt_count=1, lease_until non-null and future.
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM youtube_discoveries WHERE channel_id=? AND policy_version=?`, channelID, pv).Scan(&count); err != nil {
			t.Fatalf("iter %d: COUNT: %v", iter, err)
		}
		if count != 1 {
			t.Errorf("iter %d: expected 1 row, got %d", iter, count)
		}

		var leaseUntil string
		var attemptCount int
		if err := db.QueryRowContext(ctx, `SELECT lease_until, attempt_count FROM youtube_discoveries WHERE channel_id=? AND policy_version=?`, channelID, pv).Scan(&leaseUntil, &attemptCount); err != nil {
			t.Fatalf("iter %d: SELECT lease: %v", iter, err)
		}
		if leaseUntil == "" {
			t.Errorf("iter %d: lease_until should be non-null", iter)
		}
		if attemptCount != 1 {
			t.Errorf("iter %d: attempt_count should be 1, got %d", iter, attemptCount)
		}
		// lease_until must be in the future.
		leaseTime, parseErr := time.Parse(time.RFC3339, leaseUntil)
		if parseErr != nil {
			t.Errorf("iter %d: lease_until not RFC3339: %v", iter, parseErr)
		} else if !leaseTime.After(time.Now().UTC()) {
			t.Errorf("iter %d: lease_until should be future, got %v", iter, leaseTime)
		}
	}
}

// TestTryReserve_ActiveLease_NoReclaim verifies that 20 concurrent
// TryReserve calls on a row with an active (future) lease_until all lose.
// No goroutine reclaims an active lease.
func TestTryReserve_ActiveLease_NoReclaim(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	const channelID = "ch-active"
	const videoID = "vid-active"

	// Worker A wins — creates row with lease_until = now + 300s.
	_, won, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("first TryReserve should win: err=%v won=%v", err, won)
	}

	// 20 concurrent calls — all must lose (active lease).
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan bool, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, won, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", time.Now().UTC().Format(time.RFC3339))
			if err != nil {
				t.Errorf("TryReserve error: %v", err)
			}
			results <- won
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for w := range results {
		if w {
			t.Error("active lease: no goroutine should win on a row with active lease_until")
		}
	}

	// Verify: attempt_count unchanged (still 1 — the initial win).
	var attemptCount int
	if err := db.QueryRowContext(ctx, `SELECT attempt_count FROM youtube_discoveries WHERE channel_id=? AND video_id=?`, channelID, videoID).Scan(&attemptCount); err != nil {
		t.Fatalf("SELECT attempt_count: %v", err)
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count should be 1 (unchanged), got %d", attemptCount)
	}
}

// TestTryReserve_ExpiredLease_ExactlyOneReclaimer verifies that 50
// concurrent TryReserve calls on a row with an expired lease produce
// exactly 1 winner (the reclaimer) and increment attempt_count by 1.
// Uses SetNowForTests to advance time past the lease.
func TestTryReserve_ExpiredLease_ExactlyOneReclaimer(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	const channelID = "ch-expired"
	const videoID = "vid-expired"

	// Freeze clock at a known time.
	baseTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	repo.SetNowForTests(func() time.Time { return baseTime })

	// Worker A wins — lease_until = baseTime + 300s.
	_, won, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("first TryReserve should win: err=%v won=%v", err, won)
	}

	// Verify initial attempt_count = 1.
	var initialAttempt int
	db.QueryRowContext(ctx, `SELECT attempt_count FROM youtube_discoveries WHERE channel_id=? AND video_id=?`, channelID, videoID).Scan(&initialAttempt)
	if initialAttempt != 1 {
		t.Fatalf("initial attempt_count should be 1, got %d", initialAttempt)
	}

	// Advance clock 301 seconds past the lease.
	repo.SetNowForTests(func() time.Time { return baseTime.Add(301 * time.Second) })

	// 50 concurrent goroutines — exactly 1 reclaims.
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan bool, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, won, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Add(301*time.Second).Format(time.RFC3339))
			if err != nil {
				t.Errorf("TryReserve error: %v", err)
			}
			results <- won
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	wins := 0
	for w := range results {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("expired lease race: exactly 1 goroutine should reclaim, got %d", wins)
	}

	// Verify: attempt_count = previous + 1 (the reclaim bumped it).
	var finalAttempt int
	if err := db.QueryRowContext(ctx, `SELECT attempt_count FROM youtube_discoveries WHERE channel_id=? AND video_id=?`, channelID, videoID).Scan(&finalAttempt); err != nil {
		t.Fatalf("SELECT final attempt_count: %v", err)
	}
	if finalAttempt != initialAttempt+1 {
		t.Errorf("attempt_count should be %d (previous+1), got %d", initialAttempt+1, finalAttempt)
	}

	// Verify: new lease_until is non-null and future.
	var leaseUntil string
	if err := db.QueryRowContext(ctx, `SELECT lease_until FROM youtube_discoveries WHERE channel_id=? AND video_id=?`, channelID, videoID).Scan(&leaseUntil); err != nil {
		t.Fatalf("SELECT lease_until: %v", err)
	}
	if leaseUntil == "" {
		t.Error("lease_until should be non-null after reclaim")
	}

	repo.SetNowForTests(nil) // restore production clock
}

// TestTryReserve_CrashAfterReservation_Reclaimable verifies the
// crash-recovery contract:
//  1. Worker A wins TryReserve.
//  2. Worker A does NOT call MarkEnqueued (simulating crash).
//  3. Before lease expires, Worker B loses.
//  4. Clock advances past the lease.
//  5. Worker B retries and wins (reclaims the row).
func TestTryReserve_CrashAfterReservation_Reclaimable(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	const channelID = "ch-crash"
	const videoID = "vid-crash"

	// Step 1: Worker A wins.
	baseTime := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	repo.SetNowForTests(func() time.Time { return baseTime })

	idA, wonA, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Format(time.RFC3339))
	if err != nil || !wonA {
		t.Fatalf("step 1: Worker A should win: err=%v won=%v", err, wonA)
	}

	// Step 2: Worker A does NOT call MarkEnqueued (simulating crash).
	// Step 3: Before lease expires (baseTime + 100s < baseTime + 300s), Worker B loses.
	repo.SetNowForTests(func() time.Time { return baseTime.Add(100 * time.Second) })
	_, wonB, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Add(100*time.Second).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("step 3: Worker B TryReserve error: %v", err)
	}
	if wonB {
		t.Error("step 3: Worker B should NOT win while lease is active")
	}

	// Step 4: Clock advances past the lease (baseTime + 400s > baseTime + 300s).
	repo.SetNowForTests(func() time.Time { return baseTime.Add(400 * time.Second) })

	// Step 5: Worker B retries and wins.
	idB, wonB2, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Add(400*time.Second).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("step 5: Worker B retry error: %v", err)
	}
	if !wonB2 {
		t.Error("step 5: Worker B should win after lease expires")
	}
	// The row ID should match (same ledger row reclaimed).
	if idA != idB {
		t.Errorf("reclaimed row id %q != original id %q (same row should be reclaimed)", idB, idA)
	}

	// Verify: attempt_count = 2 (initial 1 + reclaim bump).
	var attemptCount int
	if err := db.QueryRowContext(ctx, `SELECT attempt_count FROM youtube_discoveries WHERE id=?`, idA).Scan(&attemptCount); err != nil {
		t.Fatalf("SELECT attempt_count: %v", err)
	}
	if attemptCount != 2 {
		t.Errorf("attempt_count should be 2 (1 initial + 1 reclaim), got %d", attemptCount)
	}

	repo.SetNowForTests(nil)
}

// TestTryReserve_RetryableState_ExactlyOneWinner verifies that 50
// concurrent TryReserve calls on a rejected_retryable row with an
// eligible next_retry_at (<= now) produce exactly 1 winner.
func TestTryReserve_RetryableState_ExactlyOneWinner(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	const channelID = "ch-retry-race"
	const videoID = "vid-retry-race"

	// Freeze clock.
	baseTime := time.Date(2026, 7, 1, 14, 0, 0, 0, time.UTC)
	repo.SetNowForTests(func() time.Time { return baseTime })

	// Step 1: Worker A wins.
	id, won, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("step 1: TryReserve should win: err=%v won=%v", err, won)
	}

	// Step 2: MarkRejected(retryable=true) → next_retry_at = baseTime + 30s.
	if err := repo.MarkRejected(ctx, id, "transient", true); err != nil {
		t.Fatalf("step 2: MarkRejected(retryable): %v", err)
	}

	// Step 3: Advance clock 61s → next_retry_at (baseTime+60s) is now eligible.
	// INSERT sets attempt_count=1, MarkRejected bumps to 2,
	// ComputeRetryBackoffSeconds(2) = 60s, so we need >60s advance.
	repo.SetNowForTests(func() time.Time { return baseTime.Add(61 * time.Second) })

	// 50 goroutines race — exactly 1 reclaims.
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan bool, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, won, _, err := repo.TryReserve(ctx, channelID, videoID, "v1", "https://x", "t", baseTime.Add(61*time.Second).Format(time.RFC3339))
			if err != nil {
				t.Errorf("TryReserve error: %v", err)
			}
			results <- won
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	wins := 0
	for w := range results {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("retryable race: exactly 1 goroutine should reclaim, got %d", wins)
	}

	// Verify: state is back to 'pending' (reclaimed).
	var gotState string
	var attemptCount int
	if err := db.QueryRowContext(ctx, `SELECT state, attempt_count FROM youtube_discoveries WHERE id=?`, id).Scan(&gotState, &attemptCount); err != nil {
		t.Fatalf("SELECT state: %v", err)
	}
	if gotState != "pending" {
		t.Errorf("reclaimed row state = %q, want pending", gotState)
	}
	// attempt_count: 1 (initial) + 1 (MarkRejected bump) + 1 (reclaim) = 3
	if attemptCount != 3 {
		t.Errorf("attempt_count = %d, want 3 (1+1+1)", attemptCount)
	}

	repo.SetNowForTests(nil)
}
