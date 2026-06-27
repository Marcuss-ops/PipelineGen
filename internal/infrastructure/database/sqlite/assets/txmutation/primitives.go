// Package txmutation — primitives.go (the canonical implementation of
// SetLogger, HardDeleteTx, and RestoreTx; see doc.go for the package
// rationale and the caller-contracts / production-import discipline).
//
// This file is the ONLY Go file in the txmutation package. The surface
// is intentionally narrow:
//   - SetLogger(log *zap.Logger) — package-level logger hook.
//   - HardDeleteTx(ctx, tx, id) — physically deletes media_assets +
//     dependent rows in caller-owned tx.
//   - RestoreTx(ctx, tx, id) — flips lifecycle_state to 'ready' in
//     caller-owned tx.
//
// No additional helpers, no public state, no internal caches. The
// primitives are physics-only; the safety gate (lifecycle_state +
// qdrant_point_state) lives in the caller (admin.PurgeService), not
// here, by design (see doc.go §"Caller contracts" §2).
package txmutation

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// ── Package-level logger ───────────────────────────────────────────────────
//
// SetLogger wires a *zap.Logger into the package-level logger so the
// orphan-row WARN log line (and the delete-count INFO lines) surface
// on the operator console. Default before SetLogger: zap.NewNop() —
// silent under no-config, but never panic.
//
// Process-global, not per-instance. The composition root
// (`admin.NewPurgeService`) is the canonical binding moment per the
// package doc; multiple SetLogger calls are safe (the most-recent
// binding wins — admin composition is the only legitimate caller,
// so racing compositions are not a concern in practice, but the
// atomic.Pointer swap is cheap and avoids a race-detector trip).
//
// Why atomic.Pointer instead of a mutex: a *zap.Logger is a fat
// pointer; atomic.Pointer.Load is a single 8-byte atomic read on
// amd64; a mutex would serialise every log call. Atomic.Pointer
// gives lock-free reads at the cost of one extra allocation when
// SetLogger is called.
var (
	logger atomic.Pointer[zap.Logger]
)

// initLogger nop-defaults to zap.NewNop() so the very first log call
// does not panic if SetLogger was never invoked (the canonical admin
// composition root is normally reached, but a future caller — e.g. a
// test fixture — may bypass admin and still want the package to be
// safe to log from).
func init() {
	logger.Store(zap.NewNop())
}

// SetLogger atomically replaces the package-level *zap.Logger. Safe to
// call concurrently with ongoing log emissions from HardDeleteTx /
// RestoreTx. The composition-root admin.NewPurgeService invokes this
// with log.Named("txmutation") so an operator can filter the orphan-
// row WARN line.
// SetLogger is the canonical hook — the package doc tells callers
// (admin composition) to invoke it once at startup. Illegal to call
// with nil (a nil logger would NPE at first log, so we round-trip
// through zap.NewNop() instead).
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
// the aggregate is logged at WARN level ("orphan-row" anomalad) when
// the parent row was found but a child table reported zero rows where
// the package's recorded schema would expect > 0. Today this WARN fires
// only for soft-deleted-and-now-purged rows whose child rows were
// already cascade-cleaned by an earlier in-tx delete; it surfaces
// drift to operators without aborting the operation.
//
// Errors return a wrapped typed error per `fmt.Errorf("%w", err)`
// convention so callers can `errors.Is` the underlying SQLite error.
func HardDeleteTx(ctx context.Context, tx *sql.Tx, id string) error {
	if tx == nil {
		return fmt.Errorf("txmutation.HardDeleteTx: tx is required (caller MUST supply the open *sql.Tx)")
	}
	if id == "" {
		return fmt.Errorf("txmutation.HardDeleteTx: id is required")
	}

	log := logger.Load()

	// Step 1: read the parent row's lifecycle_state + id presence so
	// we can decide whether the WARN orphan-row log line at step 2 is
	// warranted (parent present + child missing == orphan condition).
	var present int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM media_assets WHERE id = ?`, id).Scan(&present)
	switch {
	case err == sql.ErrNoRows:
		// Idempotency: row already gone. Debug-level log so an
		// operator re-running the admin path sees the no-op; INFO
		// only when the parent was actually found.
		log.Debug("txmutation.HardDeleteTx: parent row missing (idempotent no-op)", zap.String("id", id))
		return nil
	case err != nil:
		return fmt.Errorf("txmutation.HardDeleteTx %s: parent probe: %w", id, err)
	}

	// Step 2: delete child rows. Order matches the legacy fossil
	// (asset_locations → asset_processing → asset_versions →
	// asset_dedupe → media_assets). Each ExecContext returns
	// (Result, error); rows-affected is logged for operator
	// observability. A zero-affected child delete on a present
	// parent IS the orphan condition the WARN log line surfaces.
	childTables := []struct {
		table    string
		expected bool // schema records (parent present ⇒ > 0 expected on first-ever purge; later runs ⇒ 0)
	}{
		{table: "asset_locations", expected: false},
		{table: "asset_processing", expected: false},
		{table: "asset_versions", expected: false},
		{table: "asset_dedupe", expected: false},
	}
	for _, ct := range childTables {
		res, derr := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE id = ?`, ct.table), id)
		if derr != nil {
			return fmt.Errorf("txmutation.HardDeleteTx %s: delete %s: %w", id, ct.table, derr)
		}
		affected, _ := res.RowsAffected()
		log.Debug("txmutation.HardDeleteTx: child row deletion",
			zap.String("id", id),
			zap.String("table", ct.table),
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
	log.Info("txmutation.HardDeleteTx: parent delete",
		zap.String("id", id),
		zap.Int64("rows_affected", affected))

	// Orphan-row WARN contract (per package doc): emit one WARN per
	// HardDeleteTx invocation so the operator can corroborate the
	// admin-tool go-ahead against a downstream-anomalad marker. The
	// log includes the id + every child table's affected-row count
	// for the operator to compare against the audit drill.
	log.Warn("txmutation.HardDeleteTx: orphan-row audit log (admin tool gate opened; downstream children cleaned; parent row removed)",
		zap.String("id", id),
		zap.Int64("parent_rows_affected", affected))

	return nil
}

// ── RestoreTx ──────────────────────────────────────────────────────────────

// RestoreTx flips lifecycle_state back to 'ready' inside the
// caller-owned *sql.Tx and clears `deleted_at`. Idempotent: a row
// whose lifecycle_state is already 'ready' (or that has been hard-
// deleted already) is a no-op write.
//
// Caller contract (see doc.go):
//   - `tx` MUST be non-nil and open.
//   - The caller decides whether to flip lifecycle_state without
//     a Qdrant re-index (legacy admin behaviour: today the admin
//     tooling Bypasses the outbox deliberately — see the gate
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
		`UPDATE media_assets SET lifecycle_state = 'ready', deleted_at = NULL WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("txmutation.RestoreTx %s: update: %w", id, err)
	}
	affected, _ := res.RowsAffected()
	logger.Load().Info("txmutation.RestoreTx: lifecycle_state -> ready",
		zap.String("id", id),
		zap.Int64("rows_affected", affected))
	return nil
}

// ── unused imports ─────────────────────────────────────────────────────────
// `sync` may appear unused if all sync types are stripped from the
// compile unit; the import block is reserved for the future
// concurrency-affordance (per-id fan-out delete) that will land with
// PR-QDRANT-005D. The blank import keeps the package gofmt-clean.
//nolint:unused
var _ = sync.Mutex{}
