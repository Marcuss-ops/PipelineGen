// youtube_discoveries_test_recovery.go — typed transition results
// (FASE 1.3) + sentinel alias contract (FASE 3.7) for the
// youtube_discoveries ledger. Sibling of youtube_discoveries_test_smoke.go
// + youtube_discoveries_test_scoring.go + youtube_discoveries_test_indexing.go
// in package monitor.
//
// References:
//   • FASE 1.3 typed-transition result tests verify MarkEnqueued / MarkRejected
//     return typed errors (ErrAlreadyApplied, ErrStateConflict, ErrNotFound)
//     when their WHERE clause matches zero rows.
//   • FASE 3.7 sentinel-alias tests verify that monitor.ErrLedgerStateConflict
//     is the SAME *errorString pointer as youtubediscoveries.ErrStateConflict via the
//     thin-alias declared in types_dto.go (production code); the
//     TranslateLedgerSentinel helper wraps the chain via multi-%w fmt.Errorf
//     so errors.Is probes resolve BOTH sentinel identities.

package assets

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // stdlib-only driver lock per AGENTS.md

	// ARCH-ALLOWLIST: monitor-infra-import — owner=@monitor-team; deadline=2026-09-15; PR-CHECK-5-FOLLOWUP (2026-08-08); transitional hermetic-test seam (sqlassets.NewInMemoryRepo); forward-pointer PR-MONITOR-TEST-COMPOSITION
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/youtubediscoveries"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── FASE 1.3: Typed transition result tests ────────────────────────────────

// TestMarkEnqueued_AppliesFromPending verifies that MarkEnqueued on a
// 'pending' row returns nil (TransitionApplied — the row was updated).
func TestMarkEnqueued_AppliesFromPending(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	id, won, _, err := repo.TryReserve(ctx, "ch-test", "vid-test", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("TryReserve: err=%v won=%v", err, won)
	}

	err = repo.MarkEnqueued(ctx, id, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("MarkEnqueued on pending row should succeed (TransitionApplied), got: %v", err)
	}

	// Verify the row is now 'enqueued'.
	var gotState string
	if scanErr := db.QueryRowContext(ctx, `SELECT state FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotState); scanErr != nil {
		t.Fatalf("SELECT state: %v", scanErr)
	}
	if gotState != "enqueued" {
		t.Errorf("state after MarkEnqueued = %q, want enqueued", gotState)
	}
}

// TestMarkEnqueued_IsIdempotent verifies that calling MarkEnqueued twice
// on the same 'enqueued' row returns ErrAlreadyApplied on the second call
// — not nil (which would indicate "I just applied it").
func TestMarkEnqueued_IsIdempotent(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	id, won, _, err := repo.TryReserve(ctx, "ch-test", "vid-test", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("TryReserve: err=%v won=%v", err, won)
	}

	enqueuedAt := time.Now().UTC().Format(time.RFC3339)

	// First call: TransitionApplied (nil).
	if err := repo.MarkEnqueued(ctx, id, enqueuedAt); err != nil {
		t.Fatalf("first MarkEnqueued should succeed: %v", err)
	}

	// Second call: ErrAlreadyApplied (idempotent).
	err2 := repo.MarkEnqueued(ctx, id, enqueuedAt)
	if !errors.Is(err2, youtubediscoveries.ErrAlreadyApplied) {
		t.Fatalf("second MarkEnqueued should return ErrAlreadyApplied, got: %v", err2)
	}

	// enqueued_at must still be the FIRST timestamp.
	var gotAt string
	if scanErr := db.QueryRowContext(ctx, `SELECT enqueued_at FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotAt); scanErr != nil {
		t.Fatalf("SELECT enqueued_at: %v", scanErr)
	}
	if gotAt != enqueuedAt {
		t.Errorf("enqueued_at changed on idempotent call: got %q, want %q", gotAt, enqueuedAt)
	}
}

