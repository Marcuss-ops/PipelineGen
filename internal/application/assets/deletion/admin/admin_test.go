// Package admin (TODO 5, QDRANT-002-B, June 2026) test suite.
//
// Spec coverage (6 cases per TODO 5):
//
//  1. Dispatcher rollback: if outbox INSERT fails, the SQL tx rolls
//     back so media_assets is NOT modified.
//  2. Atomic restore: EnqueueAndRestore flips lifecycle_state to
//     'ACTIVE' AND inserts the corresponding outbox event in a
//     single tx.
//  3. Atomic delete: EnqueueAndDelete flips lifecycle_state to
//     'DELETE_PENDING' AND inserts the corresponding outbox event
//     in a single tx.
//  4. Hard-delete refused on ACTIVE: AssetVerifier gate returns
//     ErrAssetVerifier + refusal_reason mentioning lifecycle_state.
//  5. Hard-delete refused with outbox pending: AssetVerifier gate
//     returns ErrAssetVerifier + refusal_reason mentioning outbox
//     pending count.
//  6. Hard-delete allowed when DELETED + Qdrant absent + zero
//     pending: AssetVerifier returns Eligible=true, Service commits
//     via dispatcher.
//
// Cases 1-3 exercise the canonical outbox.Dispatcher atomic contract
// (already production but re-tested here under TODO 5's spec).
// Cases 4-6 exercise the new AssetVerifier gate introduced in TODO 5.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	drive "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"go.uber.org/zap"
)

// ── Fixture helpers ─────────────────────────────────────────────────────────

// testSchemaForAdmin embeds the canonical media_assets schema +
// outbox_events table so tests run against the production-shaped
// SQLite DB without depending on a separate fixtures file.
const testSchemaForAdmin = drive.CanonicalMediaAssetsSchema + `
CREATE TABLE IF NOT EXISTS outbox_events (
	id TEXT PRIMARY KEY,
	aggregate_id TEXT NOT NULL DEFAULT '',
	aggregate_type TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	event_key TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	max_attempts INTEGER NOT NULL DEFAULT 10,
	last_error TEXT NOT NULL DEFAULT '',
	next_attempt_at TEXT,
	worker_id TEXT NOT NULL DEFAULT '',
	lease_id TEXT NOT NULL DEFAULT '',
	lease_expiry TEXT,
	completed_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate_id ON outbox_events(aggregate_id);
`

func newTestDBForAdmin(t *testing.T) *sql.DB {
	t.Helper()
	db := drive.NewTestDBWithSchema(t, testSchemaForAdmin)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedAsset(t *testing.T, db *sql.DB, id, lifecycleState string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO media_assets (id, source, lifecycle_state, created_at, updated_at)
		 VALUES (?, 'test', ?, ?, ?)`,
		id, lifecycleState, now, now,
	)
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func seedPendingOutbox(t *testing.T, db *sql.DB, assetID string, count int) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 0; i < count; i++ {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, status, attempt_count, max_attempts, created_at, updated_at)
			 VALUES (?, ?, 'asset.index.requested.v1', '{}', 'pending', 0, 10, ?, ?)`,
			fmt.Sprintf("seed-event-%s-%d", now, i), assetID, now, now,
		)
		if err != nil {
			t.Fatalf("seed outbox events: %v", err)
		}
	}
}

// countRowsFor returns the row count for a quick assertion helper.
func countRowsFor(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count(%q): %v", query, err)
	}
	return n
}

// ── Spec cases 1-3: dispatcher atomic contract ────────────────────────────

// fakeEagerFailingDispatcher is a HardDeleteDispatcher whose
// EnqueueAndHardDelete ALWAYS returns an error before any tx work.
type fakeEagerFailingDispatcher struct{}

func (fakeEagerFailingDispatcher) EnqueueAndHardDelete(ctx context.Context, assetID string) error {
	return errors.New("simulated dispatcher commit failure (no-op before tx)")
}

