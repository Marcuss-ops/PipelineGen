// Package finalizer — concrete implementations of the Spina Dorsale
// AssetFinalizerTx and ArtifactPreparationService (FASE 3, July 2026).
//
// Canonical reference: Piano d'Azione Completo § 5.
package assets

import (
	"context"
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// sqlTxAdapter wraps a *sql.Tx so it satisfies finalization.Transaction.
//
// The adapter is intentionally a zero-cost value type (one pointer field).
// Both sql.Result and *sql.Row structurally satisfy finalization.Result
// and finalization.Row respectively, so no Result/Row wrapping is needed.
type sqlTxAdapter struct {
	tx *sql.Tx
}

// Compile-time assertion: the adapter satisfies the domain Transaction.
var _ finalization.Transaction = sqlTxAdapter{}

// ExecContext delegates to tx.ExecContext. The returned sql.Result
// structurally satisfies finalization.Result (both have LastInsertId()
// and RowsAffected() with identical signatures).
func (a sqlTxAdapter) ExecContext(ctx context.Context, query string, args ...any) (finalization.Result, error) {
	return a.tx.ExecContext(ctx, query, args...)
}

// QueryRowContext delegates to tx.QueryRowContext. The returned *sql.Row
// structurally satisfies finalization.Row (both have Scan(dest ...any)
// with identical signatures).
func (a sqlTxAdapter) QueryRowContext(ctx context.Context, query string, args ...any) finalization.Row {
	return a.tx.QueryRowContext(ctx, query, args...)
}

// WrapTx adapts a *sql.Tx into a finalization.Transaction.
// Exported for use by the JobFinalizer (opens *sql.Tx, wraps it
// for AssetFinalizerTx consumption).
func WrapTx(tx *sql.Tx) finalization.Transaction {
	return sqlTxAdapter{tx: tx}
}

// UnwrapSQLTx returns the underlying *sql.Tx when the transaction
// was produced by WrapTx. This lets AssetCommitter callers that
// need a concrete *sql.Tx participate in the same transaction.
func UnwrapSQLTx(t finalization.Transaction) (*sql.Tx, bool) {
	a, ok := t.(sqlTxAdapter)
	if !ok {
		return nil, false
	}
	return a.tx, true
}
