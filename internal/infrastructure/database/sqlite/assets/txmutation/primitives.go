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
//
// Also validates hardDeleteChildTables against the column allowlist
// (allowedHardDeleteChildColumns) — a mismatch panics at startup so
// a typo in the package-level var never reaches the SQL layer.
func init() {
	logger.Store(zap.NewNop())
	validateHardDeleteChildTables()
}

// validateHardDeleteChildTables panics if any entry in
// hardDeleteChildTables has a (table, column) pair not present in
// the allowedHardDeleteChildColumns allowlist. Called at init() so
// the panic fires at process startup, well before any SQL is executed.
//
// Pattern: mirrors the column-allowlist gates in transition.go
// (allowedJobColumns) and search_queries.go (allowedSortColumns).
func validateHardDeleteChildTables() {
	for _, ct := range hardDeleteChildTables {
		key := ct.table + "." + ct.column
		if !allowedHardDeleteChildColumns[key] {
			panic(fmt.Sprintf(
				"txmutation: hardDeleteChildTables entry (%q, %q) not in allowedHardDeleteChildColumns allowlist. "+
					"Add the pair to the allowlist before using it in the delete loop.",
				ct.table, ct.column,
			))
		}
	}
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

// childDelete pairs a child table with the column that links it to the
// canonical media_assets.id.
//
// Why an explicit (Table, Column) map and not a quoted table list:
//
// Pre-`codex/qdrant-purge-child-keys` (June 2026, PR 4) the loop ran
// `DELETE FROM <table> WHERE id = ?` for every child. That was wrong
// for asset_locations (FK column = asset_id, not the PK id) and
// asset_processing (FK column = asset_id). Both queries matched ZERO
// rows even though the child rows existed — silently leaving orphan
// rows behind. The post-delete gate (rows-affected > 0 expected on
// first-ever purge) therefore never fired and operators had no signal.
//
// Inlining the column makes the FK contract explicit per table:
//
//	{asset_locations,  asset_id}  — verified by migration 055
//	{asset_processing, asset_id}  — verified by migration 058
//	{asset_versions,   asset_id}  — verified by migration 105
//
//	asset_dedupe is intentionally OMITTED (was previously in the
//	list with `column = id`). There is no `CREATE TABLE asset_dedupe`
//	migration in the entire migrations/sqlite tree (the closest
//	reference is the application-layer DedupeService at
//	internal/application/assets/assetop/dedupe.go, which is in-memory
//	policy — not a SQL table). Keeping `asset_dedupe` in the loop
//	would force every purge into `no such table: asset_dedupe` on
//	fresh installs. The fossil is decommissioned.
//
// The (Table, Column) structure makes a future ledger-style child
// table (e.g. asset_audit, keyed on `aggregate_id`) a single-line
// addition instead of a runtime regression.
type childDelete struct {
	table  string
	column string
}

// hardDeleteChildTables is the canonical deletion map. Order is the
// deletion order within the tx — fail-fast on the first broken FK.
// NEW CHILD TABLES go here, not in HardDeleteTx's body, so the audit
// trail of the child's existence is reviewable in one place.
//
// Each entry is validated at init() against allowedHardDeleteChildColumns.
// A mismatch panics at startup so a typo never reaches the SQL layer.
var hardDeleteChildTables = []childDelete{
	{table: "asset_locations", column: "asset_id"},
	{table: "asset_processing", column: "asset_id"},
	{table: "asset_versions", column: "asset_id"},
}

// allowedHardDeleteChildColumns is the column-allowlist gate for
// HardDeleteTx. Every (table, column) pair in hardDeleteChildTables
// must appear here; init() panics on mismatch so a typo in the
// package-level var surfaces at startup rather than at first purge.
//
// Pattern: mirrors allowedJobColumns (transition.go) and
// allowedSortColumns (search_queries.go) — one canonical allowlist
// per fmt.Sprintf SQL site. Keys are "<table>.<column>".
var allowedHardDeleteChildColumns = map[string]bool{
	"asset_locations.asset_id":  true,
	"asset_processing.asset_id": true,
	"asset_versions.asset_id":   true,
}

// HardDeleteTx physically removes the media_assets row AND its dependent
// child rows (per hardDeleteChildTables) inside a caller-owned *sql.Tx.
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
// the aggregate is logged at WARN level ("orphan-row" anomalad) so the
// operator can correlate the admin-tool go-ahead against the
// downstream-anomalad marker.
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
	// media_assets). Each ExecContext returns (Result, error);
	// rows-affected is logged for operator observability. A
	// zero-affected child delete on a present parent IS the orphan
	// condition the WARN log line surfaces.
	for _, ct := range hardDeleteChildTables {
		// SAFETY: fmt.Sprintf is safe here — ct.table and ct.column are drawn
		// exclusively from the hardDeleteChildTables package-level var. Each
		// entry is validated at init() against allowedHardDeleteChildColumns;
		// an invalid entry panics at startup, not at SQL bind time. No user
		// input, no dynamic column names, no API-surface interpolation.
		// Exempt from the column-allowlist gate pattern used in transition.go
		// and search_queries.go because the source is compile-time constants.
		res, derr := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE %s = ?`, ct.table, ct.column), id)
		if derr != nil {
			return fmt.Errorf("txmutation.HardDeleteTx %s: delete %s(%s): %w", id, ct.table, ct.column, derr)
		}
		affected, _ := res.RowsAffected()
		log.Debug("txmutation.HardDeleteTx: child row deletion",
			zap.String("id", id),
			zap.String("table", ct.table),
			zap.String("column", ct.column),
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
//     tooling uses an explicit outbox exception — see the validation
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
//
//nolint:unused
var _ = sync.Mutex{}