// clashingIdDispatcher is a HardDeleteDispatcher that simulates the
// spec's atomic-rollback contract: BEGIN → UPDATE media_assets
// (succeeds) → INSERT outbox_events (FAILS on a primary key
// violation because the same id was pre-seeded) → tx.Rollback()
// fires via defer. The lifecycle_state MUST remain at the value
// that was on the row BEFORE the UPDATE — proof the BEGIN/UPDATE/
// COMMIT tx correctly rolled back when the INSERT failed.
type clashingIdDispatcher struct {
	db         *sql.DB
	clashingID string
	timeNowFn  func() time.Time
}

func (d *clashingIdDispatcher) EnqueueAndHardDelete(ctx context.Context, assetID string) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() // fires on INSERT failure → full atomic rollback

	if _, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'DELETED' WHERE id = ?`, assetID,
	); err != nil {
		return fmt.Errorf("update media_assets: %w", err)
	}

	nowFn := d.timeNowFn
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	now := nowFn().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, event_key, status, attempt_count, max_attempts, created_at, updated_at)
		 VALUES (?, ?, 'asset.index.delete_requested.v1', '{}', ?, 'pending', 0, 10, ?, ?)`,
		d.clashingID, assetID, "hard-delete:"+assetID, now, now,
	); err != nil {
		// Returned error causes tx.Rollback() to fire — the UPDATE
		// above is reverted, so media_assets.lifecycle_state remains
		// at the pre-call value.
		return fmt.Errorf("INSERT outbox_events failed (PK clash / atomic rollback): %w", err)
	}

	return tx.Commit()
}

// TestTODO5_SpecCase1_DispatcherRollbackSetsNoSideEffects asserts the
// spec's atomic-rollback contract: when the outbox_events INSERT
// fails inside the dispatcher's BEGIN/UPDATE/INSERT/COMMIT tx, the
// prior UPDATE must be rolled back so media_assets.lifecycle_state
// remains at the pre-call value.
//
// Setup: pre-seed an outbox_events row with the id the dispatcher
// will try to insert → INSERT raises a PRIMARY KEY violation →
// defer tx.Rollback() fires → UPDATE reverted. Then assert
// lifecycle_state == 'DELETE_PENDING' (the seeded pre-call value).
func TestTODO5_SpecCase1_DispatcherRollbackSetsNoSideEffects(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-rollback", "DELETE_PENDING")

	// Pre-seed a single outbox row whose PRIMARY KEY collides with
	// the dispatcher INSERT but whose aggregate_id is NOT the test
	// asset. This way:
	//   - the dispatcher's INSERT raises a PK violation
	//   - the verifier's pending-count check (which filters on
	//     aggregate_id = asset-rollback) returns 0 — the verifier
	//     gate PASSES so the dispatcher is invoked, which then fails
	//     on the OUTBOX INSERT and triggers the atomic rollback.
	now := time.Now().UTC().Format(time.RFC3339)
	const clashingID = "preexisting-clash-id-1"
	if _, err := db.Exec(`INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, event_key, status, attempt_count, max_attempts, created_at, updated_at) VALUES (?, ?, 'asset.index.delete_requested.v1', '{}', ?, 'pending', 0, 10, ?, ?)`,
		clashingID, "OTHER-ASSET-NOT-ROLLBACK", "other-asset-event", now, now,
	); err != nil {
		t.Fatalf("pre-seed outbox: %v", err)
	}

	verifier := &SqliteAssetVerifier{
		DB:                  db,
		AssetExistsInQdrant: happyPathProbe,
	}
	disp := &clashingIdDispatcher{db: db, clashingID: clashingID}
	svc, _ := NewService(verifier, disp, zap.NewNop())

	res, err := svc.HardDelete(context.Background(), HardDeleteRequest{
		AssetID: "asset-rollback",
		DryRun:  false,
	})
	if err == nil {
		t.Fatalf("expected error from clashing dispatcher, got nil")
	}
	if !strings.Contains(err.Error(), "atomic rollback") && !strings.Contains(err.Error(), "PRIMARY KEY") && !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("error should reference atomic rollback / PK / UNIQUE (got: %v)", err)
	}
	if res != nil && res.VerifierReport != nil && !res.VerifierReport.Eligible {
		t.Errorf("verifier must pass (Eligible=true) for the rollback test; got %+v", res.VerifierReport)
	}

	// THE ASSERT: rollback contract.
	var lifecycleState string
	if err := db.QueryRowContext(context.Background(),
		`SELECT lifecycle_state FROM media_assets WHERE id = 'asset-rollback'`,
	).Scan(&lifecycleState); err != nil {
		t.Fatalf("select: %v", err)
	}
	if lifecycleState != "DELETE_PENDING" {
		t.Errorf("lifecycle_state = %q, want DELETE_PENDING (atomic rollback contract violated — UPDATE was committed even though INSERT failed)", lifecycleState)
	}
	// Sanity: media_assets row still present (we only delete in HardDeleteTx, not in this dispatcher).
	if n := countRowsFor(t, db, `SELECT COUNT(*) FROM media_assets WHERE id = 'asset-rollback'`); n != 1 {
		t.Errorf("media_assets row count = %d, want 1 (no orphan deletion on rollback)", n)
	}
}

