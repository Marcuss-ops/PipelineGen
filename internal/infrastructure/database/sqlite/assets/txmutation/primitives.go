// Package txmutation — primitives.go (the canonical implementation of
// SetLogger, HardDeleteTx, and RestoreTx; see doc.go for the package
// rationale and the caller-contracts / production-import discipline).
//
// This file is the ONLY Go file in the txmutation package. The surface
// is intentionally narrow:
//   - SetLogger(log *zap.Logger) — package-level logger hook.
//   - HardDeleteTx(ctx, tx, id) — physically deletes media_assets +
//     dependent rows in caller-owned tx.
//   - RestoreTx(ctx, tx, id) — flips lifecycle_state to canonical
//     'ACTIVE' in caller-owned tx.
//
// No additional helpers, no public state, no internal caches. The
// primitives are physics-only; the safety gate (lifecycle_state +
// qdrant_point_state) lives in the caller (admin.PurgeService), not
// here, by design (see doc.go §"Caller contracts" §2).
//
// Lifecycle state SSOT (PR 1, June 2026): all writers emit canonical
// UPPERCASE enum values from asset.LifecycleState. RestoreTx writes
// 'ACTIVE'; pre-PR1 lowercase 'ready' is no longer accepted by any
// code path that consumed this primitive.
package txmutation

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	"go.uber.org/zap"
)

// ── Package-level logger ───────────────────────────────────────────────────
//
// SetLogger wires a *zap.Logger into the package-level logger so the
// per-table child-delete INFO lines surface on the operator console.
// Default before SetLogger: zap.NewNop() — silent under no-config, but
// never panic.
//
// Process-global, not per-instance. The composition root
// (`admin.NewPurgeService`) is the canonical binding moment per the
// package doc; multiple SetLogger calls are safe (the most-recent
// binding wins — admin composition is the only legitimate caller, so
// racing compositions are not a concern in practice, but the
// atomic.Pointer swap is cheap and avoids a race-detector trip).
//
// Why atomic.Pointer instead of a mutex: a *zap.Logger is a fat
// pointer; atomic.Pointer.Load is a single 8-byte atomic read on
// amd64; a mutex would serialise every log call. Atomic.Pointer gives
// lock-free reads at the cost of one extra allocation when SetLogger
// is called.
var logger atomic.Pointer[zap.Logger]

// init nop-defaults the package-level logger to zap.NewNop() so the
// very first log call does not panic if SetLogger was never invoked.
// A future caller (e.g. a test fixture) that bypasses admin still gets
// a safe logger.
func init() {
	logger.Store(zap.NewNop())
}

// SetLogger atomically replaces the package-level *zap.Logger. Safe to
// call concurrently with ongoing log emissions from HardDeleteTx /
// RestoreTx. The composition-root admin.NewPurgeService invokes this
// with log.Named("txmutation") so an operator can filter the delete
// cascade INFO lines.
//
// Illegal to pass nil: a nil logger would NPE at first log call, so
// we round-trip through zap.NewNop() instead.
func SetLogger(log *zap.Logger) {
	if log == nil {
		log = zap.NewNop()
	}
	logger.Store(log)
}

// ── HardDeleteTx ───────────────────────────────────────────────────────────