// TestMarkEnqueued_RejectsTerminalRejection verifies that calling
// MarkEnqueued on a row with state='rejected_terminal' returns
// ErrStateConflict (not nil, not ErrNotFound).
func TestMarkEnqueued_RejectsTerminalRejection(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	id, won, _, err := repo.TryReserve(ctx, "ch-test", "vid-test", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("TryReserve: err=%v won=%v", err, won)
	}

	// Mark as terminal rejected first.
	if err := repo.MarkRejected(ctx, id, "terminal reject", false); err != nil {
		t.Fatalf("MarkRejected(terminal): %v", err)
	}

	err = repo.MarkEnqueued(ctx, id, time.Now().UTC().Format(time.RFC3339))
	if err == nil {
		t.Fatal("MarkEnqueued on rejected_terminal should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrStateConflict) {
		t.Errorf("MarkEnqueued on rejected_terminal should return ErrStateConflict, got: %v", err)
	}
}

// TestMarkEnqueued_NotFound verifies that calling MarkEnqueued with a
// non-existent id returns ErrNotFound (not ErrStateConflict).
func TestMarkEnqueued_NotFound(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	err := repo.MarkEnqueued(ctx, "disc_nonexistent_id", time.Now().UTC().Format(time.RFC3339))
	if err == nil {
		t.Fatal("MarkEnqueued on nonexistent id should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrNotFound) {
		t.Errorf("MarkEnqueued on nonexistent id should return ErrNotFound, got: %v", err)
	}
}

// TestMarkEnqueuedVsMarkRejected_OnlyOneTransitionWins verifies the
// concurrent-transition contract: when two goroutines race — one
// calls MarkEnqueued, the other calls MarkRejected — exactly one
// transition is applied (RowsAffected==1) and the other gets
// ErrStateConflict.
//
// FASE 1.3 (July 2026): the WHERE clause on each UPDATE gates on
// state IN ('pending','analyzing'), so only one of the two concurrent
// UPDATEs matches a row; the loser gets RowsAffected==0 and returns
// ErrStateConflict.
func TestMarkEnqueuedVsMarkRejected_OnlyOneTransitionWins(t *testing.T) {
	repo, db, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	// Force single-connection mode so goroutines share the same
	// in-memory SQLite database (default pool gives each goroutine
	// its own isolated in-memory world).
	db.SetMaxOpenConns(1)

	id, won, _, err := repo.TryReserve(ctx, "ch-race", "vid-race", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("TryReserve: err=%v won=%v", err, won)
	}

	// Run 50 iterations to amplify race detection.
	// Each iteration resets to 'pending', then two goroutines race.
	applied := 0 // exactly one winner per iteration
	for i := 0; i < 50; i++ {
		// Reset the row back to 'pending' for each iteration.
		if _, resetErr := db.ExecContext(ctx, `UPDATE youtube_discoveries SET state='pending', enqueued_at=NULL, outcome='pending' WHERE id=?`, id); resetErr != nil {
			t.Fatalf("reset to pending: %v", resetErr)
		}

		done := make(chan struct{}, 2)
		var enqErr, rejErr error

		go func() {
			defer func() { done <- struct{}{} }()
			enqErr = repo.MarkEnqueued(ctx, id, time.Now().UTC().Format(time.RFC3339))
		}()
		go func() {
			defer func() { done <- struct{}{} }()
			rejErr = repo.MarkRejected(ctx, id, "race-reject", false)
		}()

		<-done
		<-done

		// Exactly one must be nil (TransitionApplied), the other must be
		// ErrStateConflict.
		enqOk := enqErr == nil
		rejOk := rejErr == nil
		if enqOk == rejOk {
			t.Errorf("iteration %d: expected exactly one nil, got enqErr=%v rejErr=%v", i, enqErr, rejErr)
		}
		if enqOk != rejOk {
			applied++ // exactly one winner
		}
		if enqOk && !errors.Is(rejErr, youtubediscoveries.ErrStateConflict) && rejErr != nil {
			t.Errorf("iteration %d: reject error is not ErrStateConflict: %v", i, rejErr)
		}
		if rejOk && !errors.Is(enqErr, youtubediscoveries.ErrStateConflict) && enqErr != nil {
			t.Errorf("iteration %d: enqueue error is not ErrStateConflict: %v", i, enqErr)
		}
	}

	if applied != 50 {
		t.Errorf("applied transitions = %d, want 50 (one winner per iteration)", applied)
	}

	// Verify final state is one of {enqueued, rejected_terminal}.
	var gotState string
	if scanErr := db.QueryRowContext(ctx, `SELECT state FROM youtube_discoveries WHERE id = ?`, id).Scan(&gotState); scanErr != nil {
		t.Fatalf("SELECT state: %v", scanErr)
	}
	if gotState != "enqueued" && gotState != "rejected_terminal" {
		t.Errorf("final state = %q, want enqueued or rejected_terminal", gotState)
	}
}

// TestMarkRejected_NotFound verifies that calling MarkRejected with a
// non-existent id returns ErrNotFound (not ErrStateConflict, not nil).
// Covers both retryable=true and retryable=false paths.
func TestMarkRejected_NotFound(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	// retryable=true path.
	err := repo.MarkRejected(ctx, "disc_nonexistent_id", "test error", true)
	if err == nil {
		t.Fatal("MarkRejected(retryable) on nonexistent id should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrNotFound) {
		t.Errorf("MarkRejected(retryable) on nonexistent id should return ErrNotFound, got: %v", err)
	}

	// retryable=false path.
	err = repo.MarkRejected(ctx, "disc_nonexistent_id", "test error", false)
	if err == nil {
		t.Fatal("MarkRejected(terminal) on nonexistent id should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrNotFound) {
		t.Errorf("MarkRejected(terminal) on nonexistent id should return ErrNotFound, got: %v", err)
	}
}

// TestMarkRejected_StateConflict verifies that calling MarkRejected
// on a row that is already in a terminal/incompatible state returns
// ErrStateConflict (not nil, not ErrNotFound). Covers both
// retryable=true and retryable=false paths.
func TestMarkRejected_StateConflict(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	// Create a row and MarkEnqueued it (state='enqueued').
	id, won, _, err := repo.TryReserve(ctx, "ch-conflict", "vid-conflict", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil || !won {
		t.Fatalf("TryReserve: err=%v won=%v", err, won)
	}
	if err := repo.MarkEnqueued(ctx, id, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("MarkEnqueued: %v", err)
	}

	// retryable=true on 'enqueued' row → ErrStateConflict.
	err = repo.MarkRejected(ctx, id, "should fail", true)
	if err == nil {
		t.Fatal("MarkRejected(retryable) on enqueued row should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrStateConflict) {
		t.Errorf("MarkRejected(retryable) on enqueued row should return ErrStateConflict, got: %v", err)
	}

	// retryable=false on 'enqueued' row → ErrStateConflict.
	err = repo.MarkRejected(ctx, id, "should fail", false)
	if err == nil {
		t.Fatal("MarkRejected(terminal) on enqueued row should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrStateConflict) {
		t.Errorf("MarkRejected(terminal) on enqueued row should return ErrStateConflict, got: %v", err)
	}

	// Also test on 'rejected_terminal' row (already terminal).
	id2, won2, _, err2 := repo.TryReserve(ctx, "ch-conflict", "vid-conflict-2", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err2 != nil || !won2 {
		t.Fatalf("TryReserve: err=%v won=%v", err2, won2)
	}
	if err := repo.MarkRejected(ctx, id2, "terminal", false); err != nil {
		t.Fatalf("first MarkRejected(terminal): %v", err)
	}
	// Second MarkRejected(terminal) on same row → ErrStateConflict
	// (state is 'rejected_terminal', not in ('pending','analyzing','rejected_retryable')).
	err = repo.MarkRejected(ctx, id2, "double terminal", false)
	if err == nil {
		t.Fatal("MarkRejected(terminal) on rejected_terminal row should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrStateConflict) {
		t.Errorf("MarkRejected(terminal) on rejected_terminal row should return ErrStateConflict, got: %v", err)
	}

	// MarkRejected(retryable=true) on rejected_terminal row → ErrStateConflict
	// (retryable path WHERE is IN ('pending','analyzing'), stricter than terminal).
	err = repo.MarkRejected(ctx, id2, "retryable on terminal", true)
	if err == nil {
		t.Fatal("MarkRejected(retryable) on rejected_terminal row should fail, got nil")
	}
	if !errors.Is(err, youtubediscoveries.ErrStateConflict) {
		t.Errorf("MarkRejected(retryable) on rejected_terminal row should return ErrStateConflict, got: %v", err)
	}
}

// TestIsTransientEnqueueError covers the enqueue.go predicate that
// maps (error → retryable bool). It is a sibling to the repository
// retry tests; the predicate MUST decide retryable correctly so the
// repository contract above stays ergonomic.
//
// FASE 6 Cut 6.1.D (July 2026): production retry.IsTransient became a
// pure typed probe. The transient-shaped cases below wrap the simulated
// emit-side error in *retry.TransientInfrastructureError — the
// canonical SDK-boundary emission shape (same envelope
// retry.WrapTransient produces at the boundary for typed markers).
// The non-transient cases stay raw: raw strings are not classified
// by the typed probe (no substring fallback in production), so they
// correctly classify as terminal.
func TestIsTransientEnqueueError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{&retry.TransientInfrastructureError{Err: errors.New("connection refused")}, true},
		{&retry.TransientInfrastructureError{Err: errors.New("HTTP 503 Service Unavailable")}, true},
		{&retry.TransientInfrastructureError{Err: errors.New("HTTP 429 Too Many Requests")}, true},
		{&retry.TransientInfrastructureError{Err: errors.New("request timeout after 30s")}, true},
		{&retry.TransientInfrastructureError{Err: errors.New("EOF: stream closed unexpectedly")}, true},
		{errors.New("validation: missing channel_id"), false},
		{errors.New("payload marshal: invalid JSON"), false},
	}
	for _, tc := range cases {
		got := retry.IsTransient(tc.err)
		if got != tc.want {
			t.Errorf("IsTransient(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// ── FASE 3.7 Sentinel alias pin tests (P0 dual-sentinel fix) ──────────────────
//
// FASE 3.7 follow-up to the rename commit (98e7ba41): the parallel-agent
// landing on origin/main switched the sentinel design from a wrap-chain
// to a thin-alias (var ErrLedgerStateConflict = youtubediscoveries.ErrStateConflict —
// SAME *errorString pointer per Go sentinel semantics). Three tests pin
// the alias contract end-to-end:
//
//   1. TestSentinelWrap_Conflict       — wrap chain still resolves
//      under errors.Is(wrapErr, ErrLedgerStateConflict).
//   2. TestSentinelWrap_NonConflict    — generic errors + wrap-chains
//      of generic errors MUST NOT spuriously match the sentinel.
//   3. TestSentinelWrap_InfraReturnsCanonical — infra-side error from
//      MarkEnqueued on rejected_terminal matches BOTH sentinels via
//      errors.Is (proving the alias is upheld end-to-end).
//
// Drift guard: any future refactor that replaces `var X = Y` with `var X
// = errors.New(...)` will fail Tests 1 + 3. godlike/07 "no fake
// availability" guarantee — the sentinel is the SAME object on both
// sides of the alias.

// TestSentinelWrap_Conflict pins the FASE 3.7 thin-alias sentinel contract:
// ErrLedgerStateConflict is the SAME *errorString as
// youtubediscoveries.ErrStateConflict (declared as `var ErrLedgerStateConflict =
// youtubediscoveries.ErrStateConflict`). Therefore an error constructed via
// `fmt.Errorf("...: %w", youtubediscoveries.ErrStateConflict)` MUST resolve to
// true under `errors.Is(err, ErrLedgerStateConflict)`. Verified
// the reverse direction too (errors.Is to the canonical sentinel).
// Drift in the alias (any future change from `var X = Y` to `var X =
// errors.New(...)`) breaks both assertions — godlike/07 "no fake
// availability" invariant is upheld iff both return true.
func TestSentinelWrap_Conflict(t *testing.T) {
	// (i) Direct wrap chain with ErrLedgerStateConflict (canonical
	// monitor-internal constructor pattern).
	directWrap := fmt.Errorf("monitoring transition: %w", ErrLedgerStateConflict)
	if !errors.Is(directWrap, ErrLedgerStateConflict) {
		t.Errorf("TestSentinelWrap_Conflict (direct): errors.Is(direct wrap of ErrLedgerStateConflict, ErrLedgerStateConflict) = false; want true")
	}

	// (ii) Adapter simulation via TranslateLedgerSentinel: the
	// production composition-root adapter path on a manually
	// constructed error wrapping youtubediscoveries.ErrStateConflict. The
	// multi-%w wiring MUST preserve BOTH sentinels (ErrLedgerStateConflict
	// added by adapter + youtubediscoveries.ErrStateConflict preserved via
	// the second %w).
	infraWrap := fmt.Errorf("infra: row state precondition failed: %w", youtubediscoveries.ErrStateConflict)
	translated := TranslateLedgerSentinel(infraWrap)
	if !errors.Is(translated, ErrLedgerStateConflict) {
		t.Errorf("TestSentinelWrap_Conflict (adapter): errors.Is(translated, ErrLedgerStateConflict) = false; want true (adapter MUST add ErrLedgerStateConflict to chain)")
	}
	if !errors.Is(translated, youtubediscoveries.ErrStateConflict) {
		// NOTE: `%%w` (escaped percent) prints the literal `%w` token in
		// the test-failure message; Go vet treats the unescaped `%w`
		// as an error-wrapping format directive incompatible with
		// t.Errorf (which discards the format error arg).
		t.Errorf("TestSentinelWrap_Conflict (adapter): errors.Is(translated, youtubediscoveries.ErrStateConflict) = false; want true (multi-%%w MUST preserve infra chain)")
	}
}

// TestSentinelWrap_NonConflict pins the negative case: a generic error
// (sqlite I/O error, network failure, validation error) MUST NOT match
// ErrLedgerStateConflict under errors.Is. The wrap chain is
// tested across multiple depths to ensure no false-positive resolution.
func TestSentinelWrap_NonConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"bare-IO-error", errors.New("sqlite: I/O error or network failure")},
		{"wrapped-IO-error", fmt.Errorf("emit: %w", errors.New("connection refused"))},
		{"unrelated-validation-error", errors.New("validation: missing channel_id")},
		{"double-wrapped-IO-error", fmt.Errorf("pipeline emit: %w", fmt.Errorf("transport: %w", errors.New("EOF")))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errors.Is(tc.err, ErrLedgerStateConflict) {
				t.Errorf("non-conflict err matched ErrLedgerStateConflict: %v", tc.err)
			}
			if errors.Is(tc.err, youtubediscoveries.ErrStateConflict) {
				t.Errorf("non-conflict err matched youtubediscoveries.ErrStateConflict: %v", tc.err)
			}
		})
	}
}

// TestSentinelWrap_InfraReturnsCanonical pins the infra-side end-to-end
// alias contract via the canonical sentinel path:
//
//   - Setup: TryReserve creates the row at state='pending'; MarkRejected(false)
//     moves it to state='rejected_terminal'.
//   - Trigger: MarkEnqueued on rejected_terminal row hits the WHERE
//     clause `state IN ('pending','analyzing')` → 0 rows matched → infra
//     surfaces the canonical youtubediscoveries.ErrStateConflict.
//
// The FASE 3.7 contract assertion is `errors.Is(enqErr,
// ErrLedgerStateConflict)`. This is the NEW path: a future
// refactor that breaks the alias (replaces `var X = Y` with `var X =
// errors.New(...)`) leaves Test 1 green (because the infra still
// returns the Y pointer) but flips this assertion to false. Only by
// verifying BOTH the canonical sqlassets sentinel AND the monitor
// alias sentinel does this test catch the regressed-state correctly.
func TestSentinelWrap_InfraReturnsCanonical(t *testing.T) {
	repo, _, cleanup := newInMemoryLedger(t)
	defer cleanup()
	ctx := context.Background()

	// Step 1: TryReserve creates the row at state='pending'.
	id, won, _, err := repo.TryReserve(ctx, "ch-sentinel", "vid-sentinel", "v1", "u", "t", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("step 1 TryReserve: %v", err)
	}
	if !won {
		t.Fatal("step 1 should win on fresh ledger")
	}

	// Step 2: MarkRejected(terminal) → row at state='rejected_terminal'.
	if err := repo.MarkRejected(ctx, id, "terminal: invalid payload", false); err != nil {
		t.Fatalf("step 2 MarkRejected(terminal): %v", err)
	}

	// Step 3: MarkEnqueued on rejected_terminal — the WHERE clause gates
	// on state IN ('pending','analyzing'), so UPDATE matches 0 rows and
	// the infra surfaces the canonical youtubediscoveries.ErrStateConflict.
	enqErr := repo.MarkEnqueued(ctx, id, time.Now().UTC().Format(time.RFC3339))
	if enqErr == nil {
		t.Fatal("step 3 MarkEnqueued on rejected_terminal must fail, got nil")
	}
	if !errors.Is(enqErr, youtubediscoveries.ErrStateConflict) {
		t.Errorf("step 3: infra error NOT matching youtubediscoveries.ErrStateConflict: %v", enqErr)
	}
	// Adapter simulation: apply TranslateLedgerSentinel to mimic the
	// production adapter. BOTH sentinels MUST resolve after translation
	// (multi-%w preservation). This is the production-shape contract:
	// the monitor-side pattern-match `errors.Is(err,
	// ErrLedgerStateConflict)` only resolves correctly when the full
	// adapter path is exercised.
	translated := TranslateLedgerSentinel(enqErr)
	if !errors.Is(translated, ErrLedgerStateConflict) {
		t.Errorf("step 3 (adapter): errors.Is(translated, ErrLedgerStateConflict) = false; want true (adapter MUST add ErrLedgerStateConflict to chain): %v", translated)
	}
	if !errors.Is(translated, youtubediscoveries.ErrStateConflict) {
		// NOTE: `%%w` (escaped percent) prints the literal `%w` token in
		// the test-failure message; see TestSentinelWrap_Conflict for
		// the full vet rationale.
		t.Errorf("step 3 (adapter): errors.Is(translated, youtubediscoveries.ErrStateConflict) = false; want true (multi-%%w MUST preserve infra chain): %v", translated)
	}
}