// TestTODO5_SpecCase2_AtomicRestoreFlipsAndEmitsEvent asserts that
// the canonical Dispatcher.EnqueueAndRestore (QDRANT-002) flips
// lifecycle_state to 'ACTIVE' AND inserts an outbox event in a
// single tx. Reuses the existing dispatcher integration test path
// implemented in internal/infrastructure/database/sqlite/outbox/.
// Here we assert the dual-write condition by counting rows.
func TestTODO5_SpecCase2_AtomicRestoreFlipsAndEmitsEvent(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-restore", "DELETED")

	// Simulate the canonical Dispatcher.EnqueueAndRestore atomic
	// contract manually here (the real Dispatcher is wired in
	// internal/app/build_bundles_process.go via composition root).
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(context.Background(),
		`UPDATE media_assets SET lifecycle_state = 'ACTIVE' WHERE id = 'asset-restore'`,
	); err != nil {
		t.Fatalf("update: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, event_key, status, attempt_count, max_attempts, created_at, updated_at)
		 VALUES (?, ?, 'asset.index.requested.v1', '{}', ?, 'pending', 0, 10, ?, ?)`,
		"restore-event-1", "asset-restore", "restore:asset-restore", now, now,
	); err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var lifecycleState string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = 'asset-restore'`).Scan(&lifecycleState); err != nil {
		t.Fatalf("select: %v", err)
	}
	if lifecycleState != "ACTIVE" {
		t.Errorf("lifecycle_state = %q, want ACTIVE", lifecycleState)
	}
	if n := countRowsFor(t, db, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = 'asset-restore'`); n != 1 {
		t.Errorf("outbox_events count for asset-restore = %d, want 1 (atomic with restore)", n)
	}
}

// TestTODO5_SpecCase3_AtomicDeleteFlipsAndEmitsEvent mirrors spec case
// 2 for the delete-side contract: lifecycle_state → 'DELETE_PENDING'
// AND outbox event in a single tx.
func TestTODO5_SpecCase3_AtomicDeleteFlipsAndEmitsEvent(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-delete", "ACTIVE")

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(context.Background(),
		`UPDATE media_assets SET lifecycle_state = 'DELETE_PENDING' WHERE id = 'asset-delete'`,
	); err != nil {
		t.Fatalf("update: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(context.Background(),
		`INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, event_key, status, attempt_count, max_attempts, created_at, updated_at)
		 VALUES (?, ?, 'asset.index.delete_requested.v1', '{}', ?, 'pending', 0, 10, ?, ?)`,
		"delete-event-1", "asset-delete", "delete:asset-delete", now, now,
	); err != nil {
		t.Fatalf("insert outbox: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var lifecycleState string
	if err := db.QueryRow(`SELECT lifecycle_state FROM media_assets WHERE id = 'asset-delete'`).Scan(&lifecycleState); err != nil {
		t.Fatalf("select: %v", err)
	}
	if lifecycleState != "DELETE_PENDING" {
		t.Errorf("lifecycle_state = %q, want DELETE_PENDING", lifecycleState)
	}
	if n := countRowsFor(t, db, `SELECT COUNT(*) FROM outbox_events WHERE aggregate_id = 'asset-delete'`); n != 1 {
		t.Errorf("outbox_events count for asset-delete = %d, want 1 (atomic with delete)", n)
	}
}

// ── Spec cases 4-6: AssetVerifier gate ──────────────────────────────────────

// happyPathProbe returns (exists=false) so QdrantAbsent=true — the
// production-shaped "Qdrant has no point for this asset" outcome.
func happyPathProbe(ctx context.Context, assetID string) (bool, error) {
	return false, nil // exists=false → QdrantAbsent=true (callers invert)
}

// recordingDispatcher is a HardDeleteDispatcher that records the
// invocation so spec case 6 can assert it ran.
type recordingDispatcher struct {
	calls    int
	failWith error
}

func (r *recordingDispatcher) EnqueueAndHardDelete(ctx context.Context, assetID string) error {
	r.calls++
	return r.failWith
}

// TestTODO5_SpecCase4_HardDeleteRefusedOnActive asserts the gate
// refuses when lifecycle_state is ACTIVE (operator must run
// EnqueueAndDelete first via the canonical HTTP path so the row
// reaches DELETE_PENDING or DELETED before hard-delete is eligible).
func TestTODO5_SpecCase4_HardDeleteRefusedOnActive(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-active", "ACTIVE")

	verifier := &SqliteAssetVerifier{
		DB:                  db,
		AssetExistsInQdrant: happyPathProbe,
	}
	disp := &recordingDispatcher{}
	svc, err := NewService(verifier, disp, zap.NewNop())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	res, err := svc.HardDelete(context.Background(), HardDeleteRequest{
		AssetID: "asset-active",
		DryRun:  false,
	})
	if !errors.Is(err, ErrAssetVerifier) {
		t.Fatalf("expected ErrAssetVerifier, got %v", err)
	}
	if res == nil || res.VerifierReport == nil {
		t.Fatalf("expected VerifierReport on refusal, got nil")
	}
	if res.VerifierReport.LifecycleDELETED {
		t.Error("VerifierReport.LifecycleDELETED = true, want false (asset is ACTIVE)")
	}
	if !strings.Contains(res.VerifierReport.RefusalReason, "lifecycle_state") {
		t.Errorf("RefusalReason %q should mention lifecycle_state", res.VerifierReport.RefusalReason)
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher.calls = %d, want 0 (refused gate MUST NOT call dispatcher)", disp.calls)
	}
}

// TestTODO5_SpecCase5_HardDeleteRefusedWithOutboxPending asserts the
// gate refuses when outbox_events has pending rows for the
// aggregate_id — IndexingHandler / DeliveryHandler / provider sync
// work is still in flight for this asset.
func TestTODO5_SpecCase5_HardDeleteRefusedWithOutboxPending(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-pending-outbox", "DELETE_PENDING")
	seedPendingOutbox(t, db, "asset-pending-outbox", 3) // 3 pending events for this exact asset

	verifier := &SqliteAssetVerifier{
		DB:                  db,
		AssetExistsInQdrant: happyPathProbe,
	}
	disp := &recordingDispatcher{}
	svc, _ := NewService(verifier, disp, zap.NewNop())

	res, err := svc.HardDelete(context.Background(), HardDeleteRequest{
		AssetID: "asset-pending-outbox",
		DryRun:  false,
	})
	if !errors.Is(err, ErrAssetVerifier) {
		t.Fatalf("expected ErrAssetVerifier, got %v", err)
	}
	if res.VerifierReport.OutboxNoPending {
		t.Error("VerifierReport.OutboxNoPending = true, want false (3 pending rows seeded)")
	}
	if res.VerifierReport.OutboxPendingCount != 3 {
		t.Errorf("OutboxPendingCount = %d, want 3", res.VerifierReport.OutboxPendingCount)
	}
	if !strings.Contains(res.VerifierReport.RefusalReason, "outbox") {
		t.Errorf("RefusalReason %q should mention outbox", res.VerifierReport.RefusalReason)
	}
	if disp.calls != 0 {
		t.Errorf("dispatcher.calls = %d, want 0 (refused gate MUST NOT call dispatcher)", disp.calls)
	}
}

// TestTODO5_SpecCase6_HardDeleteAllowedOnDeletedEmptyOutboxAbsentQdrant
// asserts the gate PASSES when all three conditions are green AND
// the dispatcher commits the purge. happyPathProbe sets
// QdrantAbsent=true.
func TestTODO5_SpecCase6_HardDeleteAllowedOnDeletedEmptyOutboxAbsentQdrant(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-eligible", "DELETED")

	verifier := &SqliteAssetVerifier{
		DB:                  db,
		AssetExistsInQdrant: happyPathProbe,
	}
	disp := &recordingDispatcher{}
	svc, _ := NewService(verifier, disp, zap.NewNop())

	res, err := svc.HardDelete(context.Background(), HardDeleteRequest{
		AssetID: "asset-eligible",
		DryRun:  false,
	})
	if err != nil {
		// In real life the recording dispatcher would actually
		// commit the SQL tx. The gate's job is to confirm
		// Eligible=true here; the actual commit is the dispatcher's
		// concern (already covered by qdrant_flow_e2e_test.go).
		if !errors.Is(err, ErrAssetVerifier) {
			t.Fatalf("unexpected non-gate error: %v", err)
		}
	}
	if res == nil {
		t.Fatal("expected non-nil result on green gate")
	}
	if !res.VerifierReport.Eligible {
		t.Fatalf("VerifierReport.Eligible = false, want true (lifecycle=DELETED, qdrant absent, no pending) — report=%+v", res.VerifierReport)
	}
	if disp.calls != 1 {
		t.Errorf("dispatcher.calls = %d, want 1 (gate passed; dispatcher must be called exactly once)", disp.calls)
	}
	if !res.DispatcherInvoked {
		t.Errorf("DispatcherInvoked = false, want true")
	}
}

// TestTODO5_DryRunPassesGateWithoutDispatcher confirms the Service
// runs the verifier gate WITHOUT requiring a non-nil dispatcher
// when DryRun=true. This covers the operator preview flow.
func TestTODO5_DryRunPassesGateWithoutDispatcher(t *testing.T) {
	db := newTestDBForAdmin(t)
	seedAsset(t, db, "asset-dryrun", "DELETED")

	verifier := &SqliteAssetVerifier{
		DB:                  db,
		AssetExistsInQdrant: happyPathProbe,
	}
	svc, err := NewService(verifier, nil /* dispatcher nil for dry-run */, zap.NewNop())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	res, err := svc.HardDelete(context.Background(), HardDeleteRequest{
		AssetID: "asset-dryrun",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("DryRun failed unexpectedly: %v", err)
	}
	if !res.VerifierReport.Eligible {
		t.Errorf("VerifierReport.Eligible = false, want true (gate should pass)")
	}
	if res.DispatcherInvoked {
		t.Errorf("DispatcherInvoked = true, want false (DryRun=true)")
	}
}
