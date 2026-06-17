// Package txmanager provides a thin wrapper around *sql.DB that ensures
// transactional atomicity between media_assets mutations and outbox inserts.
//
// The pattern is:
//
//	err := txmanager.InTransaction(ctx, func(tx *sql.Tx) error {
//	    // 1. UPSERT media_assets
//	    // 2. INSERT media_index_outbox
//	    return nil
//	})
//
// If either step fails, the entire transaction rolls back — the outbox
// never contains orphan entries and media_assets never contains orphan
// embeddings that were never dispatched.
package outbox

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"
)

// Manager wraps a *sql.DB and provides transactional helpers.
type Manager struct {
	db  *sql.DB
	log *zap.Logger
}

// NewManager creates a new transaction manager.
func NewManager(db *sql.DB, log *zap.Logger) *Manager {
	return &Manager{db: db, log: log}
}

// DB returns the underlying database connection.
func (m *Manager) DB() *sql.DB {
	return m.db
}

// InTransaction executes fn inside a transaction. If fn returns an error
// (or panics), the transaction is rolled back. On success, the transaction
// is committed.
func (m *Manager) InTransaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			m.log.Warn("rollback failed", zap.Error(rbErr))
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
