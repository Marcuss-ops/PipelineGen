// Package media_test — dual_write_timestamps_test.go pins the migration 004 /
// BASELINE_PLAN.md §5 DUAL-WRITE contract on the PostgreSQL media outbox: every
// statement that mutates a dual-written TEXT timestamp must fill its
// TIMESTAMPTZ mirror in the SAME statement.
//
// WHY THIS EXISTS. 004 adds the *_ts expand columns so the read path can later
// flip to typed timestamps without a second backfill, and BASELINE_PLAN.md §5
// states the writer performs a single-transaction dual-write. Only ONE mirror
// had a pin before this file (media_assets.index_state_updated_at_ts, asserted
// by outbox_worker_test.go), so the outbox lease and terminal paths had drifted
// silently. The drift was measured on a live media database:
//
//	lease_expiry   = '2026-09-16T10:40:28Z'   lease_expiry_ts   = NULL
//	completed_at   = '2026-09-16T10:35:29Z'   completed_at_ts   = NULL
//	updated_at     = '2026-09-16T10:35:29Z'   updated_at_ts     = '2026-09-16 10:35:27+00'
//
// i.e. the TEXT columns moved and the mirrors did not. A reader that trusted
// the documented "readers prefer *_ts when present" contract would have read
// NULL — or a stale insert-time value — for every claimed/completed event.
// This test makes that contract executable instead of aspirational.
package media_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// outboxMirrorState is the projection this test reads back for one event.
type outboxMirrorState struct {
	text       string
	tsSet      bool
	updTSIsSet bool
}

// readOutboxMirrorState reads the TEXT sibling, the *_ts mirror and the
// updated_at_ts mirror for the named column of one outbox row.
func readOutboxMirrorState(t *testing.T, db *sql.DB, eventID int64, column string) outboxMirrorState {
	t.Helper()
	var st outboxMirrorState
	// The column name is test-controlled (never user input), so the format
	// splice cannot inject SQL.
	query := `SELECT COALESCE(` + column + `, ''), (` + column + `_ts IS NOT NULL), (updated_at_ts IS NOT NULL) ` +
		`FROM outbox_events WHERE id = $1`
	if err := db.QueryRow(query, eventID).Scan(&st.text, &st.tsSet, &st.updTSIsSet); err != nil {
		t.Fatalf("read %s mirror state for event %d: %v", column, eventID, err)
	}
	return st
}

// TestOutboxDualWriteTimestampsMirrorText pins the claim, completion,
// retry and terminal paths of the PostgreSQL media outbox against the 004
// dual-write contract.
func TestOutboxDualWriteTimestampsMirrorText(t *testing.T) {
	db := newMediaTestDB(t)
	ctx := context.Background()
	repo := pgmedia.NewOutboxRepository(db)

	const (
		claimAsset = "yt_dualwrite_claim_v1"
		retryAsset = "yt_dualwrite_retry_v1"
		deadAsset  = "yt_dualwrite_deadletter_v1"
		workerID   = "dual-write-worker"
	)

	for _, id := range []string{claimAsset, retryAsset, deadAsset} {
		seedIndexableAsset(t, db, id)
	}

	claimOne := func() *pgmedia.OutboxClaim {
		t.Helper()
		claim, err := repo.ClaimNext(ctx, workerID, time.Minute)
		if err != nil {
			t.Fatalf("ClaimNext: %v", err)
		}
		if claim == nil {
			t.Fatal("expected a pending asset.index.requested event")
		}
		return claim
	}

	// ── Claim path: lease_expiry + updated_at ────────────────────────────
	claimed := claimOne()
	lease := readOutboxMirrorState(t, db, claimed.Event.ID, "lease_expiry")
	if lease.text == "" {
		t.Error("claim must stamp lease_expiry")
	}
	if !lease.tsSet {
		t.Error("lease_expiry_ts must mirror lease_expiry in the same statement (dual-write expand)")
	}
	if !lease.updTSIsSet {
		t.Error("updated_at_ts must mirror updated_at on claim (dual-write expand)")
	}

	// ── Completion path: completed_at + updated_at ───────────────────────
	if err := repo.MarkCompleted(ctx, claimed.Event.ID, claimed.LeaseID); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	completed := readOutboxMirrorState(t, db, claimed.Event.ID, "completed_at")
	if completed.text == "" {
		t.Error("MarkCompleted must stamp completed_at")
	}
	if !completed.tsSet {
		t.Error("completed_at_ts must mirror completed_at (dual-write expand)")
	}
	if !completed.updTSIsSet {
		t.Error("updated_at_ts must mirror updated_at on completion (dual-write expand)")
	}

	// ── Retry path: next_attempt_at + lease reset ────────────────────────
	retried := claimOne()
	if err := repo.MarkFailed(ctx, retried.Event.ID, retried.LeaseID, "transient", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	next := readOutboxMirrorState(t, db, retried.Event.ID, "next_attempt_at")
	if next.text == "" {
		t.Error("MarkFailed retry must stamp next_attempt_at")
	}
	if !next.tsSet {
		t.Error("next_attempt_at_ts must mirror next_attempt_at (dual-write expand)")
	}
	// A retry releases the lease, so the mirror must be cleared with it —
	// otherwise a reader could see a lease that the TEXT column says is gone.
	var leaseReleased bool
	var leaseTSCleared bool
	if err := db.QueryRow(
		`SELECT COALESCE(lease_expiry, '') = '', lease_expiry_ts IS NULL FROM outbox_events WHERE id = $1`,
		retried.Event.ID,
	).Scan(&leaseReleased, &leaseTSCleared); err != nil {
		t.Fatalf("read released lease: %v", err)
	}
	if !leaseReleased {
		t.Error("MarkFailed retry must release lease_expiry")
	}
	if !leaseTSCleared {
		t.Error("lease_expiry_ts must be cleared with lease_expiry (dual-write expand)")
	}

	// ── Terminal path: dead_letter + lease reset ─────────────────────────
	dead := claimOne()
	if err := repo.MarkDeadLetter(ctx, dead.Event.ID, dead.LeaseID, "terminal"); err != nil {
		t.Fatalf("MarkDeadLetter: %v", err)
	}
	leased := readOutboxMirrorState(t, db, dead.Event.ID, "lease_expiry")
	if leased.text != "" {
		t.Errorf("MarkDeadLetter must clear lease_expiry, got %q", leased.text)
	}
	if leased.tsSet {
		t.Error("lease_expiry_ts must be NULL once lease_expiry is cleared (dual-write expand)")
	}
	if !leased.updTSIsSet {
		t.Error("updated_at_ts must mirror updated_at on dead-letter (dual-write expand)")
	}
}
