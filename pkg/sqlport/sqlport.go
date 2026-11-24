// Package sqlport declares the narrow SQL surfaces capabilities may name
// without importing database/sql or a database driver.
//
// The interfaces use database/sql's concrete Result, Row and Rows types, so
// *sql.Tx satisfies Tx structurally and *sql.DB satisfies ExecQueryer. A
// capability can therefore keep a caller-owned transaction without owning an
// engine import, while the SQL implementation stays in internal/platform.
package sqlport

import (
	"context"
	"database/sql"
)

// ErrNoRows is the canonical no-row sentinel re-exported for capability code.
var ErrNoRows = sql.ErrNoRows

// NullString is the canonical nullable-string scanner re-exported for
// capability code that reads legacy nullable columns.
type NullString = sql.NullString

// Result is the narrow result surface of ExecContext.
type Result = sql.Result

// Row is the narrow single-row surface of QueryRowContext.
type Row = *sql.Row

// Rows is the narrow multi-row surface of QueryContext.
type Rows = *sql.Rows

// Execer is the write half of a SQL handle.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer is the multi-row read half of a SQL handle.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// QueryRower is the single-row read half of a SQL handle.
type QueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ExecQueryer is a read/write SQL handle without transaction lifecycle.
type ExecQueryer interface {
	Execer
	Queryer
	QueryRower
}

// WriteTx is the caller-owned write transaction surface. It deliberately
// omits Commit and Rollback so consumers other than the transaction owner
// cannot end the transaction.
type WriteTx interface {
	Execer
	QueryRower
}

// Tx is a caller-owned transaction with lifecycle control.
type Tx interface {
	WriteTx
	Queryer
	Commit() error
	Rollback() error
}

// Beginner opens a transaction. The production adapter is WrapDB; tests may
// implement it with an in-memory database or a fake.
type Beginner interface {
	BeginTx(ctx context.Context) (Tx, error)
}

// DB is the read/write handle plus transaction opener.
type DB interface {
	Beginner
	ExecQueryer
}

type dbAdapter struct {
	*sql.DB
}

// WrapDB adapts *sql.DB to DB. It returns nil for a nil database so callers
// can fail closed at composition time.
func WrapDB(db *sql.DB) DB {
	if db == nil {
		return nil
	}
	return dbAdapter{DB: db}
}

// BeginTx opens a transaction with the database/sql default options.
func (d dbAdapter) BeginTx(ctx context.Context) (Tx, error) {
	return d.DB.BeginTx(ctx, nil)
}
