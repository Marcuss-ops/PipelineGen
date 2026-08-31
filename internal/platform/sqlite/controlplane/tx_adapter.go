// Package controlplane — tx_adapter.go: *sql.Tx → capcontrol.Transaction.
//
// The controlplane capability owns a neutral Transaction port (ExecResult +
// Row, no database/sql import). This adapter wraps a *sql.Tx so the concrete
// SQLite implementation satisfies that port. Both sql.Result and *sql.Row
// structurally satisfy capcontrol.ExecResult and capcontrol.Row respectively,
// so no Result/Row re-wrapping is needed (mirrors
// internal/capabilities/assets/finalizer/tx_adapter.go).
package controlplane

import (
	"context"
	"database/sql"

	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
)

// sqlTxAdapter wraps a *sql.Tx so it satisfies capcontrol.Transaction.
// It is a zero-cost value type (one pointer field).
type sqlTxAdapter struct {
	tx *sql.Tx
}

// Compile-time assertion: the adapter satisfies the neutral port.
var _ capcontrol.Transaction = sqlTxAdapter{}

// ExecContext delegates to tx.ExecContext. The returned sql.Result
// structurally satisfies capcontrol.ExecResult (both expose
// LastInsertId() and RowsAffected() with identical signatures).
func (a sqlTxAdapter) ExecContext(ctx context.Context, query string, args ...any) (capcontrol.ExecResult, error) {
	return a.tx.ExecContext(ctx, query, args...)
}

// QueryRowContext delegates to tx.QueryRowContext. The returned *sql.Row
// structurally satisfies capcontrol.Row (both expose Scan and Err).
func (a sqlTxAdapter) QueryRowContext(ctx context.Context, query string, args ...any) capcontrol.Row {
	return a.tx.QueryRowContext(ctx, query, args...)
}

// WrapTx adapts a *sql.Tx into a capcontrol.Transaction for callers that
// own the transaction and want the UnitOfWork to participate in it.
func WrapTx(tx *sql.Tx) capcontrol.Transaction {
	if tx == nil {
		return nil
	}
	return sqlTxAdapter{tx: tx}
}

// UnwrapSQLTx returns the underlying *sql.Tx when the transaction was
// produced by WrapTx, letting callers recover the concrete handle to run
// their own SQL inside the same transaction.
func UnwrapSQLTx(t capcontrol.Transaction) (*sql.Tx, bool) {
	if t == nil {
		return nil, false
	}
	a, ok := t.(sqlTxAdapter)
	if !ok {
		return nil, false
	}
	return a.tx, true
}