// HardDeleteTx physically removes the media_assets row AND its dependent
// child rows in `asset_locations`, `asset_processing`, `asset_versions`,
// `asset_dedupe` (the historical HardDeleteClip fossil surface).
//
// Caller contract (see doc.go):
//   - `tx` MUST be non-nil and open (caller owns the lifecycle).
//   - The safety gate (lifecycle_state='DELETED' AND
//     qdrant_point_state='absent') MUST be evaluated by the caller
//     before invoking here. admin.PurgeService.HardDeleteClip is the
//     canonical gate site. The primitive does NOT re-evaluate the
//     gate because layered responsibility is the stated design
//     (doc.go §"Caller contracts" §2).
//
// Idempotent: deleting a row that has already been hard-deleted returns
// nil. The tx-bound primitive does not check sql.ErrNoRows because the
// tx-bound DELETE returns "rows affected = 0" without an error — a
// successful no-op is the contract.
//
// Order of deletes: child → parent. Each delete returns rows-affected;
// the aggregate is logged at DEBUG level so an operator running the
// audit-drill script can corroborate the cascade. No WARN is emitted
// today because the orphan-row detection contract is owned by the
// caller (admin.PurgeService) — the primitive is physics-only.
//
// Errors return a wrapped typed error per `fmt.Errorf("%w", err)`
// convention so callers can `errors.Is` against the underlying SQLite
// error.
func HardDeleteTx(ctx context.Context, tx *sql.Tx, id string) error {
	if tx == nil {
		return fmt.Errorf("txmutation.HardDeleteTx: tx is required (caller MUST supply the open *sql.Tx)")
	}
	if id == "" {
		return fmt.Errorf("txmutation.HardDeleteTx: id is required")
	}

	log := logger.Load()

	// Step 1: probe parent presence so the happy-path idempotency
	// contract (already-deleted row ⇒ nil, not an error) is honoured.
	// The probe runs inside the caller's tx so the row read is
	// tx-scoped and concurrent writers see consistent state.
	var present int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM media_assets WHERE id = ?`, id).Scan(&present)
	switch {
	case err == sql.ErrNoRows:
		// Idempotent no-op: the row was already hard-deleted (or
		// never existed). Debug-level so a re-running admin path
		// sees the no-op without alerting.
		log.Debug("txmutation.HardDeleteTx: parent row missing (idempotent no-op)", zap.String("id", id))
		return nil
	case err != nil:
		return fmt.Errorf("txmutation.HardDeleteTx %s: parent probe: %w", id, err)
	}

	// Step 2: delete child rows. Order matches the legacy fossil
	// (asset_locations → asset_processing → asset_versions →
	// asset_dedupe → media_assets). The list is a flat const slice —
	// no fmt.Sprintf runtime cost.
	for _, table := range hardDeleteChildTables {
		stmt := "DELETE FROM " + table + " WHERE id = ?"
		res, derr := tx.ExecContext(ctx, stmt, id)
		if derr != nil {
			return fmt.Errorf("txmutation.HardDeleteTx %s: delete %s: %w", id, table, derr)
		}
		affected, _ := res.RowsAffected()
		log.Debug("txmutation.HardDeleteTx: child deletion",
			zap.String("id", id),
			zap.String("table", table),
			zap.Int64("rows_affected", affected))
	}

	// Step 3: parent delete. Always the last operation in the tx so
	// the FK chains are torn down in the right order. RowsAffected=0
	// on the parent after the probe at step 1 means an external actor
	// raced us — log at INFO so the operator sees the race without
	// the package swallowing it silently.
	res, err := tx.ExecContext(ctx, `DELETE FROM media_assets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("txmutation.HardDeleteTx %s: parent delete: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	log.Info("txmutation.HardDeleteTx: parent deleted",
		zap.String("id", id),
		zap.Int64("rows_affected", affected))

	return nil
}

// hardDeleteChildTables is the canonical ordered list of dependent
// media_assets child tables purged by HardDeleteTx. Order matters:
// child tables MUST be deleted before parent (media_assets) so FK
// chains do not block the parent delete. The list mirrors the legacy
// HardDeleteClip fossil surface (clips_repository.go:240 fossil,
// per deprecation manifest PR-CLIP-RAW-MUTATIONS).
var hardDeleteChildTables = []string{
	"asset_locations",
	"asset_processing",
	"asset_versions",
	"asset_dedupe",
}

// ── RestoreTx ──────────────────────────────────────────────────────────────

// RestoreTx flips `lifecycle_state` back to canonical 'ACTIVE' and
// clears `deleted_at` inside the caller-owned *sql.Tx. Idempotent: a
// row whose lifecycle_state is already 'ACTIVE' (or that has been
// hard-deleted already) is a no-op write.
//
// Canonical case convention (PR 1 — Lifecycle state SSOT, June 2026):
// every lifecycle_state value is UPPERCASE. See asset.LifecycleState
// for the closed set { STAGING, PROCESSING, ACTIVE, DELETE_PENDING,
// DELETED, ERROR }. Pre-PR1 writers used lowercase 'ready'/'deleted';
// migration 101 rewrites historical rows to the canonical set. This
// primitive writes UPPERCASE 'ACTIVE' so production rows never expose
// the legacy lowercase pattern again.
//
// Caller contract (see doc.go):
//   - `tx` MUST be non-nil and open.
//   - The caller decides whether to flip lifecycle_state without
//     a Qdrant re-index (legacy admin behaviour: today the admin
//     tooling bypasses the outbox deliberately — see the gate
//     reference at admin.PurgeService.RestoreClip pkg-doc). This
//     primitive does NOT emit a Qdrant re-index event; the caller
//     can route a fresh outbox event if it wants vector rebuild.
func RestoreTx(ctx context.Context, tx *sql.Tx, id string) error {
	if tx == nil {
		return fmt.Errorf("txmutation.RestoreTx: tx is required (caller MUST supply the open *sql.Tx)")
	}
	if id == "" {
		return fmt.Errorf("txmutation.RestoreTx: id is required")
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET lifecycle_state = 'ACTIVE', deleted_at = NULL WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("txmutation.RestoreTx %s: update: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	logger.Load().Info("txmutation.RestoreTx: lifecycle_state -> ACTIVE",
		zap.String("id", id),
		zap.Int64("rows_affected", affected))
	return nil
}
